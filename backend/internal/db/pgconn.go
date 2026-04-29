// Package db extends sqlc-generated code with a pgxpool factory + health probe.
//
// The factory is the ONE place HUAKAI code opens a real PostgreSQL connection.
// Tests outside `_test.go` files MUST NOT call sql.Open or pgx.Connect directly;
// instead they use this Open() with a test DSN, or pass the *Queries shim
// returned by sqlc.
package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotConfigured is returned when Open is called with an empty DSN.
// Per the integration sprint plan: "If a function cannot reach PG, it
// returns a typed error, not a 200 OK." This sentinel is the contract.
var ErrNotConfigured = errors.New("db: HUAKAI_DATABASE_URL not configured")

// PoolConfig is HUAKAI-specific tuning on top of pgxpool.Config.
type PoolConfig struct {
	DSN             string
	MaxConns        int32         // default 16
	MinConns        int32         // default 2
	MaxConnLifetime time.Duration // default 30m
	MaxConnIdleTime time.Duration // default 5m
	HealthCheckTime time.Duration // default 1m
	ConnectTimeout  time.Duration // default 10s
}

// Open creates a pgxpool.Pool, applies HUAKAI defaults, and probes liveness.
// On success, the pool is ready to back db.New(pool) for sqlc-generated queries.
// On failure (timeout, auth, unreachable host), Open returns a wrapped error;
// the caller MUST NOT proceed to handle requests — see plan §F contract.
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
