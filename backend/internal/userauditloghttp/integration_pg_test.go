//go:build integration_pg

package userauditloghttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/userauditlog"
	"github.com/BloomingProsperity/HUAKAI/internal/userkey"
)

func TestPGUserAuditEventsSelfScoped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openUserAuditPool(t, ctx)
	f := newUserAuditFixture(t, ctx, pool)

	store := userauditlog.NewPostgresStore(pool)
	keySvc := userkey.NewService(pool, nil, userkey.WithAuditSink(store))
	issued, err := keySvc.Issue(ctx, userkey.IssueRequest{
		TenantID:  f.tenantID,
		UserID:    f.userA,
		Name:      "audit-self-key",
		RequestID: "req-issue-self",
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := keySvc.Revoke(ctx, userkey.RevokeRequest{
		TenantID:  f.tenantID,
		UserID:    f.userA,
		APIKeyID:  issued.APIKeyID,
		Reason:    "rotation",
		RequestID: "req-revoke-self",
	}); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	muxA := mountAuditEventsForTest(store, &sessionauth.SessionIdentity{TenantID: f.tenantID, UserID: f.userA})
	recA := httptest.NewRecorder()
	reqA := httptest.NewRequest(http.MethodGet, "/v1/me/audit-events?limit=10", nil)
	muxA.ServeHTTP(recA, reqA)
	if recA.Code != http.StatusOK {
		t.Fatalf("user A status=%d body=%s want 200", recA.Code, recA.Body.String())
	}
	var bodyA auditEventsResponse
	if err := json.Unmarshal(recA.Body.Bytes(), &bodyA); err != nil {
		t.Fatalf("decode user A body: %v body=%s", err, recA.Body.String())
	}
	if bodyA.Count != 2 || len(bodyA.AuditEvents) != 2 {
		t.Fatalf("user A count=%d len=%d body=%s want exactly 2 audit events", bodyA.Count, len(bodyA.AuditEvents), recA.Body.String())
	}
	if bodyA.AuditEvents[0].Action != userauditlog.ActionIssueAPIKey ||
		bodyA.AuditEvents[1].Action != userauditlog.ActionRevokeAPIKey {
		t.Fatalf("events order/actions=%s then %s want issue then revoke", bodyA.AuditEvents[0].Action, bodyA.AuditEvents[1].Action)
	}
	for i, ev := range bodyA.AuditEvents {
		if ev.Outcome != userauditlog.OutcomeCommitted {
			t.Fatalf("event %d outcome=%s want committed", i, ev.Outcome)
		}
		if ev.APIKeyID == nil || *ev.APIKeyID != issued.APIKeyID {
			t.Fatalf("event %d api_key_id=%v want %d", i, ev.APIKeyID, issued.APIKeyID)
		}
		if ev.KeyPrefix != issued.KeyPrefix {
			t.Fatalf("event %d key_prefix=%q want %q", i, ev.KeyPrefix, issued.KeyPrefix)
		}
	}
	if strings.Contains(recA.Body.String(), issued.Plaintext) {
		t.Fatalf("audit response leaked plaintext: %s", recA.Body.String())
	}
	var keyHash string
	if err := pool.QueryRow(ctx, `SELECT key_hash FROM api_keys WHERE id=$1`, issued.APIKeyID).Scan(&keyHash); err != nil {
		t.Fatalf("read key_hash: %v", err)
	}
	if keyHash != "" && strings.Contains(recA.Body.String(), keyHash) {
		t.Fatalf("audit response leaked key_hash: %s", recA.Body.String())
	}

	muxB := mountAuditEventsForTest(store, &sessionauth.SessionIdentity{TenantID: f.tenantID, UserID: f.userB})
	recB := httptest.NewRecorder()
	reqB := httptest.NewRequest(http.MethodGet, "/v1/me/audit-events?limit=10", nil)
	muxB.ServeHTTP(recB, reqB)
	if recB.Code != http.StatusOK {
		t.Fatalf("user B status=%d body=%s want 200", recB.Code, recB.Body.String())
	}
	var bodyB auditEventsResponse
	if err := json.Unmarshal(recB.Body.Bytes(), &bodyB); err != nil {
		t.Fatalf("decode user B body: %v body=%s", err, recB.Body.String())
	}
	if bodyB.Count != 0 || len(bodyB.AuditEvents) != 0 {
		t.Fatalf("same-tenant user B saw user A audit rows: count=%d len=%d body=%s", bodyB.Count, len(bodyB.AuditEvents), recB.Body.String())
	}
}

func openUserAuditPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type userAuditFixture struct {
	tenantID int64
	userA    int64
	userB    int64
}

func newUserAuditFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) userAuditFixture {
	t.Helper()
	suffix := uuid.NewString()
	f := userAuditFixture{}
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"user-audit-tenant-"+suffix,
	).Scan(&f.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		f.tenantID, "audit-user-a-"+suffix,
	).Scan(&f.userA); err != nil {
		t.Fatalf("seed user A: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		f.tenantID, "audit-user-b-"+suffix,
	).Scan(&f.userB); err != nil {
		t.Fatalf("seed user B: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM user_audit_events WHERE tenant_id = $1`, f.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM api_keys WHERE tenant_id = $1`, f.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM users WHERE tenant_id = $1`, f.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id = $1`, f.tenantID)
	})
	return f
}
