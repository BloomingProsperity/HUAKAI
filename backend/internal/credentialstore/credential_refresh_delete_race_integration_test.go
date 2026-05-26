//go:build integration_pg

package credentialstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSaveRefreshSuccessRejectsAfterDelete(t *testing.T) {
	ctx, pool := openCredentialAuditTxPool(t)
	fixture, meta := seedCredentialWithStore(t, ctx, pool, "refresh-delete-race")
	defer cleanupCredentialAuditTxFixture(t, context.Background(), pool, fixture)

	store := NewStore(pool, mustTestKeyProvider(t), DefaultHandlerRegistry())
	rec, err := store.ResolveActive(ctx, fixture.tenantID, fixture.providerAccountID)
	if err != nil {
		t.Fatalf("ResolveActive: %v", err)
	}
	if rec.ID != meta.ID {
		t.Fatalf("ResolveActive credential id=%d want %d", rec.ID, meta.ID)
	}
	if rec.CredentialVersion != 1 {
		t.Fatalf("stale record version=%d want 1", rec.CredentialVersion)
	}

	if err := store.Delete(ctx, fixture.tenantID, fixture.providerAccountID, meta.ID, "owner"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := store.SaveRefreshSuccess(ctx, rec, []byte(`{"api_key":"sk-refresh-delete-race-next"}`), time.Now().Add(time.Hour), "refresh_succeeded"); err == nil {
		t.Fatal("SaveRefreshSuccess after Delete returned nil error")
	} else if msg := err.Error(); !strings.Contains(msg, "cas lost") && !strings.Contains(msg, "deleted") {
		t.Fatalf("SaveRefreshSuccess error=%q want cas lost or deleted", msg)
	}

	snap := credentialRefreshDeleteRaceSnapshotForID(t, ctx, pool, meta.ID)
	if snap.State != StateRevoked {
		t.Fatalf("state=%q want %q", snap.State, StateRevoked)
	}
	if !snap.DeletedAt.Valid {
		t.Fatal("deleted_at is NULL; stale refresh resurrected credential")
	}
	if snap.Version != 1 {
		t.Fatalf("credential_version=%d want 1", snap.Version)
	}
}

func TestSaveRefreshFailureRejectsAfterRotate(t *testing.T) {
	ctx, pool := openCredentialAuditTxPool(t)
	fixture, meta := seedCredentialWithStore(t, ctx, pool, "refresh-failure-rotate-race")
	defer cleanupCredentialAuditTxFixture(t, context.Background(), pool, fixture)

	store := NewStore(pool, mustTestKeyProvider(t), DefaultHandlerRegistry())
	staleRec, err := store.ResolveActive(ctx, fixture.tenantID, fixture.providerAccountID)
	if err != nil {
		t.Fatalf("ResolveActive: %v", err)
	}
	if staleRec.ID != meta.ID {
		t.Fatalf("ResolveActive credential id=%d want %d", staleRec.ID, meta.ID)
	}
	if staleRec.CredentialVersion != 1 {
		t.Fatalf("stale record version=%d want 1", staleRec.CredentialVersion)
	}

	if err := store.SaveRefreshSuccess(ctx, staleRec, []byte(`{"api_key":"sk-refresh-failure-rotate-race-next"}`), time.Now().Add(time.Hour), "refresh_succeeded"); err != nil {
		t.Fatalf("SaveRefreshSuccess: %v", err)
	}
	if staleRec.CredentialVersion != 1 {
		t.Fatalf("stale record version mutated to %d; test no longer exercises stale version 1", staleRec.CredentialVersion)
	}

	err = store.SaveRefreshFailure(ctx, staleRec, "invalid_grant", time.Now().Add(time.Minute))
	if err == nil {
		t.Fatal("SaveRefreshFailure after rotate returned nil error")
	}
	if !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("SaveRefreshFailure after rotate error=%v want %v", err, ErrCredentialNotFound)
	}

	snap := credentialRefreshDeleteRaceSnapshotForID(t, ctx, pool, meta.ID)
	if snap.State != StateActive {
		t.Fatalf("state=%q want %q; stale failure revoked rotated credential", snap.State, StateActive)
	}
	if snap.FailureClass.Valid {
		t.Fatalf("failure_class=%q want NULL", snap.FailureClass.String)
	}
	if snap.Version != 2 {
		t.Fatalf("credential_version=%d want 2", snap.Version)
	}
}

type credentialRefreshDeleteRaceSnapshot struct {
	State        string
	Version      int32
	DeletedAt    pgtype.Timestamptz
	FailureClass pgtype.Text
}

func credentialRefreshDeleteRaceSnapshotForID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, credentialID int64) credentialRefreshDeleteRaceSnapshot {
	t.Helper()
	var s credentialRefreshDeleteRaceSnapshot
	if err := pool.QueryRow(ctx, `
		SELECT state, credential_version, deleted_at, failure_class
		FROM account_credentials
		WHERE id=$1`, credentialID).Scan(&s.State, &s.Version, &s.DeletedAt, &s.FailureClass); err != nil {
		t.Fatalf("credential refresh/delete race snapshot: %v", err)
	}
	return s
}
