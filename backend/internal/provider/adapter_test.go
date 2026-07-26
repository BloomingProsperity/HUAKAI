// EndpointForCredential 单元测试:重点覆盖 API key、upstream_passthrough 的
// base_url 与 adapter default endpoint 路径拼接，尤其是多段 API root
// (OpenRouter "/api/v1"、Groq "/openai/v1") 不得拼出重复版本段。
package provider

import (
	"context"
	"errors"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/ssrfpolicy"
)

func passthroughCred(baseURL string) Credential {
	return Credential{
		Type:  CredentialTypeUpstreamPassthrough,
		Value: "sk-proxy",
		Extra: map[string]string{"base_url": baseURL},
	}
}

func apiKeyCred(baseURL string) Credential {
	return Credential{
		Type:  CredentialTypeAPIKey,
		Value: "sk-api-key",
		Extra: map[string]string{"base_url": baseURL},
	}
}

func setPassthroughPolicyEnv(t *testing.T, key, value string) {
	t.Helper()
	t.Setenv(key, value)
	ssrfpolicy.ResetForTesting()
	t.Cleanup(ssrfpolicy.ResetForTesting)
}

func TestEndpointForCredential(t *testing.T) {
	cases := []struct {
		name           string
		adapterDefault string
		cred           Credential
		want           string
	}{
		{
			name:           "API key 未配置 base_url 原样返回 adapter default",
			adapterDefault: "https://api.openai.com/v1/chat/completions",
			cred:           Credential{Type: CredentialTypeAPIKey, Value: "sk-x"},
			want:           "https://api.openai.com/v1/chat/completions",
		},
		{
			name:           "API key 使用 operator 自配上游地址",
			adapterDefault: "https://api.kimi.com/coding/v1/chat/completions",
			cred:           apiKeyCred("https://api.moonshot.cn/v1"),
			want:           "https://api.moonshot.cn/v1/chat/completions",
		},
		{
			name:           "OAuth 凭据忽略 base_url",
			adapterDefault: "https://api.example.com/v1/chat/completions",
			cred:           Credential{Type: CredentialTypeOAuthAccessToken, Extra: map[string]string{"base_url": "https://operator.example/v1"}},
			want:           "https://api.example.com/v1/chat/completions",
		},
		{
			name:           "Session 凭据忽略 base_url",
			adapterDefault: "https://api.example.com/v1/chat/completions",
			cred:           Credential{Type: CredentialTypeSessionToken, Extra: map[string]string{"base_url": "https://operator.example/v1"}},
			want:           "https://api.example.com/v1/chat/completions",
		},
		{
			name:           "SigV4 凭据忽略 base_url",
			adapterDefault: "https://api.example.com/v1/chat/completions",
			cred:           Credential{Type: CredentialTypeAWSSigV4, Extra: map[string]string{"base_url": "https://operator.example/v1"}},
			want:           "https://api.example.com/v1/chat/completions",
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

func TestEndpointForCredentialRejectsUnsafeCustomBaseURL(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		leak    string
	}{
		{name: "http scheme", baseURL: "http://proxy.example/v1"},
		{name: "http loopback IPv4", baseURL: "http://127.0.0.1:8080", leak: "127.0.0.1"},
		{name: "http link local metadata", baseURL: "http://169.254.169.254/latest/meta-data", leak: "169.254.169.254"},
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
	credentialCases := []struct {
		name  string
		build func(string) Credential
	}{
		{name: "api_key", build: apiKeyCred},
		{name: "upstream_passthrough", build: passthroughCred},
	}
	for _, credentialCase := range credentialCases {
		t.Run(credentialCase.name, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					_, err := EndpointForCredential(
						"https://api.openai.com/v1/chat/completions",
						credentialCase.build(tc.baseURL),
					)
					if !errors.Is(err, ErrUnsafePassthroughEndpoint) {
						t.Fatalf("EndpointForCredential error=%v, want ErrUnsafePassthroughEndpoint", err)
					}
					if tc.leak != "" && strings.Contains(err.Error(), tc.leak) {
						t.Fatalf("blocked endpoint error leaked raw destination %q: %v", tc.leak, err)
					}
				})
			}
		})
	}
}

func TestUsesCustomPassthroughEndpointCredentialKinds(t *testing.T) {
	tests := []struct {
		name string
		cred Credential
		want bool
	}{
		{name: "API key 带 base_url", cred: apiKeyCred("https://upstream.example/v1"), want: true},
		{name: "API key 不带 base_url", cred: Credential{Type: CredentialTypeAPIKey}, want: false},
		{name: "透传凭据带 base_url", cred: passthroughCred("https://upstream.example/v1"), want: true},
		{name: "透传凭据带专用 endpoint", cred: Credential{Type: CredentialTypeUpstreamPassthrough, Extra: map[string]string{"endpoint_api": "https://upstream.example"}}, want: true},
		{name: "OAuth 不得启用自定义地址", cred: Credential{Type: CredentialTypeOAuthAccessToken, Extra: map[string]string{"base_url": "https://upstream.example"}}, want: false},
		{name: "Session 不得启用自定义地址", cred: Credential{Type: CredentialTypeSessionToken, Extra: map[string]string{"base_url": "https://upstream.example"}}, want: false},
		{name: "SigV4 不得启用自定义地址", cred: Credential{Type: CredentialTypeAWSSigV4, Extra: map[string]string{"base_url": "https://upstream.example"}}, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := UsesCustomPassthroughEndpoint(tc.cred); got != tc.want {
				t.Fatalf("UsesCustomPassthroughEndpoint()=%t want %t", got, tc.want)
			}
		})
	}
}

func TestRequestUsesCustomPassthroughEndpointSessionMatchesActualOrigin(t *testing.T) {
	customURL, err := url.Parse("https://session-proxy.example/chat/completions")
	if err != nil {
		t.Fatal(err)
	}
	officialURL, err := url.Parse("https://chatgpt.com/backend-api/codex/responses")
	if err != nil {
		t.Fatal(err)
	}
	cred := Credential{
		Type:  CredentialTypeSessionToken,
		Extra: map[string]string{"base_url": "https://session-proxy.example:443/api"},
	}
	if !RequestUsesCustomPassthroughEndpoint(cred, customURL) {
		t.Fatal("session 请求已采用同 origin 的 base_url，应识别为自定义 endpoint")
	}
	if RequestUsesCustomPassthroughEndpoint(cred, officialURL) {
		t.Fatal("适配器忽略 base_url 并使用官方地址时，不应误判为自定义 endpoint")
	}
}

func TestBlockCloudMetadataHosts(t *testing.T) {
	blocked := []string{
		"metadata.google.internal",
		"metadata.goog",
		"instance-data",
		"instance-data.ec2.internal",
		"localhost",
		"localhost.localdomain",
	}
	for _, host := range blocked {
		t.Run(host, func(t *testing.T) {
			_, err := validatePassthroughHost(host)
			if !errors.Is(err, ErrUnsafePassthroughEndpoint) {
				t.Fatalf("validatePassthroughHost(%q) error=%v, want ErrUnsafePassthroughEndpoint", host, err)
			}
		})
	}

	got, err := validatePassthroughHost("api.openai.com")
	if err != nil {
		t.Fatalf("api.openai.com rejected: %v", err)
	}
	if got != "api.openai.com" {
		t.Fatalf("host=%q want api.openai.com", got)
	}
}

func TestPassthroughEndpointPortAllowlist(t *testing.T) {
	setPassthroughPolicyEnv(t, ssrfpolicy.PortAllowlistEnv, "443,8000-9000")

	_, err := EndpointForCredential(
		"https://api.openai.com/v1/chat/completions",
		passthroughCred("https://proxy.example:7000/v1"),
	)
	if !errors.Is(err, ErrUnsafePassthroughEndpoint) {
		t.Fatalf("port 7000 error=%v, want ErrUnsafePassthroughEndpoint", err)
	}

	for _, baseURL := range []string{
		"https://proxy.example:443/v1",
		"https://proxy.example/v1",
		"https://proxy.example:8500/v1",
	} {
		t.Run(baseURL, func(t *testing.T) {
			_, err := EndpointForCredential(
				"https://api.openai.com/v1/chat/completions",
				passthroughCred(baseURL),
			)
			if err != nil {
				t.Fatalf("%s rejected under allowlist 443,8000-9000: %v", baseURL, err)
			}
		})
	}

	setPassthroughPolicyEnv(t, ssrfpolicy.PortAllowlistEnv, "")
	_, err = EndpointForCredential(
		"https://api.openai.com/v1/chat/completions",
		passthroughCred("https://proxy.example:7000/v1"),
	)
	if err != nil {
		t.Fatalf("empty port allowlist must preserve allow-all default, got %v", err)
	}
}

func TestDomainAllowlistWildcard(t *testing.T) {
	setPassthroughPolicyEnv(t, ssrfpolicy.DomainAllowlistEnv, "api.openai.com,*.anthropic.com")

	if _, err := validatePassthroughHost("evil.com"); !errors.Is(err, ErrUnsafePassthroughEndpoint) {
		t.Fatalf("evil.com error=%v, want ErrUnsafePassthroughEndpoint", err)
	}
	if _, err := validatePassthroughHost("93.184.216.34"); !errors.Is(err, ErrUnsafePassthroughEndpoint) {
		t.Fatalf("public IP literal must still match non-empty allowlist, err=%v", err)
	}
	for _, host := range []string{"api.openai.com", "x.anthropic.com"} {
		t.Run(host, func(t *testing.T) {
			if _, err := validatePassthroughHost(host); err != nil {
				t.Fatalf("%s rejected by allowlist: %v", host, err)
			}
		})
	}

	setPassthroughPolicyEnv(t, ssrfpolicy.DomainDenylistEnv, "x.anthropic.com")
	if _, err := validatePassthroughHost("x.anthropic.com"); !errors.Is(err, ErrUnsafePassthroughEndpoint) {
		t.Fatalf("denylist must win over allowlist, err=%v", err)
	}

	setPassthroughPolicyEnv(t, ssrfpolicy.DomainAllowlistEnv, "")
	setPassthroughPolicyEnv(t, ssrfpolicy.DomainDenylistEnv, "")
	if _, err := validatePassthroughHost("evil.com"); err != nil {
		t.Fatalf("empty domain policy must preserve default pass-through, got %v", err)
	}
}

func TestAllowPrivateIPHostsScopedEscapeHatch(t *testing.T) {
	setPassthroughPolicyEnv(t, ssrfpolicy.AllowPrivateIPHostsEnv, "10.1.2.3,private-proxy.example")

	_, err := EndpointForCredential(
		"https://api.openai.com/v1/chat/completions",
		passthroughCred("https://10.1.2.3/v1"),
	)
	if err != nil {
		t.Fatalf("explicit allow-private IP literal rejected: %v", err)
	}

	_, err = EndpointForCredential(
		"https://api.openai.com/v1/chat/completions",
		passthroughCred("https://10.1.2.4/v1"),
	)
	if !errors.Is(err, ErrUnsafePassthroughEndpoint) {
		t.Fatalf("unlisted private IP error=%v, want ErrUnsafePassthroughEndpoint", err)
	}

	restore := SwapPassthroughEndpointLookupForTesting(func(_ context.Context, network, host string) ([]netip.Addr, error) {
		if network != "ip" {
			t.Fatalf("lookup network=%q, want ip", network)
		}
		return []netip.Addr{netip.MustParseAddr("10.1.2.3")}, nil
	})
	t.Cleanup(restore)

	u, err := url.Parse("https://private-proxy.example/v1")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePassthroughEndpointTarget(context.Background(), u); err != nil {
		t.Fatalf("listed private resolving host rejected: %v", err)
	}

	u, err = url.Parse("https://other-proxy.example/v1")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePassthroughEndpointTarget(context.Background(), u); !errors.Is(err, ErrUnsafePassthroughEndpoint) {
		t.Fatalf("unlisted private resolving host error=%v, want ErrUnsafePassthroughEndpoint", err)
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
