package hermesops

import (
	"context"
	"errors"
	"testing"
)

// TestResolveProposalReturnsPlanAndNeverMutates 是 LLM 提议路径的核心结构性守卫:
// ResolveProposal 必须返回只读的 dry-run plan,且绝不能调用 Mutate。有区分度:
// 一旦 ResolveProposal 被改成调用 spec.Mutate(让 LLM 能执行),mutateCalled 就会翻转,此测试变红。
func TestResolveProposalReturnsPlanAndNeverMutates(t *testing.T) {
	mutateCalled := false
	spec := mutatingSpec("account_pause") // 可提议的改动型工具
	spec.Resolve = func(_ context.Context, _ ToolRequest) (MutationPlan, error) {
		return MutationPlan{TargetType: "provider_account", TargetID: 42, Preview: map[string]any{"next_enabled": false}}, nil
	}
	spec.Mutate = func(_ context.Context, _ ToolRequest, _ MutationPlan) (ToolResult, error) {
		mutateCalled = true
		return ToolResult{}, nil
	}
	reg := NewRegistry()
	reg.Register(spec)

	plan, err := reg.ResolveProposal(context.Background(), "account_pause", RolePlatformAdmin, ToolRequest{TenantID: 7, Role: RolePlatformAdmin})
	if err != nil {
		t.Fatalf("ResolveProposal: %v", err)
	}
	if plan.TargetID != 42 || plan.TargetType != "provider_account" {
		t.Fatalf("dry-run plan not returned: %+v", plan)
	}
	if mutateCalled {
		t.Fatal("ResolveProposal called Mutate — the LLM-propose path MUST be read-only (Resolve only)")
	}
}

func TestResolveProposalRefusesNonProposable(t *testing.T) {
	// 一个未标记 Proposable 的改动型工具(不可逆 / A 级,例如凭证轮换)必须被拒绝 ——
	// LLM 永远不能提议它。
	reg := NewRegistry()
	reg.Register(nonProposableMutatingSpec("renew_trigger"))
	_, err := reg.ResolveProposal(context.Background(), "renew_trigger", RolePlatformAdmin, ToolRequest{TenantID: 7, Role: RolePlatformAdmin})
	if !errors.Is(err, ErrNotProposable) {
		t.Fatalf("non-proposable mutating tool must be ErrNotProposable, got %v", err)
	}
}

func TestResolveProposalRefusesReadOnly(t *testing.T) {
	reg := NewRegistry()
	reg.Register(okSpec("audit_lookup", RoleTenantOperator))
	_, err := reg.ResolveProposal(context.Background(), "audit_lookup", RoleTenantOperator, ToolRequest{TenantID: 7, Role: RoleTenantOperator})
	if !errors.Is(err, ErrNotMutating) {
		t.Fatalf("read-only tool must be ErrNotMutating, got %v", err)
	}
}

func TestResolveProposalRefusesInsufficientRole(t *testing.T) {
	reg := NewRegistry()
	reg.Register(mutatingSpec("account_pause")) // 需要 RolePlatformAdmin
	_, err := reg.ResolveProposal(context.Background(), "account_pause", RoleTenantOperator, ToolRequest{TenantID: 7, Role: RoleTenantOperator})
	if !errors.Is(err, ErrToolForbidden) {
		t.Fatalf("insufficient role must be ErrToolForbidden, got %v", err)
	}
}

func TestResolveProposalUnknownTool(t *testing.T) {
	reg := NewRegistry()
	_, err := reg.ResolveProposal(context.Background(), "nope", RolePlatformAdmin, ToolRequest{})
	if !errors.Is(err, ErrToolUnknown) {
		t.Fatalf("unknown tool must be ErrToolUnknown, got %v", err)
	}
}

// TestProposableCatalogExcludesNonProposableMutating:一个"改动型但不可提议"的工具
// 绝不能出现在 ProposableCatalog 中。有区分度:去掉
// `case s.Mutating: continue` 这个排除分支,renew_trigger 就会泄露给 LLM → 变红。
func TestProposableCatalogExcludesNonProposableMutating(t *testing.T) {
	reg := NewRegistry()
	reg.Register(okSpec("audit_lookup", RoleTenantOperator)) // 只读
	reg.Register(mutatingSpec("account_pause"))              // 可提议的改动型
	reg.Register(nonProposableMutatingSpec("renew_trigger")) // 不可提议的改动型
	names := map[string]bool{}
	for _, c := range reg.ProposableCatalog() {
		names[c.Name] = true
	}
	if !names["audit_lookup"] || !names["account_pause"] {
		t.Fatalf("proposable catalog missing expected tools: %v", names)
	}
	if names["renew_trigger"] {
		t.Fatal("NON-proposable mutating tool renew_trigger leaked into the proposable catalog (LLM must never see it)")
	}
}
