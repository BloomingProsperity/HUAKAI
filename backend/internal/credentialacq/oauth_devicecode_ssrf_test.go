package credentialacq

import (
	"errors"
	"net/http"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
)

// 守:device-code 默认 HTTP client 必须 SSRF-protected,拦截内网/元数据 IP(auth_url/token_url
// 运维提供,裸 client 可被打到 169.254.169.254)。Mutation: 改回裸 http.DefaultClient ->
// 不拦截(变普通 dial 错误)-> errors.Is(ErrOAuthEndpointBlocked) 红。
func TestDeviceCodeDefaultClientBlocksSSRF(t *testing.T) {
	client := defaultDeviceCodeHTTPClient()
	req, err := http.NewRequest(http.MethodPost, "http://169.254.169.254/token", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	_, derr := client.Do(req)
	if derr == nil {
		t.Fatal("device-code default client must block SSRF to link-local metadata IP 169.254.169.254")
	}
	if !errors.Is(derr, auth.ErrOAuthEndpointBlocked) {
		t.Fatalf("err=%v want auth.ErrOAuthEndpointBlocked (SSRF block, not a plain dial error)", derr)
	}
}
