package copilot

import (
	"net/http"
	"testing"
)

// TestCopilotHTTPClientsAreSSRFProtected 抓对抗 bug-hunt 第三轮 S2(S2-054 漏修):
// Copilot service-token 刷新与设备码引导出站携带高价值 GitHub access token。未注入 HTTPClient 时(生产
// 唯二构造点 wiring/mode_refresh 均零值 Adapter),两处 httpClient() 必须回退到 SSRF 防护 client,而非裸
// http.DefaultClient(后者读 HTTP_PROXY 经 env 代理外发 token、无拨号层 IP 校验、不禁 3xx)。
// §14 变异:把任一 httpClient() 改回 `return http.DefaultClient` → 其 Transport 为 nil(断言 ok=false)→ 本测试红。
func TestCopilotHTTPClientsAreSSRFProtected(t *testing.T) {
	assertCopilotSSRFProtected(t, CopilotRefreshAdapter{}.httpClient(), "CopilotRefreshAdapter")
	assertCopilotSSRFProtected(t, OAuthBootstrap{}.httpClient(), "OAuthBootstrap")
}

func assertCopilotSSRFProtected(t *testing.T, c *http.Client, name string) {
	t.Helper()
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("%s.httpClient() 应返回 SSRF 防护 client(Transport 为 *http.Transport),got %T —— 裸 http.DefaultClient 的 Transport 为 nil", name, c.Transport)
	}
	if tr.Proxy != nil {
		t.Fatalf("%s: SSRF 防护 client 必须禁用代理(Transport.Proxy=nil),否则 HTTP_PROXY 会外发 GitHub token", name)
	}
	if c.CheckRedirect == nil {
		t.Fatalf("%s: SSRF 防护 client 必须设置 CheckRedirect 禁 3xx 重定向", name)
	}
}
