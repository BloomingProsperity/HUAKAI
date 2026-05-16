package channelhealth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type DBTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type PostgresStore struct {
	db      DBTX
	beginTx func(context.Context, pgx.TxOptions) (pgx.Tx, error)
	signer  *sign.Signer
}

func NewPostgresStore(db DBTX) *PostgresStore {
	store := &PostgresStore{db: db}
	if beginner, ok := db.(interface {
		BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
	}); ok {
		store.beginTx = beginner.BeginTx
	}
	return store
}

func NewPostgresStoreWithAuditSigner(db DBTX, signer *sign.Signer) *PostgresStore {
	store := NewPostgresStore(db)
	store.signer = signer
	return store
}

func (s *PostgresStore) WithTx(ctx context.Context, fn func(Store) error) error {
	if s == nil || s.db == nil {
		return errors.New("channelhealth: postgres store not configured")
	}
	if s.beginTx == nil {
		if fn == nil {
			return nil
		}
		return fn(s)
	}
	tx, err := s.beginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	txStore := &PostgresStore{db: tx, signer: s.signer}
	if fn != nil {
		if err := fn(txStore); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) Get(ctx context.Context, key ChannelKey) (Record, error) {
	if s == nil || s.db == nil {
		return Record{}, errors.New("channelhealth: postgres store not configured")
	}
	row := s.db.QueryRow(ctx, `
SELECT tenant_id, channel_id, vendor, provider_account_id, account_credential_id,
       credential_version, state, score::float8, reason_class, confidence_tier,
       cooldown_until, ramp_stage_pct, ramp_started_at, state_entered_at,
       last_transition_at, policy_version, sample_window, last_signal_class,
       last_signal_at, manual_pause_reason, manual_override_actor_id,
       manual_override_reason, ramp_failure_count, recovery_blocked_reason,
       created_at, updated_at
FROM channel_health_state
WHERE tenant_id = $1
  AND vendor = $2
  AND account_credential_id = $3
  AND credential_version = $4`,
		key.TenantID, key.Vendor, key.AccountCredentialID, key.CredentialVersion)
	return scanRecord(row)
}

func (s *PostgresStore) LatestByProviderAccount(ctx context.Context, tenantID, providerAccountID int64) (Record, error) {
	if s == nil || s.db == nil {
		return Record{}, errors.New("channelhealth: postgres store not configured")
	}
	row := s.db.QueryRow(ctx, `
SELECT tenant_id, channel_id, vendor, provider_account_id, account_credential_id,
       credential_version, state, score::float8, reason_class, confidence_tier,
       cooldown_until, ramp_stage_pct, ramp_started_at, state_entered_at,
       last_transition_at, policy_version, sample_window, last_signal_class,
       last_signal_at, manual_pause_reason, manual_override_actor_id,
       manual_override_reason, ramp_failure_count, recovery_blocked_reason,
       created_at, updated_at
FROM channel_health_state
WHERE tenant_id = $1
  AND provider_account_id = $2
ORDER BY credential_version DESC, updated_at DESC
LIMIT 1`,
		tenantID, providerAccountID)
	return scanRecord(row)
}

func (s *PostgresStore) UpsertRecord(ctx context.Context, rec Record) (Record, error) {
	if s == nil || s.db == nil {
		return Record{}, errors.New("channelhealth: postgres store not configured")
	}
	rec.Key.ChannelID = rec.Key.StableChannelID()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = rec.CreatedAt
	}
	window, err := json.Marshal(rec.SampleWindow)
	if err != nil {
		return Record{}, fmt.Errorf("channelhealth: marshal sample window: %w", err)
	}
	row := s.db.QueryRow(ctx, `
INSERT INTO channel_health_state (
    tenant_id, channel_id, vendor, provider_account_id, account_credential_id,
    credential_version, state, score, reason_class, confidence_tier,
    cooldown_until, ramp_stage_pct, ramp_started_at, state_entered_at,
    last_transition_at, policy_version, sample_window, last_signal_class,
    last_signal_at, manual_pause_reason, manual_override_actor_id,
    manual_override_reason, ramp_failure_count, recovery_blocked_reason,
    created_at, updated_at
) VALUES (
    $1, $2, $3, NULLIF($4, 0), $5,
    $6, $7, $8, $9, $10,
    $11, NULLIF($12, 0), $13, $14,
    $15, $16, $17::jsonb, $18,
    $19, NULLIF($20, ''), NULLIF($21, ''),
    NULLIF($22, ''), $23, NULLIF($24, ''),
    $25, $26
)
ON CONFLICT (tenant_id, vendor, account_credential_id, credential_version)
DO UPDATE SET
    channel_id = EXCLUDED.channel_id,
    provider_account_id = EXCLUDED.provider_account_id,
    state = EXCLUDED.state,
    score = EXCLUDED.score,
    reason_class = EXCLUDED.reason_class,
    confidence_tier = EXCLUDED.confidence_tier,
    cooldown_until = EXCLUDED.cooldown_until,
    ramp_stage_pct = EXCLUDED.ramp_stage_pct,
    ramp_started_at = EXCLUDED.ramp_started_at,
    state_entered_at = EXCLUDED.state_entered_at,
    last_transition_at = EXCLUDED.last_transition_at,
    policy_version = EXCLUDED.policy_version,
    sample_window = EXCLUDED.sample_window,
    last_signal_class = EXCLUDED.last_signal_class,
    last_signal_at = EXCLUDED.last_signal_at,
    manual_pause_reason = EXCLUDED.manual_pause_reason,
    manual_override_actor_id = EXCLUDED.manual_override_actor_id,
    manual_override_reason = EXCLUDED.manual_override_reason,
    ramp_failure_count = EXCLUDED.ramp_failure_count,
    recovery_blocked_reason = EXCLUDED.recovery_blocked_reason,
    updated_at = EXCLUDED.updated_at
RETURNING tenant_id, channel_id, vendor, provider_account_id, account_credential_id,
       credential_version, state, score::float8, reason_class, confidence_tier,
       cooldown_until, ramp_stage_pct, ramp_started_at, state_entered_at,
       last_transition_at, policy_version, sample_window, last_signal_class,
       last_signal_at, manual_pause_reason, manual_override_actor_id,
       manual_override_reason, ramp_failure_count, recovery_blocked_reason,
       created_at, updated_at`,
		rec.Key.TenantID, rec.Key.ChannelID, rec.Key.Vendor, rec.Key.ProviderAccountID, rec.Key.AccountCredentialID,
		rec.Key.CredentialVersion, rec.State, rec.Score, rec.ReasonClass, rec.Confidence,
		rec.CooldownUntil, rec.RampStagePct, rec.RampStartedAt, rec.StateEnteredAt,
		rec.LastTransitionAt, rec.PolicyVersion, window, rec.LastSignalClass,
		rec.LastSignalAt, rec.ManualPauseReason, rec.ManualOverrideActorID,
		rec.ManualOverrideReason, rec.RampFailureCount, rec.RecoveryBlockedReason,
		rec.CreatedAt, rec.UpdatedAt)
	return scanRecord(row)
}

func (s *PostgresStore) AppendAudit(ctx context.Context, ev AuditEvent) error {
	if s == nil || s.db == nil {
		return errors.New("channelhealth: postgres store not configured")
	}
	ev.Key.ChannelID = ev.Key.StableChannelID()
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = time.Now().UTC()
	}
	payload, err := json.Marshal(ev.Payload)
	if err != nil {
		return fmt.Errorf("channelhealth: marshal audit payload: %w", err)
	}
	_, err = s.db.Exec(ctx, `
INSERT INTO channel_health_audit_events (
    tenant_id, event_type, channel_id, vendor, provider_account_id,
    account_credential_id, credential_version, previous_state, new_state,
    reason_class, policy_version, request_id, actor_id, payload, occurred_at
) VALUES (
    $1, $2, $3, $4, NULLIF($5, 0),
    $6, $7, NULLIF($8, ''), $9,
    $10, $11, NULLIF($12, ''), NULLIF($13, ''), $14::jsonb, $15
)`,
		ev.Key.TenantID, ev.Type, ev.Key.ChannelID, ev.Key.Vendor, ev.Key.ProviderAccountID,
		ev.Key.AccountCredentialID, ev.Key.CredentialVersion, ev.PreviousState, ev.NewState,
		ev.ReasonClass, ev.PolicyVersion, ev.RequestID, ev.ActorID, payload, ev.OccurredAt)
	if err != nil {
		return err
	}
	if s.signer == nil {
		return nil
	}
	entry, err := trustLedgerEntryForAudit(ev)
	if err != nil {
		return fmt.Errorf("channelhealth: build trust ledger entry: %w", err)
	}
	if _, err := auditledger.AppendInTransaction(ctx, s.db, s.signer, entry); err != nil {
		return fmt.Errorf("channelhealth: append trust ledger: %w", err)
	}
	return nil
}

func (s *PostgresStore) AppendAlert(ctx context.Context, alert Alert) error {
	if s == nil || s.db == nil {
		return errors.New("channelhealth: postgres store not configured")
	}
	alert.Key.ChannelID = alert.Key.StableChannelID()
	if alert.CreatedAt.IsZero() {
		alert.CreatedAt = time.Now().UTC()
	}
	if alert.Severity == "" {
		alert.Severity = "high"
	}
	payload, err := json.Marshal(alert.Payload)
	if err != nil {
		return fmt.Errorf("channelhealth: marshal alert payload: %w", err)
	}
	_, err = s.db.Exec(ctx, `
INSERT INTO channel_health_admin_alerts (
    tenant_id, channel_id, provider_account_id, account_credential_id,
    credential_version, alert_type, severity, reason_class, payload, created_at
) VALUES (
    $1, $2, NULLIF($3, 0), $4,
    $5, $6, $7, $8, $9::jsonb, $10
)`,
		alert.Key.TenantID, alert.Key.ChannelID, alert.Key.ProviderAccountID, alert.Key.AccountCredentialID,
		alert.Key.CredentialVersion, alert.Type, alert.Severity, alert.ReasonClass, payload, alert.CreatedAt)
	return err
}

func scanRecord(row pgx.Row) (Record, error) {
	var (
		rec                   Record
		providerAccountID     *int64
		state, reason, conf   string
		cooldownUntil         *time.Time
		rampStage             *int
		rampStartedAt         *time.Time
		windowRaw             []byte
		lastSignal            string
		lastSignalAt          *time.Time
		manualPauseReason     *string
		manualActor           *string
		manualReason          *string
		recoveryBlockedReason *string
	)
	err := row.Scan(
		&rec.Key.TenantID,
		&rec.Key.ChannelID,
		&rec.Key.Vendor,
		&providerAccountID,
		&rec.Key.AccountCredentialID,
		&rec.Key.CredentialVersion,
		&state,
		&rec.Score,
		&reason,
		&conf,
		&cooldownUntil,
		&rampStage,
		&rampStartedAt,
		&rec.StateEnteredAt,
		&rec.LastTransitionAt,
		&rec.PolicyVersion,
		&windowRaw,
		&lastSignal,
		&lastSignalAt,
		&manualPauseReason,
		&manualActor,
		&manualReason,
		&rec.RampFailureCount,
		&recoveryBlockedReason,
		&rec.CreatedAt,
		&rec.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, err
	}
	if providerAccountID != nil {
		rec.Key.ProviderAccountID = *providerAccountID
	}
	rec.State = HealthState(state)
	rec.ReasonClass = SignalClass(reason)
	rec.Confidence = ConfidenceTier(conf)
	rec.CooldownUntil = cooldownUntil
	rec.RampStartedAt = rampStartedAt
	if rampStage != nil {
		rec.RampStagePct = *rampStage
	}
	rec.LastSignalClass = SignalClass(lastSignal)
	rec.LastSignalAt = lastSignalAt
	if manualPauseReason != nil {
		rec.ManualPauseReason = *manualPauseReason
	}
	if manualActor != nil {
		rec.ManualOverrideActorID = *manualActor
	}
	if manualReason != nil {
		rec.ManualOverrideReason = *manualReason
	}
	if recoveryBlockedReason != nil {
		rec.RecoveryBlockedReason = *recoveryBlockedReason
	}
	if len(windowRaw) > 0 {
		if err := json.Unmarshal(windowRaw, &rec.SampleWindow); err != nil {
			return Record{}, fmt.Errorf("channelhealth: unmarshal sample window: %w", err)
		}
	}
	return rec, nil
}

var _ Store = (*PostgresStore)(nil)
