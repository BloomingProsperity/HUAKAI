package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// testDist 是内嵌前端的内存替身,使 handler 在默认的 (!embed) 构建下、
// 无真实 dist 存在时也完全可测。
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

// 真实构建的 asset 原样提供。变异:破坏 assetName/fileExists 后,
// 已知 asset 返回 404 而非其字节。
func TestHandlerServesStaticAsset(t *testing.T) {
	rec := get(t, Handler(testDist()), "/assets/app.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("asset status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "hk-app") {
		t.Fatalf("asset body=%q want the real app.js bytes", rec.Body.String())
	}
}

// 一个未知的(非 API、非文件)路径属于客户端路由 → 提供 index.html,而非
// 404。变异:去掉 serveIndex 兜底后,该路径返回 404 → 变红。
// fixture 的 shell 标记与任何 404 body 都不同,故有区分度。
func TestHandlerSPAFallbackToIndex(t *testing.T) {
	rec := get(t, Handler(testDist()), "/dashboard/settings/deep-link")
	if rec.Code != http.StatusOK {
		t.Fatalf("client-route status=%d want 200 (SPA fallback); body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "spa-shell") {
		t.Fatalf("client-route body=%q want the index.html shell", rec.Body.String())
	}
}

// SPA 绝不能应答 API/运维面。变异:移除 isAPIPath 护栏后,
// 这些路径会落到 index.html(200 HTML)→ 变红。
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

// 根路径在 no-cache 下提供 shell,这样重新部署后新的带 hash 的 asset
// 名总会被重新拉取。变异:改为通过 file server 提供 "/"(无
// no-cache header),本测试变红。它守护了大多数用户命中的规范入口,
// 这是 TestHandlerRootServesShell 单独无法覆盖的。
func TestHandlerRootServesShellNoCache(t *testing.T) {
	rec := get(t, Handler(testDist()), "/")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "spa-shell") {
		t.Fatalf("root status=%d body=%q want 200 + shell", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("root Cache-Control=%q want no-cache (shell must not be cached)", cc)
	}
}

// 一个仅与精确 API 端点(/metrics、/healthz)共享前缀的路径属于客户端路由,
// 而非 API —— 它必须得到 SPA,而非 404。
// 变异:把 /metrics 改成按前缀而非精确匹配,本测试变红。
func TestHandlerExactAPIEndpointsDoNotOvermatch(t *testing.T) {
	h := Handler(testDist())
	for _, p := range []string{"/metrics-overview", "/healthz-status"} {
		rec := get(t, h, p)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "spa-shell") {
			t.Fatalf("client route %q status=%d want 200 SPA shell (not over-matched as API)", p, rec.Code)
		}
	}
}

// nil fsys(前端未内嵌)→ nil handler,这样调用方保留默认的
// 404。变异:对 nil fsys 返回非 nil handler,本测试失败。
func TestHandlerNilWhenNotEmbedded(t *testing.T) {
	if Handler(nil) != nil {
		t.Fatalf("Handler(nil) must be nil so the router keeps its default NotFound")
	}
}

// 路径穿越经由 assetName 被 fs.ValidPath 拒绝;穿越尝试
// 被当作客户端路由(shell)处理,绝不会逃逸。
func TestHandlerRejectsTraversal(t *testing.T) {
	if name := assetName("/../../etc/passwd"); name != "" {
		t.Fatalf("assetName(traversal)=%q want empty (fs.ValidPath rejects)", name)
	}
}
