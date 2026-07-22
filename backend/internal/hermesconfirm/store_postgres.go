package hermesconfirm

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const expiredCleanupBatch = 1000

// PostgresStore 只持久化确认值的哈希，并通过 DELETE RETURNING 实现跨副本原子单次消费。
type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) Issue(ctx context.Context, p PendingConfirmation) (string, error) {
	if s == nil || s.pool == nil {
		return "", fmt.Errorf("%w: database unavailable", ErrInvalidPending)
	}
	if err := validatePending(p); err != nil {
		return "", err
	}
	id, err := randomCorrelationID()
	if err != nil {
		return "", err
	}
	digest := confirmationDigest(id)
	argsBinding := confirmationBindingDigest(id, "args", p.ArgsDigest)
	planBinding := confirmationBindingDigest(id, "plan", p.PlanDigest)
	if _, err := s.pool.Exec(ctx, `
INSERT INTO hermes_pending_confirmations (
    token_hash, tool_name, tenant_id, actor_source, actor_id, target_id,
    args_binding_hash, plan_binding_hash, expires_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, clock_timestamp() + INTERVAL '5 minutes')
`, digest[:], p.ToolName, p.TenantID, p.ActorSource, p.ActorID, p.TargetID, argsBinding[:], planBinding[:]); err != nil {
		return "", fmt.Errorf("store Hermes confirmation: %w", err)
	}
	// 清理是有界的辅助动作，失败不撤销已经成功签发的确认。
	_, _ = s.pool.Exec(ctx, `
WITH expired AS (
    SELECT token_hash
    FROM hermes_pending_confirmations
    WHERE expires_at <= clock_timestamp()
    ORDER BY expires_at
    LIMIT $1
)
DELETE FROM hermes_pending_confirmations pending
USING expired
WHERE pending.token_hash = expired.token_hash
`, expiredCleanupBatch)
	return id, nil
}

func (s *PostgresStore) ConsumeWithStatus(ctx context.Context, id string, expected PendingConfirmation) (PendingConfirmation, ConsumeStatus, error) {
	if s == nil || s.pool == nil {
		return PendingConfirmation{}, ConsumeMissing, fmt.Errorf("confirmation database unavailable")
	}
	if err := validatePending(expected); err != nil {
		return PendingConfirmation{}, ConsumeMismatch, err
	}
	digest := confirmationDigest(id)
	var entry PendingConfirmation
	var storedArgsBinding []byte
	var storedPlanBinding []byte
	var expired bool
	err := s.pool.QueryRow(ctx, `
DELETE FROM hermes_pending_confirmations
WHERE token_hash = $1
RETURNING tool_name, tenant_id, actor_source, actor_id, target_id, expires_at,
          args_binding_hash, plan_binding_hash, expires_at <= clock_timestamp()
`, digest[:]).Scan(
		&entry.ToolName,
		&entry.TenantID,
		&entry.ActorSource,
		&entry.ActorID,
		&entry.TargetID,
		&entry.ExpiresAt,
		&storedArgsBinding,
		&storedPlanBinding,
		&expired,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return PendingConfirmation{}, ConsumeMissing, nil
	}
	if err != nil {
		return PendingConfirmation{}, ConsumeMissing, fmt.Errorf("consume Hermes confirmation: %w", err)
	}
	if expired {
		return PendingConfirmation{}, ConsumeExpired, nil
	}
	wantArgsBinding := confirmationBindingDigest(id, "args", expected.ArgsDigest)
	wantPlanBinding := confirmationBindingDigest(id, "plan", expected.PlanDigest)
	if entry.ToolName != expected.ToolName ||
		entry.TenantID != expected.TenantID ||
		entry.ActorSource != expected.ActorSource ||
		entry.ActorID != expected.ActorID ||
		entry.TargetID != expected.TargetID ||
		!hmac.Equal(storedArgsBinding, wantArgsBinding[:]) ||
		!hmac.Equal(storedPlanBinding, wantPlanBinding[:]) {
		return PendingConfirmation{}, ConsumeMismatch, nil
	}
	entry.ArgsDigest = expected.ArgsDigest
	entry.PlanDigest = expected.PlanDigest
	entry.ExpiresAt = entry.ExpiresAt.UTC()
	return entry, ConsumeOK, nil
}

func confirmationDigest(id string) [sha256.Size]byte {
	return sha256.Sum256([]byte(id))
}

func confirmationBindingDigest(id, kind string, digest BindingDigest) [sha256.Size]byte {
	mac := hmac.New(sha256.New, []byte(id))
	_, _ = mac.Write([]byte("huakai-hermes-confirmation-v1"))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(kind))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write(digest[:])
	var out [sha256.Size]byte
	copy(out[:], mac.Sum(nil))
	return out
}

var _ Store = (*PostgresStore)(nil)
var _ Store = (*Cache)(nil)
