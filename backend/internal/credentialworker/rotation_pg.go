package credentialworker

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresRotationStore is the production RotationStore (CRED-288b). It is a
// thin raw-pgx adapter on purpose: adding a new sqlc query is avoided while the
// committed sqlc output is drifted from a clean regen.
type PostgresRotationStore struct {
	pool *pgxpool.Pool
}

// NewPostgresRotationStore builds the rotation scan store over a pgx pool.
func NewPostgresRotationStore(pool *pgxpool.Pool) *PostgresRotationStore {
	return &PostgresRotationStore{pool: pool}
}

// DueForRotation returns up to limit active, non-deleted account credentials
// whose issuance (created_at) is older than olderThan — the credentials a TTL
// rotation policy should rotate. Oldest first so a small per-tick limit drains
// the most overdue credentials first.
func (s *PostgresRotationStore) DueForRotation(ctx context.Context, olderThan time.Time, limit int) ([]RotationCandidate, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	const q = `
SELECT id, tenant_id, provider_account_id, created_at
FROM account_credentials
WHERE state = 'active'
  AND deleted_at IS NULL
  AND created_at < $1
ORDER BY created_at ASC
LIMIT $2`
	rows, err := s.pool.Query(ctx, q, olderThan, limit)
	if err != nil {
		return nil, fmt.Errorf("credentialworker: due-for-rotation query: %w", err)
	}
	defer rows.Close()
	var out []RotationCandidate
	for rows.Next() {
		var c RotationCandidate
		if err := rows.Scan(&c.CredentialID, &c.TenantID, &c.ProviderAccountID, &c.LastRefreshAt); err != nil {
			return nil, fmt.Errorf("credentialworker: due-for-rotation scan: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("credentialworker: due-for-rotation rows: %w", err)
	}
	return out, nil
}

// FlagNeedsRotation idempotently transitions an active credential into
// needs_rotation. It is scoped by id+tenant+provider-account and only flips an
// 'active' row, so a re-scan or a concurrently-changed row is a safe no-op.
//
// This sets the operational rotation flag only; the sensitive rotation itself
// (credential refresh / re-acquisition) is performed downstream through the
// audited refresh flow, not here.
func (s *PostgresRotationStore) FlagNeedsRotation(ctx context.Context, c RotationCandidate) error {
	if s == nil || s.pool == nil {
		return nil
	}
	const q = `
UPDATE account_credentials
SET state = 'needs_rotation', updated_at = now()
WHERE id = $1 AND tenant_id = $2 AND provider_account_id = $3
  AND state = 'active' AND deleted_at IS NULL`
	if _, err := s.pool.Exec(ctx, q, c.CredentialID, c.TenantID, c.ProviderAccountID); err != nil {
		return fmt.Errorf("credentialworker: flag needs_rotation (cred %d): %w", c.CredentialID, err)
	}
	return nil
}

var _ RotationStore = (*PostgresRotationStore)(nil)
