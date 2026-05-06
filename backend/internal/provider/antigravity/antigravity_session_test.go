// 包 antigravity — AntigravitySessionAdapter 烟雾测试。
package antigravity

import (
	"context"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func TestAntigravitySessionAdapter_Platform(t *testing.T) {
	a := &AntigravitySessionAdapter{}
	if got := a.Platform(); got != "antigravity" {
		t.Errorf("Platform()=%q want antigravity", got)
	}
}

func TestAntigravitySessionAdapter_RejectsAPIKey(t *testing.T) {
	a := &AntigravitySessionAdapter{}
	for _, ct := range a.AcceptableCredentialTypes() {
		if ct == provider.CredentialTypeAPIKey {
			t.Error("AcceptableCredentialTypes 不应包含 apikey")
		}
	}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "antigravity-default",
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-x"},
	})
	if err == nil || !strings.Contains(err.Error(), "apikey") {
		t.Errorf("apikey 凭据应被拒绝，err=%v", err)
	}
}

func TestAntigravitySessionAdapter_RejectsEmptyValue(t *testing.T) {
	a := &AntigravitySessionAdapter{}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "antigravity-default",
		Credential:      provider.Credential{Type: provider.CredentialTypeSessionToken, Value: " "},
	})
	if err == nil {
		t.Error("空白 Credential.Value 应被拒绝")
	}
}

func TestAntigravitySessionAdapter_RejectsEmptyModelID(t *testing.T) {
	a := &AntigravitySessionAdapter{}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "",
		Credential:      provider.Credential{Type: provider.CredentialTypeSessionToken, Value: "ag-token"},
	})
	if err == nil {
		t.Error("空 UpstreamModelID 应被拒绝")
	}
}

func TestAntigravitySessionAdapter_HappyPath_InjectsAuthorization(t *testing.T) {
	a := &AntigravitySessionAdapter{}
	req, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "antigravity-default",
		InboundBody:     []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: "ag-session",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer ag-session" {
		t.Errorf("Authorization=%q want Bearer ag-session", got)
	}
}
