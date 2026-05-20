// EndpointForCredential 单元测试: 重点覆盖 upstream_passthrough base_url 与
// adapter default endpoint 的路径拼接, 尤其是多段 API root (OpenRouter
// "/api/v1"、Groq "/openai/v1") 不得拼出重复版本段。
package provider

import "testing"

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
			got := EndpointForCredential(tc.adapterDefault, tc.cred)
			if got != tc.want {
				t.Fatalf("EndpointForCredential = %q, want %q", got, tc.want)
			}
		})
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
		{"/chat/completions", "/chat/completions"}, // 无版本段原样返回
		{"/v1", "/v1"},                             // 版本段已是末段原样返回
	}
	for _, tc := range cases {
		if got := apiEndpointSuffix(tc.path); got != tc.want {
			t.Fatalf("apiEndpointSuffix(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}
