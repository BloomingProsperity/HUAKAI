package modulehttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/modulecatalog"
	"github.com/BloomingProsperity/HUAKAI/internal/moduleregistry"
)

// fakeSource is a controllable Source for unit tests (no DB, no real wiring).
type fakeSource struct {
	snaps   []moduleregistry.ModuleSnapshot
	catalog map[string]modulecatalog.CatalogModule
	idToPkg map[string]string
}

func (f *fakeSource) Snapshot(context.Context) []moduleregistry.ModuleSnapshot { return f.snaps }
func (f *fakeSource) CatalogLookup(pkg string) (modulecatalog.CatalogModule, bool) {
	m, ok := f.catalog[pkg]
	return m, ok
}
func (f *fakeSource) CatalogPkgFor(id string) (string, bool) {
	p, ok := f.idToPkg[id]
	return p, ok
}

func newFakeSource() *fakeSource {
	return &fakeSource{
		snaps: []moduleregistry.ModuleSnapshot{
			{
				Descriptor: moduleregistry.ModuleDescriptor{
					ID: "billing.service", Category: "money-path", Title: "Billing",
					Capabilities: []string{"settle", "reserve"},
				},
				Probe: moduleregistry.ProbeResult{Status: moduleregistry.StatusOK, Detail: "wired"},
			},
			{
				Descriptor: moduleregistry.ModuleDescriptor{
					ID: "routing.selector", Category: "routing", Title: "Selector",
				},
				Probe: moduleregistry.ProbeResult{Status: moduleregistry.StatusUnknown},
			},
		},
		catalog: map[string]modulecatalog.CatalogModule{
			"billing": {Pkg: "billing", Section: "§5 计费", FeatureID: "F-BILL-001", Status: "tested", Parity: "strong"},
		},
		idToPkg: map[string]string{
			"billing.service": "billing",
			// routing.selector intentionally UNmapped -> live-only, no overlay.
		},
	}
}

// TestMergeJoinsLiveAndCatalog — the merge must fold the static overlay onto the
// matching live module AND carry the live probe through.
// Regression: if Merge stopped calling CatalogLookup/CatalogPkgFor (overlay
// dropped), billing's Catalog would be nil; if it dropped the probe, LiveProbe
// status would be empty. Either goes RED.
func TestMergeJoinsLiveAndCatalog(t *testing.T) {
	views := Merge(context.Background(), newFakeSource(), "")
	if len(views) != 2 {
		t.Fatalf("views=%d want 2", len(views))
	}
	var billing *ModuleView
	for i := range views {
		if views[i].ID == "billing.service" {
			billing = &views[i]
		}
	}
	if billing == nil {
		t.Fatalf("billing.service missing from merged view")
	}
	if billing.Catalog == nil || billing.Catalog.FeatureID != "F-BILL-001" {
		t.Fatalf("billing static overlay not merged: %+v", billing.Catalog)
	}
	if billing.LiveProbe.Status != moduleregistry.StatusOK {
		t.Fatalf("billing live probe=%q want ok (probe not carried through)", billing.LiveProbe.Status)
	}
	// routing.selector is unmapped: it must appear live-only with NO overlay.
	for _, v := range views {
		if v.ID == "routing.selector" && v.Catalog != nil {
			t.Fatalf("unmapped module got a spurious catalog overlay: %+v", v.Catalog)
		}
	}
}

// TestHandlerReturnsSeededModules — the endpoint returns the live modules.
// Regression: if the handler returned an empty body or 500 on a valid source,
// the count assertion or status assertion goes RED.
func TestHandlerReturnsSeededModules(t *testing.T) {
	h := NewModulesHandler(newFakeSource())
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/admin/v1/modules", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp ModulesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Modules) != 2 {
		t.Fatalf("modules=%d want 2", len(resp.Modules))
	}
}

// TestHandlerCategoryFilter — ?category= filters to one category.
// Regression: if the handler ignored the query param (passed "" to Merge always)
// both modules would return for ?category=money-path and the count goes RED;
// this is a discriminating fixture because the two seeds have DIFFERENT
// categories, so a broken filter produces 2, a correct filter produces 1.
func TestHandlerCategoryFilter(t *testing.T) {
	h := NewModulesHandler(newFakeSource())
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/admin/v1/modules?category=money-path", nil))

	var resp ModulesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Modules) != 1 {
		t.Fatalf("filtered modules=%d want 1 (money-path only)", len(resp.Modules))
	}
	if resp.Modules[0].ID != "billing.service" {
		t.Fatalf("filtered to %q want billing.service", resp.Modules[0].ID)
	}
}

// TestContextSummaryMirrorsMerge — the Hermes feed returns the same merged data.
// Regression: if ContextSummary diverged from Merge (e.g. dropped the overlay)
// the billing overlay assertion goes RED, proving the Hermes feed and the admin
// endpoint share one source of truth.
func TestContextSummaryMirrorsMerge(t *testing.T) {
	summary := ContextSummary(context.Background(), newFakeSource(), "")
	if len(summary) != 2 {
		t.Fatalf("summary len=%d want 2", len(summary))
	}
	found := false
	for _, v := range summary {
		if v.ID == "billing.service" && v.Catalog != nil && v.Catalog.FeatureID == "F-BILL-001" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ContextSummary did not carry billing static overlay")
	}
}

// TestHandlerNilSourceFailsClosed — a nil source must not panic; it returns 503
// with an empty list, never a 200 implying "zero modules exist".
func TestHandlerNilSourceFailsClosed(t *testing.T) {
	h := NewModulesHandler(nil)
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/admin/v1/modules", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil source status=%d want 503", rec.Code)
	}
}
