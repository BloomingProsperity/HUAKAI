package router

import (
	"context"
	"errors"
	"testing"
)

// 超出 binding RPM 预算的请求在选号前被 ErrBindingRateLimited 拒绝。
// 变异守卫:若忽略 Check 裁定,请求会通过,本测转红。
func TestBindingRateLimitSelector_OverBudget_Rejects(t *testing.T) {
	c := recCounter()
	c.Record(7, 0) // binding 7 已用掉 RPM-1
	sel := NewBindingRateLimitSelector(fakeRecSelector{res: &SelectionResult{AccountID: 9}}, c)
	req := SelectionRequest{BindingID: 7, BindingRPMLimit: 1}
	res, err := sel.Select(context.Background(), req)
	if !errors.Is(err, ErrBindingRateLimited) || res != nil {
		t.Fatalf("超预算 binding 应被 ErrBindingRateLimited 拒, got res=%v err=%v", res, err)
	}
	// 另一个 binding 不受影响(预算逐 binding 独立)。
	if _, err := sel.Select(context.Background(), SelectionRequest{BindingID: 8, BindingRPMLimit: 1}); err != nil {
		t.Fatalf("不同 binding 应通过, got %v", err)
	}
}

// 预算内正常选号 AND 被记录——由两次成功选号后第三次越过 RPM-2 预算证明 reserve-on-select 生效。
func TestBindingRateLimitSelector_UnderBudget_PassesAndRecords(t *testing.T) {
	c := recCounter()
	sel := NewBindingRateLimitSelector(fakeRecSelector{res: &SelectionResult{AccountID: 9}}, c)
	req := SelectionRequest{BindingID: 7, BindingRPMLimit: 2}
	for i := 0; i < 2; i++ {
		if res, err := sel.Select(context.Background(), req); err != nil || res.AccountID != 9 {
			t.Fatalf("RPM-2 内第 %d 次应通过, got res=%v err=%v", i, res, err)
		}
	}
	if _, err := sel.Select(context.Background(), req); !errors.Is(err, ErrBindingRateLimited) {
		t.Fatalf("第 3 次越过 RPM-2 应被拒(证明每次选号都记账), got %v", err)
	}
}

// TPM 按请求估算输入 token 强制。
func TestBindingRateLimitSelector_TPM(t *testing.T) {
	c := recCounter()
	sel := NewBindingRateLimitSelector(fakeRecSelector{res: &SelectionResult{AccountID: 9}}, c)
	req := SelectionRequest{BindingID: 7, BindingTPMLimit: 100, EstimatedInputTokens: 101}
	if _, err := sel.Select(context.Background(), req); !errors.Is(err, ErrBindingRateLimited) {
		t.Fatalf("单个 101-token 请求应被 TPM-100 预算拒, got %v", err)
	}
}

// 默认 inert:零限额 / nil 计数器 / BindingID 0 都 pass-through。
func TestBindingRateLimitSelector_InertByDefault(t *testing.T) {
	c := recCounter()
	c.Record(7, 0)
	c.Record(7, 0)
	pass := &SelectionResult{AccountID: 9}
	// 零限额 = off
	if _, err := NewBindingRateLimitSelector(fakeRecSelector{res: pass}, c).Select(context.Background(), SelectionRequest{BindingID: 7, BindingRPMLimit: 0, BindingTPMLimit: 0}); err != nil {
		t.Fatalf("零限额应通过, got %v", err)
	}
	// nil 计数器
	if _, err := NewBindingRateLimitSelector(fakeRecSelector{res: pass}, nil).Select(context.Background(), SelectionRequest{BindingID: 7, BindingRPMLimit: 1}); err != nil {
		t.Fatalf("nil 计数器应通过, got %v", err)
	}
	// 无 BindingID(未命中具体 binding)— 永不被 binding 限流
	if _, err := NewBindingRateLimitSelector(fakeRecSelector{res: pass}, c).Select(context.Background(), SelectionRequest{BindingID: 0, BindingRPMLimit: 1}); err != nil {
		t.Fatalf("BindingID 0 应通过, got %v", err)
	}
}

// WaitPlan 结果不消费 binding 预算(只有拿到具体账号才记账)。
func TestBindingRateLimitSelector_WaitPlanNotRecorded(t *testing.T) {
	c := recCounter()
	wp := &SelectionResult{AccountID: 9, WaitPlan: &WaitPlan{AccountID: 9}}
	sel := NewBindingRateLimitSelector(fakeRecSelector{res: wp}, c)
	req := SelectionRequest{BindingID: 7, BindingRPMLimit: 1}
	if _, err := sel.Select(context.Background(), req); err != nil {
		t.Fatalf("WaitPlan 选号应通过, got %v", err)
	}
	// 预算未消费 → 下一次请求仍允许
	if _, err := sel.Select(context.Background(), req); err != nil {
		t.Fatalf("WaitPlan 不应消费预算, got %v", err)
	}
}
