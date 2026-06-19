package main

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/openapicheck"
	"github.com/BloomingProsperity/HUAKAI/internal/webui"
)

// TestWebUISPAGuardCoversEveryRegisteredRoute walks the REAL gateway router and
// asserts every registered route is recognized by webui.IsAPIPath. The embedded
// SPA is wired as the router's NotFound handler, so any API root NOT covered by
// the guard would let a typo/unmatched path under it be served the SPA shell
// (200 HTML) instead of a 404 — the exact contract break the guard prevents.
//
// This is the regression net: it fails the moment a new top-level API route is
// mounted without adding its root to the webui guard, so the guard can never
// silently fall behind the router again.
func TestWebUISPAGuardCoversEveryRegisteredRoute(t *testing.T) {
	r := buildTestRouter(t)
	for _, op := range openapicheck.WalkChiOperations(r) {
		if !webui.IsAPIPath(op.Path) {
			t.Fatalf("registered route %s %s is not covered by webui.IsAPIPath — an unmatched path under this root would be served the SPA shell instead of 404; add its root to webui apiPathPrefixes/apiPathExact", op.Method, op.Path)
		}
	}
}
