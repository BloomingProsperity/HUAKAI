// Package routingsignal 持久化跨实例共享的账号请求结果信号。
package routingsignal

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const ewmaAlpha = 0.20

var ErrNotConfigured = errors.New("路由信号存储未配置")

// Observation 是一次已完成上游尝试的最小反馈，不包含请求正文或上游错误原文。
type Observation struct {
	TenantID          int64
	ProviderAccountID int64
	Success           bool
	Latency           time.Duration
	LatencyValid      bool
	ObservedAt        time.Time
}

// Recorder 接收所有协议共用的上游尝试结果。
type Recorder interface {
	RecordRoutingSignal(context.Context, Observation) error
}

// PostgresRecorder 通过单条原子 upsert 更新 EWMA，多个 gateway 副本共享同一状态轴。
type PostgresRecorder struct {
	pool *pgxpool.Pool
}

func NewPostgresRecorder(pool *pgxpool.Pool) *PostgresRecorder {
	return &PostgresRecorder{pool: pool}
}

func (r *PostgresRecorder) RecordRoutingSignal(ctx context.Context, in Observation) error {
	if r == nil || r.pool == nil {
		return ErrNotConfigured
	}
	if in.TenantID <= 0 || in.ProviderAccountID <= 0 {
		return errors.New("路由信号账号身份无效")
	}
	observedAt := in.ObservedAt.UTC()
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	outcome := "error"
	successSample := 0.0
	errorSample := 1.0
	if in.Success {
		outcome = "success"
		successSample = 1.0
		errorSample = 0.0
	}
	var latencyMS any
	if in.LatencyValid && in.Latency >= 0 {
		latencyMS = float64(in.Latency) / float64(time.Millisecond)
	}
	_, err := r.pool.Exec(ctx, `
INSERT INTO provider_account_routing_signals (
    tenant_id, provider_account_id, success_ewma, error_ewma,
    response_latency_ms_ewma, sample_count, last_outcome,
    last_success_at, last_error_at, observed_at
) VALUES (
    $1, $2, $3, $4, $5, 1, $6,
    CASE WHEN $6::text = 'success' THEN $7::timestamptz ELSE NULL END,
    CASE WHEN $6::text = 'error' THEN $7::timestamptz ELSE NULL END,
    $7::timestamptz
)
ON CONFLICT (tenant_id, provider_account_id) DO UPDATE SET
    success_ewma = provider_account_routing_signals.success_ewma * (1 - $8::double precision)
        + EXCLUDED.success_ewma * $8::double precision,
    error_ewma = provider_account_routing_signals.error_ewma * (1 - $8::double precision)
        + EXCLUDED.error_ewma * $8::double precision,
    response_latency_ms_ewma = CASE
        WHEN EXCLUDED.response_latency_ms_ewma IS NULL THEN provider_account_routing_signals.response_latency_ms_ewma
        WHEN provider_account_routing_signals.response_latency_ms_ewma IS NULL THEN EXCLUDED.response_latency_ms_ewma
        ELSE provider_account_routing_signals.response_latency_ms_ewma * (1 - $8::double precision)
            + EXCLUDED.response_latency_ms_ewma * $8::double precision
    END,
    sample_count = provider_account_routing_signals.sample_count + 1,
    last_outcome = EXCLUDED.last_outcome,
    last_success_at = COALESCE(EXCLUDED.last_success_at, provider_account_routing_signals.last_success_at),
    last_error_at = COALESCE(EXCLUDED.last_error_at, provider_account_routing_signals.last_error_at),
    observed_at = GREATEST(provider_account_routing_signals.observed_at, EXCLUDED.observed_at),
    updated_at = now()`,
		in.TenantID, in.ProviderAccountID, successSample, errorSample,
		latencyMS, outcome, observedAt, ewmaAlpha,
	)
	return err
}

var _ Recorder = (*PostgresRecorder)(nil)
