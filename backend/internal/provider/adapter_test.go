// EndpointForCredential 单元测试: 重点覆盖 upstream_passthrough base_url 与
// adapter default endpoint 的路径拼接, 尤其是多段 API root (OpenRouter
// "/api/v1"、Groq "/openai/v1") 不得拼出重复版本段。
package provider

import (
	"context"
	"errors"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

func passthroughCred(baseURL string) Credential {
	return Credential{
		Type:  CredentialTypeUpstreamPassthrough,
		Value: "sk-proxy",
		Extra: map[string]string{"base_url": baseURL},
	}
}

func TestEndpointForCredential(t *testing.T) {
	cases := []struct {
		name           string
		adapterDefault string
		cred           Credential
		want           string
	}{
		{
			name:           "非 passthrough 凭据原样返回 adapter default",
			adapterDefault: "https://api.openai.com/v1/chat/completions",
			cred:           Credential{Type: CredentialTypeAPIKey, Value: "sk-x"},
			want:           "https://api.openai.com/v1/chat/completions",
		},
		{
			name:           "base_url 仅 scheme+host 用 adapter 全 path",
			adapterDefault: "https://api.openai.com/v1/chat/completions",
			cred:           passthroughCred("https://proxy.example"),
			want:           "https://proxy.example/v1/chat/completions",
		},
		{
			name:           "base_url 末段是单段版本 /v1 拼 endpoint suffix",
			adapterDefault: "https://api.openai.com/v1/chat/completions",
			cred:           passthroughCred("https://proxy.example/v1"),
			want:           "https://proxy.example/v1/chat/completions",
		},
		{
			name:           "公网 IPv4 字面量允许但仍拼 endpoint suffix",
			adapterDefault: "https://api.openai.com/v1/chat/completions",
			cred:           passthroughCred("https://93.184.216.34/v1"),
			want:           "https://93.184.216.34/v1/chat/completions",
		},
		{
			// 回归: adapter default 多段 root "/api/v1/chat/completions",
			// base_url 也带 "/api/v1" — 之前只剥首段会拼出 /api/v1/v1/...。
			name:           "OpenRouter 多段 API root 不重复版本段",
			adapterDefault: "https://openrouter.ai/api/v1/chat/completions",
			cred:           passthroughCred("https://proxy.example/api/v1"),
			want:           "https://proxy.example/api/v1/chat/completions",
		},
		{
			// 回归: Groq adapter default "/openai/v1/chat/completions"。
			name:           "Groq 多段 API root 不重复版本段",
			adapterDefault: "https://api.groq.com/openai/v1/chat/completions",
			cred:           passthroughCred("https://proxy.example/openai/v1"),
			want:           "https://proxy.example/openai/v1/chat/completions",
		},
		{
			// 回归 P3: gemini path, model 名形如 v2-pro 不被误判成版本段。
			name:           "gemini 多段 root + model 名含 v 数字",
			adapterDefault: "https://generativelanguage.googleapis.com/v1beta/models/v2-pro:generateContent",
			cred:           passthroughCred("https://proxy.example/v1beta"),
			want:           "https://proxy.example/v1beta/models/v2-pro:generateContent",
		},
		{
			name:           "base_url 已含完整 adapter path 原样信任",
			adapterDefault: "https://openrouter.ai/api/v1/chat/completions",
			cred:           passthroughCred("https://proxy.example/api/v1/chat/completions"),
			want:           "https://proxy.example/api/v1/chat/completions",
		},
		{
			name:           "base_url 末段非版本号视为自定义完整路径原样信任",
			adapterDefault: "https://api.openai.com/v1/chat/completions",
			cred:           passthroughCred("https://proxy.example/api/gateway"),
			want:           "https://proxy.example/api/gateway",
		},
		{
			name:           "base_url 自带 query 必须保留",
			adapterDefault: "https://api.openai.com/v1/chat/completions",
			cred:           passthroughCred("https://proxy.example/v1?token=abc"),
			want:           "https://proxy.example/v1/chat/completions?token=abc",
		},
		{
			name:           "passthrough 但 base_url 为空回落 adapter default",
			adapterDefault: "https://api.openai.com/v1/chat/completions",
			cred:           passthroughCred("   "),
			want:           "https://api.openai.com/v1/chat/completions",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EndpointForCredential(tc.adapterDefault, tc.cred)
			if err != nil {
				t.Fatalf("EndpointForCredential returned error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("EndpointForCredential = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEndpointForCredentialRejectsUnsafePassthroughBaseURL(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		leak    string
	}{
		{name: "http scheme", baseURL: "http://proxy.example/v1"},
		{name: "loopback IPv4", baseURL: "https://127.0.0.1:8443/v1", leak: "127.0.0.1"},
		{name: "private IPv4", baseURL: "https://10.1.2.3/v1", leak: "10.1.2.3"},
		{name: "link local metadata", baseURL: "https://169.254.169.254/latest", leak: "169.254.169.254"},
		{name: "IPv6 loopback", baseURL: "https://[::1]/v1", leak: "::1"},
		{name: "userinfo", baseURL: "https://user:pass@proxy.example/v1", leak: "user:pass"},
		{name: "trailing dot", baseURL: "https://proxy.example./v1", leak: "proxy.example."},
		{name: "local suffix", baseURL: "https://proxy.local/v1", leak: "proxy.local"},
		{name: "decimal loopback", baseURL: "https://2130706433/v1", leak: "2130706433"},
		{name: "octal loopback", baseURL: "https://0177.0.0.1/v1", leak: "0177.0.0.1"},
		{name: "IPv4-compatible IPv6", baseURL: "https://[::7f00:1]/v1", leak: "::7f00:1"},
		{name: "local use NAT64", baseURL: "https://[64:ff9b:1::a00:1]/v1", leak: "64:ff9b:1"},
		{name: "encoded host", baseURL: "https://%31%32%37.0.0.1/v1", leak: "%31%32%37"},
		{name: "invalid port", baseURL: "https://proxy.example:70000/v1", leak: "70000"},
		{name: "empty port", baseURL: "https://proxy.example:/v1", leak: "proxy.example"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := EndpointForCredential(
				"https://api.openai.com/v1/chat/completions",
				passthroughCred(tc.baseURL),
			)
			if !errors.Is(err, ErrUnsafePassthroughEndpoint) {
				t.Fatalf("EndpointForCredential error=%v, want ErrUnsafePassthroughEndpoint", err)
			}
			if tc.leak != "" && strings.Contains(err.Error(), tc.leak) {
				t.Fatalf("blocked endpoint error leaked raw destination %q: %v", tc.leak, err)
			}
		})
	}
}

func TestValidatePassthroughEndpointTargetRejectsHostnameAliasToLoopback(t *testing.T) {
	restore := SwapPassthroughEndpointLookupForTesting(func(_ context.Context, network, host string) ([]netip.Addr, error) {
		if network != "ip" {
			t.Fatalf("lookup network=%q, want ip", network)
		}
		if host != "ip6-localhost" {
			t.Fatalf("lookup host=%q, want ip6-localhost", host)
		}
		return []netip.Addr{netip.MustParseAddr("::1")}, nil
	})
	t.Cleanup(restore)

	u, err := url.Parse("https://ip6-localhost/v1")
	if err != nil {
		t.Fatal(err)
	}
	err = ValidatePassthroughEndpointTarget(context.Background(), u)
	if !errors.Is(err, ErrUnsafePassthroughEndpoint) {
		t.Fatalf("ValidatePassthroughEndpointTarget error=%v, want ErrUnsafePassthroughEndpoint", err)
	}
	if strings.Contains(err.Error(), "ip6-localhost") {
		t.Fatalf("blocked endpoint error leaked raw hostname: %v", err)
	}
}

func TestAPIEndpointSuffix(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/v1/chat/completions", "/chat/completions"},
		{"/api/v1/chat/completions", "/chat/completions"},
		{"/openai/v1/chat/completions", "/chat/completions"},
		{"/v1beta/models/x:generateContent", "/models/x:generateContent"},
		{"/v1beta/models/v2-pro:generateContent", "/models/v2-pro:generateContent"}, // P3: model 名含 v 数字不被误判
		{"/chat/completions", "/chat/completions"},                                  // 无版本段原样返回
		{"/v1", "/v1"}, // 版本段已是末段原样返回
	}
	for _, tc := range cases {
		if got := apiEndpointSuffix(tc.path); got != tc.want {
			t.Fatalf("apiEndpointSuffix(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}
