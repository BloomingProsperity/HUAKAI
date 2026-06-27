package antigravity

import (
	"net/http"
	"testing"
)

// TestRefreshAdapterDefaultClientIsSSRFProtected 抓对抗 bug-hunt 第三轮审查发现的镜像组最后一个漏修点:
// antigravity RefreshAdapter 与 gemini/kiro/cursor/windsurf/openai_codex 同属 vendor token 刷新出站,未注入
// HTTPClient 时必须回退 SSRF 防护 client 而非裸 http.DefaultClient。
// §14 变异:把 httpClient() 改回 `return http.DefaultClient` → Transport 为 nil(类型断言 ok=false)→ 本测试红。
func TestRefreshAdapterDefaultClientIsSSRFProtected(t *testing.T) {
	c := RefreshAdapter{}.httpClient()
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("httpClient() 应返回 SSRF 防护 client(Transport 为 *http.Transport),got %T —— 裸 http.DefaultClient 的 Transport 为 nil", c.Transport)
	}
	if tr.Proxy != nil {
		t.Fatal("SSRF 防护 client 必须禁用代理(Transport.Proxy=nil),否则 HTTP_PROXY 会外发凭据")
	}
	if c.CheckRedirect == nil {
		t.Fatal("SSRF 防护 client 必须设置 CheckRedirect 禁 3xx 重定向")
	}
}
