//go:build integration_pg

package admin

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// poolHealthFixture 把一个租户的池-渠道-账号-凭据-FSM 图谱种进真实库,并记下每个账号
// 在合同里应落的栏位,供断言与变异守卫使用。
type poolHealthFixture struct {
	tenantID   int64
	poolMainID int64
	poolIdleID int64
	accounts   map[string]int64
	now        time.Time
}

func seedPoolHealthGraph(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string) poolHealthFixture {
	t.Helper()
	fx := poolHealthFixture{accounts: map[string]int64{}, now: time.Now().UTC().Truncate(time.Microsecond)}
	mustScan := func(dst *int64, sql string, args ...any) {
		t.Helper()
		if err := pool.QueryRow(ctx, sql, args...).Scan(dst); err != nil {
			t.Fatalf("%s: %v", sql[:40], err)
		}
	}
	mustScan(&fx.tenantID, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "pool-health-"+suffix)
	var providerOn, providerOff int64
	mustScan(&providerOn, `INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		VALUES ($1, $2, 'Pool Health Provider', 'openai_chat') RETURNING id`, fx.tenantID, "ph-on-"+suffix)
	mustScan(&providerOff, `INSERT INTO providers (tenant_id, code, display_name, upstream_protocol, enabled)
		VALUES ($1, $2, 'Pool Health Provider Off', 'openai_chat', false) RETURNING id`, fx.tenantID, "ph-off-"+suffix)
	mustScan(&fx.poolMainID, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`, fx.tenantID, "ph-main-"+suffix)
	mustScan(&fx.poolIdleID, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`, fx.tenantID, "ph-idle-"+suffix)
	var poolGone int64
	mustScan(&poolGone, `INSERT INTO pool_groups (tenant_id, name, deleted_at) VALUES ($1, $2, now()) RETURNING id`, fx.tenantID, "ph-gone-"+suffix)
	var chanOn, chanOff, chanGone, chanDeletedLive int64
	mustScan(&chanOn, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`, fx.tenantID, fx.poolMainID, "ph-chan-on-"+suffix)
	mustScan(&chanOff, `INSERT INTO channels (tenant_id, pool_group_id, name, enabled) VALUES ($1, $2, $3, false) RETURNING id`, fx.tenantID, fx.poolMainID, "ph-chan-off-"+suffix)
	mustScan(&chanGone, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`, fx.tenantID, poolGone, "ph-chan-gone-"+suffix)
	mustScan(&chanDeletedLive, `INSERT INTO channels (tenant_id, pool_group_id, name, deleted_at) VALUES ($1, $2, $3, now()) RETURNING id`, fx.tenantID, fx.poolMainID, "ph-chan-deleted-"+suffix)

	type acct struct {
		key            string
		channel        int64
		provider       int64
		enabled        bool
		health         string
		until          *time.Time
		expiresAt      *time.Time
		disableCooling bool
		deleted        bool
		credState      string // "" = 不建凭据
		gracePast      bool
		fsm            string // "" = 无 FSM 记录
		fsmUntil       *time.Time
		rampPct        *int32
	}
	plus := func(d time.Duration) *time.Time { t := fx.now.Add(d); return &t }
	pct := func(v int32) *int32 { return &v }
	accounts := []acct{
		{key: "ok", channel: chanOn, provider: providerOn, enabled: true, health: "healthy", credState: "active"},
		{key: "disabled", channel: chanOn, provider: providerOn, enabled: false, health: "healthy", credState: "active"},
		{key: "revoked", channel: chanOn, provider: providerOn, enabled: true, health: "revoked", until: plus(time.Hour), credState: "active"},
		{key: "revoked_expired", channel: chanOn, provider: providerOn, enabled: true, health: "revoked", until: plus(-time.Minute), credState: "active"},
		{key: "throttled", channel: chanOn, provider: providerOn, enabled: true, health: "throttled", until: plus(10 * time.Minute), credState: "active"},
		{key: "throttled_null", channel: chanOn, provider: providerOn, enabled: true, health: "throttled", credState: "active"},
		{key: "throttled_null_fsm", channel: chanOn, provider: providerOn, enabled: true, health: "throttled", credState: "active", fsm: "cooling_down", fsmUntil: plus(time.Minute)},
		{key: "nocred", channel: chanOn, provider: providerOn, enabled: true, health: "healthy"},
		{key: "grace", channel: chanOn, provider: providerOn, enabled: true, health: "healthy", credState: "refreshing_with_grace"},
		{key: "grace_expired", channel: chanOn, provider: providerOn, enabled: true, health: "healthy", credState: "refreshing_with_grace", gracePast: true},
		{key: "fsm_cooling", channel: chanOn, provider: providerOn, enabled: true, health: "healthy", credState: "active", fsm: "cooling_down", fsmUntil: plus(5 * time.Minute)},
		{key: "fsm_cooling_exempt", channel: chanOn, provider: providerOn, enabled: true, health: "healthy", disableCooling: true, credState: "active", fsm: "cooling_down", fsmUntil: plus(time.Minute)},
		{key: "fsm_degraded", channel: chanOn, provider: providerOn, enabled: true, health: "healthy", credState: "active", fsm: "degraded"},
		{key: "fsm_ramping", channel: chanOn, provider: providerOn, enabled: true, health: "healthy", credState: "active", fsm: "ramping", rampPct: pct(10)},
		{key: "fsm_disabled", channel: chanOn, provider: providerOn, enabled: true, health: "healthy", credState: "active", fsm: "disabled"},
		{key: "fsm_paused", channel: chanOn, provider: providerOn, enabled: true, health: "healthy", credState: "active", fsm: "manual_paused"},
		{key: "fsm_latest", channel: chanOn, provider: providerOn, enabled: true, health: "healthy", credState: "active"},
		{key: "expired", channel: chanOn, provider: providerOn, enabled: true, health: "healthy", expiresAt: plus(-time.Second), credState: "active"},
		{key: "channel_off", channel: chanOff, provider: providerOn, enabled: true, health: "healthy", credState: "active"},
		{key: "provider_off", channel: chanOn, provider: providerOff, enabled: true, health: "healthy", credState: "active"},
		{key: "unpooled", channel: chanGone, provider: providerOn, enabled: true, health: "healthy", credState: "active"},
		// 叠加事实:同一账号具备多种事实时只按优先级落一栏。
		{key: "disabled_throttled", channel: chanOn, provider: providerOn, enabled: false, health: "throttled", until: plus(time.Hour), credState: "active"},
		{key: "revoked_fsm_degraded", channel: chanOn, provider: providerOn, enabled: true, health: "revoked", until: plus(time.Hour), credState: "active", fsm: "degraded"},
		{key: "throttled_fsm_degraded", channel: chanOn, provider: providerOn, enabled: true, health: "throttled", until: plus(20 * time.Minute), credState: "active", fsm: "degraded"},
		{key: "fsm_disabled_exempt", channel: chanOn, provider: providerOn, enabled: true, health: "healthy", disableCooling: true, credState: "active", fsm: "disabled"},
		// 豁免边界与放量阶段为空。
		{key: "fsm_ramping_null", channel: chanOn, provider: providerOn, enabled: true, health: "healthy", credState: "active", fsm: "ramping"},
		{key: "fsm_ramping_exempt", channel: chanOn, provider: providerOn, enabled: true, health: "healthy", disableCooling: true, credState: "active", fsm: "ramping", rampPct: pct(10)},
		{key: "throttled_exempt", channel: chanOn, provider: providerOn, enabled: true, health: "throttled", until: plus(40 * time.Minute), disableCooling: true, credState: "active"},
		// 软删账号不进任何栏也不进 unpooled;活池下软删渠道的账号进 unpooled。
		{key: "deleted_acct", channel: chanOn, provider: providerOn, enabled: true, health: "healthy", deleted: true, credState: "active"},
		{key: "chan_deleted_live_pool", channel: chanDeletedLive, provider: providerOn, enabled: true, health: "healthy", credState: "active"},
	}
	for i, a := range accounts {
		var id int64
		var deletedAt *time.Time
		if a.deleted {
			deletedAt = plus(-time.Minute)
		}
		mustScan(&id, `INSERT INTO provider_accounts (
				tenant_id, provider_id, channel_id, name, account_type, enabled, health_state,
				health_state_until, expires_at, disable_cooling, deleted_at
			) VALUES ($1, $2, $3, $4, 'api_key', $5, $6, $7, $8, $9, $10) RETURNING id`,
			fx.tenantID, a.provider, a.channel, fmt.Sprintf("ph-%s-%s", a.key, suffix), a.enabled, a.health, a.until, a.expiresAt, a.disableCooling, deletedAt)
		fx.accounts[a.key] = id
		if a.credState == "" {
			continue
		}
		var graceUntil *time.Time
		if a.credState == "refreshing_with_grace" {
			if a.gracePast {
				graceUntil = plus(-time.Minute)
			} else {
				graceUntil = plus(time.Hour)
			}
		}
		var credID int64
		mustScan(&credID, `INSERT INTO account_credentials (
				tenant_id, provider_account_id, vendor, auth_mode, state, credential_version, grace_until,
				encrypted_payload, key_id, nonce, aad_hash
			) VALUES ($1, $2, 'openai', 'api_key', $3, 1, $4, $5, 'test-key', $6, $7) RETURNING id`,
			fx.tenantID, id, a.credState, graceUntil, []byte("ciphertext"), []byte("nonce-12345678"), fmt.Sprintf("aad-ph-%d-%d", i, id))
		insertFSM := func(version int32, state string, until *time.Time, ramp *int32, updatedAt time.Time) {
			t.Helper()
			if _, err := pool.Exec(ctx, `INSERT INTO channel_health_state (
					tenant_id, channel_id, vendor, provider_account_id, account_credential_id, credential_version,
					state, cooldown_until, ramp_stage_pct, policy_version, updated_at
				) VALUES ($1, $2, 'openai', $3, $4, $5, $6, $7, $8, 'test', $9)`,
				fx.tenantID, strconv.FormatInt(a.channel, 10), id, credID, version, state, until, ramp, updatedAt); err != nil {
				t.Fatalf("insert channel_health_state %s v%d: %v", a.key, version, err)
			}
		}
		if a.fsm != "" {
			insertFSM(1, a.fsm, a.fsmUntil, a.rampPct, fx.now)
		}
		if a.key == "fsm_latest" {
			// 旧凭据版本的 disabled 记录反而更新;最新记录必须按 credential_version 优先取 v2 active。
			insertFSM(1, "disabled", nil, nil, fx.now.Add(time.Minute))
			insertFSM(2, "active", nil, nil, fx.now.Add(-time.Hour))
		}
	}
	return fx
}

func cleanupPoolHealthGraph(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64) {
	t.Helper()
	_, _ = pool.Exec(ctx, `DELETE FROM channel_health_state WHERE tenant_id = $1`, tenantID)
	cleanupAdminProviderAccountHealthGraph(t, ctx, pool, tenantID)
}

// TestSummarizeProviderAccountHealthByPoolMatchesSchedulingTruth 用真实 SQL 验证按池分栏(30 个账号:主池 27 + 未挂池 2 + 软删 1):
// 每个账号事实只落一栏且与选号候选谓词/健康门一致,叠加多种事实的账号按优先级只落一栏;
// 最新 FSM 按 credential_version 优先;disable_cooling 只豁免 FSM 冷却/放量,不豁免 disabled;
// 停用渠道/停用 provider/过期账号/无可服务凭据都进 unavailable;冷却账号的已知恢复时刻逐个对齐、
// 未知为 NULL;软删账号不计入任何地方,软删池或软删渠道下的账号只计入 unpooled,
// 使 Σ池.total + unpooled == 租户级健康汇总总数;生产选号候选查询在同一图谱上的结果集
// 恰好等于投影的数据库层可调度集;另一租户完全不可见;查询前后不产生任何写入。
// 变异守卫:去掉凭据 EXISTS 谓词 → nocred/grace_expired 变 schedulable;最新 FSM 排序反向 →
// fsm_latest 变 unavailable;unavailable 与 cooling 段对调 → disabled_throttled 变 cooling;
// 去掉 recovery 的 NULL 守卫 → throttled_null_fsm 出现 +1m;任一都让本测试变红。
func TestSummarizeProviderAccountHealthByPoolMatchesSchedulingTruth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openAdminAuditIntegrationPool(t, ctx)
	q := New(pool)

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	fx := seedPoolHealthGraph(t, ctx, pool, suffix)
	other := seedPoolHealthGraph(t, ctx, pool, suffix+"-other")
	t.Cleanup(func() {
		cleanupPoolHealthGraph(t, context.Background(), pool, fx.tenantID)
		cleanupPoolHealthGraph(t, context.Background(), pool, other.tenantID)
	})
	before := snapshotWriteWitness(t, ctx, pool, fx.tenantID)

	if _, err := q.GetPoolHealthTenant(ctx, fx.tenantID); err != nil {
		t.Fatalf("存在的租户应通过存在性门: %v", err)
	}
	if _, err := q.GetPoolHealthTenant(ctx, fx.tenantID+1_000_000); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("不存在的租户必须以 ErrNoRows 被存在性门拒绝(handler 据此映射 404),实得 %v", err)
	}

	rows, err := q.SummarizeProviderAccountHealthByPool(ctx, fx.tenantID)
	if err != nil {
		t.Fatalf("SummarizeProviderAccountHealthByPool: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("池行数=%d，期望 2(主池 + 空池;软删池不出现): %+v", len(rows), rows)
	}
	main, idle := rows[0], rows[1]
	if main.PoolGroupID != fx.poolMainID || idle.PoolGroupID != fx.poolIdleID {
		t.Fatalf("池顺序应按 id 稳定: %+v", rows)
	}
	if main.TotalAccounts != 27 || main.SchedulableAccounts != 6 || main.DegradedAccounts != 2 ||
		main.CoolingDownAccounts != 7 || main.UnavailableAccounts != 12 {
		t.Fatalf("主池分栏不符: total=%d schedulable=%d degraded=%d cooling=%d unavailable=%d",
			main.TotalAccounts, main.SchedulableAccounts, main.DegradedAccounts, main.CoolingDownAccounts, main.UnavailableAccounts)
	}
	if main.SchedulableAccounts+main.DegradedAccounts+main.CoolingDownAccounts+main.UnavailableAccounts != main.TotalAccounts {
		t.Fatalf("四栏之和必须等于 total: %+v", main)
	}
	if want := sortedIDs(fx, "ok", "revoked_expired", "grace", "fsm_cooling_exempt", "fsm_latest", "fsm_ramping_exempt"); !equalIDs(main.SchedulableIds, want) {
		t.Fatalf("schedulable_ids=%v，期望=%v(实得 %s)", main.SchedulableIds, want, describeIDs(fx, main.SchedulableIds))
	}
	if want := sortedIDs(fx, "fsm_degraded", "fsm_ramping"); !equalIDs(main.DegradedIds, want) {
		t.Fatalf("degraded_ids=%v，期望=%v(实得 %s)", main.DegradedIds, want, describeIDs(fx, main.DegradedIds))
	}
	if want := sortedIDs(fx, "throttled", "throttled_null", "fsm_cooling", "throttled_null_fsm", "throttled_fsm_degraded", "fsm_ramping_null", "throttled_exempt"); !equalIDs(main.CoolingIds, want) {
		t.Fatalf("cooling_ids=%v，期望=%v(实得 %s)", main.CoolingIds, want, describeIDs(fx, main.CoolingIds))
	}
	if want := sortedIDs(fx, "fsm_cooling_exempt", "fsm_ramping_exempt", "throttled_exempt"); !equalIDs(main.CoolingExemptIds, want) {
		t.Fatalf("cooling_exempt_ids=%v，期望=%v(实得 %s)", main.CoolingExemptIds, want, describeIDs(fx, main.CoolingExemptIds))
	}
	// 冷却账号已知恢复时刻逐个对齐:throttled +10m、FSM 冷却 +5m、throttled+FSM degraded +20m、
	// throttled 且豁免 +40m;throttled 无截止、throttled 无截止但 FSM 冷却 +1m(数据库层未知 → 整体未知,
	// 不得用 FSM 的 +1m 冒充)、ramping 阶段为空 → NULL。
	if len(main.CoolingRecoveryAt) != len(main.CoolingIds) {
		t.Fatalf("cooling_recovery_at 长度=%d 与 cooling_ids 长度=%d 不对齐", len(main.CoolingRecoveryAt), len(main.CoolingIds))
	}
	wantRecovery := map[string]*time.Time{
		"throttled": ptrTime(fx.now.Add(10 * time.Minute)), "throttled_null": nil, "fsm_cooling": ptrTime(fx.now.Add(5 * time.Minute)),
		"throttled_null_fsm": nil, "throttled_fsm_degraded": ptrTime(fx.now.Add(20 * time.Minute)), "fsm_ramping_null": nil,
		"throttled_exempt": ptrTime(fx.now.Add(40 * time.Minute)),
	}
	for i, id := range main.CoolingIds {
		key := describeIDs(fx, []int64{id})
		want, known := wantRecovery[key[:len(key)-1]]
		if !known {
			t.Fatalf("冷却账号 %s 不在期望表中", key)
		}
		got := main.CoolingRecoveryAt[i]
		switch {
		case want == nil && got.Valid:
			t.Fatalf("%s 恢复时刻应未知,实得 %v", key, got.Time)
		case want != nil && (!got.Valid || !got.Time.Equal(*want)):
			t.Fatalf("%s 恢复时刻=%v，期望=%v", key, got, *want)
		}
	}
	if idle.TotalAccounts != 0 || idle.SchedulableAccounts != 0 || idle.UnavailableAccounts != 0 ||
		len(idle.SchedulableIds) != 0 || len(idle.CoolingIds) != 0 {
		t.Fatalf("空池必须全 0 且无任何账号: %+v", idle)
	}

	// 投影与调度真相一致性:生产选号候选查询(不含 FSM 门)在同一图谱上返回的账号集合,
	// 必须恰好等于 schedulable ∪ degraded ∪「仅被 FSM 门挡下」的账号;任一方向的谓词漂移都会让集合不等。
	candidates, err := dbbilling.New(pool).ListEligibleAccountsByPoolGroup(ctx, dbbilling.ListEligibleAccountsByPoolGroupParams{
		RequestedModel: "any-model", TenantID: fx.tenantID, PoolGroupID: fx.poolMainID,
		RequestedProtocolFamily: "", RequiredCapabilities: []string{}, RequireModelListed: false,
	})
	if err != nil {
		t.Fatalf("ListEligibleAccountsByPoolGroup: %v", err)
	}
	gotCandidates := make([]int64, 0, len(candidates))
	for _, c := range candidates {
		gotCandidates = append(gotCandidates, c.ID)
	}
	wantCandidates := append(append([]int64{}, main.SchedulableIds...), main.DegradedIds...)
	wantCandidates = append(wantCandidates, sortedIDs(fx, "fsm_cooling", "fsm_disabled", "fsm_paused", "fsm_disabled_exempt", "fsm_ramping_null")...)
	if !equalIDs(sortInts(gotCandidates), sortInts(wantCandidates)) {
		t.Fatalf("选号候选集(%s)与投影的数据库层可调度集(%s)不一致", describeIDs(fx, gotCandidates), describeIDs(fx, wantCandidates))
	}

	unpooled, err := q.CountUnpooledProviderAccounts(ctx, fx.tenantID)
	if err != nil {
		t.Fatalf("CountUnpooledProviderAccounts: %v", err)
	}
	if unpooled != 2 {
		t.Fatalf("unpooled=%d，期望 2(软删池下 1 个 + 活池下软删渠道 1 个;软删账号不计)", unpooled)
	}
	tenantRows, err := q.SummarizeProviderAccountHealth(ctx, fx.tenantID)
	if err != nil {
		t.Fatalf("SummarizeProviderAccountHealth: %v", err)
	}
	var tenantTotal int64
	for _, r := range tenantRows {
		tenantTotal += r.N
	}
	if main.TotalAccounts+idle.TotalAccounts+unpooled != tenantTotal {
		t.Fatalf("对账失败: Σ池.total(%d) + unpooled(%d) != 租户级总数(%d)", main.TotalAccounts+idle.TotalAccounts, unpooled, tenantTotal)
	}

	// 跨租户隔离:另一租户同构图谱,但本租户的行里不得出现它的任何账号 id。
	for _, id := range append(append(append([]int64{}, main.SchedulableIds...), main.DegradedIds...), main.CoolingIds...) {
		for key, otherID := range other.accounts {
			if id == otherID {
				t.Fatalf("跨租户泄漏: 账号 %s(%d) 属于另一租户", key, id)
			}
		}
	}
	otherRows, err := q.SummarizeProviderAccountHealthByPool(ctx, other.tenantID)
	if err != nil || len(otherRows) != 2 || otherRows[0].TotalAccounts != 27 {
		t.Fatalf("另一租户应得到自己的同构投影: err=%v rows=%+v", err, otherRows)
	}

	// 只读:三条查询 + 选号候选查询跑完后,账号、FSM 与用量事实的行数和最新更新时间都不变。
	if after := snapshotWriteWitness(t, ctx, pool, fx.tenantID); after != before {
		t.Fatalf("只读查询改变了持久化事实: before=%q after=%q", before, after)
	}
}

// snapshotWriteWitness 取账号、FSM 与用量表的行数和最新 updated_at,作为"查询前后无写入"的见证。
func snapshotWriteWitness(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64) string {
	t.Helper()
	var witness string
	if err := pool.QueryRow(ctx, `
		SELECT concat_ws('|',
			(SELECT count(*) || ':' || coalesce(max(updated_at)::text, '') FROM provider_accounts WHERE tenant_id = $1),
			(SELECT count(*) || ':' || coalesce(max(updated_at)::text, '') FROM channel_health_state WHERE tenant_id = $1),
			(SELECT count(*) || ':' || coalesce(max(updated_at)::text, '') FROM account_credentials WHERE tenant_id = $1),
			(SELECT count(*)::text FROM usage_records WHERE tenant_id = $1))`, tenantID).Scan(&witness); err != nil {
		t.Fatalf("snapshotWriteWitness: %v", err)
	}
	return witness
}

func ptrTime(t time.Time) *time.Time { return &t }

func sortedIDs(fx poolHealthFixture, keys ...string) []int64 {
	ids := make([]int64, 0, len(keys))
	for _, k := range keys {
		ids = append(ids, fx.accounts[k])
	}
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j] < ids[j-1]; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
	return ids
}

func sortInts(ids []int64) []int64 {
	out := append([]int64{}, ids...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func describeIDs(fx poolHealthFixture, ids []int64) string {
	out := ""
	for _, id := range ids {
		for key, v := range fx.accounts {
			if v == id {
				out += key + " "
			}
		}
	}
	return out
}
