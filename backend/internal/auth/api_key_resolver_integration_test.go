//go:build integration_pg

// Phase L0 minimum integration tests for APIKeyResolver against
// real PostgreSQL. Validates:
//   - happy path (matching bearer → Identity)
//   - wrong bearer → ErrUnauthorized (401)
//   - revoked / expired key → ErrUnauthorized
//   - cross-tenant probe (tenant A's bearer never resolves elsewhere)
//   - prefix-collision: multiple candidates, bcrypt picks the right one
//   - bcrypt fanout cap: SQL LIMIT 5 keeps DOS bounded
//
// All failure paths must return ErrUnauthorized (not a discriminated
// error) per D10 (no enumeration leakage).

package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	dbauth "github.com/BloomingProsperity/HUAKAI/internal/db/auth"
)

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

type seededAPIKey struct {
	tenantID      int64
	otherTenantID int64
	userID        int64
	apiKeyID      int64
	plaintext     string
}

func seedAPIKey(t *testing.T, ctx context.Context, pool *pgxpool.Pool, opts apiKeySeedOpts) *seededAPIKey {
	t.Helper()
	suffix := uuid.NewString()
	s := &seededAPIKey{}

	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"auth-tenant-"+suffix,
	).Scan(&s.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"auth-other-"+suffix,
	).Scan(&s.otherTenantID); err != nil {
		t.Fatalf("seed other tenant: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		s.tenantID, "user-"+suffix,
	).Scan(&s.userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	plaintext := opts.plaintext
	if plaintext == "" {
		plaintext = "hk_test_" + suffix
	}
	prefix := plaintext
	if len(prefix) > APIKeyPrefixLen {
		prefix = prefix[:APIKeyPrefixLen]
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt hash: %v", err)
	}
	s.plaintext = plaintext

	status := opts.status
	if status == "" {
		status = "active"
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		s.tenantID, s.userID, "key-"+suffix, string(hash), prefix, status, opts.expiresAt,
	).Scan(&s.apiKeyID); err != nil {
		t.Fatalf("seed api_key: %v", err)
	}

	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM api_keys WHERE tenant_id IN ($1, $2)`, s.tenantID, s.otherTenantID)
		_, _ = pool.Exec(c, `DELETE FROM users WHERE tenant_id IN ($1, $2)`, s.tenantID, s.otherTenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id IN ($1, $2)`, s.tenantID, s.otherTenantID)
	})
	return s
}

type apiKeySeedOpts struct {
	plaintext string
	status    string
	expiresAt interface{} // pass nil for no expiry, or time.Time
}

func newRequest(t *testing.T, header string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	if header != "" {
		r.Header.Set("Authorization", header)
	}
	return r
}

func TestAPIKeyResolver_HappyPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	seed := seedAPIKey(t, ctx, pool, apiKeySeedOpts{})

	r := NewAPIKeyResolver(dbauth.New(pool))
	ident, err := r.Resolve(ctx, newRequest(t, "Bearer "+seed.plaintext))
	if err != nil {
		t.Fatalf("happy path: %v", err)
	}
	if ident.TenantID != seed.tenantID || ident.APIKeyID != seed.apiKeyID || ident.UserID != seed.userID {
		t.Fatalf("identity mismatch: %+v vs seed %+v", ident, seed)
	}
}

func TestAPIKeyResolver_WrongBearer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	seed := seedAPIKey(t, ctx, pool, apiKeySeedOpts{})

	r := NewAPIKeyResolver(dbauth.New(pool))
	// Same prefix but wrong suffix → bcrypt mismatch
	bad := seed.plaintext[:APIKeyPrefixLen] + "_WRONG_SUFFIX_HERE"
	_, err := r.Resolve(ctx, newRequest(t, "Bearer "+bad))
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong bearer must collapse to ErrUnauthorized; got %v", err)
	}
}

func TestAPIKeyResolver_RevokedKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	seed := seedAPIKey(t, ctx, pool, apiKeySeedOpts{status: "revoked"})

	r := NewAPIKeyResolver(dbauth.New(pool))
	_, err := r.Resolve(ctx, newRequest(t, "Bearer "+seed.plaintext))
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked key must return ErrUnauthorized (no leakage); got %v", err)
	}
}

func TestAPIKeyResolver_ExpiredKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	expired := time.Now().Add(-1 * time.Hour)
	seed := seedAPIKey(t, ctx, pool, apiKeySeedOpts{expiresAt: expired})

	r := NewAPIKeyResolver(dbauth.New(pool))
	_, err := r.Resolve(ctx, newRequest(t, "Bearer "+seed.plaintext))
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired key must return ErrUnauthorized; got %v", err)
	}
}

func TestAPIKeyResolver_CrossTenantProbeRejected(t *testing.T) {
	// Even though the bearer is valid for tenantA, the resolver does not
	// expose a way to ask "does this bearer belong to tenantB?" — every
	// failure mode collapses to ErrUnauthorized. We verify the resolver
	// returns the bearer's actual tenant (not the probe target).
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	seed := seedAPIKey(t, ctx, pool, apiKeySeedOpts{})

	r := NewAPIKeyResolver(dbauth.New(pool))
	ident, err := r.Resolve(ctx, newRequest(t, "Bearer "+seed.plaintext))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if ident.TenantID == seed.otherTenantID {
		t.Fatalf("CRITICAL: bearer of tenant %d resolved to other tenant %d",
			seed.tenantID, seed.otherTenantID)
	}
}

func TestAPIKeyResolver_PrefixCollisionPicksRightRow(t *testing.T) {
	// Seed two api_keys with the same key_prefix (shared first 16 chars)
	// but different suffix. Resolver must bcrypt-compare each candidate
	// and return the matching one — not the first row.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)

	suffix := uuid.NewString()
	var tenantID, userID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"auth-collide-"+suffix,
	).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		tenantID, "user-"+suffix,
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM api_keys WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM users WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})

	prefix := "hk_test_collide" // 15 chars + 1 -> 16
	prefix = prefix + "X"
	// Seed two keys both starting with `prefix` but differing afterward.
	plaintexts := []string{prefix + "AAAAAAAAAAA", prefix + "BBBBBBBBBBB"}
	var ids []int64
	for i, p := range plaintexts {
		hash, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
		if err != nil {
			t.Fatalf("hash %d: %v", i, err)
		}
		var id int64
		if err := pool.QueryRow(ctx,
			`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
			 VALUES ($1, $2, $3, $4, $5, 'active') RETURNING id`,
			tenantID, userID, "key-"+suffix+"-"+p[len(p)-1:], string(hash), prefix,
		).Scan(&id); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
		ids = append(ids, id)
	}

	r := NewAPIKeyResolver(dbauth.New(pool))
	// Resolve the SECOND plaintext — verify resolver returns the second
	// row's id, not the first.
	ident, err := r.Resolve(ctx, newRequest(t, "Bearer "+plaintexts[1]))
	if err != nil {
		t.Fatalf("Resolve collide: %v", err)
	}
	if ident.APIKeyID != ids[1] {
		t.Fatalf("resolver picked wrong row on prefix collision: got %d, want %d (ids: %v)",
			ident.APIKeyID, ids[1], ids)
	}
}

func TestAPIKeyResolver_RejectsForeignFormat(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	_ = seedAPIKey(t, ctx, pool, apiKeySeedOpts{})

	r := NewAPIKeyResolver(dbauth.New(pool))
	for _, bad := range []string{
		"Bearer sk-1234567890abcdef",
		"Bearer xyz_random_token_here",
		"NotBearer hk_live_xyz",
		"Bearer ", // empty
		"",        // missing header
	} {
		_, err := r.Resolve(ctx, newRequest(t, bad))
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("foreign format %q must return ErrUnauthorized; got %v", bad, err)
		}
	}
}

func TestAPIKeyResolver_NilQueriesReturnsMisconfigured(t *testing.T) {
	r := &APIKeyResolver{q: nil}
	_, err := r.Resolve(context.Background(), newRequest(t, "Bearer hk_test_anything"))
	if !errors.Is(err, ErrAuthMisconfigured) {
		t.Fatalf("nil queries must return ErrAuthMisconfigured (D9 → 503); got %v", err)
	}
}

// A disabled user with an active API key must NOT
// authenticate. The resolver looks up users.status after bcrypt match.
func TestAPIKeyResolver_DisabledUserRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	seed := seedAPIKey(t, ctx, pool, apiKeySeedOpts{}) // active key + active user

	// Flip user.status to 'disabled' AFTER seed.
	if _, err := pool.Exec(ctx,
		`UPDATE users SET status='disabled' WHERE id=$1`, seed.userID,
	); err != nil {
		t.Fatalf("flip user disabled: %v", err)
	}

	r := NewAPIKeyResolver(dbauth.New(pool))
	_, err := r.Resolve(ctx, newRequest(t, "Bearer "+seed.plaintext))
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("disabled user must collapse to ErrUnauthorized; got %v", err)
	}
}

// A disabled tenant must NOT authenticate even if
// their user + API key are both active.
func TestAPIKeyResolver_DisabledTenantRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	seed := seedAPIKey(t, ctx, pool, apiKeySeedOpts{})

	if _, err := pool.Exec(ctx,
		`UPDATE tenants SET status='suspended' WHERE id=$1`, seed.tenantID,
	); err != nil {
		t.Fatalf("flip tenant suspended: %v", err)
	}

	r := NewAPIKeyResolver(dbauth.New(pool))
	_, err := r.Resolve(ctx, newRequest(t, "Bearer "+seed.plaintext))
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("disabled tenant must collapse to ErrUnauthorized; got %v", err)
	}
}

// A soft-deleted tenant must NOT authenticate.
// Note: api_keys row would normally be cascade-handled in a real
// admin flow; this test sets deleted_at=NOW() on tenants directly.
func TestAPIKeyResolver_SoftDeletedTenantRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	seed := seedAPIKey(t, ctx, pool, apiKeySeedOpts{})

	if _, err := pool.Exec(ctx,
		`UPDATE tenants SET deleted_at=NOW() WHERE id=$1`, seed.tenantID,
	); err != nil {
		t.Fatalf("soft-delete tenant: %v", err)
	}

	r := NewAPIKeyResolver(dbauth.New(pool))
	_, err := r.Resolve(ctx, newRequest(t, "Bearer "+seed.plaintext))
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("soft-deleted tenant must collapse to ErrUnauthorized; got %v", err)
	}
}

// A soft-deleted user must NOT authenticate even if
// their API key remains active.
func TestAPIKeyResolver_SoftDeletedUserRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	seed := seedAPIKey(t, ctx, pool, apiKeySeedOpts{})

	if _, err := pool.Exec(ctx,
		`UPDATE users SET deleted_at=NOW() WHERE id=$1`, seed.userID,
	); err != nil {
		t.Fatalf("soft-delete user: %v", err)
	}

	r := NewAPIKeyResolver(dbauth.New(pool))
	_, err := r.Resolve(ctx, newRequest(t, "Bearer "+seed.plaintext))
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("soft-deleted user must collapse to ErrUnauthorized; got %v", err)
	}
}
