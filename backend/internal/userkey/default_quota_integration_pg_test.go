//go:build integration_pg

package userkey

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/BloomingProsperity/HUAKAI/internal/config"
	"github.com/BloomingProsperity/HUAKAI/internal/quota"
)

// newServiceWithDefaultQuota 构造启用默认配额兜底的 Service(测试用 MinCost 加速)。
func newServiceWithDefaultQuota(pool *pgxpool.Pool, cfg config.DefaultKeyQuota) *Service {
	s := NewService(pool, nil, WithDefaultKeyQuota(cfg))
	s.bcryptCost = bcrypt.MinCost
	return s
}

// TestUserKey_Issue_SeedsDefaultQuotaPolicies 守 #1(中转站对外卖额度防滥用):
// 新建自助 key 必须种入保守默认配额——RPM(requests/fixed-60s)+ 并发(concurrency/none),
// 均 enforce、enabled,scope_id 取 key.id 十进制串(与 relay 请求时 quotaenforce 构造
// api_key scope 的口径一致,否则策略永不命中、形同虚设)。
//
// self-proving + 判别对照:
//   - 启用路:断言两条默认 Enforce 策略真落库且参数精确;再经 quota.ResolvePolicies 用同一
//     scope_id 反查,证策略真被 resolver 命中(scope_id 对得上才会进 Ordered)。
//   - 关闭路:同样建 key 但 Enabled=false → 0 条默认策略、resolver 解析为空。
//
// MUTATION:若 seedDefaultKeyQuota 不接线、或忽略 Enabled、或 scope_id 用错(非 key.id 十进制),
// 启用路的落库/解析断言或关闭路的"无策略"断言必有一条 RED。
func TestUserKey_Issue_SeedsDefaultQuotaPolicies(t *testing.T) {
	ctx := context.Background()
	pool := openPool(t, ctx)

	// ---- 启用路:RPM=60 / 并发=5 ----
	f := newFixture(t, ctx, pool)
	svc := newServiceWithDefaultQuota(pool, config.DefaultKeyQuota{Enabled: true, RPM: 60, ConcurrencyMax: 5})
	res, err := svc.Issue(ctx, IssueRequest{TenantID: f.tenantID, UserID: f.userA, Name: "dq-on", Environment: EnvLive})
	if err != nil {
		t.Fatalf("Issue(启用): %v", err)
	}
	scopeID := strconv.FormatInt(res.APIKeyID, 10)

	type policyRow struct {
		windowKind, mode, limit string
		windowSeconds, priority int
		enabled                 bool
	}
	byMetric := map[string]policyRow{}
	rows, err := pool.Query(ctx,
		`SELECT metric, window_kind, window_seconds, limit_value::text, mode, enabled, priority
		   FROM quota_policies
		  WHERE tenant_id=$1 AND scope_kind='api_key' AND scope_id=$2`,
		f.tenantID, scopeID)
	if err != nil {
		t.Fatalf("query policies: %v", err)
	}
	for rows.Next() {
		var metric string
		var r policyRow
		if err := rows.Scan(&metric, &r.windowKind, &r.windowSeconds, &r.limit, &r.mode, &r.enabled, &r.priority); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		byMetric[metric] = r
	}
	rows.Close()

	if len(byMetric) != 2 {
		t.Fatalf("want 2 default policies for new key; got %d (%+v)", len(byMetric), byMetric)
	}
	if rpm := byMetric["requests"]; rpm.windowKind != "fixed" || rpm.windowSeconds != 60 ||
		rpm.limit != "60.00000000" || rpm.mode != "enforce" || !rpm.enabled {
		t.Fatalf("RPM 默认策略参数不对: %+v", rpm)
	}
	if conc := byMetric["concurrency"]; conc.windowKind != "none" ||
		conc.limit != "5.00000000" || conc.mode != "enforce" || !conc.enabled {
		t.Fatalf("并发默认策略参数不对: %+v", conc)
	}

	// scope_id 真命中:resolver 用与 relay 同口径的 api_key scope 反查,两条 Enforce 策略都应被解析到。
	store := quota.NewPostgresStore(pool)
	resolved, err := quota.ResolvePolicies(ctx, store, f.tenantID,
		[]quota.Scope{{TenantID: f.tenantID, Kind: quota.ScopeAPIKey, ID: scopeID}},
		"",
		[]quota.Metric{quota.MetricRequests, quota.MetricConcurrency}, time.Now().UTC())
	if err != nil {
		t.Fatalf("ResolvePolicies: %v", err)
	}
	resolvedMetrics := map[quota.Metric]bool{}
	for _, p := range resolved.Ordered {
		if p.Scope.Kind == quota.ScopeAPIKey && p.Scope.ID == scopeID && p.Mode == quota.ModeEnforce {
			resolvedMetrics[p.Metric] = true
		}
	}
	if !resolvedMetrics[quota.MetricRequests] || !resolvedMetrics[quota.MetricConcurrency] {
		t.Fatalf("种的默认策略未被 resolver 用同一 scope_id 命中(scope_id 口径不一致?): %+v", resolved.Ordered)
	}

	// ---- 判别对照:关闭默认配额 → 建 key 无任何默认策略 ----
	// fixture 故意带正 limit(RPM 60/并发 5),使 Enabled=false 成为唯一阻止种策略的因素:
	// 若删掉 seeder 的 `if !cfg.Enabled` 守卫,本路会种出 2 条策略 → 下方 cnt!=0 → RED。
	// (若 fixture 把 limit 也留零值,删 Enabled 守卫后会被 limit<=0 跳过吞掉、不翻红 → 总开关失守。)
	f2 := newFixture(t, ctx, pool)
	svcOff := newServiceWithDefaultQuota(pool, config.DefaultKeyQuota{Enabled: false, RPM: 60, ConcurrencyMax: 5})
	res2, err := svcOff.Issue(ctx, IssueRequest{TenantID: f2.tenantID, UserID: f2.userA, Name: "dq-off", Environment: EnvLive})
	if err != nil {
		t.Fatalf("Issue(关闭): %v", err)
	}
	var cnt int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM quota_policies WHERE tenant_id=$1 AND scope_kind='api_key' AND scope_id=$2`,
		f2.tenantID, strconv.FormatInt(res2.APIKeyID, 10)).Scan(&cnt); err != nil {
		t.Fatalf("count(关闭): %v", err)
	}
	if cnt != 0 {
		t.Fatalf("禁用默认配额时不应种任何默认策略; got %d", cnt)
	}
}

// TestUserKey_Issue_DefaultQuotaSkipsNonPositiveDimension 守:limit<=0 的维度不种
// (运维把某维度关掉=该 env 设 0),另一维度仍正常种。
// MUTATION:若 seed 不判 limit<=0 跳过,会种出 limit=0 的 enforce 策略(等于全拒),本断言 RED。
func TestUserKey_Issue_DefaultQuotaSkipsNonPositiveDimension(t *testing.T) {
	ctx := context.Background()
	pool := openPool(t, ctx)
	f := newFixture(t, ctx, pool)
	// 只开并发、关掉 RPM(RPM=0 视为该维度不种)。
	svc := newServiceWithDefaultQuota(pool, config.DefaultKeyQuota{Enabled: true, RPM: 0, ConcurrencyMax: 5})
	res, err := svc.Issue(ctx, IssueRequest{TenantID: f.tenantID, UserID: f.userA, Name: "dq-partial", Environment: EnvLive})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	scopeID := strconv.FormatInt(res.APIKeyID, 10)
	metrics := map[string]bool{}
	rows, err := pool.Query(ctx,
		`SELECT metric FROM quota_policies WHERE tenant_id=$1 AND scope_kind='api_key' AND scope_id=$2`,
		f.tenantID, scopeID)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		metrics[m] = true
	}
	rows.Close()
	if metrics["requests"] {
		t.Fatalf("RPM=0 不应种 requests 策略; got %+v", metrics)
	}
	if !metrics["concurrency"] {
		t.Fatalf("并发=5 应种 concurrency 策略; got %+v", metrics)
	}
}
