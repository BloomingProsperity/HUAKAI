// Package riskoverviewhttp 暴露一个**只读**的平台风控总览端点:把散落的已接线风控信号
// (已禁用 Key / 触发中告警 / 已封禁用户 / 设了 IP 黑名单的 Key)聚合成一张计数表,供运营台
// 一眼看清当前风险面。本切片**零处置、零写入、零新引擎**——只 fan-out 已有表的 COUNT。
// 所有查询强制按 tenant_id 收敛(防 auditexporthttp 那次跨租户 IDOR S0 复发)。
package riskoverviewhttp

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Overview 是某租户当前的风控信号计数快照(均为只读聚合)。
type Overview struct {
	// DisabledKeys:status='disabled' 的 API Key 数(含内容审核自动封 + 运维手动禁)。
	DisabledKeys int64 `json:"disabled_keys"`
	// FiringAlerts:state='firing' 的告警事件数(alerting 引擎触发中、未恢复)。
	FiringAlerts int64 `json:"firing_alerts"`
	// DisabledUsers:status='disabled' 的终端用户数(封号)。
	DisabledUsers int64 `json:"disabled_users"`
	// IPBlacklistedKeys:设置了非空 ip_blacklist 的 API Key 数(请求时 IP 拒绝名单)。
	IPBlacklistedKeys int64 `json:"ip_blacklisted_keys"`
}

// Store 抽象只读风控聚合,便于 handler 单测注入 fake。
type Store interface {
	Overview(ctx context.Context, tenantID int64) (Overview, error)
}

// PostgresStore 直接对现有表跑只读 COUNT。绝不写任何状态、不调任何处置。
type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

// Overview 跑 4 条按 tenant_id 收敛的 COUNT。任一失败即整体失败(fail-loud,不返回半张表误导运维)。
func (s *PostgresStore) Overview(ctx context.Context, tenantID int64) (Overview, error) {
	var out Overview
	// ① 已禁用 API Key(内容审核 ban_counter 自动封 + 运维手动禁,均落 status='disabled')。
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM api_keys WHERE tenant_id=$1 AND status='disabled' AND deleted_at IS NULL`,
		tenantID,
	).Scan(&out.DisabledKeys); err != nil {
		return Overview{}, err
	}
	// ② 触发中告警事件(alerting 引擎 state='firing' 未恢复)。
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM alert_events WHERE tenant_id=$1 AND state='firing'`,
		tenantID,
	).Scan(&out.FiringAlerts); err != nil {
		return Overview{}, err
	}
	// ③ 已封禁终端用户(users.status='disabled')。
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE tenant_id=$1 AND status='disabled' AND deleted_at IS NULL`,
		tenantID,
	).Scan(&out.DisabledUsers); err != nil {
		return Overview{}, err
	}
	// ④ 设置了非空 IP 黑名单的 API Key(api_keys.ip_blacklist 非空)。
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM api_keys WHERE tenant_id=$1 AND ip_blacklist IS NOT NULL AND ip_blacklist <> '' AND deleted_at IS NULL`,
		tenantID,
	).Scan(&out.IPBlacklistedKeys); err != nil {
		return Overview{}, err
	}
	return out, nil
}
