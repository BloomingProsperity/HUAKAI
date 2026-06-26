package hermesops

import (
	"context"
	"errors"
	"testing"
)

// TestResolveProposalReturnsPlanAndNeverMutates is the central STRUCTURAL guard for
// the LLM-propose path: ResolveProposal must return the read-only dry-run plan and
// must NEVER call Mutate. DISCRIMINATING: if ResolveProposal were ever changed to
// invoke spec.Mutate (so the LLM could execute), mutateCalled flips and this RED.
func TestResolveProposalReturnsPlanAndNeverMutates(t *testing.T) {
	mutateCalled := false
	spec := mutatingSpec("account_pause") // proposable mutating
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
	// A mutating tool not marked Proposable (irreversible / A-level, e.g. credential
	// rotation) must be refused — the LLM can never propose it.
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
	reg.Register(mutatingSpec("account_pause")) // requires RolePlatformAdmin
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

// TestProposableCatalogExcludesNonProposableMutating: a mutating-but-not-proposable
// tool must NOT appear in ProposableCatalog. DISCRIMINATING: drop the
// `case s.Mutating: continue` exclusion and renew_trigger leaks to the LLM → RED.
func TestProposableCatalogExcludesNonProposableMutating(t *testing.T) {
	reg := NewRegistry()
	reg.Register(okSpec("audit_lookup", RoleTenantOperator)) // read-only
	reg.Register(mutatingSpec("account_pause"))              // proposable mutating
	reg.Register(nonProposableMutatingSpec("renew_trigger")) // NON-proposable mutating
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
