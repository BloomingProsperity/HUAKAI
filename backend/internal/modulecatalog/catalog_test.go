package modulecatalog

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// featureTreePath is the project feature tree relative to this package dir
// (backend/internal/modulecatalog -> repo root docs/...).
const featureTreePath = "../../../docs/process/feature-tree/feature-tree.json"

// committedCatalogPath is the embedded artifact's on-disk location.
const committedCatalogPath = "module-catalog.json"

// TestEmbeddedCatalogParses — the committed artifact must load and be non-empty.
// Regression: if module-catalog.json is hand-edited into invalid JSON, or the
// generator emits a shape the loader can't decode, Load() errors -> RED. The
// non-empty assertion also catches an accidentally-blanked artifact.
func TestEmbeddedCatalogParses(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatalf("Load embedded catalog: %v", err)
	}
	if len(c.Modules) == 0 {
		t.Fatalf("embedded catalog has 0 modules — generator regression or blanked artifact")
	}
	// A known money-path package must be present so Lookup is exercised against
	// real data (the seed convention depends on billing being catalogued).
	if _, ok := c.Lookup("billing"); !ok {
		t.Fatalf("catalog missing 'billing' entry; Lookup over real data failed")
	}
}

// TestGeneratorOutputParsesAndMapsSection — generator output is well-formed and
// carries the section/feature mapping (not just bare pkg names).
// Regression: if GenerateFromBytes dropped the section/feature_id wiring (e.g.
// stopped copying lf.section), the billing entry's Section would be empty -> RED.
func TestGeneratorOutputParsesAndMapsSection(t *testing.T) {
	cat, err := GenerateFromFile(featureTreePath)
	if err != nil {
		t.Fatalf("GenerateFromFile: %v", err)
	}
	if len(cat.Modules) == 0 {
		t.Fatalf("generated 0 modules")
	}
	m, ok := cat.Lookup("billing")
	if !ok {
		t.Fatalf("generated catalog missing 'billing'")
	}
	if m.Section == "" || m.FeatureID == "" {
		t.Fatalf("billing entry missing section/feature mapping: %+v", m)
	}
	// Synthetic non-Go markers must NOT be indexed as modules.
	if _, ok := cat.Lookup("(rust)"); ok {
		t.Fatalf("synthetic pkg '(rust)' leaked into catalog as a module")
	}
}

// TestCatalogStalenessGuard — THE drift guard. It regenerates the catalog in
// memory from the live feature tree and byte-compares against the committed
// module-catalog.json.
//
// One-sentence regression: if someone edits docs/.../feature-tree.json (adds a
// module, changes a status/parity) but forgets to re-run modulecatalog-gen, the
// committed artifact no longer matches the fresh regeneration and this test goes
// RED — so the static catalog can never silently drift out of sync with the
// feature tree.
func TestCatalogStalenessGuard(t *testing.T) {
	cat, err := GenerateFromFile(featureTreePath)
	if err != nil {
		t.Fatalf("regenerate from feature tree: %v", err)
	}
	fresh, err := cat.MarshalDeterministic()
	if err != nil {
		t.Fatalf("marshal fresh catalog: %v", err)
	}
	committed, err := os.ReadFile(committedCatalogPath)
	if err != nil {
		t.Fatalf("read committed catalog: %v", err)
	}
	if !bytes.Equal(bytes.TrimRight(fresh, "\n"), bytes.TrimRight(committed, "\n")) {
		t.Fatalf("module-catalog.json is STALE vs feature-tree.json.\n"+
			"Run: (cd %s && go run ./cmd/modulecatalog-gen)\n"+
			"then commit internal/modulecatalog/module-catalog.json.",
			backendDirHint())
	}
}

func backendDirHint() string {
	if abs, err := filepath.Abs("../.."); err == nil {
		return abs
	}
	return "backend"
}
