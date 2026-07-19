//go:build integration_pg

package gatewayhttp

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/pool/dispatcher"
	"github.com/BloomingProsperity/HUAKAI/internal/pool/router"
	ratelimit "github.com/BloomingProsperity/HUAKAI/internal/rate"
	"github.com/BloomingProsperity/HUAKAI/internal/rate/precheck"
)

// TestProviderAccountAdvancedWriteToSelectionAndRPMGate 串起真实 Insert/Update、
// 管理读回、候选过滤、快照投影和 RPM 预检。fixture 让过期号优先级更高，避免
// 两个候选没有判别差异时出现假绿。
func TestProviderAccountAdvancedWriteToSelectionAndRPMGate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Fatal("integration_pg 必须显式设置 HUAKAI_DATABASE_URL")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("打开测试数据库: %v", err)
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("开启事务: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	suffix := fmt.Sprintf("account-advanced-%d", time.Now().UnixNano())
	var tenantID, providerID, poolGroupID, channelID int64
	if err := tx.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "tenant-"+suffix).Scan(&tenantID); err != nil {
		t.Fatalf("插入租户: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol) VALUES ($1,$2,$3,'anthropic_messages') RETURNING id`,
		tenantID, "provider-"+suffix, "Provider "+suffix,
	).Scan(&providerID); err != nil {
		t.Fatalf("插入 provider: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1,$2) RETURNING id`, tenantID, "pool-"+suffix).Scan(&poolGroupID); err != nil {
		t.Fatalf("插入 pool group: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1,$2,$3) RETURNING id`, tenantID, poolGroupID, "channel-"+suffix).Scan(&channelID); err != nil {
		t.Fatalf("插入 channel: %v", err)
	}

	adminQueries := admindb.New(tx)
	trueValue := true
	expiredPriority := int32(1)
	activePriority := int32(100)
	rpm := int64(1)
	tpm := int64(5000)
	windowCost := int64(700)
	maxSessions := int32(3)
	refreshLead := int32(90)
	upstreamCostRatio := 0.5
	expiredID, err := adminQueries.InsertProviderAccount(ctx, admindb.InsertProviderAccountParams{
		TenantID: tenantID, ProviderID: providerID, ChannelID: channelID,
		Name: "expired-" + suffix, AccountType: "api_key", Credentials: []byte(`{}`), Extra: []byte(`{}`),
		Priority:  &expiredPriority,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().UTC().Add(-time.Hour), Valid: true},
		RPMLimit:  &rpm,
	})
	if err != nil {
		t.Fatalf("通过生产 Insert 写过期账号: %v", err)
	}
	activeID, err := adminQueries.InsertProviderAccount(ctx, admindb.InsertProviderAccountParams{
		TenantID: tenantID, ProviderID: providerID, ChannelID: channelID,
		Name: "active-" + suffix, AccountType: "api_key", Credentials: []byte(`{}`), Extra: []byte(`{}`),
		Priority: &activePriority, UpstreamCostRatio: &upstreamCostRatio,
		RPMLimit: &rpm, TPMLimit: &tpm, WindowCostLimitCents: &windowCost, MaxSessions: &maxSessions,
		DisableCooling: &trueValue, RefreshLeadSeconds: &refreshLead, TLSFingerprintRotate: &trueValue,
		CustomErrorCodesEnabled: &trueValue, CustomErrorCodes: []int32{429}, PoolMode: &trueValue,
		TempUnschedulableEnabled:   &trueValue,
		TempUnschedulableRulesJSON: []byte(`[{"rule_id":"busy-529","error_code":529,"keywords":["busy"],"duration_minutes":5,"client_status":422,"client_code":"account_busy","message_mode":"custom","client_message":"账号暂不可用","affect_health":false}]`),
	})
	if err != nil {
		t.Fatalf("通过生产 Insert 写活跃账号: %v", err)
	}
	// 选号以 account_credentials 为凭据真相源。两个账号都补同态可服务凭据，
	// 让候选差异只由 expires_at 决定，避免把空壳账号安全门误当作高级字段接线失败。
	for _, accountID := range []int64{expiredID, activeID} {
		if _, err := tx.Exec(ctx, `
			INSERT INTO account_credentials (
				tenant_id, provider_account_id, vendor, auth_mode, state, credential_version,
				encrypted_payload, key_id, nonce, aad_hash
			) VALUES ($1, $2, 'anthropic', 'api_key', 'active', 1, $3, 'advanced-test-key', $4, $5)`,
			tenantID,
			accountID,
			[]byte("ciphertext"),
			[]byte("nonce-12345678"),
			fmt.Sprintf("advanced-aad-%d", accountID),
		); err != nil {
			t.Fatalf("写入账号 %d 的可服务凭据: %v", accountID, err)
		}
	}

	readback, err := adminQueries.GetAdminProviderAccount(ctx, admindb.GetAdminProviderAccountParams{ID: activeID, TenantID: tenantID})
	if err != nil {
		t.Fatalf("管理读回活跃账号: %v", err)
	}
	if readback.RPMLimit != 1 || readback.TPMLimit != 5000 || readback.WindowCostLimitCents != 700 || readback.MaxSessions != 3 {
		t.Fatalf("生产 Insert 数值字段未原样读回: %+v", readback)
	}
	if readback.UpstreamCostRatio == nil || *readback.UpstreamCostRatio != upstreamCostRatio {
		t.Fatalf("生产 Insert 成本比例未原样读回: %+v", readback)
	}
	invalidCostTx, err := tx.Begin(ctx)
	if err != nil {
		t.Fatalf("开启成本约束保存点: %v", err)
	}
	if _, err := invalidCostTx.Exec(ctx, `UPDATE provider_accounts SET upstream_cost_ratio = 0 WHERE id = $1`, activeID); err == nil {
		t.Fatal("数据库必须拒绝 upstream_cost_ratio=0")
	}
	if err := invalidCostTx.Rollback(ctx); err != nil {
		t.Fatalf("回滚成本约束保存点: %v", err)
	}
	if !readback.DisableCooling || readback.RefreshLeadSeconds == nil || *readback.RefreshLeadSeconds != 90 || !readback.TLSFingerprintRotate {
		t.Fatalf("生产 Insert 开关/刷新字段未原样读回: %+v", readback)
	}
	rules := ratelimit.ParseTempUnschedulableRules(readback.TempUnschedulableRules)
	if len(rules) != 1 || rules[0].RuleID != "busy-529" || rules[0].ClientStatus == nil || *rules[0].ClientStatus != 422 ||
		rules[0].ClientCode != "account_busy" || rules[0].MessageMode != "custom" || rules[0].ClientMessage != "账号暂不可用" ||
		rules[0].AffectHealth == nil || *rules[0].AffectHealth {
		t.Fatalf("生产 Insert 的错误投影规则未完整读回: %+v", rules)
	}
	policyProvider := ratelimit.NewPostgresAccountErrorRulesProvider(tx)
	policy := policyProvider.GetAccountErrorPolicy(activeID)
	if len(policy.Rules) != 1 || len(policy.CustomErrorCodes) != 1 || policy.CustomErrorCodes[0] != 429 || !policy.PoolMode {
		t.Fatalf("生产规则提供器未读取完整账号策略: %+v", policy)
	}
	decisionNow := time.Date(2026, 7, 19, 9, 0, 0, 0, time.UTC)
	rateService := ratelimit.NewUpstreamRateService(func() time.Time { return decisionNow }, time.Minute,
		ratelimit.WithAccountErrorRulesProvider(policyProvider))
	decision, err := rateService.HandleUpstreamError(ctx, activeID, 529, nil, []byte(`{"error":{"message":"BUSY"}}`))
	if err != nil {
		t.Fatalf("生产错误策略决策: %v", err)
	}
	if decision.StateChange != ratelimit.StateTempUnsched || decision.ClientStatus != 422 ||
		decision.ClientCode != "account_busy" || decision.ClientMessage != "账号暂不可用" ||
		decision.ClientRuleID != "busy-529" || !decision.SuppressHealthSignal ||
		!decision.CooldownUntil.Equal(decisionNow.Add(5*time.Minute)) {
		t.Fatalf("数据库规则未形成完整运行时决策: %+v", decision)
	}

	source := dispatcher.NewDBAccountSource(dbbilling.New(tx))
	accounts, err := source.ListAccounts(ctx, dispatcher.SelectionRequest{
		TenantID: tenantID, PoolGroupID: poolGroupID,
		RequestedModel: "claude-test", ProtocolFamily: "anthropic_messages",
	})
	if err != nil {
		t.Fatalf("生产候选查询: %v", err)
	}
	var active *dispatcher.AccountSnapshot
	for _, account := range accounts {
		if account.ID == expiredID {
			t.Fatalf("优先级更高的过期账号仍进入候选: id=%d", expiredID)
		}
		if account.ID == activeID {
			active = account
		}
	}
	if active == nil {
		t.Fatalf("活跃账号未进入候选: ids=%v", snapshotIDs(accounts))
	}
	if active.RPMLimit != 1 || active.TPMLimit != 5000 || active.WindowCostLimitCents != 700 || active.MaxSessions != 3 || !active.DisableCooling {
		t.Fatalf("高级字段未进入生产快照: %+v", active)
	}
	if active.UpstreamCostRatio == nil || *active.UpstreamCostRatio != upstreamCostRatio {
		t.Fatalf("成本比例未进入生产快照: %+v", active)
	}

	fixedNow := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	counter := precheck.New(time.Minute, func() time.Time { return fixedNow })
	gate := router.RatePrecheckGate{Counter: counter}
	allowed, reason, err := gate.Allow(ctx, active, router.SelectionRequest{EstimatedInputTokens: 10})
	if err != nil || !allowed || reason != "" {
		t.Fatalf("RPM 首次预检应放行: allowed=%v reason=%s err=%v", allowed, reason, err)
	}
	counter.Record(active.ID, 10)
	allowed, reason, err = gate.Allow(ctx, active, router.SelectionRequest{EstimatedInputTokens: 10})
	if err != nil || allowed || reason != router.GateFailureRatePrecheck {
		t.Fatalf("rpm_limit=1 的第二次预检应拒绝: allowed=%v reason=%s err=%v", allowed, reason, err)
	}

	updatedWindow := int64(999)
	updated, err := adminQueries.UpdateAdminProviderAccount(ctx, admindb.UpdateAdminProviderAccountParams{
		ID: activeID, TenantID: tenantID,
		WindowCostLimitCents: &updatedWindow,
		SetUpstreamCostRatio: true, UpstreamCostRatio: nil,
		SetRefreshLeadSeconds: true, RefreshLeadSeconds: nil,
	})
	if err != nil {
		t.Fatalf("生产 Update 修改一项并清 nullable: %v", err)
	}
	if updated.WindowCostLimitCents != 999 || updated.RefreshLeadSeconds != nil {
		t.Fatalf("生产 Update 目标字段未生效: %+v", updated)
	}
	if updated.UpstreamCostRatio != nil {
		t.Fatalf("生产 Update 未清除成本比例: %+v", updated)
	}
	if updated.RPMLimit != 1 || updated.TPMLimit != 5000 || updated.MaxSessions != 3 || !updated.DisableCooling || !updated.TLSFingerprintRotate {
		t.Fatalf("生产 Update 误清其他高级字段: %+v", updated)
	}

	// 判别性补强:显式把每个标量高级字段改到新值,断言全部真生效。
	// 上面的 update 只对 window_cost 做了"显式改值"断言,其余字段仅有"不被误清"
	// 断言——若某字段的 UPDATE SET 被改成恒不更新,原值恰等于期望值时不会变红。
	// 这里给每个字段一个与原值不同的新值,任一字段的 UPDATE 写入缺失都会红。
	newRPM := int64(2)
	newTPM := int64(9999)
	newMaxSessions := int32(7)
	newDisableCooling := false
	newTLSRotate := false
	explicit, err := adminQueries.UpdateAdminProviderAccount(ctx, admindb.UpdateAdminProviderAccountParams{
		ID: activeID, TenantID: tenantID,
		RPMLimit: &newRPM, TPMLimit: &newTPM, MaxSessions: &newMaxSessions,
		DisableCooling: &newDisableCooling, TLSFingerprintRotate: &newTLSRotate,
	})
	if err != nil {
		t.Fatalf("生产 Update 显式改标量字段: %v", err)
	}
	if explicit.RPMLimit != 2 || explicit.TPMLimit != 9999 || explicit.MaxSessions != 7 ||
		explicit.DisableCooling || explicit.TLSFingerprintRotate {
		t.Fatalf("生产 Update 未把标量字段改到新值: %+v", explicit)
	}
	// window_cost 未在本次 update 中提交,应保持上一步的 999 不被误清。
	if explicit.WindowCostLimitCents != 999 {
		t.Fatalf("生产 Update 误清未提交的 window_cost: %+v", explicit)
	}
}

func snapshotIDs(accounts []*dispatcher.AccountSnapshot) []int64 {
	out := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		out = append(out, account.ID)
	}
	return out
}
