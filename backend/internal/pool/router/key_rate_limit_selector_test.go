package router

import (
	"context"
	"errors"
	"testing"
)

// 一个已耗尽 RPM 预算的 key 会在选号之前被以 ErrKeyRateLimited 拒绝。
// 变异守卫：若忽略 Check 的裁决，请求会通过，此处变红。
func TestKeyRateLimitSelector_OverBudget_Rejects(t *testing.T) {
	c := recCounter()
	c.Record(7, 0) // key 7 已用掉它的 RPM-1
	sel := NewKeyRateLimitSelector(fakeRecSelector{res: &SelectionResult{AccountID: 9}}, c, 1, 0)
	res, err := sel.Select(context.Background(), SelectionRequest{APIKeyID: 7})
	if !errors.Is(err, ErrKeyRateLimited) || res != nil {
		t.Fatalf("over-budget key must be rejected with ErrKeyRateLimited, got res=%v err=%v", res, err)
	}
	// 另一个 key 不受影响。
	if _, err := sel.Select(context.Background(), SelectionRequest{APIKeyID: 8}); err != nil {
		t.Fatalf("untouched key must pass, got %v", err)
	}
}

// 在预算之内时，请求正常选号且会被记录——由两次成功选号后第三次请求越过
// RPM-2 预算来证明。
func TestKeyRateLimitSelector_UnderBudget_PassesAndRecords(t *testing.T) {
	c := recCounter()
	sel := NewKeyRateLimitSelector(fakeRecSelector{res: &SelectionResult{AccountID: 9}}, c, 2, 0)
	req := SelectionRequest{APIKeyID: 7}
	for i := 0; i < 2; i++ {
		if res, err := sel.Select(context.Background(), req); err != nil || res.AccountID != 9 {
			t.Fatalf("request %d under RPM-2 must pass, got res=%v err=%v", i, res, err)
		}
	}
	if _, err := sel.Select(context.Background(), req); !errors.Is(err, ErrKeyRateLimited) {
		t.Fatalf("3rd request over RPM-2 must be rejected (proves each select recorded), got %v", err)
	}
}

// TPM 依据请求的 estimated input token 数来强制执行。
func TestKeyRateLimitSelector_TPM(t *testing.T) {
	c := recCounter()
	sel := NewKeyRateLimitSelector(fakeRecSelector{res: &SelectionResult{AccountID: 9}}, c, 0, 100)
	if _, err := sel.Select(context.Background(), SelectionRequest{APIKeyID: 7, EstimatedInputTokens: 101}); !errors.Is(err, ErrKeyRateLimited) {
		t.Fatalf("a single 101-token request must be rejected on a TPM-100 budget, got %v", err)
	}
}

// 默认惰性：零上限、nil counter、以及无 API key id 都会放行通过。
func TestKeyRateLimitSelector_InertByDefault(t *testing.T) {
	c := recCounter()
	c.Record(7, 0)
	c.Record(7, 0)
	pass := &SelectionResult{AccountID: 9}
	// 零上限 = 关闭
	if _, err := NewKeyRateLimitSelector(fakeRecSelector{res: pass}, c, 0, 0).Select(context.Background(), SelectionRequest{APIKeyID: 7}); err != nil {
		t.Fatalf("zero limits must pass, got %v", err)
	}
	// nil counter（计数器为 nil）
	if _, err := NewKeyRateLimitSelector(fakeRecSelector{res: pass}, nil, 1, 1).Select(context.Background(), SelectionRequest{APIKeyID: 7}); err != nil {
		t.Fatalf("nil counter must pass, got %v", err)
	}
	// 无 API key id（未鉴权路径）——绝不按 key 限流
	if _, err := NewKeyRateLimitSelector(fakeRecSelector{res: pass}, c, 1, 0).Select(context.Background(), SelectionRequest{APIKeyID: 0}); err != nil {
		t.Fatalf("APIKeyID 0 must pass, got %v", err)
	}
}

// 一个 wait-plan 结果不消耗 key 预算（只有具体的选号才消耗）。
func TestKeyRateLimitSelector_WaitPlanNotRecorded(t *testing.T) {
	c := recCounter()
	wp := &SelectionResult{AccountID: 9, WaitPlan: &WaitPlan{AccountID: 9}}
	sel := NewKeyRateLimitSelector(fakeRecSelector{res: wp}, c, 1, 0)
	if _, err := sel.Select(context.Background(), SelectionRequest{APIKeyID: 7}); err != nil {
		t.Fatalf("wait-plan select must pass, got %v", err)
	}
	// 预算未被消耗 → 紧接着的请求仍被放行
	if _, err := sel.Select(context.Background(), SelectionRequest{APIKeyID: 7}); err != nil {
		t.Fatalf("wait-plan must not consume budget, got %v", err)
	}
}
