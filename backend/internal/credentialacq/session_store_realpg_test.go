package credentialacq

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
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

// TestCreateRejectsCrossTenantProviderAccountPG guards S2-010 against real Postgres:
// credential_acquisition_flow_sessions must enforce that its tenant_id matches the
// referenced Provider Account's tenant. The broken schema used only
// provider_account_id REFERENCES provider_accounts(id), so tenant A could create a
// flow row pointing at tenant B's account; finalization checks were too late to
// protect the flow state itself.
//
// Mutation check: replace the composite FK with the old single-column FK and the
// cross-tenant insert succeeds (err==nil) -> red. Control: tenant B with tenant
// B's account still creates a normal flow.
func TestCreateRejectsCrossTenantProviderAccountPG(t *testing.T) {
	ctx := context.Background()
	pool := openCredentialAcqTestPool(t, ctx)
	now := time.Now().UTC()
	store := NewPostgresSessionStore(pool).WithNow(func() time.Time { return now })
	tenantA, _ := seedCredentialAcqProviderAccount(t, ctx, pool, "a-"+uuid.NewString())
	tenantB, accountB := seedCredentialAcqProviderAccount(t, ctx, pool, "b-"+uuid.NewString())

	_, err := store.Create(ctx, Session{
		ID:                   uuid.NewString(),
		TenantID:             tenantA,
		ProviderAccountID:    accountB,
		Vendor:               "openai",
		AuthMode:             "api_key",
		Kind:                 FlowKindPaste,
		Status:               StatusStarted,
		ActorID:              "admin-1",
		ActorRole:            "platform_admin",
		ClientIdentitySource: ClientSourceNone,
		RequestedScopes:      []string{},
		RedactedContext:      map[string]any{"case": "cross_tenant_fk"},
		ExpiresAt:            now.Add(10 * time.Minute),
	})
	if err == nil {
		t.Fatalf("cross-tenant flow insert succeeded: tenant_id=%d provider_account_id=%d; want FK rejection", tenantA, accountB)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("cross-tenant flow insert err=%T %[1]v, want postgres foreign_key_violation", err)
	}

	created, err := store.Create(ctx, Session{
		ID:                   uuid.NewString(),
		TenantID:             tenantB,
		ProviderAccountID:    accountB,
		Vendor:               "openai",
		AuthMode:             "api_key",
		Kind:                 FlowKindPaste,
		Status:               StatusStarted,
		ActorID:              "admin-1",
		ActorRole:            "platform_admin",
		ClientIdentitySource: ClientSourceNone,
		RequestedScopes:      []string{},
		RedactedContext:      map[string]any{"case": "same_tenant_control"},
		ExpiresAt:            now.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("same-tenant control insert failed: %v", err)
	}
	if created.TenantID != tenantB || created.ProviderAccountID != accountB {
		t.Fatalf("same-tenant control row=(tenant=%d account=%d), want (%d,%d)", created.TenantID, created.ProviderAccountID, tenantB, accountB)
	}
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

// TestUpdateStatusAndCancelRejectTerminalFlowsPG guards S1-012 against real Postgres: it proves the two
// CAS predicates the fake test double cannot prove (the fake reimplements the rule in Go via
// isTerminalStatus, so it stays green even if the SQL were deleted):
//
//	UpdateStatus: WHERE ... AND status NOT IN ('finalized','cancelled','expired','failed')
//	Cancel:       WHERE ... AND status NOT IN ('finalized','cancelled','expired','failed')   // 'failed' added by S1-012
//
// Without these, the Get→write TOCTOU lets a concurrent Cancel/expire be overwritten — e.g.
// CompleteOAuthCallback's UpdateStatus(callback_received/validated) would resurrect an already-cancelled
// flow, and a failed flow could be flipped to cancelled.
//
// Mutation checks:
//   - delete `AND status NOT IN (...)` from UpdateStatus's SQL → case (b) updates the cancelled row
//     (err==nil) → red.
//   - drop `'failed'` from Cancel's NOT IN set → case (c) cancels the failed row (err==nil) → red.
//
// Discriminating controls (a)/(d) prove the predicates are precise, not blanket: an active flow is still
// cancellable, and a started flow can still be advanced.
func TestUpdateStatusAndCancelRejectTerminalFlowsPG(t *testing.T) {
	ctx := context.Background()
	pool := openCredentialAcqTestPool(t, ctx)
	keys, err := credentialstore.NewStaticKeyProvider("test-v1", bytes.Repeat([]byte{6}, 32))
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	now := time.Now().UTC()
	store := NewPostgresSessionStoreWithKeys(pool, keys).WithNow(func() time.Time { return now })
	tenantID, paID := seedCredentialAcqProviderAccount(t, ctx, pool, uuid.NewString())

	mk := func(status FlowStatus) string {
		id := uuid.NewString()
		if _, err := store.Create(ctx, Session{
			ID: id, TenantID: tenantID, ProviderAccountID: paID, Vendor: "openai", AuthMode: "chatgpt_oauth",
			Kind: FlowKindOAuth, Status: status, ActorID: "admin-1", ActorRole: "platform_admin",
			ClientIdentitySource: ClientSourcePublicCLI,
			RequestedScopes:      []string{"openid"},
			RedactedContext:      map[string]any{"path": "oauth"},
			ExpiresAt:            now.Add(10 * time.Minute),
		}); err != nil {
			t.Fatalf("Create %s: %v", status, err)
		}
		return id
	}

	// (a) control: an active (started) flow is cancellable.
	active := mk(StatusStarted)
	cancelled, err := store.Cancel(ctx, active)
	if err != nil {
		t.Fatalf("Cancel of active flow: %v", err)
	}
	if cancelled.Status != StatusCancelled {
		t.Fatalf("Cancel(active).Status=%q want cancelled", cancelled.Status)
	}
	// (b) UpdateStatus must not resurrect the now-cancelled flow.
	if _, err := store.UpdateStatus(ctx, active, StatusCallbackReceived, "", ""); !errors.Is(err, ErrFlowReplay) {
		t.Fatalf("UpdateStatus on cancelled flow: err=%v want ErrFlowReplay", err)
	}

	// (c) a failed flow must not be Cancel-able (terminal→terminal flip blocked; 'failed' added by S1-012).
	failedID := mk(StatusStarted)
	if _, err := store.MarkFailed(ctx, failedID, "exchange_failed", "redacted"); err != nil {
		t.Fatalf("MarkFailed of started flow: %v", err)
	}
	if _, err := store.Cancel(ctx, failedID); !errors.Is(err, ErrFlowReplay) {
		t.Fatalf("Cancel on failed flow: err=%v want ErrFlowReplay", err)
	}

	// (d) control: a started flow can still be advanced by UpdateStatus.
	advancing := mk(StatusStarted)
	waiting, err := store.UpdateStatus(ctx, advancing, StatusWaitingForUser, "", "")
	if err != nil {
		t.Fatalf("UpdateStatus advance of started flow: %v", err)
	}
	if waiting.Status != StatusWaitingForUser {
		t.Fatalf("UpdateStatus(started→waiting).Status=%q want waiting_for_user", waiting.Status)
	}
}
