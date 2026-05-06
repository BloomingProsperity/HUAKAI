// 包 copilot — CopilotSessionAdapter 烟雾测试。
package copilot

import (
	"context"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func TestCopilotSessionAdapter_Platform(t *testing.T) {
	a := &CopilotSessionAdapter{}
	if got := a.Platform(); got != "copilot" {
		t.Errorf("Platform()=%q want copilot", got)
	}
}

func TestCopilotSessionAdapter_RejectsAPIKey(t *testing.T) {
	a := &CopilotSessionAdapter{}
	for _, ct := range a.AcceptableCredentialTypes() {
		if ct == provider.CredentialTypeAPIKey {
			t.Error("AcceptableCredentialTypes 不应包含 apikey")
		}
	}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "gpt-4o",
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-x"},
	})
	if err == nil || !strings.Contains(err.Error(), "apikey") {
		t.Errorf("apikey 凭据应被拒绝，err=%v", err)
	}
}

func TestCopilotSessionAdapter_RejectsEmptyValue(t *testing.T) {
	a := &CopilotSessionAdapter{}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "gpt-4o",
		Credential:      provider.Credential{Type: provider.CredentialTypeSessionToken, Value: "   "},
	})
	if err == nil {
		t.Error("空白 Credential.Value 应被拒绝")
	}
}

func TestCopilotSessionAdapter_RejectsEmptyModelID(t *testing.T) {
	a := &CopilotSessionAdapter{}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "",
		Credential:      provider.Credential{Type: provider.CredentialTypeSessionToken, Value: "ghu_xxx"},
	})
	if err == nil {
		t.Error("空 UpstreamModelID 应被拒绝")
	}
}

func TestCopilotSessionAdapter_HappyPath_InjectsAuthorization(t *testing.T) {
	a := &CopilotSessionAdapter{}
	req, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "gpt-4o",
		InboundBody:     []byte(`{"model":"gpt-4o"}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: "ghu_token",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer ghu_token" {
		t.Errorf("Authorization=%q want Bearer ghu_token", got)
	}
}
