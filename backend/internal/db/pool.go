package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBTX 是手写仓储和事务回调共享的最小 pgx 执行接口。
type DBTX interface {
	Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error)
	Query(context.Context, string, ...interface{}) (pgx.Rows, error)
	QueryRow(context.Context, string, ...interface{}) pgx.Row
}

// ErrNotConfigured 表示没有配置数据库 DSN。
var ErrNotConfigured = errors.New("db: HUAKAI_DATABASE_URL not configured")

// PoolConfig 是 pgxpool.Config 之上的 HUAKAI 默认连接池配置。
type PoolConfig struct {
	DSN             string
	MaxConns        int32         // default 16
	MinConns        int32         // default 2
	MaxConnLifetime time.Duration // default 30m
	MaxConnIdleTime time.Duration // default 5m
	HealthCheckTime time.Duration // default 1m
	ConnectTimeout  time.Duration // default 10s
}

// Open 创建 pgxpool.Pool，套用 HUAKAI 默认值，并在返回前做一次 Ping。
func Open(ctx context.Context, cfg PoolConfig) (*pgxpool.Pool, error) {
	if cfg.DSN == "" {
		return nil, ErrNotConfigured
	}
	pgxCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("db: parse DSN: %w", err)
	}
	if cfg.MaxConns > 0 {
		pgxCfg.MaxConns = cfg.MaxConns
	} else {
		pgxCfg.MaxConns = 16
	}
	if cfg.MinConns > 0 {
		pgxCfg.MinConns = cfg.MinConns
	} else {
		pgxCfg.MinConns = 2
	}
	pgxCfg.MaxConnLifetime = nonZeroDuration(cfg.MaxConnLifetime, 30*time.Minute)
	pgxCfg.MaxConnIdleTime = nonZeroDuration(cfg.MaxConnIdleTime, 5*time.Minute)
	pgxCfg.HealthCheckPeriod = nonZeroDuration(cfg.HealthCheckTime, time.Minute)

	connectCtx := ctx
	connectTimeout := nonZeroDuration(cfg.ConnectTimeout, 10*time.Second)
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		connectCtx, cancel = context.WithTimeout(ctx, connectTimeout)
		defer cancel()
	}

	pool, err := pgxpool.NewWithConfig(connectCtx, pgxCfg)
	if err != nil {
		return nil, fmt.Errorf("db: connect: %w", err)
	}
	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return pool, nil
}

func nonZeroDuration(d, fallback time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return fallback
}
