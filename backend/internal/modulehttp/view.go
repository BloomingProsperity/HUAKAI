// Package modulehttp serves the merged module-knowledge view: it joins the LIVE
// moduleregistry (runtime descriptors + health probes) with the STATIC
// modulecatalog (feature-tree-derived identity: section, feature id, parity
// status, owning packages), and exposes:
//
//   - GET /admin/v1/modules (+ ?category=) — admin-gated, read-only, for Hermes
//     and an admin UI to query each module's identity + capabilities + status +
//     live probe.
//
// The Hermes runner-context feed accessor is added in the H3 wave alongside its
// consumer (kept out of this wave so no unwired accessor ships unused).
//
// Privacy: this surface carries module identity, enum statuses, and short
// diagnostic detail strings only — never secrets or user data. It is the
// operator/assistant root-cause spine, intentionally off every request hot path.
package modulehttp

import (
	"context"

	"github.com/BloomingProsperity/HUAKAI/internal/modulecatalog"
	"github.com/BloomingProsperity/HUAKAI/internal/moduleregistry"
)

// ModuleView is one module's merged identity + runtime state.
type ModuleView struct {
	ID           string   `json:"id"`
	Category     string   `json:"category"`
	Title        string   `json:"title"`
	Capabilities []string `json:"capabilities,omitempty"`
	// Static overlay (from the feature-tree catalog), nil when no catalog match.
	Catalog *CatalogOverlay `json:"catalog,omitempty"`
	// Live probe result from the registry Snapshot.
	LiveProbe moduleregistry.ProbeResult `json:"live_probe"`
}

// CatalogOverlay is the static knowledge merged onto a live module.
type CatalogOverlay struct {
	Section   string   `json:"section,omitempty"`
	FeatureID string   `json:"feature_id,omitempty"`
	Status    string   `json:"status,omitempty"`
	Parity    string   `json:"parity,omitempty"`
	Pkgs      []string `json:"pkgs,omitempty"`
}

// Source provides the two halves of the merged view. Splitting it behind an
// interface keeps the handler unit-testable with fakes (no DB, no real wiring).
type Source interface {
	// Snapshot returns the live descriptors + probe results.
	Snapshot(ctx context.Context) []moduleregistry.ModuleSnapshot
	// CatalogLookup returns the static overlay for a package short-name.
	CatalogLookup(pkg string) (modulecatalog.CatalogModule, bool)
	// CatalogPkgFor maps a live module ID to its catalog package short-name. The
	// seeds register an explicit mapping; an unmapped ID yields ("", false) and
	// the view simply omits the static overlay (live-only module).
	CatalogPkgFor(moduleID string) (string, bool)
}

// Merge joins the live snapshot with the static catalog into the operator view,
// optionally filtered by category (empty category = all). The result preserves
// the snapshot's sorted-by-ID order.
func Merge(ctx context.Context, src Source, category string) []ModuleView {
	snaps := src.Snapshot(ctx)
	views := make([]ModuleView, 0, len(snaps))
	for _, s := range snaps {
		d := s.Descriptor
		if category != "" && d.Category != category {
			continue
		}
		v := ModuleView{
			ID:           d.ID,
			Category:     d.Category,
			Title:        d.Title,
			Capabilities: d.Capabilities,
			LiveProbe:    s.Probe,
		}
		if pkg, ok := src.CatalogPkgFor(d.ID); ok {
			if cm, found := src.CatalogLookup(pkg); found {
				v.Catalog = &CatalogOverlay{
					Section:   cm.Section,
					FeatureID: cm.FeatureID,
					Status:    cm.Status,
					Parity:    cm.Parity,
					Pkgs:      cm.Pkgs,
				}
			}
		}
		views = append(views, v)
	}
	return views
}
