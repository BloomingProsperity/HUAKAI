// HUAKAI · iKun

package subscriptionenforce

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	poolrouter "github.com/BloomingProsperity/HUAKAI/internal/pool/router"
)

// groupAwareRepo: premium 档允许 pool {5}, default 档允许 pool {7}, 两档都已配置。
type groupAwareRepo struct{}

func (groupAwareRepo) GroupRoutes(_ context.Context, _ int64, userGroup, _ string) (GroupRoutes, error) {
	switch userGroup {
	case "premium":
		return GroupRoutes{Configured: true, Allowed: map[int64]struct{}{5: {}}}, nil
	case "default":
		return GroupRoutes{Configured: true, Allowed: map[int64]struct{}{7: {}}}, nil
	}
	return GroupRoutes{}, nil
}

type oneAccountSource struct{}

func (oneAccountSource) ListAccounts(context.Context, poolrouter.SelectionRequest) ([]*poolrouter.AccountSnapshot, error) {
	return []*poolrouter.AccountSnapshot{{ID: 1, MaxConcurrency: 4}}, nil
}

type grantingSlots struct{}

func (grantingSlots) Acquire(context.Context, *poolrouter.AccountSnapshot, poolrouter.SelectionRequest) (*poolrouter.AcquireResult, error) {
	tok := uuid.New()
	return &poolrouter.AcquireResult{
		AcquisitionToken: tok,
		Release:          poolrouter.NewIdempotentRelease(tok, func(context.Context) error { return nil }),
	}, nil
}

// TestGroupPolicyGate_RestrictsTierThroughDefaultSelector 守"把真 gate 按 buildSelector 的
// 方式接进 DefaultSelector 的 gate 链(经 ForSelection 预备)后, 选号时按订阅档限制 pool":
// premium 可进 pool 5; default 打 pool 5 被拒(其允许集是 {7})→ ErrNoEligibleAccount。
// 这是 selector_wiring 把 GroupPolicy 从 AllowAllGate 换成真 gate 的端到端行为验证。
// mutation: GroupPolicy 仍是 AllowAllGate(selector_wiring 漏接真 gate)→ default 也拿到
// 账号 1 → ErrNoEligibleAccount 断言红。
func TestGroupPolicyGate_RestrictsTierThroughDefaultSelector(t *testing.T) {
	chain := poolrouter.DefaultGateChain()
	chain.GroupPolicy = NewGroupPolicyGate(groupAwareRepo{})
	sel := poolrouter.NewDefaultSelector(
		oneAccountSource{},
		poolrouter.WithGateChain(chain),
		poolrouter.WithSlotManager(grantingSlots{}),
	)

	// premium 打 pool 5 (在其允许集) → 选到账号 1。
	res, err := sel.Select(context.Background(), poolrouter.SelectionRequest{
		TenantID: 1, UserGroup: "premium", RequestedModel: "claude-3-5-sonnet", PoolGroupID: 5,
	})
	if err != nil {
		t.Fatalf("premium select: %v", err)
	}
	if res == nil || res.AccountID != 1 {
		t.Fatalf("premium got %+v, want account 1", res)
	}

	// default 打 pool 5 (其允许集是 {7}, 5 不在) → 全候选被 gate 滤 → ErrNoEligibleAccount。
	if _, err := sel.Select(context.Background(), poolrouter.SelectionRequest{
		TenantID: 1, UserGroup: "default", RequestedModel: "claude-3-5-sonnet", PoolGroupID: 5,
	}); !errors.Is(err, poolrouter.ErrNoEligibleAccount) {
		t.Fatalf("default into premium pool: err=%v, want ErrNoEligibleAccount (gate must deny)", err)
	}
}

func TestGroupPolicyGate_RepoErrorPropagatesThroughPASRSelector(t *testing.T) {
	chain := poolrouter.DefaultGateChain()
	chain.GroupPolicy = NewGroupPolicyGate(&fakeRoutesRepo{err: errors.New("routes read failed")})
	ring := poolrouter.NewAccountRing([]int64{1}, 17)
	sel, err := poolrouter.NewPASRSelector(poolrouter.PASRSelectorConfig{
		Accounts: oneAccountSource{},
		Gates:    chain,
		Segments: poolrouter.NewSegmentTable(poolrouter.SegmentTableConfig{}),
		RingProvider: func() *poolrouter.AccountRing {
			return ring
		},
	})
	if err != nil {
		t.Fatalf("NewPASRSelector: %v", err)
	}

	res, err := sel.Select(context.Background(), poolrouter.SelectionRequest{
		TenantID: 1, UserGroup: "premium", RequestedModel: "claude-3-5-sonnet", PoolGroupID: 5,
	})
	if res != nil || !errors.Is(err, poolrouter.ErrGroupPolicyUnavailable) {
		t.Fatalf("PASR result=%+v err=%v want nil+ErrGroupPolicyUnavailable", res, err)
	}
}
