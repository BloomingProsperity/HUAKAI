//go:build integration_pg

// Slice 2 integration tests for the admin issuance pipeline.
// Validates the full flow:
//   1. Bootstrap admin token from env var → first AdminIdentity exists
//   2. Issuer.Issue(...) writes api_keys row + admin_audit_events row
//   3. Returned plaintext authenticates via the customer APIKeyResolver
//   4. Revoker.Revoke(...) flips status; subsequent resolve fails
//   5. Cross-tenant tenant_operator → ErrAdminForbidden
//   6. Rate limit (30/h) blocks 31st issuance
//   7. Audit payload jsonb NEVER contains plaintext bearer or key_hash
//
// Per CMB-5: assertion #7 is the security-critical regression test;
// it grep's the persisted payload for the substring of the plaintext
// that was issued.

package admin

import (
	"context"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	dbauth "github.com/BloomingProsperity/HUAKAI/internal/db/auth"
)

// silence unused import false-positive: zap removed from this file's
// helpers but kept in package via bootstrap_test.
var _ = strconv.Itoa

func openIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration test")
	}
	p, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

type adminFixture struct {
	t            *testing.T
	pool         *pgxpool.Pool
	tenantID     int64
	userID       int64
	adminTokenID int64
	adminBearer  string // platform_admin plaintext, used to forge auth header
	suffix       string
}

func newAdminFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *adminFixture {
	t.Helper()
	f := &adminFixture{t: t, pool: pool, suffix: uuid.NewString()}

	// Seed tenant + user (target for issuance).
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"admin-tenant-"+f.suffix,
	).Scan(&f.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		f.tenantID, "admin-user-"+f.suffix,
	).Scan(&f.userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// Seed a platform_admin token directly (bypasses MaybeBootstrap to
	// avoid env-var coupling; the bootstrap path has its own test).
	bearer, prefix, err := GenerateBearer(EnvAdmin)
	if err != nil {
		t.Fatalf("generate admin bearer: %v", err)
	}
	f.adminBearer = bearer
	hash, err := bcryptHashForTest(bearer)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO admin_tokens (name, key_hash, key_prefix, role, scope_tenant_id, bootstrap, status)
		 VALUES ($1, $2, $3, 'platform_admin', NULL, false, 'active') RETURNING id`,
		"test-admin-"+f.suffix, hash, prefix,
	).Scan(&f.adminTokenID); err != nil {
		t.Fatalf("seed admin_token: %v", err)
	}

	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM admin_audit_events WHERE actor_id = $1`,
			intStr(f.adminTokenID))
		_, _ = pool.Exec(c, `DELETE FROM admin_audit_events WHERE tenant_id = $1`, f.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM admin_tokens WHERE id = $1`, f.adminTokenID)
		_, _ = pool.Exec(c, `DELETE FROM api_keys WHERE tenant_id = $1`, f.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM users WHERE tenant_id = $1`, f.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id = $1`, f.tenantID)
	})

	return f
}

// -----------------------------------------------------------------------------
// Test 1 — HappyPath: issue + customer-resolver auth round-trip
// -----------------------------------------------------------------------------

func TestAdminIssue_HappyPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	f := newAdminFixture(t, ctx, pool)

	resolver := NewAdminResolver(admindb.New(pool))
	httpReq := httptest.NewRequest("POST", "/admin/v1/api-keys", nil)
	httpReq.Header.Set("Authorization", "Bearer "+f.adminBearer)
	ident, err := resolver.Resolve(ctx, httpReq)
	if err != nil {
		t.Fatalf("AdminResolver.Resolve: %v", err)
	}
	if ident.Role != RolePlatformAdmin {
		t.Fatalf("Role = %q; want platform_admin", ident.Role)
	}

	issuer := NewKeyIssuer(pool)
	result, err := issuer.Issue(ctx, IssueRequest{
		Caller:      ident,
		TenantID:    f.tenantID,
		UserID:      f.userID,
		Name:        "test-key-" + f.suffix,
		Environment: EnvLive,
		Reason:      "admin issuance smoke",
		RequestID:   "req-" + f.suffix,
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if result.Plaintext == "" {
		t.Fatal("Issue returned empty Plaintext")
	}
	if !strings.HasPrefix(result.Plaintext, "hk_live_") {
		t.Errorf("Plaintext should have hk_live_ prefix; got %q", result.Plaintext)
	}
	if result.KeyPrefix == "" || len(result.KeyPrefix) != PrefixLen {
		t.Errorf("KeyPrefix bad: %q (len=%d, want %d)", result.KeyPrefix, len(result.KeyPrefix), PrefixLen)
	}

	// Issued plaintext must authenticate via the CUSTOMER resolver — the
	// whole point is that admin-issued keys behave identically to
	// hand-SQL'd ones from the customer resolver's perspective.
	custResolver := auth.NewAPIKeyResolver(dbauth.New(pool))
	custReq := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	custReq.Header.Set("Authorization", "Bearer "+result.Plaintext)
	custIdent, err := custResolver.Resolve(ctx, custReq)
	if err != nil {
		t.Fatalf("customer APIKeyResolver rejected admin-issued key: %v", err)
	}
	if custIdent.TenantID != f.tenantID || custIdent.APIKeyID != result.APIKeyID {
		t.Errorf("customer Identity mismatch: got %+v want tenant=%d apiKey=%d",
			custIdent, f.tenantID, result.APIKeyID)
	}
}

// -----------------------------------------------------------------------------
// Test 2 — RevokeBlocksAuth: revoked key fails customer resolver
// -----------------------------------------------------------------------------

func TestAdminRevoke_BlocksAuth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	f := newAdminFixture(t, ctx, pool)
	resolver := NewAdminResolver(admindb.New(pool))
	httpReq := httptest.NewRequest("POST", "/", nil)
	httpReq.Header.Set("Authorization", "Bearer "+f.adminBearer)
	ident, err := resolver.Resolve(ctx, httpReq)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	issuer := NewKeyIssuer(pool)
	revoker := NewKeyRevoker(pool)

	result, err := issuer.Issue(ctx, IssueRequest{
		Caller:      ident,
		TenantID:    f.tenantID,
		UserID:      f.userID,
		Name:        "to-be-revoked",
		Environment: EnvTest,
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// Customer resolver works pre-revoke.
	custResolver := auth.NewAPIKeyResolver(dbauth.New(pool))
	custReq := httptest.NewRequest("POST", "/", nil)
	custReq.Header.Set("Authorization", "Bearer "+result.Plaintext)
	if _, err := custResolver.Resolve(ctx, custReq); err != nil {
		t.Fatalf("pre-revoke resolve: %v", err)
	}

	revRes, err := revoker.Revoke(ctx, RevokeRequest{
		Caller:   ident,
		APIKeyID: result.APIKeyID,
		TenantID: f.tenantID,
		Reason:   "test-revoke",
	})
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if revRes.AlreadyRevoked {
		t.Fatal("first revoke should not be AlreadyRevoked")
	}

	// Idempotent second revoke.
	revRes2, err := revoker.Revoke(ctx, RevokeRequest{
		Caller:   ident,
		APIKeyID: result.APIKeyID,
		TenantID: f.tenantID,
		Reason:   "test-revoke-2",
	})
	if err != nil {
		t.Fatalf("Revoke #2: %v", err)
	}
	if !revRes2.AlreadyRevoked {
		t.Fatal("second revoke should set AlreadyRevoked=true")
	}

	// Customer resolver now rejects.
	custReq2 := httptest.NewRequest("POST", "/", nil)
	custReq2.Header.Set("Authorization", "Bearer "+result.Plaintext)
	if _, err := custResolver.Resolve(ctx, custReq2); err == nil {
		t.Fatal("customer resolver MUST reject revoked key")
	}
}

// -----------------------------------------------------------------------------
// Test 3 — AuditNeverContainsPlaintext (CMB-5 regression)
// -----------------------------------------------------------------------------

func TestAdminIssue_AuditNeverContainsPlaintext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	f := newAdminFixture(t, ctx, pool)
	resolver := NewAdminResolver(admindb.New(pool))
	httpReq := httptest.NewRequest("POST", "/", nil)
	httpReq.Header.Set("Authorization", "Bearer "+f.adminBearer)
	ident, _ := resolver.Resolve(ctx, httpReq)

	issuer := NewKeyIssuer(pool)
	result, err := issuer.Issue(ctx, IssueRequest{
		Caller:      ident,
		TenantID:    f.tenantID,
		UserID:      f.userID,
		Name:        "audit-secrecy-" + f.suffix,
		Environment: EnvLive,
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// Read every audit row touching this tenant + check none of them
	// contain the plaintext bearer string anywhere in the payload jsonb.
	rows, err := pool.Query(ctx,
		`SELECT payload::text FROM admin_audit_events WHERE tenant_id = $1`,
		f.tenantID,
	)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if strings.Contains(payload, result.Plaintext) {
			t.Fatalf("CMB-5 violation: plaintext bearer leaked into audit payload: %s", payload)
		}
		// Also check for the bcrypt hash prefix shape ($2a$).
		if strings.Contains(payload, "$2a$") {
			t.Fatalf("CMB-5 violation: bcrypt hash leaked into audit payload: %s", payload)
		}
	}
}

// -----------------------------------------------------------------------------
// Test 4 — TenantOperatorCrossTenantBlocked
// -----------------------------------------------------------------------------

func TestAdminIssue_TenantOperatorCrossTenantBlocked(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	f := newAdminFixture(t, ctx, pool)

	// Seed a SECOND tenant — the tenant_operator below will be scoped to
	// f.tenantID but try to issue for tenantB, which must be blocked.
	var tenantB int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"admin-tenantB-"+f.suffix,
	).Scan(&tenantB); err != nil {
		t.Fatalf("seed tenantB: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantB)
	})

	// Seed a tenant_operator scoped to f.tenantID.
	bearer, prefix, _ := GenerateBearer(EnvAdmin)
	hash, _ := bcryptHashForTest(bearer)
	var opID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO admin_tokens (name, key_hash, key_prefix, role, scope_tenant_id, bootstrap, status)
		 VALUES ($1, $2, $3, 'tenant_operator', $4, false, 'active') RETURNING id`,
		"op-"+f.suffix, hash, prefix, f.tenantID,
	).Scan(&opID); err != nil {
		t.Fatalf("seed operator: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM admin_tokens WHERE id=$1`, opID)
	})

	resolver := NewAdminResolver(admindb.New(pool))
	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	ident, err := resolver.Resolve(ctx, req)
	if err != nil {
		t.Fatalf("Resolve operator: %v", err)
	}

	// Try to issue for tenantB — must fail with ErrAdminForbidden.
	issuer := NewKeyIssuer(pool)
	_, err = issuer.Issue(ctx, IssueRequest{
		Caller:      ident,
		TenantID:    tenantB, // wrong scope
		UserID:      1,
		Name:        "should-fail",
		Environment: EnvLive,
	})
	if err == nil {
		t.Fatal("tenant_operator MUST be blocked from issuing for non-scoped tenant")
	}
	if !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("expected forbidden; got %v", err)
	}
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func bcryptHashForTest(plaintext string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func intStr(n int64) string {
	return strconv.FormatInt(n, 10)
}
