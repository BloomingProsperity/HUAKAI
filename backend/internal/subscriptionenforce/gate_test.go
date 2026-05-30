// HUAKAI · iKun

package subscriptionenforce

import (
	"context"
	"errors"
	"testing"

	poolrouter "github.com/BloomingProsperity/HUAKAI/internal/pool/router"
)

type fakeRoutesRepo struct {
	allowed map[int64]struct{}
	err     error
	called  int
}

func (f *fakeRoutesRepo) AllowedPoolGroups(_ context.Context, _ int64, _ string, _ string) (map[int64]struct{}, error) {
	f.called++
	return f.allowed, f.err
}

func req(userGroup string, poolGroupID int64) poolrouter.SelectionRequest {
	return poolrouter.SelectionRequest{
		TenantID: 1, UserGroup: userGroup, RequestedModel: "claude-3-5-sonnet", PoolGroupID: poolGroupID,
	}
}

// 守核心限制语义: 配了分组路由后, 候选池在允许集内放行、不在则拒。
// mutation: gate 改回 AllowAll(恒 true) → 不允许池(9)的拒断言变红 (高档用户能溜进未授权池)。
func TestGroupPolicyGate_RestrictsToAllowedPool(t *testing.T) {
	repo := &fakeRoutesRepo{allowed: map[int64]struct{}{5: {}}}
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

// 守空档放行 + 不查库: user_group 空时直接放行, 绝不触 repo (无谓 DB 往返 + 老链路零影响)。
// mutation: gate 漏掉空档短路 → repo.called>0 / 行为依赖库 → 红。
func TestGroupPolicyGate_EmptyUserGroupAllowsWithoutRepo(t *testing.T) {
	repo := &fakeRoutesRepo{allowed: map[int64]struct{}{5: {}}}
	g := NewGroupPolicyGate(repo)

	ok, _, err := g.Allow(context.Background(), nil, req("", 9))
	if err != nil || !ok {
		t.Fatalf("empty user_group: ok=%v err=%v, want allow", ok, err)
	}
	if repo.called != 0 {
		t.Fatalf("repo called %d times for empty user_group, want 0", repo.called)
	}
}

// 守未配置直通: 该租户/档/模型无路由规则(允许集空) → 放行 (不破坏未启用分组路由的租户)。
// mutation: 把"空集→放行"改成"空集→拒" → 红 (会拒掉所有未配置分组路由的付费用户)。
func TestGroupPolicyGate_NoRulesAllows(t *testing.T) {
	repo := &fakeRoutesRepo{allowed: map[int64]struct{}{}}
	g := NewGroupPolicyGate(repo)

	if ok, _, err := g.Allow(context.Background(), nil, req("premium", 9)); err != nil || !ok {
		t.Fatalf("no rules: ok=%v err=%v, want allow", ok, err)
	}
}

// 守 repo 错 fail-open: 路由档位非钱/安全闸, 瞬时 DB 错放行 (不拒付费用户)。
// mutation: 改成 fail-closed(err→拒) → 红 (DB 抖动会拒掉所有高档用户路由)。
func TestGroupPolicyGate_RepoErrorFailsOpen(t *testing.T) {
	repo := &fakeRoutesRepo{err: errors.New("transient db error")}
	g := NewGroupPolicyGate(repo)

	if ok, _, err := g.Allow(context.Background(), nil, req("premium", 9)); err != nil || !ok {
		t.Fatalf("repo error: ok=%v err=%v, want fail-open allow with no err", ok, err)
	}
}

// 守 nil repo 休眠安全: 字段已加但未注入 repo 时 gate 恒放行 (S1a 休眠态)。
func TestGroupPolicyGate_NilRepoAllows(t *testing.T) {
	g := NewGroupPolicyGate(nil)
	if ok, _, err := g.Allow(context.Background(), nil, req("premium", 9)); err != nil || !ok {
		t.Fatalf("nil repo: ok=%v err=%v, want allow", ok, err)
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
