// Package webui serves the embedded single-page admin/portal frontend from the
// gateway binary. The static assets are compiled in only under the `embed` build
// tag (see embed_on.go); the default build uses a disabled stub (embed_off.go) so
// the gateway compiles and is fully testable without a frontend dist present.
//
// The handler is wired as the router's NotFound handler, so it only ever sees
// requests that matched no registered route. It therefore can NEVER shadow a real
// backend route; the API-path guard below only ensures a mistyped API path returns
// a plain 404 instead of the SPA's index.html.
package webui

import (
	"io/fs"
	"net/http"
	"strings"
)

// apiPathPrefixes are request roots owned by the gateway's API/operational
// surface (directory-style — matched by prefix). An unmatched path under any of
// these returns 404 rather than falling through to the SPA shell. A regression
// test in cmd/gateway walks the real router and asserts every registered route is
// covered here, so a future top-level mount cannot silently regress this guard.
var apiPathPrefixes = []string{
	"/v1/", "/v1beta/", "/engines/", "/backend-api/",
	"/mj/", "/suno/", "/video/",
	"/admin/", "/debug/", "/internal/", "/.well-known/",
}

// apiPathExact are single-endpoint API/operational paths (matched exactly, so the
// SPA may still own sibling paths like /metrics-overview).
var apiPathExact = []string{"/metrics", "/healthz"}

// IsAPIPath reports whether p belongs to the gateway's API/operational surface and
// must therefore never be answered with the SPA shell. Exported so cmd/gateway can
// assert full coverage against the real router.
func IsAPIPath(p string) bool {
	for _, e := range apiPathExact {
		if p == e {
			return true
		}
	}
	for _, pre := range apiPathPrefixes {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	return false
}

// Handler returns an http.Handler that serves the SPA from fsys: a matching
// static asset is served directly; any other non-API path falls back to
// index.html so client-side routing survives deep links and page refresh. It
// returns nil when fsys is nil (frontend not embedded), and the caller keeps the
// router's default 404.
//
// Path traversal is impossible: fsys is an io/fs.FS, whose Open rejects paths that
// escape the tree (fs.ValidPath), and http.FileServerFS only serves within it.
func Handler(fsys fs.FS) http.Handler {
	if fsys == nil {
		return nil
	}
	assets := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		// The SPA must never answer for the API/operational surface.
		if IsAPIPath(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		// A real content-hashed asset is served by the file server. The shell
		// itself (root, /index.html, or any unknown client route) always goes
		// through serveIndex so it carries the no-cache header consistently.
		name := assetName(r.URL.Path)
		if name != "" && name != "index.html" && fileExists(fsys, name) {
			assets.ServeHTTP(w, r)
			return
		}
		serveIndex(w, r, fsys)
	})
}

// assetName maps a request path to a clean fs path, or "" if it is not a valid
// embeddable name. The root maps to index.html.
func assetName(p string) string {
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return "index.html"
	}
	if !fs.ValidPath(p) {
		return ""
	}
	return p
}

func fileExists(fsys fs.FS, name string) bool {
	f, err := fsys.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	return err == nil && !st.IsDir()
}

func serveIndex(w http.ResponseWriter, r *http.Request, fsys fs.FS) {
	b, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The shell references content-hashed assets, so it must not be cached.
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}
