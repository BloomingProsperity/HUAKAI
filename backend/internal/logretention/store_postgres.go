package logretention

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

type tableSpec struct {
	name                  string
	timeColumn            string
	orderColumn           string
	fixedCategory         string
	requiredNotNullColumn string
}

var ordinaryLogTables = []tableSpec{
	{name: "ops_runtime_logs", timeColumn: "ingested_at"},
	{name: "admin_audit_events", timeColumn: "ingested_at", fixedCategory: "operation"},
	{name: "user_audit_events", timeColumn: "ingested_at", fixedCategory: "security"},
	{name: "channel_health_audit_events", timeColumn: "ingested_at", fixedCategory: "recovery"},
	{name: "credential_audit_events", timeColumn: "ingested_at", fixedCategory: "security"},
	{name: "hermes_audit_events", timeColumn: "ingested_at", fixedCategory: "operation"},
	{name: "hermes_tool_calls", timeColumn: "ingested_at"},
	{name: "hermes_mutation_recovery", timeColumn: "ingested_at", orderColumn: "operation_id", fixedCategory: "recovery", requiredNotNullColumn: "audit_committed_at"},
	{name: "oauth_refresh_audit_events", timeColumn: "ingested_at", fixedCategory: "security"},
	{name: "pool_routing_audit_events", timeColumn: "ingested_at", fixedCategory: "operation"},
	{name: "rate_limit_audit_events", timeColumn: "ingested_at", fixedCategory: "recovery"},
	{name: "quota_audit_events", timeColumn: "ingested_at", fixedCategory: "financial"},
	{name: "payment_audit_events", timeColumn: "ingested_at", fixedCategory: "financial"},
	{name: "subscription_plan_audit_events", timeColumn: "ingested_at", fixedCategory: "financial"},
	{name: "moderation_log", timeColumn: "ingested_at", fixedCategory: "security"},
	{name: "referral_reward_audit_events", timeColumn: "ingested_at", fixedCategory: "financial"},
}

type batchResult struct {
	acquired   bool
	deleted    int64
	byCategory map[string]int64
}

type batchStore interface {
	retentionCutoff(context.Context) (time.Time, error)
	deleteExpiredBatch(context.Context, tableSpec, time.Time, int) (batchResult, error)
}

type postgresStore struct {
	db db.DBTX
}

func (s *postgresStore) retentionCutoff(ctx context.Context) (time.Time, error) {
	if s == nil || s.db == nil {
		return time.Time{}, fmt.Errorf("日志保留数据库未配置")
	}
	var cutoff time.Time
	if err := s.db.QueryRow(ctx, `SELECT CURRENT_TIMESTAMP - INTERVAL '30 days'`).Scan(&cutoff); err != nil {
		return time.Time{}, err
	}
	return cutoff.UTC(), nil
}

func (s *postgresStore) deleteExpiredBatch(ctx context.Context, spec tableSpec, cutoff time.Time, limit int) (batchResult, error) {
	if s == nil || s.db == nil {
		return batchResult{}, fmt.Errorf("日志保留数据库未配置")
	}
	tableName := pgx.Identifier{spec.name}.Sanitize()
	timeColumn := pgx.Identifier{spec.timeColumn}.Sanitize()
	orderColumnName := spec.orderColumn
	if orderColumnName == "" {
		orderColumnName = "id"
	}
	orderColumn := pgx.Identifier{orderColumnName}.Sanitize()
	categoryExpression := "target." + pgx.Identifier{"log_category"}.Sanitize()
	retentionGuard := ""
	if spec.requiredNotNullColumn != "" {
		retentionGuard = " AND target." + pgx.Identifier{spec.requiredNotNullColumn}.Sanitize() + " IS NOT NULL"
	}
	queryArgs := []any{"huakai.global-log-retention." + spec.name, cutoff.UTC(), limit}
	if spec.fixedCategory != "" {
		categoryExpression = "$4::text"
		queryArgs = append(queryArgs, spec.fixedCategory)
	}
	query := fmt.Sprintf(`
WITH lease AS MATERIALIZED (
    SELECT pg_try_advisory_xact_lock(hashtextextended($1, 0)) AS acquired
), victims AS MATERIALIZED (
    SELECT target.ctid
    FROM %s AS target
    CROSS JOIN lease
	    WHERE lease.acquired AND target.%s < $2%s
	ORDER BY target.%s, target.%s
    LIMIT $3
    FOR UPDATE OF target SKIP LOCKED
), deleted AS (
    DELETE FROM %s AS target
    USING victims
    WHERE target.ctid = victims.ctid
    RETURNING %s AS category
), counts AS (
    SELECT category, count(*)::bigint AS total
    FROM deleted
    GROUP BY category
)
SELECT lease.acquired,
       COALESCE(sum(counts.total), 0)::bigint,
       COALESCE(
           jsonb_object_agg(counts.category, counts.total)
               FILTER (WHERE counts.category IS NOT NULL),
           '{}'::jsonb
       )
FROM lease
LEFT JOIN counts ON true
	GROUP BY lease.acquired`, tableName, timeColumn, retentionGuard, timeColumn, orderColumn, tableName, categoryExpression)

	var result batchResult
	var categoryJSON []byte
	err := s.db.QueryRow(ctx, query, queryArgs...).
		Scan(&result.acquired, &result.deleted, &categoryJSON)
	if err != nil {
		return batchResult{}, err
	}
	result.byCategory = map[string]int64{}
	if len(categoryJSON) > 0 {
		if err := json.Unmarshal(categoryJSON, &result.byCategory); err != nil {
			return batchResult{}, fmt.Errorf("解析日志清理分类计数: %w", err)
		}
	}
	return result, nil
}
