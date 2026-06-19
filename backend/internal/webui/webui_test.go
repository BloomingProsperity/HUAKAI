package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// testDist is an in-memory stand-in for the embedded frontend so the handler is
// fully testable under the default (!embed) build, with no real dist present.
func testDist() fs.FS {
	return fstest.MapFS{
		"index.html":    {Data: []byte("<!doctype html><title>spa-shell</title><div id=root>")},
		"assets/app.js": {Data: []byte("console.log('hk-app')")},
		"favicon.ico":   {Data: []byte("icon-bytes")},
	}
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// A real built asset is served as-is. Mutation: break assetName/fileExists and a
// known asset 404s instead of returning its bytes.
func TestHandlerServesStaticAsset(t *testing.T) {
	rec := get(t, Handler(testDist()), "/assets/app.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("asset status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "hk-app") {
		t.Fatalf("asset body=%q want the real app.js bytes", rec.Body.String())
	}
}

// An unknown (non-API, non-file) path is a client route → serve index.html, NOT
// 404. Mutation: drop the serveIndex fallback and this path 404s → red. The
// fixture's shell marker differs from any 404 body, so it discriminates.
func TestHandlerSPAFallbackToIndex(t *testing.T) {
	rec := get(t, Handler(testDist()), "/dashboard/settings/deep-link")
	if rec.Code != http.StatusOK {
		t.Fatalf("client-route status=%d want 200 (SPA fallback); body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "spa-shell") {
		t.Fatalf("client-route body=%q want the index.html shell", rec.Body.String())
	}
}

// The SPA must never answer for the API/operational surface. Mutation: remove the
// isAPIPath guard and these paths fall through to index.html (200 HTML) → red.
func TestHandlerNeverShadowsAPIPaths(t *testing.T) {
	h := Handler(testDist())
	for _, p := range []string{
		"/v1/chat/completions", "/v1beta/models", "/engines/x/embeddings",
		"/backend-api/codex/responses", "/admin/v1/users", "/debug/vars",
		"/metrics", "/internal/x", "/.well-known/huakai-pubkey.json", "/healthz",
		"/mj/submit/foo", "/suno/fetch/zzz", "/video/submit",
	} {
		rec := get(t, h, p)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("API path %q status=%d want 404 (must not fall through to SPA); body=%s", p, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "spa-shell") {
			t.Fatalf("API path %q leaked the SPA shell: %s", p, rec.Body.String())
		}
	}
}

// The root path serves the shell WITH no-cache, so a redeploy's new hashed asset
// names are always re-fetched. Mutation: serve "/" via the file server (no
// no-cache header) and this goes red. Guards the canonical entry that most users
// hit, which TestHandlerRootServesShell alone does not.
func TestHandlerRootServesShellNoCache(t *testing.T) {
	rec := get(t, Handler(testDist()), "/")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "spa-shell") {
		t.Fatalf("root status=%d body=%q want 200 + shell", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("root Cache-Control=%q want no-cache (shell must not be cached)", cc)
	}
}

// A path that merely shares a prefix with an exact API endpoint (/metrics,
// /healthz) is a client route, not the API — it must get the SPA, not a 404.
// Mutation: match /metrics by prefix instead of exactly and this goes red.
func TestHandlerExactAPIEndpointsDoNotOvermatch(t *testing.T) {
	h := Handler(testDist())
	for _, p := range []string{"/metrics-overview", "/healthz-status"} {
		rec := get(t, h, p)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "spa-shell") {
			t.Fatalf("client route %q status=%d want 200 SPA shell (not over-matched as API)", p, rec.Code)
		}
	}
}

// nil fsys (frontend not embedded) → nil handler, so the caller keeps the default
// 404. Mutation: return a non-nil handler for nil fsys and this fails.
func TestHandlerNilWhenNotEmbedded(t *testing.T) {
	if Handler(nil) != nil {
		t.Fatalf("Handler(nil) must be nil so the router keeps its default NotFound")
	}
}

// Path traversal is rejected by fs.ValidPath via assetName; a traversal attempt
// is treated as a client route (shell), never an escape.
func TestHandlerRejectsTraversal(t *testing.T) {
	if name := assetName("/../../etc/passwd"); name != "" {
		t.Fatalf("assetName(traversal)=%q want empty (fs.ValidPath rejects)", name)
	}
}
