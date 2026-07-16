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
		Priority: &activePriority,
		RPMLimit: &rpm, TPMLimit: &tpm, WindowCostLimitCents: &windowCost, MaxSessions: &maxSessions,
		DisableCooling: &trueValue, RefreshLeadSeconds: &refreshLead, TLSFingerprintRotate: &trueValue,
		CustomErrorCodesEnabled: &trueValue, CustomErrorCodes: []int32{429}, PoolMode: &trueValue,
		TempUnschedulableEnabled:   &trueValue,
		TempUnschedulableRulesJSON: []byte(`[{"error_code":529,"keywords":["busy"],"duration_minutes":5}]`),
	})
	if err != nil {
		t.Fatalf("通过生产 Insert 写活跃账号: %v", err)
	}

	readback, err := adminQueries.GetAdminProviderAccount(ctx, admindb.GetAdminProviderAccountParams{ID: activeID, TenantID: tenantID})
	if err != nil {
		t.Fatalf("管理读回活跃账号: %v", err)
	}
	if readback.RPMLimit != 1 || readback.TPMLimit != 5000 || readback.WindowCostLimitCents != 700 || readback.MaxSessions != 3 {
		t.Fatalf("生产 Insert 数值字段未原样读回: %+v", readback)
	}
	if !readback.DisableCooling || readback.RefreshLeadSeconds == nil || *readback.RefreshLeadSeconds != 90 || !readback.TLSFingerprintRotate {
		t.Fatalf("生产 Insert 开关/刷新字段未原样读回: %+v", readback)
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
		WindowCostLimitCents:  &updatedWindow,
		SetRefreshLeadSeconds: true, RefreshLeadSeconds: nil,
	})
	if err != nil {
		t.Fatalf("生产 Update 修改一项并清 nullable: %v", err)
	}
	if updated.WindowCostLimitCents != 999 || updated.RefreshLeadSeconds != nil {
		t.Fatalf("生产 Update 目标字段未生效: %+v", updated)
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
