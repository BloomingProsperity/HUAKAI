package credentialacq

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

// openCredentialAcqTestPool opens the local dev Postgres for integration_pg-style tests; it skips when
// HUAKAI_DATABASE_URL is unset (same env-gated pattern as the credentialworker pg tests).
func openCredentialAcqTestPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping credentialacq integration_pg")
	}
	p, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// seedCredentialAcqProviderAccount seeds the tenant -> pool_group -> channel -> provider ->
// provider_account FK chain that credential_acquisition_flow_sessions requires, returning
// (tenantID, providerAccountID) with a cleanup that unwinds the chain plus any sessions created.
func seedCredentialAcqProviderAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string) (int64, int64) {
	t.Helper()
	var tenantID int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "ca-tenant-"+suffix).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	var poolGroupID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name, top_k_default, capability_default, allow_last_resort)
		 VALUES ($1, $2, 1, 'exact_capability_only', false) RETURNING id`,
		tenantID, "ca-pg-"+suffix,
	).Scan(&poolGroupID); err != nil {
		t.Fatalf("seed pool_group: %v", err)
	}
	var channelID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		tenantID, poolGroupID, "ca-ch-"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	var providerID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, $3, 'anthropic_messages') RETURNING id`,
		tenantID, "ca-prv-"+suffix, "ca-provider-"+suffix,
	).Scan(&providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	var paID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type)
		 VALUES ($1, $2, $3, $4, 'oauth') RETURNING id`,
		tenantID, providerID, channelID, "ca-pa-"+suffix,
	).Scan(&paID); err != nil {
		t.Fatalf("seed provider_account: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM credential_acquisition_flow_sessions WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM provider_accounts WHERE id = $1`, paID)
		_, _ = pool.Exec(c, `DELETE FROM providers WHERE id = $1`, providerID)
		_, _ = pool.Exec(c, `DELETE FROM channels WHERE id = $1`, channelID)
		_, _ = pool.Exec(c, `DELETE FROM pool_groups WHERE id = $1`, poolGroupID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id = $1`, tenantID)
	})
	return tenantID, paID
}

// TestBeginFinalizeCallbackOAuthGatePG guards S1-010 [codex P2] against real Postgres: it exercises the
// actual BeginFinalize SQL predicate
//
//	AND (flow_kind <> 'oauth' OR auth_type IN ('device_code', 'sso') OR status = 'validated')
//
// which the fake test double cannot prove (the fake reimplements the rule in Go, so it would stay green
// even if the SQL clause were deleted). A callback-style PKCE OAuth flow still at status=started must be
// excluded by the UPDATE and fall through to ErrOAuthRequiresCallback — otherwise a started OAuth flow
// could be finalized with a hand-written credentials body, skipping callback/state/exchange.
//
// Mutation check: remove the `AND (flow_kind <> 'oauth' ...)` line from updateProviderAccountHealth's
// sibling BeginFinalize SQL in session_store.go and case (a) finalizes (err==nil) → red. Discriminating
// controls: (b) a validated callback flow finalizes; (c) a device_code flow at waiting_for_user is
// EXEMPT (auth_type=device_code) and finalizes — proving the gate is precise, not a blanket OAuth block.
func TestBeginFinalizeCallbackOAuthGatePG(t *testing.T) {
	ctx := context.Background()
	pool := openCredentialAcqTestPool(t, ctx)
	keys, err := credentialstore.NewStaticKeyProvider("test-v1", bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	// 真实当前时间:BeginFinalize 的 expires_at > NOW() 用 DB 的 NOW(),Go 侧 s.now() 必须与之一致,
	// 否则固定时间会让 expires_at 相对 DB 提前过期,行被 SQL 以"过期"而非"callback gate"排除(假阳性)。
	now := time.Now().UTC()
	store := NewPostgresSessionStoreWithKeys(pool, keys).WithNow(func() time.Time { return now })
	tenantID, paID := seedCredentialAcqProviderAccount(t, ctx, pool, uuid.NewString())

	mk := func(id string, status FlowStatus, deviceCode bool) string {
		if _, err := store.Create(ctx, Session{
			ID: id, TenantID: tenantID, ProviderAccountID: paID, Vendor: "openai", AuthMode: "chatgpt_oauth",
			Kind: FlowKindOAuth, Status: status, ActorID: "admin-1", ActorRole: "platform_admin",
			ClientIdentitySource: ClientSourcePublicCLI,
			RequestedScopes:      []string{"openid"},
			RedactedContext:      map[string]any{"path": "oauth"},
			ExpiresAt:            now.Add(10 * time.Minute),
		}); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
		if deviceCode {
			if err := store.SetAuthPayload(ctx, id, AuthTypeDeviceCode, map[string]any{
				"auth_type": string(AuthTypeDeviceCode), "device_code": "dev",
				"token_url": "https://device.example.test/token", "client_id": "c",
			}); err != nil {
				t.Fatalf("SetAuthPayload device_code: %v", err)
			}
		}
		return id
	}

	// (a) callback PKCE OAuth (auth_type defaults to 'pkce') still at started → real SQL excludes it.
	a := mk("11111111-1111-1111-1111-111111111111", StatusStarted, false)
	if _, err := store.BeginFinalize(ctx, a); !errors.Is(err, ErrOAuthRequiresCallback) {
		t.Fatalf("started PKCE OAuth: err=%v, want ErrOAuthRequiresCallback", err)
	}
	// (b) callback OAuth advanced to validated → finalize proceeds.
	b := mk("22222222-2222-2222-2222-222222222222", StatusValidated, false)
	if _, err := store.BeginFinalize(ctx, b); err != nil {
		t.Fatalf("validated callback OAuth must finalize: %v", err)
	}
	// (c) device_code flow at waiting_for_user → EXEMPT, finalize proceeds (no device-code regression).
	c := mk("33333333-3333-3333-3333-333333333333", StatusWaitingForUser, true)
	if _, err := store.BeginFinalize(ctx, c); err != nil {
		t.Fatalf("device_code flow must be exempt from callback-validation gate: %v", err)
	}
}
