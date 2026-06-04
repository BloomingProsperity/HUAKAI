// HUAKAI · iKun

package subscriptionenforce

import (
	"context"
	"errors"
	"testing"

	poolrouter "github.com/BloomingProsperity/HUAKAI/internal/pool/router"
)

type fakeRoutesRepo struct {
	result GroupRoutes
	err    error
	called int
	// F3: 记录入参以判别 gate 是否把 (tenantID, userGroup, model) 正确透传给 repo。
	gotTenantID  int64
	gotUserGroup string
	gotModel     string
}

func (f *fakeRoutesRepo) GroupRoutes(_ context.Context, tenantID int64, userGroup, model string) (GroupRoutes, error) {
	f.called++
	f.gotTenantID = tenantID
	f.gotUserGroup = userGroup
	f.gotModel = model
	return f.result, f.err
}

// configured 构造"已配置且命中本 model 的 pool 集为 ids"的查询结果。
func configured(ids ...int64) GroupRoutes {
	m := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		m[id] = struct{}{}
	}
	return GroupRoutes{Configured: true, Allowed: m}
}

func req(userGroup string, poolGroupID int64) poolrouter.SelectionRequest {
	return poolrouter.SelectionRequest{
		TenantID: 1, UserGroup: userGroup, RequestedModel: "claude-3-5-sonnet", PoolGroupID: poolGroupID,
	}
}

// 守核心限制语义: 配了分组路由且命中本 model 后, 候选池在允许集内放行、不在则拒。
// mutation: gate 改回 AllowAll(恒 true) → 不允许池(9)的拒断言变红 (高档用户能溜进未授权池)。
func TestGroupPolicyGate_RestrictsToAllowedPool(t *testing.T) {
	repo := &fakeRoutesRepo{result: configured(5)}
	g := NewGroupPolicyGate(repo)

	if ok, _, err := g.Allow(context.Background(), nil, req("premium", 5)); err != nil || !ok {
		t.Fatalf("allowed pool 5: ok=%v err=%v, want allow", ok, err)
	}
	ok, reason, err := g.Allow(context.Background(), nil, req("premium", 9))
	if err != nil {
		t.Fatalf("disallowed pool: unexpected err %v", err)
	}
	if ok {
		t.Fatal("pool 9 not in allowed set must be rejected, got allow")
	}
	if reason != poolrouter.GateFailureGroupPolicy {
		t.Fatalf("reason = %q, want group_policy", reason)
	}
}

// 守白名单核心(F1): 该档配了有效路由、但本 model 没命中任何规则
// (Configured=true 且 Allowed 空) → 拒, 而非放行。这是 HUAKAI 的越档拦截。
// mutation: 把"配置了但本 model 未命中 → 拒"退回旧的"空集 → 放行" → 红
// (premium 只配 claude-* 时, 请求 gemini 会越档溜进任意池)。
func TestGroupPolicyGate_ConfiguredButModelMissDenies(t *testing.T) {
	repo := &fakeRoutesRepo{result: GroupRoutes{Configured: true, Allowed: map[int64]struct{}{}}}
	g := NewGroupPolicyGate(repo)

	ok, reason, err := g.Allow(context.Background(), nil, req("premium", 9))
	if err != nil {
		t.Fatalf("unexpected err %v", err)
	}
	if ok {
		t.Fatal("configured group with no rule matching this model must be denied (white-list), got allow")
	}
	if reason != poolrouter.GateFailureGroupPolicy {
		t.Fatalf("reason = %q, want group_policy", reason)
	}
}

// 守空档放行 + 不查库: user_group 空时直接放行, 绝不触 repo (无谓 DB 往返 + 老链路零影响)。
// mutation: gate 漏掉空档短路 → repo.called>0 / 行为依赖库 → 红。
func TestGroupPolicyGate_EmptyUserGroupAllowsWithoutRepo(t *testing.T) {
	repo := &fakeRoutesRepo{result: configured(5)}
	g := NewGroupPolicyGate(repo)

	ok, _, err := g.Allow(context.Background(), nil, req("", 9))
	if err != nil || !ok {
		t.Fatalf("empty user_group: ok=%v err=%v, want allow", ok, err)
	}
	if repo.called != 0 {
		t.Fatalf("repo called %d times for empty user_group, want 0", repo.called)
	}
}

// 守未配置直通: 该 (租户,档) 无任何有效路由 (Configured=false) → 放行 (不破坏未启用分组路由
// 的老租户)。mutation: 把"未配置→放行"改成"未配置→拒" → 红 (会拒掉所有未配置分组路由的付费用户)。
func TestGroupPolicyGate_UnconfiguredGroupAllows(t *testing.T) {
	repo := &fakeRoutesRepo{result: GroupRoutes{Configured: false, Allowed: map[int64]struct{}{}}}
	g := NewGroupPolicyGate(repo)

	if ok, _, err := g.Allow(context.Background(), nil, req("premium", 9)); err != nil || !ok {
		t.Fatalf("unconfigured group: ok=%v err=%v, want allow", ok, err)
	}
}

// 守付费可用性: premium 等非默认订阅档遇到 routes repo 瞬时错误时必须 fail-open 放行,
// 但必须打 fail-open observer, 让运维看到"库抖动期间可能临时蹭池"而不是静默放行。
// mutation: 把 repo 错改成 fail-closed → ok/reason/observer 断言红 (付费用户被误拒)。
func TestGroupPolicyGate_PremiumRepoErrorFailsOpenAndObserves(t *testing.T) {
	repo := &fakeRoutesRepo{err: errors.New("transient db error")}
	var failOpenObserved int
	var failClosedObserved int
	var gotErr error
	g := NewGroupPolicyGate(repo,
		WithFailOpenObserver(func(_ context.Context, _ poolrouter.SelectionRequest, err error) {
			failOpenObserved++
			gotErr = err
		}),
		WithFailClosedObserver(func(context.Context, poolrouter.SelectionRequest, error) {
			failClosedObserved++
		}),
	)

	ok, reason, err := g.Allow(context.Background(), nil, req("premium", 9))
	if err != nil {
		t.Fatalf("repo error: unexpected err %v", err)
	}
	if !ok {
		t.Fatal("premium repo transient error must fail-open to protect paid-user availability, got deny")
	}
	if reason != "" {
		t.Fatalf("reason = %q, want empty on fail-open", reason)
	}
	if failOpenObserved != 1 {
		t.Fatalf("fail-open observer called %d times for premium repo error, want 1", failOpenObserved)
	}
	if failClosedObserved != 0 {
		t.Fatalf("fail-closed observer called %d times for premium repo error, want 0", failClosedObserved)
	}
	if gotErr == nil {
		t.Fatal("fail-open observer must receive the underlying repo error")
	}
}

// 守免费/默认组兼容: default 组是 HUAKAI 当前低档机制, repo 瞬时错误不能扩大成免费流量停服。
// mutation: 把全部 repo 错一律 fail-closed → default 放行断言红。
func TestGroupPolicyGate_DefaultRepoErrorFailsOpenForFreeTraffic(t *testing.T) {
	repo := &fakeRoutesRepo{err: errors.New("transient db error")}
	var failOpenObserved int
	var failClosedObserved int
	g := NewGroupPolicyGate(repo,
		WithFailOpenObserver(func(context.Context, poolrouter.SelectionRequest, error) {
			failOpenObserved++
		}),
		WithFailClosedObserver(func(context.Context, poolrouter.SelectionRequest, error) {
			failClosedObserved++
		}),
	)

	if ok, _, err := g.Allow(context.Background(), nil, req("default", 9)); err != nil || !ok {
		t.Fatalf("default repo error: ok=%v err=%v, want compatibility fail-open allow", ok, err)
	}
	if failOpenObserved != 1 {
		t.Fatalf("fail-open observer called %d times, want 1", failOpenObserved)
	}
	if failClosedObserved != 0 {
		t.Fatalf("fail-closed observer called %d times for default repo error, want 0", failClosedObserved)
	}
}

// 守 selector 真实后果: premium repo 瞬时错误经 DefaultSelector 时也必须保可用性放行,
// 不能把库抖动扩大成 ErrNoEligibleAccount。mutation: gate fail-closed → Select 报无账号, 本测试红。
func TestGroupPolicyGate_PremiumRepoErrorAllowsDefaultSelectorDuringTransientRepoError(t *testing.T) {
	repo := &fakeRoutesRepo{err: errors.New("transient db error")}
	chain := poolrouter.DefaultGateChain()
	chain.GroupPolicy = NewGroupPolicyGate(repo)
	sel := poolrouter.NewDefaultSelector(
		oneAccountSource{},
		poolrouter.WithGateChain(chain),
		poolrouter.WithSlotManager(grantingSlots{}),
	)

	res, err := sel.Select(context.Background(), poolrouter.SelectionRequest{
		TenantID: 1, UserGroup: "premium", RequestedModel: "claude-3-5-sonnet", PoolGroupID: 5,
	})
	if err != nil {
		t.Fatalf("premium repo transient error should fail-open through selector, got err=%v", err)
	}
	if res == nil || res.AccountID != 1 {
		t.Fatalf("premium repo transient error selector result=%+v, want account 1", res)
	}
}

// 守 repo 未注入/暂缺时的付费可用性: 非默认档无法校验 routes 时默认 fail-open,
// 但必须触发 observer 告警, 避免静默扩大高级池风险。
// mutation: nil repo fail-closed → ok/reason/observer 断言红。
func TestGroupPolicyGate_PremiumNilRepoFailsOpenAndObserves(t *testing.T) {
	var failOpenObserved int
	var failClosedObserved int
	var gotErr error
	g := NewGroupPolicyGate(nil,
		WithFailOpenObserver(func(_ context.Context, _ poolrouter.SelectionRequest, err error) {
			failOpenObserved++
			gotErr = err
		}),
		WithFailClosedObserver(func(context.Context, poolrouter.SelectionRequest, error) {
			failClosedObserved++
		}),
	)
	ok, reason, err := g.Allow(context.Background(), nil, req("premium", 9))
	if err != nil {
		t.Fatalf("nil repo premium: unexpected err %v", err)
	}
	if !ok {
		t.Fatal("nil repo premium must fail-open with alert, got deny")
	}
	if reason != "" {
		t.Fatalf("reason = %q, want empty on fail-open", reason)
	}
	if failOpenObserved != 1 {
		t.Fatalf("fail-open observer called %d times, want 1", failOpenObserved)
	}
	if failClosedObserved != 0 {
		t.Fatalf("fail-closed observer called %d times for nil repo premium, want 0", failClosedObserved)
	}
	if gotErr == nil {
		t.Fatal("fail-open observer must receive repo unavailable error")
	}
}

// 守 default 休眠兼容: 低档/default 流量在 repo 未注入时仍放行, 避免免费流量被误停。
func TestGroupPolicyGate_DefaultNilRepoAllows(t *testing.T) {
	g := NewGroupPolicyGate(nil)
	if ok, _, err := g.Allow(context.Background(), nil, req("default", 9)); err != nil || !ok {
		t.Fatalf("nil repo default: ok=%v err=%v, want allow", ok, err)
	}
}

// 守入参透传(F3): gate 必须把 req 的 TenantID/UserGroup/RequestedModel 原样传给 repo。
// fake repo 记录实收参数。mutation: gate 传错字段(如把 PoolGroupID 当 tenantID)或写死 → 红。
func TestGroupPolicyGate_PassesParamsThrough(t *testing.T) {
	repo := &fakeRoutesRepo{result: configured(7)}
	g := NewGroupPolicyGate(repo)

	r := poolrouter.SelectionRequest{TenantID: 42, UserGroup: "gold", RequestedModel: "gpt-4o", PoolGroupID: 7}
	if _, _, err := g.Allow(context.Background(), nil, r); err != nil {
		t.Fatalf("unexpected err %v", err)
	}
	if repo.gotTenantID != 42 || repo.gotUserGroup != "gold" || repo.gotModel != "gpt-4o" {
		t.Fatalf("repo got (tenant=%d, group=%q, model=%q), want (42, gold, gpt-4o)", repo.gotTenantID, repo.gotUserGroup, repo.gotModel)
	}
}

// 守 PrepareForSelection 缓存裁决(hoist 的 gate 侧不变量): 预备后逐候选 Allow 不再查库,
// 且裁决与直接 Allow 一致、与候选账号无关。
// mutation: PrepareForSelection 返回 gate 自身(每候选再查库)→ repo.called>1 红;
//
//	prepared 裁决与 Allow 分叉 → 断言红。
func TestGroupPolicyGate_PrepareForSelectionCachesVerdict(t *testing.T) {
	repo := &fakeRoutesRepo{result: configured(5)}
	g := NewGroupPolicyGate(repo)

	prepared := g.PrepareForSelection(context.Background(), req("premium", 5))
	for i := 0; i < 3; i++ {
		if ok, _, err := prepared.Allow(context.Background(), &poolrouter.AccountSnapshot{ID: int64(i)}, req("premium", 5)); err != nil || !ok {
			t.Fatalf("prepared allow pool 5 (candidate %d): ok=%v err=%v", i, ok, err)
		}
	}
	if repo.called != 1 {
		t.Fatalf("repo called %d times, want 1 (PrepareForSelection caches verdict; per-candidate Allow must not re-query)", repo.called)
	}

	repoDeny := &fakeRoutesRepo{result: configured(5)}
	gDeny := NewGroupPolicyGate(repoDeny)
	preparedDeny := gDeny.PrepareForSelection(context.Background(), req("premium", 9))
	ok, reason, err := preparedDeny.Allow(context.Background(), &poolrouter.AccountSnapshot{ID: 1}, req("premium", 9))
	if err != nil || ok || reason != poolrouter.GateFailureGroupPolicy {
		t.Fatalf("prepared deny pool 9: ok=%v reason=%q err=%v, want deny+group_policy", ok, reason, err)
	}
}

func TestModelPatternMatches(t *testing.T) {
	cases := []struct {
		pattern, model string
		want           bool
	}{
		{"*", "claude-3-5-sonnet", true},
		{"", "claude-3-5-sonnet", true},
		{"claude-*", "claude-3-5-sonnet", true},
		{"claude-*", "gpt-4o", false},
		{"claude-3-5-sonnet", "claude-3-5-sonnet", true},
		{"claude-3-5-sonnet", "claude-3-haiku", false},
		{"gpt-*", "claude-3", false},
	}
	for _, c := range cases {
		if got := ModelPatternMatches(c.pattern, c.model); got != c.want {
			t.Fatalf("ModelPatternMatches(%q,%q) = %v, want %v", c.pattern, c.model, got, c.want)
		}
	}
}
