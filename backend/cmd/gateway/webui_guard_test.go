package main

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/openapicheck"
	"github.com/BloomingProsperity/HUAKAI/internal/webui"
)

// TestWebUISPAGuardCoversEveryRegisteredRoute 遍历*真实*的网关路由器,并
// 断言每一条已注册路由都能被 webui.IsAPIPath 识别。内嵌的
// SPA 被接成路由器的 NotFound 处理器,因此任何*未*被该守护覆盖的 API 根路径,
// 其下的拼写错误/未匹配路径就会被返回 SPA 外壳
//(200 HTML)而非 404——这正是该守护所防范的契约破坏。
//
// 这是回归防护网:一旦有新的顶层 API 路由被挂载却没把其根路径加进 webui 守护,
// 它就会失败,从而保证该守护永远不会再悄悄落后于路由器。
func TestWebUISPAGuardCoversEveryRegisteredRoute(t *testing.T) {
	r := buildTestRouter(t)
	for _, op := range openapicheck.WalkChiOperations(r) {
		if !webui.IsAPIPath(op.Path) {
			t.Fatalf("registered route %s %s is not covered by webui.IsAPIPath — an unmatched path under this root would be served the SPA shell instead of 404; add its root to webui apiPathPrefixes/apiPathExact", op.Method, op.Path)
		}
	}
}
