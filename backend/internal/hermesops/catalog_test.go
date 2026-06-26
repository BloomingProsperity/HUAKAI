package hermesops

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// mutatingSpec builds a minimal PROPOSABLE MUTATING spec (Mutating=true,
// Proposable=true, like account_pause/resume) so the catalog filters have a real
// mutating tool. A mutating tool sets Resolve/Mutate (not Run); here they are
// non-nil stubs so the spec is well-formed.
func mutatingSpec(name string) ToolSpec {
	return ToolSpec{
		Name: name, Category: CategoryMutating, Mutating: true, RequiresConfirmation: true,
		Proposable:   true,
		RequiredRole: RolePlatformAdmin,
		Resolve: func(_ context.Context, _ ToolRequest) (MutationPlan, error) {
			return MutationPlan{}, nil
		},
		Mutate: func(_ context.Context, _ ToolRequest, _ MutationPlan) (ToolResult, error) {
			return ToolResult{}, nil
		},
	}
}

// nonProposableMutatingSpec builds a MUTATING spec that is NOT LLM-proposable
// (Proposable=false, like renew_trigger / credential rotation): an operator may
// drive it via the H1 confirm path, but the LLM must never see or propose it.
func nonProposableMutatingSpec(name string) ToolSpec {
	s := mutatingSpec(name)
	s.Proposable = false
	return s
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

func TestProposableCatalogIncludesMutatingWithFlags(t *testing.T) {
	// ProposableCatalog (Phase B) DOES include mutating tools — but each is flagged
	// Mutating + RequiresConfirmation so the runner/LLM render a confirmation step.
	// Read-only tools are included with NO flags. DISCRIMINATING: if the flag-set
	// on the mutating branch is dropped, the mutating entry would look like a
	// directly-runnable read-only tool — this test goes RED.
	reg := NewRegistry()
	reg.Register(okSpec("audit_lookup", RoleTenantOperator)) // read-only
	reg.Register(mutatingSpec("account_pause"))              // mutating

	cat := reg.ProposableCatalog()
	if len(cat) != 2 {
		t.Fatalf("proposable catalog size=%d want 2 (read-only + mutating)", len(cat))
	}
	byName := map[string]CatalogTool{}
	for _, c := range cat {
		byName[c.Name] = c
	}
	ro, ok := byName["audit_lookup"]
	if !ok {
		t.Fatal("read-only audit_lookup missing from proposable catalog")
	}
	if ro.Mutating || ro.RequiresConfirmation {
		t.Fatalf("read-only tool must NOT be flagged mutating/requires_confirmation: %+v", ro)
	}
	mut, ok := byName["account_pause"]
	if !ok {
		t.Fatal("mutating account_pause missing from proposable catalog (it must be PROPOSABLE)")
	}
	if !mut.Mutating || !mut.RequiresConfirmation {
		t.Fatalf("mutating tool must be flagged Mutating+RequiresConfirmation: %+v", mut)
	}
}

func TestReadOnlyCatalogJSONUnchangedByNewFields(t *testing.T) {
	// SAFETY/COMPAT: adding Mutating/RequiresConfirmation (omitempty) must leave
	// ReadOnlyCatalog's wire output byte-identical — a read-only entry must NOT
	// serialize the new keys. DISCRIMINATING: drop the omitempty tag and this RED.
	reg := NewRegistry()
	reg.Register(okSpec("audit_lookup", RoleTenantOperator))
	blob, err := json.Marshal(reg.ReadOnlyCatalog())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, k := range []string{"mutating", "requires_confirmation"} {
		if strings.Contains(string(blob), k) {
			t.Fatalf("ReadOnlyCatalog JSON leaked new key %q (omitempty broken): %s", k, blob)
		}
	}

	// And ProposableCatalog's mutating entry DOES serialize the flags.
	reg.Register(mutatingSpec("account_pause"))
	pblob, err := json.Marshal(reg.ProposableCatalog())
	if err != nil {
		t.Fatalf("marshal proposable: %v", err)
	}
	for _, k := range []string{"mutating", "requires_confirmation"} {
		if !strings.Contains(string(pblob), k) {
			t.Fatalf("ProposableCatalog JSON missing flag key %q: %s", k, pblob)
		}
	}
}
