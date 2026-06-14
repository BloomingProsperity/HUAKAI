package credentialworker

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// insertCredentialWithCreatedAt seeds one active account_credentials row with an
// explicit created_at so the rotation-age cutoff can be exercised deterministically.
func insertCredentialWithCreatedAt(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, paID int64, suffix string, createdAt time.Time) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO account_credentials (
			tenant_id, provider_account_id, vendor, auth_mode, state,
			encrypted_payload, key_id, nonce, aad_hash, created_at
		) VALUES ($1, $2, 'anthropic', 'api_key', 'active', $3, $4, $5, $6, $7)
		RETURNING id`,
		tenantID, paID,
		[]byte("ct-"+suffix), "key-"+suffix, []byte("nonce-"+suffix), "aad-"+suffix,
		createdAt,
	).Scan(&id); err != nil {
		t.Fatalf("insert credential %s: %v", suffix, err)
	}
	return id
}

func credentialState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id int64) string {
	t.Helper()
	var st string
	if err := pool.QueryRow(ctx, `SELECT state FROM account_credentials WHERE id = $1`, id).Scan(&st); err != nil {
		t.Fatalf("read state %d: %v", id, err)
	}
	return st
}

// CRED-288b end-to-end against a real Postgres: a credential older than the
// rotation cutoff is selected and flagged needs_rotation; a fresh one is left
// untouched; re-flagging is a safe no-op.
func TestPostgresRotationStore_DueAndFlag(t *testing.T) {
	ctx := context.Background()
	pool := openCredentialWorkerTestPool(t, ctx)
	defer pool.Close()

	// Two independent provider accounts (each its own tenant) so both credentials
	// can be active without tripping the per-(tenant,pa,vendor,auth_mode) unique.
	// Unique run id keeps the test re-runnable against a non-reset DB (tenant name
	// is globally unique).
	run := time.Now().UnixNano()
	tenantOld, paOld := seedCredentialWorkerProviderAccount(t, ctx, pool, fmt.Sprintf("rot288b-%d-old", run))
	tenantFresh, paFresh := seedCredentialWorkerProviderAccount(t, ctx, pool, fmt.Sprintf("rot288b-%d-fresh", run))
	now := time.Now().UTC()
	oldID := insertCredentialWithCreatedAt(t, ctx, pool, tenantOld, paOld, "old", now.Add(-200*24*time.Hour))
	freshID := insertCredentialWithCreatedAt(t, ctx, pool, tenantFresh, paFresh, "fresh", now.Add(-24*time.Hour))

	store := NewPostgresRotationStore(pool)
	cutoff := now.Add(-90 * 24 * time.Hour)

	due, err := store.DueForRotation(ctx, cutoff, 100)
	if err != nil {
		t.Fatalf("DueForRotation: %v", err)
	}
	seen := map[int64]bool{}
	for _, c := range due {
		seen[c.CredentialID] = true
	}
	// Discriminating: the 200-day-old credential is due; the 1-day-old one is NOT.
	if !seen[oldID] {
		t.Fatalf("200d-old credential %d must be due for rotation", oldID)
	}
	if seen[freshID] {
		t.Fatalf("1d-old credential %d must NOT be due for rotation", freshID)
	}

	// Flag the old one; its state flips to needs_rotation, the fresh one is intact.
	var oldCand RotationCandidate
	for _, c := range due {
		if c.CredentialID == oldID {
			oldCand = c
		}
	}
	if err := store.FlagNeedsRotation(ctx, oldCand); err != nil {
		t.Fatalf("FlagNeedsRotation: %v", err)
	}
	if got := credentialState(t, ctx, pool, oldID); got != "needs_rotation" {
		t.Fatalf("old credential state = %q, want needs_rotation", got)
	}
	if got := credentialState(t, ctx, pool, freshID); got != "active" {
		t.Fatalf("fresh credential state = %q, want active (untouched)", got)
	}

	// Idempotent: re-flagging a non-active row is a safe no-op (no error, stays needs_rotation).
	if err := store.FlagNeedsRotation(ctx, oldCand); err != nil {
		t.Fatalf("re-flag must be a no-op, got %v", err)
	}
	if got := credentialState(t, ctx, pool, oldID); got != "needs_rotation" {
		t.Fatalf("re-flag changed state to %q, want stable needs_rotation", got)
	}

	// After flagging, the old credential is no longer 'active' so a re-scan excludes it.
	due2, err := store.DueForRotation(ctx, cutoff, 100)
	if err != nil {
		t.Fatalf("DueForRotation rescan: %v", err)
	}
	for _, c := range due2 {
		if c.CredentialID == oldID {
			t.Fatalf("flagged credential %d must drop out of the due scan", oldID)
		}
	}
}
