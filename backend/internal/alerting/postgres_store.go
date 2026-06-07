package alerting

import (
	"context"
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
    tenant_id, name, metric, comparator, threshold, severity,
    window_seconds, enabled, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
RETURNING id, tenant_id, name, metric, comparator, threshold::float8, severity,
          window_seconds, enabled, created_at, updated_at`,
		rule.TenantID, rule.Name, rule.Metric, string(rule.Comparator), rule.Threshold,
		string(rule.Severity), rule.WindowSeconds, rule.Enabled, rule.CreatedAt, rule.UpdatedAt,
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
    comparator=$5,
    threshold=$6,
    severity=$7,
    window_seconds=$8,
    enabled=$9,
    updated_at=now()
WHERE tenant_id=$1 AND id=$2
RETURNING id, tenant_id, name, metric, comparator, threshold::float8, severity,
          window_seconds, enabled, created_at, updated_at`,
		rule.TenantID, rule.ID, rule.Name, rule.Metric, string(rule.Comparator),
		rule.Threshold, string(rule.Severity), rule.WindowSeconds, rule.Enabled,
	))
	if isUniqueViolation(err) {
		return AlertRule{}, ErrRuleExists
	}
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
SELECT id, tenant_id, name, metric, comparator, threshold::float8, severity,
       window_seconds, enabled, created_at, updated_at
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
SELECT id, tenant_id, name, metric, comparator, threshold::float8, severity,
       window_seconds, enabled, created_at, updated_at
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
SELECT id, tenant_id, name, metric, comparator, threshold::float8, severity,
       window_seconds, enabled, created_at, updated_at
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
SELECT DISTINCT tenant_id
FROM alert_rules
WHERE enabled=true
ORDER BY tenant_id ASC`)
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

func (s *PostgresStore) UpsertFiringEvent(ctx context.Context, tenantID, ruleID int64, observed float64, now time.Time) (AlertEvent, bool, error) {
	if s == nil || s.pool == nil {
		return AlertEvent{}, false, ErrStoreNotConfigured
	}
	current, err := scanEvent(s.pool.QueryRow(ctx, `
SELECT id, tenant_id, rule_id, state, observed_value::float8, fired_at, resolved_at
FROM alert_events
WHERE tenant_id=$1 AND rule_id=$2 AND state='firing'
ORDER BY fired_at DESC, id DESC
LIMIT 1`, tenantID, ruleID))
	if err == nil {
		event, err := scanEvent(s.pool.QueryRow(ctx, `
UPDATE alert_events
SET observed_value=$3
WHERE tenant_id=$1 AND id=$2
RETURNING id, tenant_id, rule_id, state, observed_value::float8, fired_at, resolved_at`,
			tenantID, current.ID, observed,
		))
		return event, false, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return AlertEvent{}, false, err
	}
	event, err := scanEvent(s.pool.QueryRow(ctx, `
INSERT INTO alert_events (tenant_id, rule_id, state, observed_value, fired_at)
VALUES ($1,$2,'firing',$3,$4)
RETURNING id, tenant_id, rule_id, state, observed_value::float8, fired_at, resolved_at`,
		tenantID, ruleID, observed, now.UTC(),
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
RETURNING id, tenant_id, rule_id, state, observed_value::float8, fired_at, resolved_at`,
		tenantID, ruleID, now.UTC(),
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return AlertEvent{}, false, nil
	}
	return event, err == nil, err
}

func (s *PostgresStore) ListEvents(ctx context.Context, in ListEventsInput) ([]AlertEvent, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, tenant_id, rule_id, state, observed_value::float8, fired_at, resolved_at
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
INSERT INTO alert_silences (tenant_id, rule_id, reason, starts_at, ends_at, created_at)
VALUES ($1,$2,$3,$4,$5,$6)
RETURNING id, tenant_id, rule_id, reason, starts_at, ends_at, created_at`,
		silence.TenantID, nullableInt64(silence.RuleID), silence.Reason,
		silence.StartsAt, silence.EndsAt, silence.CreatedAt,
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
SELECT id, tenant_id, rule_id, reason, starts_at, ends_at, created_at
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
SELECT id, tenant_id, rule_id, reason, starts_at, ends_at, created_at
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
	var comparator, severity string
	err := row.Scan(&rule.ID, &rule.TenantID, &rule.Name, &rule.Metric, &comparator,
		&rule.Threshold, &severity, &rule.WindowSeconds, &rule.Enabled, &rule.CreatedAt, &rule.UpdatedAt)
	rule.Comparator = Comparator(comparator)
	rule.Severity = Severity(severity)
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
	err := row.Scan(&event.ID, &event.TenantID, &event.RuleID, &state,
		&event.ObservedValue, &event.FiredAt, &resolved)
	event.State = EventState(state)
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
		&silence.StartsAt, &silence.EndsAt, &silence.CreatedAt)
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

func pgTimePtr(in pgtype.Timestamptz) *time.Time {
	if !in.Valid {
		return nil
	}
	t := in.Time.UTC()
	return &t
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
