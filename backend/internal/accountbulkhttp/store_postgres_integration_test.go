//go:build integration_pg

package accountbulkhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker"
	rootdb "github.com/BloomingProsperity/HUAKAI/internal/db"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/provideraccountrecovery"
)

type bulkFixture struct {
	tenantID, otherTenantID int64
	poolID, otherPoolID     int64
	chanA, chanB, chanOther int64
	providerA, providerB    int64
	accounts                []int64 // 0,1 在 chanA(providerA/api_key);2 在 chanB(providerB/claude_ai_oauth);3 已删除
}

func openBulkPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration test")
	}
	p, err := rootdb.Open(ctx, rootdb.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

func seedBulkFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) bulkFixture {
	t.Helper()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	var fx bulkFixture
	scan := func(dst *int64, sql string, args ...any) {
		t.Helper()
		if err := pool.QueryRow(ctx, sql, args...).Scan(dst); err != nil {
			t.Fatalf("%s: %v", sql[:40], err)
		}
	}
	scan(&fx.tenantID, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "bulk-"+suffix)
	scan(&fx.otherTenantID, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "bulk-other-"+suffix)
	scan(&fx.providerA, `INSERT INTO providers (tenant_id, code, display_name, upstream_protocol) VALUES ($1, $2, 'A', 'openai_chat') RETURNING id`, fx.tenantID, "bulk-a-"+suffix)
	scan(&fx.providerB, `INSERT INTO providers (tenant_id, code, display_name, upstream_protocol) VALUES ($1, $2, 'B', 'anthropic_messages') RETURNING id`, fx.tenantID, "bulk-b-"+suffix)
	scan(&fx.poolID, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`, fx.tenantID, "bulk-pool-"+suffix)
	scan(&fx.chanA, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, 'chan-a') RETURNING id`, fx.tenantID, fx.poolID)
	scan(&fx.chanB, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, 'chan-b') RETURNING id`, fx.tenantID, fx.poolID)
	scan(&fx.otherPoolID, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`, fx.otherTenantID, "bulk-pool-other-"+suffix)
	scan(&fx.chanOther, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, 'chan-other') RETURNING id`, fx.otherTenantID, fx.otherPoolID)
	type acct struct {
		channel, provider int64
		vendor, mode      string
		deleted           bool
	}
	specs := []acct{
		{fx.chanA, fx.providerA, "openai", "api_key", false},
		{fx.chanA, fx.providerA, "openai", "api_key", false},
		{fx.chanB, fx.providerB, "anthropic", "claude_ai_oauth", false},
		{fx.chanA, fx.providerA, "openai", "api_key", true},
	}
	for i, a := range specs {
		var id int64
		var deletedAt *time.Time
		if a.deleted {
			d := time.Now().Add(-time.Minute)
			deletedAt = &d
		}
		scan(&id, `INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type, enabled, health_state, priority, static_weight, deleted_at)
			VALUES ($1, $2, $3, $4, 'api_key', true, 'healthy', 10, 1, $5) RETURNING id`,
			fx.tenantID, a.provider, a.channel, fmt.Sprintf("bulk-%s-%d", suffix, i), deletedAt)
		fx.accounts = append(fx.accounts, id)
		if _, err := pool.Exec(ctx, `INSERT INTO account_credentials (tenant_id, provider_account_id, vendor, auth_mode, state, credential_version,
				encrypted_payload, key_id, nonce, aad_hash) VALUES ($1, $2, $3, $4, 'active', 1, $5, 'test-key', $6, $7)`,
			fx.tenantID, id, a.vendor, a.mode, []byte("ciphertext"), []byte("nonce-12345678"), fmt.Sprintf("aad-bulk-%s-%d", suffix, i)); err != nil {
			t.Fatalf("insert credential %d: %v", i, err)
		}
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, tenant := range []int64{fx.tenantID, fx.otherTenantID} {
			_, _ = pool.Exec(ctx, `DELETE FROM admin_audit_events WHERE tenant_id = $1`, tenant)
			_, _ = pool.Exec(ctx, `DELETE FROM account_credentials WHERE tenant_id = $1`, tenant)
			_, _ = pool.Exec(ctx, `DELETE FROM provider_accounts WHERE tenant_id = $1`, tenant)
			_, _ = pool.Exec(ctx, `DELETE FROM channels WHERE tenant_id = $1`, tenant)
			_, _ = pool.Exec(ctx, `DELETE FROM pool_groups WHERE tenant_id = $1`, tenant)
			_, _ = pool.Exec(ctx, `DELETE FROM providers WHERE tenant_id = $1`, tenant)
			_, _ = pool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, tenant)
		}
	})
	return fx
}

func countAudit(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, action string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM admin_audit_events WHERE tenant_id = $1 AND action = $2`, tenantID, action).Scan(&n); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	return n
}

func eligibleIDs(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, poolID int64) map[int64]bool {
	t.Helper()
	rows, err := dbbilling.New(pool).ListEligibleAccountsByPoolGroup(ctx, dbbilling.ListEligibleAccountsByPoolGroupParams{
		TenantID: tenantID, PoolGroupID: poolID, RequestedModel: "any", RequestedProtocolFamily: "", RequiredCapabilities: []string{},
	})
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	out := map[int64]bool{}
	for _, r := range rows {
		out[r.ID] = true
	}
	return out
}

// TestPostgresStoreFieldUpdateAndConvergence:逐项事务在真实 schema 上——禁用写入后候选查询立即不再命中;
// 已达期望态 skipped 不写审计;他租户 / 已删除 → not_found 且不写;dry-run 不写不审计;每个成功项一条逐项审计;
// 批次审计带计数。判别:去掉 LockProviderAccountForBulk 的租户谓词 → 他租户断言红;去掉 deleted_at 谓词 → 已删除断言红。
func TestPostgresStoreFieldUpdateAndConvergence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openBulkPool(t, ctx)
	fx := seedBulkFixture(t, ctx, pool)
	store := NewPostgresStore(pool)
	disabled := false

	before := eligibleIDs(t, ctx, pool, fx.tenantID, fx.poolID)
	if !before[fx.accounts[0]] || !before[fx.accounts[1]] {
		t.Fatalf("前置:账号应在候选集中: %v", before)
	}
	base := FieldUpdate{TenantID: fx.tenantID, ActorID: "op-1", ActorRole: "tenant_operator", RequestID: "req-bulk-1", Reason: "维护"}

	dry := base
	dry.AccountID, dry.Enabled, dry.DryRun = fx.accounts[0], &disabled, true
	if res, err := store.ApplyFieldUpdate(ctx, dry); err != nil || res.Status != StatusWouldApply || res.Detail["before"] == nil {
		t.Fatalf("dry-run 应 would_apply 并带 before/after: %+v err=%v", res, err)
	}
	if n := countAudit(t, ctx, pool, fx.tenantID, "update_provider_account"); n != 0 {
		t.Fatalf("dry-run 不得写审计: %d", n)
	}

	real := base
	real.AccountID, real.Enabled = fx.accounts[0], &disabled
	res, err := store.ApplyFieldUpdate(ctx, real)
	if err != nil || res.Status != StatusSucceeded {
		t.Fatalf("禁用应成功: %+v err=%v", res, err)
	}
	after := eligibleIDs(t, ctx, pool, fx.tenantID, fx.poolID)
	if after[fx.accounts[0]] || !after[fx.accounts[1]] {
		t.Fatalf("禁用后候选查询必须立即不再命中该账号: %v", after)
	}
	if res, err := store.ApplyFieldUpdate(ctx, real); err != nil || res.Status != StatusSkipped || res.Code != CodeAlreadyInDesiredState {
		t.Fatalf("重复禁用应 skipped: %+v err=%v", res, err)
	}
	if n := countAudit(t, ctx, pool, fx.tenantID, "update_provider_account"); n != 1 {
		t.Fatalf("只有真正写入的那次留逐项审计: %d", n)
	}

	foreign := base
	foreign.TenantID, foreign.AccountID, foreign.Enabled = fx.otherTenantID, fx.accounts[1], &disabled
	if res, err := store.ApplyFieldUpdate(ctx, foreign); err != nil || res.Code != CodeNotFound {
		t.Fatalf("他租户必须 not_found: %+v err=%v", res, err)
	}
	deleted := base
	deleted.AccountID, deleted.Enabled = fx.accounts[3], &disabled
	if res, err := store.ApplyFieldUpdate(ctx, deleted); err != nil || res.Code != CodeNotFound {
		t.Fatalf("已删除必须 not_found: %+v err=%v", res, err)
	}
	var stillEnabled bool
	if err := pool.QueryRow(ctx, `SELECT enabled FROM provider_accounts WHERE id = $1`, fx.accounts[1]).Scan(&stillEnabled); err != nil || !stillEnabled {
		t.Fatalf("他租户请求不得改动账号: enabled=%v err=%v", stillEnabled, err)
	}

	prio := int32(77)
	p := base
	p.AccountID, p.Priority = fx.accounts[1], &prio
	if res, err := store.ApplyFieldUpdate(ctx, p); err != nil || res.Status != StatusSucceeded {
		t.Fatalf("改优先级应成功: %+v err=%v", res, err)
	}
	var got int32
	if err := pool.QueryRow(ctx, `SELECT priority FROM provider_accounts WHERE id = $1`, fx.accounts[1]).Scan(&got); err != nil || got != 77 {
		t.Fatalf("优先级应写入: %d err=%v", got, err)
	}

	id, err := store.InsertBatchAudit(ctx, BatchAudit{TenantID: fx.tenantID, ActorID: "op-1", ActorRole: "tenant_operator", RequestID: "req-bulk-1", Reason: "维护", Action: ActionSetEnabled,
		Summary: map[string]any{"total": 4, "succeeded": 1, "failed": 2}})
	if err != nil || id <= 0 || countAudit(t, ctx, pool, fx.tenantID, "bulk_provider_accounts") != 1 {
		t.Fatalf("批次审计应落库: id=%d err=%v", id, err)
	}
}

// TestPostgresStoreMoveChannel:改渠道——目标渠道必须同租户(他租户渠道 channel_not_found);把 anthropic/oauth
// 账号移入 openai/api_key 的渠道触发高混合风险,未确认 → would_reject/失败并附报告;确认后写入并留审计;
// 已在目标渠道 → skipped。判别:去掉 GetChannelForBulkMove 的租户谓词 → 他租户渠道断言红;去掉风险判定 → 未确认断言红。
func TestPostgresStoreMoveChannel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openBulkPool(t, ctx)
	fx := seedBulkFixture(t, ctx, pool)
	store := NewPostgresStore(pool)
	base := ChannelMove{TenantID: fx.tenantID, ActorID: "op-1", ActorRole: "tenant_operator", RequestID: "req-bulk-2"}

	other := base
	other.AccountID, other.TargetChannelID = fx.accounts[0], fx.chanOther
	if res, err := store.MoveChannel(ctx, other); err != nil || res.Code != CodeChannelNotFound {
		t.Fatalf("他租户渠道必须拒绝: %+v err=%v", res, err)
	}
	same := base
	same.AccountID, same.TargetChannelID = fx.accounts[0], fx.chanA
	if res, err := store.MoveChannel(ctx, same); err != nil || res.Code != CodeInvalidTargetSameChannel {
		t.Fatalf("已在目标渠道应 skipped: %+v err=%v", res, err)
	}

	risky := base
	risky.AccountID, risky.TargetChannelID, risky.DryRun = fx.accounts[2], fx.chanA, true
	res, err := store.MoveChannel(ctx, risky)
	if err != nil || res.Status != StatusWouldReject || res.Code != CodeMixedRiskConfirmRequired || res.Detail["mixed_risk"] == nil {
		t.Fatalf("混合风险未确认(dry-run)应 would_reject 并附报告: %+v err=%v", res, err)
	}
	risky.DryRun = false
	if res, err := store.MoveChannel(ctx, risky); err != nil || res.Status != StatusFailed || res.Code != CodeMixedRiskConfirmRequired {
		t.Fatalf("混合风险未确认应失败: %+v err=%v", res, err)
	}
	var channel int64
	if err := pool.QueryRow(ctx, `SELECT channel_id FROM provider_accounts WHERE id = $1`, fx.accounts[2]).Scan(&channel); err != nil || channel != fx.chanB {
		t.Fatalf("未确认不得写入: channel=%d err=%v", channel, err)
	}
	risky.ConfirmMixedRisk = true
	if res, err := store.MoveChannel(ctx, risky); err != nil || res.Status != StatusSucceeded {
		t.Fatalf("确认后应写入: %+v err=%v", res, err)
	}
	if err := pool.QueryRow(ctx, `SELECT channel_id FROM provider_accounts WHERE id = $1`, fx.accounts[2]).Scan(&channel); err != nil || channel != fx.chanA {
		t.Fatalf("确认后 channel_id 应更新: %d err=%v", channel, err)
	}
	if n := countAudit(t, ctx, pool, fx.tenantID, "move_provider_account_channel"); n != 1 {
		t.Fatalf("改渠道应留一条逐项审计: %d", n)
	}
	// 同源同模式的移动没有混合风险:直接写入。
	plain := base
	plain.AccountID, plain.TargetChannelID = fx.accounts[0], fx.chanB
	if res, err := store.MoveChannel(ctx, plain); err != nil || res.Status != StatusSucceeded {
		t.Fatalf("无风险移动应直接成功: %+v err=%v", res, err)
	}
}

// TestAdminAuditWhitelistKeepsTenantLifecycleAndBulk:0240 必须是 0233 动作并集。
// 判别:白名单漏 create_tenant → 本测试红;漏 bulk_provider_accounts → 批次动作红。
func TestAdminAuditWhitelistKeepsTenantLifecycleAndBulk(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openBulkPool(t, ctx)
	fx := seedBulkFixture(t, ctx, pool)
	for _, action := range []string{"create_tenant", "enable_tenant", "disable_tenant", "delete_tenant", "move_provider_account_channel", "bulk_provider_accounts"} {
		targetType := "tenant"
		if action == "move_provider_account_channel" {
			targetType = "provider_account"
		}
		if action == "bulk_provider_accounts" {
			targetType = "provider_account_batch"
		}
		if _, err := pool.Exec(ctx, `
INSERT INTO admin_audit_events (tenant_id, actor_id, actor_role, action, target_type, reason)
VALUES ($1,'admin_token:376','platform_admin',$2,$3,'并集合同')`, fx.tenantID, action, targetType); err != nil {
			t.Fatalf("0240 必须放行动作 %q: %v", action, err)
		}
	}
}

// TestRefreshByIDPredicateAndStaticMode:GetAccountForRefreshByID 租户/删除谓词;
// api_key 账号资格可为 refreshable,但生产模式检查必须 not_applicable。
// 判别:去掉 SQL 租户谓词 → 他租户断言红;HasRefreshSemantics 走 LoadForRefresh → api_key 变错。
func TestRefreshByIDPredicateAndStaticMode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openBulkPool(t, ctx)
	fx := seedBulkFixture(t, ctx, pool)
	q := dbbilling.New(pool)

	row, err := q.GetAccountForRefreshByID(ctx, dbbilling.GetAccountForRefreshByIDParams{ID: fx.accounts[0], TenantID: fx.tenantID})
	if err != nil || !row.Refreshable {
		t.Fatalf("本租户健康 api_key 账号应能取到资格行: %+v err=%v", row, err)
	}
	if _, err := q.GetAccountForRefreshByID(ctx, dbbilling.GetAccountForRefreshByIDParams{ID: fx.accounts[0], TenantID: fx.otherTenantID}); err == nil {
		t.Fatal("他租户必须 no rows")
	}
	if _, err := q.GetAccountForRefreshByID(ctx, dbbilling.GetAccountForRefreshByIDParams{ID: fx.accounts[3], TenantID: fx.tenantID}); err == nil {
		t.Fatal("已删除必须 no rows")
	}

	store := credentialstore.NewStore(pool, nil, credentialstore.DefaultHandlerRegistry())
	ref := credentialworker.NewAccountCredentialRefresher(store, credentialworker.DefaultModeAdapterRegistry())
	ok, err := ref.HasRefreshSemantics(ctx, fx.tenantID, fx.accounts[0])
	if err != nil || ok {
		t.Fatalf("api_key 必须 not_applicable: ok=%v err=%v", ok, err)
	}
	ok, err = ref.HasRefreshSemantics(ctx, fx.tenantID, fx.accounts[2])
	if err != nil || !ok {
		t.Fatalf("claude_ai_oauth 必须可刷新: ok=%v err=%v", ok, err)
	}
	ok, err = ref.HasRefreshSemantics(ctx, fx.otherTenantID, fx.accounts[0])
	if err != nil || ok {
		t.Fatalf("他租户模式检查必须当不适用: ok=%v err=%v", ok, err)
	}
}

type recoveryChannelStub struct{}

func (recoveryChannelStub) ClearRateLimitByProviderAccount(context.Context, int64, int64, string) (channelhealth.Record, bool, error) {
	return channelhealth.Record{}, false, channelhealth.ErrNotFound
}

func (recoveryChannelStub) ForceActiveByProviderAccount(context.Context, int64, int64, string, string) (channelhealth.Record, bool, error) {
	return channelhealth.Record{}, false, channelhealth.ErrNotFound
}

type staticAdminAuth struct{ ident admin.AdminIdentity }

func (a staticAdminAuth) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return a.ident, nil
}

// TestBulkRecoveryAgainstRealService:批量清限流/恢复走生产 Service+PostgresStore。
// 他租户/已删除 → not_found 且不改本租户行;本租户限流字段被清。
func TestBulkRecoveryAgainstRealService(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openBulkPool(t, ctx)
	fx := seedBulkFixture(t, ctx, pool)
	if _, err := pool.Exec(ctx, `
UPDATE provider_accounts
SET rate_limited_at = NOW(), rate_limit_reset_at = NOW() + interval '1 hour', rate_limit_reason = 'rate_limit_rpm'
WHERE id = $1`, fx.accounts[0]); err != nil {
		t.Fatalf("seed rate limit: %v", err)
	}
	h := NewHandler(Deps{
		Auth:     staticAdminAuth{ident: admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: fx.tenantID, UserID: 9}},
		Store:    NewPostgresStore(pool),
		Recovery: provideraccountrecovery.NewService(provideraccountrecovery.NewPostgresStore(pool), recoveryChannelStub{}),
	})
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/bulk", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	body := fmt.Sprintf(`{"ids":[%d,%d,%d],"action":"clear_rate_limit","reason":"误伤"}`, fx.accounts[0], fx.accounts[3], 999999)
	rec := post(body)
	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("部分成功应 207: %d %s", rec.Code, rec.Body.String())
	}
	var resp Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	by := map[int64]ItemResult{}
	for _, item := range resp.Results {
		by[item.ID] = item
	}
	if by[fx.accounts[0]].Status != StatusSucceeded || by[fx.accounts[3]].Code != CodeNotFound || by[999999].Code != CodeNotFound {
		t.Fatalf("清限流逐项不符: %+v", resp.Results)
	}
	var reason *string
	if err := pool.QueryRow(ctx, `SELECT rate_limit_reason FROM provider_accounts WHERE id=$1`, fx.accounts[0]).Scan(&reason); err != nil || reason != nil {
		t.Fatalf("本租户限流必须被清: reason=%v err=%v", reason, err)
	}
	if n := countAudit(t, ctx, pool, fx.tenantID, "clear_provider_account_rate_limit"); n != 1 {
		t.Fatalf("成功项应留一条清限流审计: %d", n)
	}

	rec = post(fmt.Sprintf(`{"ids":[%d],"action":"recover","reason":"完整恢复"}`, fx.accounts[1]))
	if rec.Code != http.StatusOK {
		t.Fatalf("完整恢复应 200: %d %s", rec.Code, rec.Body.String())
	}
	if n := countAudit(t, ctx, pool, fx.tenantID, "recover_provider_account_state"); n != 1 {
		t.Fatalf("完整恢复应留审计: %d", n)
	}

	foreign := NewHandler(Deps{
		Auth:     staticAdminAuth{ident: admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: fx.otherTenantID, UserID: 9}},
		Store:    NewPostgresStore(pool),
		Recovery: provideraccountrecovery.NewService(provideraccountrecovery.NewPostgresStore(pool), recoveryChannelStub{}),
	})
	req := httptest.NewRequest(http.MethodPost, "/bulk", strings.NewReader(fmt.Sprintf(`{"ids":[%d],"action":"clear_rate_limit","reason":"越权"}`, fx.accounts[1])))
	req.Header.Set("Content-Type", "application/json")
	out := httptest.NewRecorder()
	foreign.ServeHTTP(out, req)
	var foreignResp Response
	if err := json.NewDecoder(out.Body).Decode(&foreignResp); err != nil || len(foreignResp.Results) != 1 || foreignResp.Results[0].Code != CodeNotFound {
		t.Fatalf("他租户清限流必须 not_found: %d %+v", out.Code, foreignResp)
	}
}
