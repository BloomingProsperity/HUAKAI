// 包 gemini — GeminiAdvancedSessionAdapter 烟雾测试。
// 测试函数前缀 GeminiAdvancedSession 区别于既有 PassthroughAdapter 测试。
package gemini

import (
	"context"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func TestGeminiAdvancedSessionAdapter_Platform(t *testing.T) {
	a := &GeminiAdvancedSessionAdapter{}
	if got := a.Platform(); got != "gemini_advanced" {
		t.Errorf("Platform()=%q want gemini_advanced", got)
	}
}

func TestGeminiAdvancedSessionAdapter_RejectsAPIKey(t *testing.T) {
	a := &GeminiAdvancedSessionAdapter{}
	for _, ct := range a.AcceptableCredentialTypes() {
		if ct == provider.CredentialTypeAPIKey {
			t.Error("AcceptableCredentialTypes 不应包含 apikey（apikey 走 PassthroughAdapter）")
		}
	}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "gemini-1.5-pro-latest",
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "AIza-x"},
	})
	if err == nil || !strings.Contains(err.Error(), "apikey") {
		t.Errorf("apikey 凭据应被拒绝，err=%v", err)
	}
}

func TestGeminiAdvancedSessionAdapter_RejectsEmptyValue(t *testing.T) {
	a := &GeminiAdvancedSessionAdapter{}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "gemini-1.5-pro-latest",
		Credential:      provider.Credential{Type: provider.CredentialTypeSessionToken, Value: ""},
	})
	if err == nil {
		t.Error("空 Credential.Value 应被拒绝")
	}
}

func TestGeminiAdvancedSessionAdapter_RejectsEmptyModelID(t *testing.T) {
	a := &GeminiAdvancedSessionAdapter{}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "",
		Credential:      provider.Credential{Type: provider.CredentialTypeSessionToken, Value: "google-cookie"},
	})
	if err == nil {
		t.Error("空 UpstreamModelID 应被拒绝")
	}
}

func TestGeminiAdvancedSessionAdapter_HappyPath_InjectsCookie(t *testing.T) {
	// Gemini Advanced 网页反转用 cookie 鉴权（不是 Bearer Authorization）；
	// SessionToken 模式下 Value 整串写入 Cookie header。
	a := &GeminiAdvancedSessionAdapter{}
	req, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "gemini-1.5-pro-latest",
		InboundBody:     []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: "__Secure-1PSID=xxx; __Secure-3PSID=yyy",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Cookie"); got != "__Secure-1PSID=xxx; __Secure-3PSID=yyy" {
		t.Errorf("Cookie=%q want Google session cookie 串", got)
	}
	// 网页反转路径不使用 Bearer
	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization=%q want 空（cookie 鉴权路径）", got)
	}
	// X-Origin 必填
	if got := req.Header.Get("X-Origin"); got != "https://gemini.google.com" {
		t.Errorf("X-Origin=%q want gemini.google.com", got)
	}
}
