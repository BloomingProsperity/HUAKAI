package credentialacq

import (
	"bytes"
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

// openCredentialAcqTestPool 为 integration_pg 风格的测试打开本地 dev Postgres;当
// HUAKAI_DATABASE_URL 未设置时跳过(与 credentialworker pg 测试相同的 env 门控模式)。
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

// seedCredentialAcqProviderAccount 播种 credential_acquisition_flow_sessions 所需的
// tenant -> pool_group -> channel -> provider -> provider_account FK 链,返回
// (tenantID, providerAccountID),并注册 cleanup 回收整条链以及创建的所有 session。
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

func seedCredentialAcqAccountCredential(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, paID int64) int64 {
	t.Helper()
	var credentialID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO account_credentials (
			tenant_id, provider_account_id, vendor, auth_mode, encrypted_payload, key_id, nonce, aad_hash
		 )
		 VALUES ($1, $2, 'openai', 'api_key', $3, 'credential-acq-test-key', $4, 'credential-acq-test-aad')
		 RETURNING id`,
		tenantID, paID, []byte{1, 2, 3}, []byte{4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
	).Scan(&credentialID); err != nil {
		t.Fatalf("seed account_credential: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM credential_acquisition_flow_sessions WHERE result_account_credential_id = $1`, credentialID)
		_, _ = pool.Exec(c, `DELETE FROM account_credentials WHERE id = $1`, credentialID)
	})
	return credentialID
}

// TestCreateRejectsCrossTenantProviderAccountPG 针对真实 Postgres 做守护:
// credential_acquisition_flow_sessions 必须强制其 tenant_id 与所引用
// Provider Account 的 tenant 一致。出错的 schema 只用了
// provider_account_id REFERENCES provider_accounts(id),于是 tenant A 能创建一条
// 指向 tenant B 账号的 flow 行;最终化检查为时已晚,无法
// 保护 flow 状态本身。
//
// 变异检查:把复合 FK 换回旧的单列 FK,跨租户
// 插入就会成功(err==nil)→ 变红。对照:tenant B 用 tenant
// B 自己的账号仍能创建一个正常的 flow。
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

// TestBeginFinalizeCallbackOAuthGatePG 针对真实 Postgres 做守护:它驱动
// 真正的 BeginFinalize SQL predicate
//
//	AND (flow_kind <> 'oauth' OR auth_type IN ('device_code', 'sso') OR status = 'validated')
//
// 这是 fake test double 无法证明的(fake 在 Go 里重实现了该规则,因此即便 SQL 子句被删
// 它也会保持绿色)。一个仍处于 status=started 的 callback 式 PKCE OAuth flow 必须
// 被 UPDATE 排除并穿透到 ErrOAuthRequiresCallback —— 否则一个 started 的 OAuth flow
// 就能用手写的 credentials 体来 finalize,跳过 callback/state/exchange。
//
// 变异检查:从 session_store.go 中 BeginFinalize 的 SQL 里删除 `AND (flow_kind <> 'oauth' ...)`
// 这一行,情形 (a) 就会 finalize(err==nil)→ 变红。区分性
// 对照:(b) 已 validated 的 callback flow 能 finalize;(c) 处于 waiting_for_user 的 device_code flow
// 是豁免的(auth_type=device_code)也能 finalize —— 证明此闸门是精确的,而非对 OAuth 一刀切阻断。
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

	// (a) callback PKCE OAuth(auth_type 默认为 'pkce')仍处于 started → 真实 SQL 将其排除。
	a := mk("11111111-1111-1111-1111-111111111111", StatusStarted, false)
	if _, err := store.BeginFinalize(ctx, a); !errors.Is(err, ErrOAuthRequiresCallback) {
		t.Fatalf("started PKCE OAuth: err=%v, want ErrOAuthRequiresCallback", err)
	}
	// (b) callback OAuth 推进到 validated → finalize 可继续。
	b := mk("22222222-2222-2222-2222-222222222222", StatusValidated, false)
	if _, err := store.BeginFinalize(ctx, b); err != nil {
		t.Fatalf("validated callback OAuth must finalize: %v", err)
	}
	// (c) 处于 waiting_for_user 的 device_code flow → 豁免,finalize 可继续(无 device-code 回归)。
	c := mk("33333333-3333-3333-3333-333333333333", StatusWaitingForUser, true)
	if _, err := store.BeginFinalize(ctx, c); err != nil {
		t.Fatalf("device_code flow must be exempt from callback-validation gate: %v", err)
	}
}

// TestUpdateStatusAndCancelRejectTerminalFlowsPG 针对真实 Postgres 做守护:它证明了
// fake test double 无法证明的两个 CAS predicate(fake 通过
// isTerminalStatus 在 Go 里重实现了该规则,因此即便 SQL 被删它也保持绿色):
//
//	UpdateStatus: WHERE ... AND status NOT IN ('finalized','cancelled','expired','failed')
//
// Cancel: WHERE... AND status NOT IN ('finalized','cancelled','expired','failed') // 后续补入了 'failed'
//
// 没有它们,Get→write 的 TOCTOU 会让并发的 Cancel/expire 被覆盖 —— 例如
// CompleteOAuthCallback 的 UpdateStatus(callback_received/validated)会复活一个已 cancelled 的
// flow,而一个 failed 的 flow 也可能被翻成 cancelled。
//
// 变异检查:
//   - 从 UpdateStatus 的 SQL 中删除 `AND status NOT IN (...)` → 情形 (b) 会更新已 cancelled 的行
//     (err==nil)→ 变红。
//   - 从 Cancel 的 NOT IN 集合中去掉 `'failed'` → 情形 (c) 会 cancel 已 failed 的行(err==nil)→ 变红。
//
// 区分性对照 (a)/(d) 证明这些 predicate 是精确的而非一刀切:一个活跃 flow 仍然
// 可被 cancel,一个 started flow 仍然可以被推进。
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

	// (a) 对照:一个活跃(started)的 flow 可被 cancel。
	active := mk(StatusStarted)
	cancelled, err := store.Cancel(ctx, active)
	if err != nil {
		t.Fatalf("Cancel of active flow: %v", err)
	}
	if cancelled.Status != StatusCancelled {
		t.Fatalf("Cancel(active).Status=%q want cancelled", cancelled.Status)
	}
	// (b) UpdateStatus 不得复活这个已 cancelled 的 flow。
	if _, err := store.UpdateStatus(ctx, active, StatusCallbackReceived, "", ""); !errors.Is(err, ErrFlowReplay) {
		t.Fatalf("UpdateStatus on cancelled flow: err=%v want ErrFlowReplay", err)
	}

	// (c) 一个 failed 的 flow 不得被 Cancel(终态→终态的翻转被阻断;后续补入了 'failed')。
	failedID := mk(StatusStarted)
	if _, err := store.MarkFailed(ctx, failedID, "exchange_failed", "redacted"); err != nil {
		t.Fatalf("MarkFailed of started flow: %v", err)
	}
	if _, err := store.Cancel(ctx, failedID); !errors.Is(err, ErrFlowReplay) {
		t.Fatalf("Cancel on failed flow: err=%v want ErrFlowReplay", err)
	}

	// (d) 对照:一个 started 的 flow 仍可被 UpdateStatus 推进。
	advancing := mk(StatusStarted)
	waiting, err := store.UpdateStatus(ctx, advancing, StatusWaitingForUser, "", "")
	if err != nil {
		t.Fatalf("UpdateStatus advance of started flow: %v", err)
	}
	if waiting.Status != StatusWaitingForUser {
		t.Fatalf("UpdateStatus(started→waiting).Status=%q want waiting_for_user", waiting.Status)
	}
}

// TestCancelFinalizeRaceGuardsPG 守护 finalize/cancel 竞态下真实的 SQL CAS predicate:
// 一旦 BeginFinalize 消费了一个 flow,在凭据创建过程中 Cancel 不得把它翻成 cancelled;
// 一旦一个 flow 已 cancelled,MarkFinalized 不得用一个
// credential id 覆盖它。正常 finalize 和同凭据的幂等 finalize 仍然允许。
//
// 变异检查:
//   - 从 Cancel SQL 删除 `AND consumed_at IS NULL` → 情形 (a) 会 cancel 已 consumed 的行,使
//     ErrFlowReplay 断言变红。
//   - 从 MarkFinalized SQL 删除 `AND status NOT IN ('cancelled', 'expired', 'failed')` → 情形
//     (b) 会把 cancelled 覆盖成 finalized,使 ErrFlowReplay/status/result 断言变红。
func TestCancelFinalizeRaceGuardsPG(t *testing.T) {
	ctx := context.Background()
	pool := openCredentialAcqTestPool(t, ctx)
	now := time.Now().UTC()
	store := NewPostgresSessionStore(pool).WithNow(func() time.Time { return now })
	tenantID, paID := seedCredentialAcqProviderAccount(t, ctx, pool, uuid.NewString())
	credID := seedCredentialAcqAccountCredential(t, ctx, pool, tenantID, paID)

	mk := func(label string) string {
		id := uuid.NewString()
		if _, err := store.Create(ctx, Session{
			ID: id, TenantID: tenantID, ProviderAccountID: paID, Vendor: "openai", AuthMode: "api_key",
			Kind: FlowKindPaste, Status: StatusStarted, ActorID: "admin-1", ActorRole: "platform_admin",
			ClientIdentitySource: ClientSourceNone,
			RequestedScopes:      []string{},
			RedactedContext:      map[string]any{"case": label},
			ExpiresAt:            now.Add(10 * time.Minute),
		}); err != nil {
			t.Fatalf("Create %s: %v", label, err)
		}
		return id
	}

	// (a) BeginFinalize 消费该 flow;并发的 Cancel 必须被当作 replay 拒绝。
	consumedID := mk("consumed_then_cancel")
	if _, err := store.BeginFinalize(ctx, consumedID); err != nil {
		t.Fatalf("BeginFinalize consumed_then_cancel: %v", err)
	}
	if _, err := store.Cancel(ctx, consumedID); !errors.Is(err, ErrFlowReplay) {
		t.Fatalf("Cancel after BeginFinalize: err=%v want ErrFlowReplay", err)
	}
	finalized, err := store.MarkFinalized(ctx, consumedID, credID)
	if err != nil {
		t.Fatalf("MarkFinalized after rejected Cancel: %v", err)
	}
	if finalized.Status != StatusFinalized || finalized.ResultAccountCredentialID != credID {
		t.Fatalf("finalized consumed flow=(status=%q credential=%d), want finalized/%d", finalized.Status, finalized.ResultAccountCredentialID, credID)
	}

	// (b) 在 finalize 之前已 cancelled 的 flow 不得被覆盖成 finalized。
	cancelledID := mk("cancelled_then_finalize")
	cancelled, err := store.Cancel(ctx, cancelledID)
	if err != nil {
		t.Fatalf("Cancel before finalize: %v", err)
	}
	if cancelled.Status != StatusCancelled || cancelled.ResultAccountCredentialID != 0 {
		t.Fatalf("Cancel returned=(status=%q credential=%d), want cancelled/0", cancelled.Status, cancelled.ResultAccountCredentialID)
	}
	if _, err := store.MarkFinalized(ctx, cancelledID, credID); !errors.Is(err, ErrFlowReplay) {
		t.Fatalf("MarkFinalized on cancelled flow: err=%v want ErrFlowReplay", err)
	}
	reloaded, err := store.Get(ctx, cancelledID)
	if err != nil {
		t.Fatalf("Get cancelled flow: %v", err)
	}
	if reloaded.Status != StatusCancelled || reloaded.ResultAccountCredentialID != 0 {
		t.Fatalf("cancelled flow after MarkFinalized=(status=%q credential=%d), want cancelled/0", reloaded.Status, reloaded.ResultAccountCredentialID)
	}

	// (c) 正常的 BeginFinalize -> MarkFinalized 仍然成功;同凭据的重试被接受。
	normalID := mk("normal_finalize")
	if _, err := store.BeginFinalize(ctx, normalID); err != nil {
		t.Fatalf("BeginFinalize normal: %v", err)
	}
	normal, err := store.MarkFinalized(ctx, normalID, credID)
	if err != nil {
		t.Fatalf("MarkFinalized normal: %v", err)
	}
	if normal.Status != StatusFinalized || normal.ResultAccountCredentialID != credID {
		t.Fatalf("normal finalized=(status=%q credential=%d), want finalized/%d", normal.Status, normal.ResultAccountCredentialID, credID)
	}
	retry, err := store.MarkFinalized(ctx, normalID, credID)
	if err != nil {
		t.Fatalf("MarkFinalized idempotent retry: %v", err)
	}
	if retry.Status != StatusFinalized || retry.ResultAccountCredentialID != credID {
		t.Fatalf("retry finalized=(status=%q credential=%d), want finalized/%d", retry.Status, retry.ResultAccountCredentialID, credID)
	}
}

// TestCompleteOAuthCallbackSerializesFlowPG 针对真实 Postgres 守护 ACF-1:
// 同一个 OAuth flow 只能有一个回调把状态从 pending 推进到 callback_received 并进入 exchange。
// 第二个并发/迟到回调必须在 exchange 前得到 ErrFlowReplay,不能把 callback_received 或
// validated 覆写成 failed。
//
// §14 变异:把 CompleteOAuthCallback 的 UpdateStatusFrom 改回 UpdateStatus,并删除入口处
// callback_received/validated replay 守卫。第二个回调会进入 exchange,随后把 flow 标成 failed
// 或让第一个回调的 validated 写失败,本测试的 exchange 次数/最终状态断言会变红。
func TestCompleteOAuthCallbackSerializesFlowPG(t *testing.T) {
	ctx := context.Background()
	pool := openCredentialAcqTestPool(t, ctx)
	now := time.Now().UTC()
	store := NewPostgresSessionStore(pool).WithNow(func() time.Time { return now })
	tenantID, paID := seedCredentialAcqProviderAccount(t, ctx, pool, uuid.NewString())
	flowID := uuid.NewString()
	state := "serial-state"
	if _, err := store.Create(ctx, Session{
		ID: flowID, TenantID: tenantID, ProviderAccountID: paID, Vendor: "openai", AuthMode: "chatgpt_oauth",
		Kind: FlowKindOAuth, Status: StatusStarted, ActorID: "admin-1", ActorRole: "platform_admin",
		ClientIdentitySource: ClientSourcePublicCLI,
		StateHash:            HashOAuthState(state),
		RequestedScopes:      []string{"openid"},
		RedactedContext:      map[string]any{"case": "oauth_callback_serial"},
		ExpiresAt:            now.Add(10 * time.Minute),
	}); err != nil {
		t.Fatalf("Create flow: %v", err)
	}

	type callbackResult struct {
		candidate CredentialCandidate
		session   Session
		err       error
	}
	enteredExchange := make(chan struct{})
	releaseExchange := make(chan struct{})
	firstDone := make(chan callbackResult, 1)
	var exchangeCalls int32

	go func() {
		candidate, session, err := CompleteOAuthCallback(ctx, store, flowID, state, "first-code",
			func(context.Context, Session, string) (CredentialCandidate, error) {
				if atomic.AddInt32(&exchangeCalls, 1) == 1 {
					close(enteredExchange)
				}
				<-releaseExchange
				return CredentialCandidate{
					TenantID: tenantID, ProviderAccountID: paID,
					Vendor: "openai", AuthMode: "chatgpt_oauth",
					Payload: samplePayloadForMode("openai", "chatgpt_oauth"),
				}, nil
			})
		firstDone <- callbackResult{candidate: candidate, session: session, err: err}
	}()

	select {
	case <-enteredExchange:
	case <-time.After(3 * time.Second):
		t.Fatal("first callback did not reach exchange")
	}

	lateExchangeCalled := false
	_, lateSession, lateErr := CompleteOAuthCallback(ctx, store, flowID, state, "late-code",
		func(context.Context, Session, string) (CredentialCandidate, error) {
			lateExchangeCalled = true
			return CredentialCandidate{}, errors.New("late callback must not exchange")
		})
	if !errors.Is(lateErr, ErrFlowReplay) {
		t.Fatalf("late callback while first in-flight: err=%v want ErrFlowReplay", lateErr)
	}
	if lateExchangeCalled {
		t.Fatal("late callback entered exchange while first callback owned the flow")
	}
	if lateSession.Status != StatusCallbackReceived {
		t.Fatalf("late callback saw status=%q want callback_received", lateSession.Status)
	}

	close(releaseExchange)
	first := <-firstDone
	if first.err != nil {
		t.Fatalf("first callback: %v", first.err)
	}
	if first.session.Status != StatusValidated {
		t.Fatalf("first callback status=%q want validated", first.session.Status)
	}
	if first.candidate.ProviderAccountID != paID {
		t.Fatalf("candidate provider_account_id=%d want %d", first.candidate.ProviderAccountID, paID)
	}
	if got := atomic.LoadInt32(&exchangeCalls); got != 1 {
		t.Fatalf("exchange calls=%d want 1", got)
	}

	afterValidatedCalled := false
	_, afterValidated, err := CompleteOAuthCallback(ctx, store, flowID, state, "after-validated-code",
		func(context.Context, Session, string) (CredentialCandidate, error) {
			afterValidatedCalled = true
			return CredentialCandidate{}, errors.New("validated callback must not exchange")
		})
	if !errors.Is(err, ErrFlowReplay) {
		t.Fatalf("callback after validated: err=%v want ErrFlowReplay", err)
	}
	if afterValidatedCalled {
		t.Fatal("callback after validated entered exchange")
	}
	if afterValidated.Status != StatusValidated {
		t.Fatalf("callback after validated returned status=%q want validated", afterValidated.Status)
	}
	reloaded, err := store.Get(ctx, flowID)
	if err != nil {
		t.Fatalf("Get flow: %v", err)
	}
	if reloaded.Status != StatusValidated || reloaded.ErrorClass != "" {
		t.Fatalf("reloaded flow=(status=%q error_class=%q), want validated/no error", reloaded.Status, reloaded.ErrorClass)
	}
}
