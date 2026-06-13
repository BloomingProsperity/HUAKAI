package hermesops

import (
	"context"
	"testing"
)

// mutatingSpec builds a minimal MUTATING spec (Mutating=true) so the catalog
// filter has a real mutating tool to exclude. A mutating tool sets Resolve/Mutate
// (not Run); here they are non-nil stubs so the spec is well-formed.
func mutatingSpec(name string) ToolSpec {
	return ToolSpec{
		Name: name, Category: CategoryMutating, Mutating: true, RequiresConfirmation: true,
		RequiredRole: RolePlatformAdmin,
		Resolve: func(_ context.Context, _ ToolRequest) (MutationPlan, error) {
			return MutationPlan{}, nil
		},
		Mutate: func(_ context.Context, _ ToolRequest, _ MutationPlan) (ToolResult, error) {
			return ToolResult{}, nil
		},
	}
}

func TestReadOnlyCatalogExcludesMutatingTools(t *testing.T) {
	// Regression (SAFETY, DISCRIMINATING): the LLM-facing catalog must expose ONLY
	// read-only tools. Mutation: if the `s.Mutating` filter in ReadOnlyCatalog is
	// dropped, account_pause would appear in the catalog and the model could be
	// told it exists. We register BOTH a read-only and a mutating tool and assert
	// the mutating one is absent AND the read-only one is present — the test fails
	// whether the filter is removed (mutating leaks in) or inverted (read-only
	// dropped).
	reg := NewRegistry()
	reg.Register(okSpec("audit_lookup", RoleTenantOperator)) // read-only
	reg.Register(mutatingSpec("account_pause"))              // mutating

	catalog := reg.ReadOnlyCatalog()

	if len(catalog) != 1 {
		t.Fatalf("catalog size=%d want 1 (only the read-only tool)", len(catalog))
	}
	if catalog[0].Name != "audit_lookup" {
		t.Fatalf("catalog[0]=%q want audit_lookup", catalog[0].Name)
	}
	for _, c := range catalog {
		if c.Name == "account_pause" {
			t.Fatalf("MUTATING tool account_pause leaked into the LLM catalog")
		}
	}
}

func TestReadOnlyCatalogIsNameSortedAndSchemaCopied(t *testing.T) {
	// Regression: catalog order must be stable (name-sorted) for deterministic LLM
	// context, and the returned InputSchema must be a copy so a caller cannot
	// mutate the registry's schema through the catalog entry.
	reg := NewRegistry()
	z := okSpec("zeta", RoleTenantOperator)
	z.InputSchema = map[string]string{"k": "v"}
	reg.Register(z)
	reg.Register(okSpec("alpha", RoleTenantOperator))

	catalog := reg.ReadOnlyCatalog()
	if len(catalog) != 2 || catalog[0].Name != "alpha" || catalog[1].Name != "zeta" {
		t.Fatalf("catalog order=%v want [alpha zeta]", []string{catalog[0].Name, catalog[1].Name})
	}
	// Mutating the returned schema must not affect the registry's stored schema.
	catalog[1].InputSchema["k"] = "tampered"
	if got, _ := reg.Get("zeta"); got.InputSchema["k"] != "v" {
		t.Fatalf("registry schema mutated through catalog copy: %q", got.InputSchema["k"])
	}
}
