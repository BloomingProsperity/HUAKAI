package alerting

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) CreateRule(ctx context.Context, rule AlertRule) (AlertRule, error) {
	if s == nil || s.pool == nil {
		return AlertRule{}, ErrStoreNotConfigured
	}
	out, err := scanRule(s.pool.QueryRow(ctx, `
INSERT INTO alert_rules (
    tenant_id, name, metric, metric_type, comparator, threshold, severity,
    window_seconds, sustained_seconds, cooldown_seconds, notify_email,
    filters, enabled, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
RETURNING id, tenant_id, name, metric, COALESCE(metric_type,''), comparator, threshold::float8, severity,
          window_seconds, sustained_seconds, cooldown_seconds, notify_email,
          filters, last_triggered_at, enabled, created_at, updated_at`,
		rule.TenantID, rule.Name, rule.Metric, nullableString(string(rule.MetricType)), string(rule.Comparator), rule.Threshold,
		string(rule.Severity), rule.WindowSeconds, rule.SustainedSeconds, rule.CooldownSeconds,
		rule.NotifyEmail, jsonStringMap(rule.Filters), rule.Enabled, rule.CreatedAt, rule.UpdatedAt,
	))
	if isUniqueViolation(err) {
		return AlertRule{}, ErrRuleExists
	}
	return out, err
}

func (s *PostgresStore) UpdateRule(ctx context.Context, rule AlertRule) (AlertRule, error) {
	if s == nil || s.pool == nil {
		return AlertRule{}, ErrStoreNotConfigured
	}
	out, err := scanRule(s.pool.QueryRow(ctx, `
UPDATE alert_rules
SET name=$3,
    metric=$4,
    metric_type=$5,
    comparator=$6,
    threshold=$7,
    severity=$8,
    window_seconds=$9,
    sustained_seconds=$10,
    cooldown_seconds=$11,
    notify_email=$12,
    filters=$13,
    enabled=$14,
    updated_at=now()
WHERE tenant_id=$1 AND id=$2
RETURNING id, tenant_id, name, metric, COALESCE(metric_type,''), comparator, threshold::float8, severity,
          window_seconds, sustained_seconds, cooldown_seconds, notify_email,
          filters, last_triggered_at, enabled, created_at, updated_at`,
		rule.TenantID, rule.ID, rule.Name, rule.Metric, nullableString(string(rule.MetricType)), string(rule.Comparator),
		rule.Threshold, string(rule.Severity), rule.WindowSeconds, rule.SustainedSeconds,
		rule.CooldownSeconds, rule.NotifyEmail, jsonStringMap(rule.Filters), rule.Enabled,
	))
	if isUniqueViolation(err) {
		return AlertRule{}, ErrRuleExists
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return AlertRule{}, ErrNotFound
	}
	return out, err
}

// SetRuleEnabledInTx 在传入的事务 tx 内、定向地翻转单条告警规则的 enabled 列(只改 enabled +
// updated_at,不动规则的任何其它字段)。它**用 tx 而非 s.pool** 执行,这样规则的状态变化能在
// Hermes orchestrator 的同一事务内,与 hermes_tool_calls + admin_audit_events 审计行原子提交。
//
// 租户 scope 在 SQL 的 WHERE tenant_id=$1 AND id=$2 处绑死(纵深防御的第二处:Resolve 已先按
// 租户复检过目标行)——一个租户绝不能翻动另一个租户的规则,即便 id 撞上也命不中(返回 0 行 →
// ErrNotFound)。RETURNING 列与 UpdateRule 完全一致,复用 scanRule。
//
// 这是加性新方法:不改构造器、不动现有的 UpdateRule(它仍走 s.pool 服务 HTTP 路径)。
func (s *PostgresStore) SetRuleEnabledInTx(ctx context.Context, tx pgx.Tx, tenantID, id int64, enabled bool) (AlertRule, error) {
	if tx == nil {
		return AlertRule{}, ErrStoreNotConfigured
	}
	out, err := scanRule(tx.QueryRow(ctx, `
UPDATE alert_rules
SET enabled=$3,
    updated_at=now()
WHERE tenant_id=$1 AND id=$2
RETURNING id, tenant_id, name, metric, COALESCE(metric_type,''), comparator, threshold::float8, severity,
          window_seconds, sustained_seconds, cooldown_seconds, notify_email,
          filters, last_triggered_at, enabled, created_at, updated_at`,
		tenantID, id, enabled,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return AlertRule{}, ErrNotFound
	}
	return out, err
}

func (s *PostgresStore) DeleteRule(ctx context.Context, tenantID, id int64) error {
	if s == nil || s.pool == nil {
		return ErrStoreNotConfigured
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM alert_rules WHERE tenant_id=$1 AND id=$2`, tenantID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) GetRule(ctx context.Context, tenantID, id int64) (AlertRule, error) {
	if s == nil || s.pool == nil {
		return AlertRule{}, ErrStoreNotConfigured
	}
	out, err := scanRule(s.pool.QueryRow(ctx, `
SELECT id, tenant_id, name, metric, COALESCE(metric_type,''), comparator, threshold::float8, severity,
       window_seconds, sustained_seconds, cooldown_seconds, notify_email,
       filters, last_triggered_at, enabled, created_at, updated_at
FROM alert_rules
WHERE tenant_id=$1 AND id=$2`, tenantID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return AlertRule{}, ErrNotFound
	}
	return out, err
}

func (s *PostgresStore) ListRules(ctx context.Context, in ListRulesInput) ([]AlertRule, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, tenant_id, name, metric, COALESCE(metric_type,''), comparator, threshold::float8, severity,
       window_seconds, sustained_seconds, cooldown_seconds, notify_email,
       filters, last_triggered_at, enabled, created_at, updated_at
FROM alert_rules
WHERE tenant_id=$1
ORDER BY created_at DESC, id DESC
LIMIT $2 OFFSET $3`, in.TenantID, int32(in.Limit), int32(in.Offset))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRules(rows)
}

func (s *PostgresStore) ListEnabledRules(ctx context.Context, tenantID int64) ([]AlertRule, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, tenant_id, name, metric, COALESCE(metric_type,''), comparator, threshold::float8, severity,
       window_seconds, sustained_seconds, cooldown_seconds, notify_email,
       filters, last_triggered_at, enabled, created_at, updated_at
FROM alert_rules
WHERE tenant_id=$1 AND enabled=true
ORDER BY id ASC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRules(rows)
}

func (s *PostgresStore) ListTenantsWithEnabledRules(ctx context.Context) ([]int64, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	rows, err := s.pool.Query(ctx, `
SELECT DISTINCT r.tenant_id
FROM alert_rules r
JOIN tenants t ON t.id=r.tenant_id
WHERE r.enabled=true
  AND t.status='active'
  AND t.deleted_at IS NULL
ORDER BY r.tenant_id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var tenantID int64
		if err := rows.Scan(&tenantID); err != nil {
			return nil, err
		}
		out = append(out, tenantID)
	}
	return out, rows.Err()
}

func (s *PostgresStore) UpsertFiringEvent(ctx context.Context, in UpsertFiringEventInput) (AlertEvent, bool, error) {
	if s == nil || s.pool == nil {
		return AlertEvent{}, false, ErrStoreNotConfigured
	}
	current, err := scanEvent(s.pool.QueryRow(ctx, `
SELECT id, tenant_id, rule_id, state, observed_value::float8,
       threshold_value::float8, metric_value::float8, dimensions,
       fired_at, resolved_at, email_sent
FROM alert_events
WHERE tenant_id=$1 AND rule_id=$2 AND state='firing'
ORDER BY fired_at DESC, id DESC
LIMIT 1`, in.TenantID, in.RuleID))
	if err == nil {
		event, err := scanEvent(s.pool.QueryRow(ctx, `
UPDATE alert_events
SET observed_value=$3,
    threshold_value=$4,
    metric_value=$5,
    dimensions=$6
WHERE tenant_id=$1 AND id=$2
RETURNING id, tenant_id, rule_id, state, observed_value::float8,
          threshold_value::float8, metric_value::float8, dimensions,
          fired_at, resolved_at, email_sent`,
			in.TenantID, current.ID, in.ObservedValue, in.ThresholdValue, in.MetricValue,
			jsonStringMap(in.Dimensions),
		))
		return event, false, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return AlertEvent{}, false, err
	}
	event, err := scanEvent(s.pool.QueryRow(ctx, `
INSERT INTO alert_events (
    tenant_id, rule_id, state, observed_value, threshold_value,
    metric_value, dimensions, fired_at
) VALUES ($1,$2,'firing',$3,$4,$5,$6,$7)
RETURNING id, tenant_id, rule_id, state, observed_value::float8,
          threshold_value::float8, metric_value::float8, dimensions,
          fired_at, resolved_at, email_sent`,
		in.TenantID, in.RuleID, in.ObservedValue, in.ThresholdValue, in.MetricValue,
		jsonStringMap(in.Dimensions), in.FiredAt.UTC(),
	))
	return event, err == nil, err
}

func (s *PostgresStore) ResolveFiringEvent(ctx context.Context, tenantID, ruleID int64, now time.Time) (AlertEvent, bool, error) {
	if s == nil || s.pool == nil {
		return AlertEvent{}, false, ErrStoreNotConfigured
	}
	event, err := scanEvent(s.pool.QueryRow(ctx, `
UPDATE alert_events
SET state='resolved',
    resolved_at=$3
WHERE id = (
    SELECT id
    FROM alert_events
    WHERE tenant_id=$1 AND rule_id=$2 AND state='firing'
    ORDER BY fired_at DESC, id DESC
    LIMIT 1
)
  AND tenant_id=$1
RETURNING id, tenant_id, rule_id, state, observed_value::float8,
          threshold_value::float8, metric_value::float8, dimensions,
          fired_at, resolved_at, email_sent`,
		tenantID, ruleID, now.UTC(),
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return AlertEvent{}, false, nil
	}
	return event, err == nil, err
}

func (s *PostgresStore) ManualResolveEvent(ctx context.Context, tenantID, eventID int64, now time.Time) (AlertEvent, error) {
	if s == nil || s.pool == nil {
		return AlertEvent{}, ErrStoreNotConfigured
	}
	event, err := scanEvent(s.pool.QueryRow(ctx, `
UPDATE alert_events
SET state='manual_resolved',
    resolved_at=$3
WHERE tenant_id=$1 AND id=$2
RETURNING id, tenant_id, rule_id, state, observed_value::float8,
          threshold_value::float8, metric_value::float8, dimensions,
          fired_at, resolved_at, email_sent`,
		tenantID, eventID, now.UTC(),
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return AlertEvent{}, ErrNotFound
	}
	return event, err
}

func (s *PostgresStore) MarkEventEmailSent(ctx context.Context, tenantID, eventID int64) (AlertEvent, error) {
	if s == nil || s.pool == nil {
		return AlertEvent{}, ErrStoreNotConfigured
	}
	event, err := scanEvent(s.pool.QueryRow(ctx, `
UPDATE alert_events
SET email_sent=true
WHERE tenant_id=$1 AND id=$2
RETURNING id, tenant_id, rule_id, state, observed_value::float8,
          threshold_value::float8, metric_value::float8, dimensions,
          fired_at, resolved_at, email_sent`,
		tenantID, eventID,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return AlertEvent{}, ErrNotFound
	}
	return event, err
}

func (s *PostgresStore) MarkRuleTriggered(ctx context.Context, tenantID, ruleID int64, now time.Time) error {
	if s == nil || s.pool == nil {
		return ErrStoreNotConfigured
	}
	tag, err := s.pool.Exec(ctx, `
UPDATE alert_rules
SET last_triggered_at=$3,
    updated_at=now()
WHERE tenant_id=$1 AND id=$2`, tenantID, ruleID, now.UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) ListEvents(ctx context.Context, in ListEventsInput) ([]AlertEvent, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, tenant_id, rule_id, state, observed_value::float8,
       threshold_value::float8, metric_value::float8, dimensions,
       fired_at, resolved_at, email_sent
FROM alert_events
WHERE tenant_id=$1
  AND ($2::bigint IS NULL OR rule_id=$2)
  AND ($3::text = '' OR state=$3)
ORDER BY fired_at DESC, id DESC
LIMIT $4 OFFSET $5`, in.TenantID, nullableInt64(in.RuleID), string(in.State), int32(in.Limit), int32(in.Offset))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}

func (s *PostgresStore) CreateSilence(ctx context.Context, silence AlertSilence) (AlertSilence, error) {
	if s == nil || s.pool == nil {
		return AlertSilence{}, ErrStoreNotConfigured
	}
	return scanSilence(s.pool.QueryRow(ctx, `
INSERT INTO alert_silences (tenant_id, rule_id, reason, starts_at, ends_at, platform, group_id, region, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
RETURNING id, tenant_id, rule_id, reason, starts_at, ends_at,
          COALESCE(platform,''), COALESCE(group_id,''), COALESCE(region,''), created_at`,
		silence.TenantID, nullableInt64(silence.RuleID), silence.Reason,
		silence.StartsAt, silence.EndsAt, nullableString(silence.Platform),
		nullableString(silence.GroupID), nullableString(silence.Region), silence.CreatedAt,
	))
}

func (s *PostgresStore) DeleteSilence(ctx context.Context, tenantID, id int64) error {
	if s == nil || s.pool == nil {
		return ErrStoreNotConfigured
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM alert_silences WHERE tenant_id=$1 AND id=$2`, tenantID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) ListSilences(ctx context.Context, in ListSilencesInput) ([]AlertSilence, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, tenant_id, rule_id, reason, starts_at, ends_at,
       COALESCE(platform,''), COALESCE(group_id,''), COALESCE(region,''), created_at
FROM alert_silences
WHERE tenant_id=$1
ORDER BY ends_at DESC, id DESC
LIMIT $2 OFFSET $3`, in.TenantID, int32(in.Limit), int32(in.Offset))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSilences(rows)
}

func (s *PostgresStore) ListActiveSilences(ctx context.Context, tenantID int64, now time.Time) ([]AlertSilence, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, tenant_id, rule_id, reason, starts_at, ends_at,
       COALESCE(platform,''), COALESCE(group_id,''), COALESCE(region,''), created_at
FROM alert_silences
WHERE tenant_id=$1
  AND starts_at <= $2
  AND ends_at >= $2
ORDER BY ends_at ASC, id ASC`, tenantID, now.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSilences(rows)
}

type rowScanner interface {
	Scan(...any) error
}

func scanRule(row rowScanner) (AlertRule, error) {
	var rule AlertRule
	var comparator, metricType, severity string
	var filters []byte
	var lastTriggered pgtype.Timestamptz
	err := row.Scan(&rule.ID, &rule.TenantID, &rule.Name, &rule.Metric, &metricType, &comparator,
		&rule.Threshold, &severity, &rule.WindowSeconds, &rule.SustainedSeconds,
		&rule.CooldownSeconds, &rule.NotifyEmail, &filters, &lastTriggered,
		&rule.Enabled, &rule.CreatedAt, &rule.UpdatedAt)
	if err != nil {
		return AlertRule{}, err
	}
	rule.MetricType = MetricType(metricType)
	rule.Comparator = Comparator(comparator)
	rule.Severity = Severity(severity)
	parsedFilters, err := parseStringMap(filters)
	if err != nil {
		return AlertRule{}, err
	}
	rule.Filters = parsedFilters
	rule.LastTriggeredAt = pgTimePtr(lastTriggered)
	rule.CreatedAt = rule.CreatedAt.UTC()
	rule.UpdatedAt = rule.UpdatedAt.UTC()
	return rule, err
}

func scanRules(rows pgx.Rows) ([]AlertRule, error) {
	out := []AlertRule{}
	for rows.Next() {
		rule, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, rows.Err()
}

func scanEvent(row rowScanner) (AlertEvent, error) {
	var event AlertEvent
	var state string
	var resolved pgtype.Timestamptz
	var threshold, metric pgtype.Float8
	var dimensions []byte
	err := row.Scan(&event.ID, &event.TenantID, &event.RuleID, &state,
		&event.ObservedValue, &threshold, &metric, &dimensions, &event.FiredAt, &resolved, &event.EmailSent)
	if err != nil {
		return AlertEvent{}, err
	}
	event.State = EventState(state)
	event.ThresholdValue = pgFloat64Ptr(threshold)
	event.MetricValue = pgFloat64Ptr(metric)
	parsedDimensions, err := parseStringMap(dimensions)
	if err != nil {
		return AlertEvent{}, err
	}
	event.Dimensions = parsedDimensions
	event.FiredAt = event.FiredAt.UTC()
	event.ResolvedAt = pgTimePtr(resolved)
	return event, err
}

func scanEvents(rows pgx.Rows) ([]AlertEvent, error) {
	out := []AlertEvent{}
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

func scanSilence(row rowScanner) (AlertSilence, error) {
	var silence AlertSilence
	var ruleID pgtype.Int8
	err := row.Scan(&silence.ID, &silence.TenantID, &ruleID, &silence.Reason,
		&silence.StartsAt, &silence.EndsAt, &silence.Platform, &silence.GroupID,
		&silence.Region, &silence.CreatedAt)
	silence.RuleID = pgInt64Ptr(ruleID)
	silence.StartsAt = silence.StartsAt.UTC()
	silence.EndsAt = silence.EndsAt.UTC()
	silence.CreatedAt = silence.CreatedAt.UTC()
	return silence, err
}

func scanSilences(rows pgx.Rows) ([]AlertSilence, error) {
	out := []AlertSilence{}
	for rows.Next() {
		silence, err := scanSilence(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, silence)
	}
	return out, rows.Err()
}

func nullableInt64(in *int64) any {
	if in == nil {
		return nil
	}
	return *in
}

func nullableString(in string) any {
	if in == "" {
		return nil
	}
	return in
}

func jsonStringMap(in map[string]string) any {
	in = normalizeStringMap(in)
	if len(in) == 0 {
		return nil
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	return raw
}

func parseStringMap(raw []byte) (map[string]string, error) {
	if len(raw) == 0 {
		return map[string]string{}, nil
	}
	var out map[string]string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return normalizeStringMap(out), nil
}

func pgTimePtr(in pgtype.Timestamptz) *time.Time {
	if !in.Valid {
		return nil
	}
	t := in.Time.UTC()
	return &t
}

func pgFloat64Ptr(in pgtype.Float8) *float64 {
	if !in.Valid {
		return nil
	}
	v := in.Float64
	return &v
}

func pgInt64Ptr(in pgtype.Int8) *int64 {
	if !in.Valid {
		return nil
	}
	v := in.Int64
	return &v
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
