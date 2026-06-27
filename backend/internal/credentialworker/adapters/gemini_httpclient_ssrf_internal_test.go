package adapters

import (
	"net/http"
	"testing"
)

// TestGeminiRefreshDefaultClientIsSSRFProtected 抓对抗 bug-hunt 第三轮 S2(S2-054 漏修):
// 未注入 HTTPClient 时(operator OAuth 路径 mode_refresh 不赋值 client),GeminiRefresh.httpClient() 必须回退到
// SSRF 防护 client(Proxy=nil 防 env 代理外发 refresh_token/client_secret + CheckRedirect 禁 3xx + 拨号层 IP 校验),
// 而非裸 http.DefaultClient。
// §14 变异:把 httpClient() 改回 `return http.DefaultClient` → 其 Transport 为 nil(类型断言 ok=false)→ 本测试红。
func TestGeminiRefreshDefaultClientIsSSRFProtected(t *testing.T) {
	assertGeminiSSRFProtected(t, GeminiRefresh{}.httpClient())
}

func assertGeminiSSRFProtected(t *testing.T, c *http.Client) {
	t.Helper()
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("httpClient() 应返回 SSRF 防护 client(Transport 为 *http.Transport),got %T —— 裸 http.DefaultClient 的 Transport 为 nil", c.Transport)
	}
	if tr.Proxy != nil {
		t.Fatal("SSRF 防护 client 必须禁用代理(Transport.Proxy=nil),否则 HTTP_PROXY 会外发 refresh_token/client_secret")
	}
	if c.CheckRedirect == nil {
		t.Fatal("SSRF 防护 client 必须设置 CheckRedirect 禁 3xx 重定向")
	}
}
