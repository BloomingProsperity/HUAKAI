// Package webui 从 gateway 二进制内提供内嵌的单页管理/门户前端。静态资源仅在
// `embed` 构建 tag 下才会编译进来（见 embed_on.go）；默认构建使用一个禁用的桩
// （embed_off.go），这样在没有前端 dist 的情况下 gateway 也能编译并完整可测。
//
// 该 handler 被接到 router 的 NotFound handler 上，因此它只会见到未匹配任何已注册
// 路由的请求。它绝不可能遮蔽真正的后端路由；下面的 API 路径守卫只是确保一个拼错的
// API 路径返回纯 404，而不是 SPA 的 index.html。
package webui

import (
	"io/fs"
	"net/http"
	"strings"
)

// apiPathPrefixes 是归属 gateway API/运维面的请求根路径（目录式——按前缀匹配）。
// 这些前缀下任何未匹配的路径都返回 404，而不会落到 SPA 外壳。cmd/gateway 里有一个
// 回归测试会遍历真实 router，断言每条已注册路由都被这里覆盖，因此将来新增的顶层
// mount 不会悄悄令该守卫退化。
var apiPathPrefixes = []string{
	"/v1/", "/v1beta/", "/engines/", "/backend-api/",
	"/mj/", "/suno/", "/video/",
	// 注意:管理 API 的规范前缀是 /admin/v1/;裸 /admin/* 是 SPA 管理页深链接
	// (如 /admin/model-registry),绝不能整段划为 API,否则直开/刷新一律 404。
	"/admin/v1/", "/debug/", "/internal/", "/.well-known/",
}

// apiPathExact 是单端点的 API/运维路径（精确匹配，因此 SPA 仍可拥有同级路径，
// 比如 /metrics-overview;/setup 页面本身也仍走 SPA 兜底,只有这两条子路径是 API）。
var apiPathExact = []string{"/metrics", "/healthz", "/setup/status", "/setup/install"}

// IsAPIPath 报告 p 是否属于 gateway 的 API/运维面，因而绝不能用 SPA 外壳来响应。
// 导出它是为了让 cmd/gateway 能针对真实 router 断言完整覆盖。
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

// Handler 返回一个从 fsys 提供 SPA 的 http.Handler：命中的静态资源直接提供；其余
// 任何非 API 路径回退到 index.html，这样客户端路由在深链接和页面刷新时仍能存活。
// 当 fsys 为 nil（前端未内嵌）时返回 nil，调用方保留 router 的默认 404。
//
// 路径穿越不可能发生：fsys 是 io/fs.FS，其 Open 会拒绝逃出该树的路径（fs.ValidPath），
// 且 http.FileServerFS 只在该树内提供文件。
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
		// SPA 绝不能为 API/运维面响应。
		if IsAPIPath(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		// 真正带内容哈希的资源由文件服务器提供。外壳本身（根路径、/index.html，
		// 或任何未知的客户端路由）始终走 serveIndex，以便一致地带上 no-cache 头。
		name := assetName(r.URL.Path)
		if name != "" && name != "index.html" && fileExists(fsys, name) {
			assets.ServeHTTP(w, r)
			return
		}
		serveIndex(w, r, fsys)
	})
}

// assetName 把请求路径映射为一个干净的 fs 路径；若不是合法的可内嵌名称则返回 ""。
// 根路径映射到 index.html。
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
	// 外壳引用了带内容哈希的资源，因此它不能被缓存。
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}
