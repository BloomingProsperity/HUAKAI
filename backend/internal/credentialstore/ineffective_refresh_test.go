package credentialstore

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Pure-helper unit tests (no DB required)
// ---------------------------------------------------------------------------

// TestIneffectiveRefreshNextAttemptEffective verifies the DEFAULT/SAFE path:
// when refreshBeforeAt is in the future the helper returns normalNext unchanged.
// Mutation self-check: if the helper returns now+backoff even in the effective
// case this test goes RED.
func TestIneffectiveRefreshNextAttemptEffective(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	refreshBeforeAt := now.Add(5 * time.Minute) // in the future -> effective
	normalNext := time.Time{}                   // NULL / zero sentinel
	got := ineffectiveRefreshNextAttempt(refreshBeforeAt, now, normalNext)
	if got != normalNext {
		t.Fatalf("effective refresh: got next_attempt_at=%v, want normalNext=%v (MUST be unchanged)", got, normalNext)
	}
}

// TestIneffectiveRefreshNextAttemptIneffectiveExact verifies refreshBeforeAt == now
// (boundary: exactly now) is treated as ineffective.
func TestIneffectiveRefreshNextAttemptIneffectiveExact(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	refreshBeforeAt := now // == now -> immediately due -> ineffective
	got := ineffectiveRefreshNextAttempt(refreshBeforeAt, now, time.Time{})
	want := now.Add(IneffectiveRefreshBackoff)
	if got != want {
		t.Fatalf("ineffective (exact now): got=%v want=%v", got, want)
	}
}

// TestIneffectiveRefreshNextAttemptIneffectivePast verifies refreshBeforeAt in
// the past is treated as ineffective.
func TestIneffectiveRefreshNextAttemptIneffectivePast(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	refreshBeforeAt := now.Add(-1 * time.Minute) // past -> ineffective
	got := ineffectiveRefreshNextAttempt(refreshBeforeAt, now, time.Time{})
	want := now.Add(IneffectiveRefreshBackoff)
	if got != want {
		t.Fatalf("ineffective (past): got=%v want=%v", got, want)
	}
}

// TestIneffectiveRefreshBackoffValue asserts the const is exactly 30s so a
// reviewer immediately sees a drift if changed carelessly.
func TestIneffectiveRefreshBackoffValue(t *testing.T) {
	if IneffectiveRefreshBackoff != 30*time.Second {
		t.Fatalf("IneffectiveRefreshBackoff=%v want 30s", IneffectiveRefreshBackoff)
	}
}

// ---------------------------------------------------------------------------
// Integration test — requires HUAKAI_DATABASE_URL (skipped otherwise)
// ---------------------------------------------------------------------------

func TestSaveRefreshSuccessIneffectiveSetsNextAttemptAt(t *testing.T) {
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if strings.TrimSpace(dsn) == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping ineffective-refresh integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("pg ping: %v", err)
	}

	// Seed minimal fixture.
	suffix := fmt.Sprintf("ineff-%d", time.Now().UnixNano())
	var tenantID, providerID, poolGroupID, channelID, providerAccountID int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "ineff-"+suffix).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO providers (tenant_id, code, display_name, upstream_protocol) VALUES ($1, $2, $3, 'openai_chat') RETURNING id`, tenantID, "openai-"+suffix, "OpenAI "+suffix).Scan(&providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`, tenantID, "pool-"+suffix).Scan(&poolGroupID); err != nil {
		t.Fatalf("seed pool_group: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`, tenantID, poolGroupID, "chan-"+suffix).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type, credentials) VALUES ($1,$2,$3,$4,'api_key','{}') RETURNING id`,
		tenantID, providerID, channelID, "pa-"+suffix).Scan(&providerAccountID); err != nil {
		t.Fatalf("seed provider_account: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		pool.Exec(bg, `DELETE FROM account_credentials WHERE tenant_id=$1`, tenantID)
		pool.Exec(bg, `DELETE FROM provider_accounts WHERE tenant_id=$1`, tenantID)
		pool.Exec(bg, `DELETE FROM channels WHERE tenant_id=$1`, tenantID)
		pool.Exec(bg, `DELETE FROM pool_groups WHERE tenant_id=$1`, tenantID)
		pool.Exec(bg, `DELETE FROM providers WHERE tenant_id=$1`, tenantID)
		pool.Exec(bg, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})

	store := NewStore(pool, mustTestKeyProvider(t), DefaultHandlerRegistry())
	meta, err := store.Create(ctx, CreateCredentialInput{
		TenantID: tenantID, ProviderAccountID: providerAccountID,
		Vendor: VendorOpenAI, AuthMode: AuthModeAPIKey,
		Payload: []byte(`{"api_key":"sk-ineff-test"}`),
		ActorID: "test",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	rec := CredentialRecord{
		ID: meta.ID, TenantID: tenantID, ProviderAccountID: providerAccountID,
		Vendor: VendorOpenAI, AuthMode: AuthModeAPIKey,
		CredentialVersion: meta.Version,
		PlaintextPayload:  []byte(`{"api_key":"sk-ineff-test"}`),
	}

	before := time.Now()
	// Pass an accessExpiresAt that is only 1 minute in the future — well inside
	// the 15-minute RefreshWindow — so refreshBeforeAt will be < now (ineffective).
	ineffectiveExpiry := time.Now().Add(1 * time.Minute)
	if err := store.SaveRefreshSuccess(ctx, rec, []byte(`{"api_key":"sk-ineff-test"}`), ineffectiveExpiry, "refresh_succeeded"); err != nil {
		t.Fatalf("SaveRefreshSuccess: %v", err)
	}
	after := time.Now()

	var nextAttemptAt *time.Time
	err = pool.QueryRow(ctx, `SELECT next_attempt_at FROM account_credentials WHERE id=$1`, meta.ID).Scan(&nextAttemptAt)
	if err != nil {
		t.Fatalf("query next_attempt_at: %v", err)
	}
	if nextAttemptAt == nil {
		t.Fatal("ineffective SaveRefreshSuccess: next_attempt_at is NULL, want ~now+30s")
	}
	lo := before.Add(IneffectiveRefreshBackoff)
	hi := after.Add(IneffectiveRefreshBackoff).Add(2 * time.Second) // small fuzz
	if nextAttemptAt.Before(lo) || nextAttemptAt.After(hi) {
		t.Fatalf("next_attempt_at=%v want in [%v, %v]", nextAttemptAt, lo, hi)
	}
}
