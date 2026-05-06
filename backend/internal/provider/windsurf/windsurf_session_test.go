// 包 windsurf — WindsurfSessionAdapter 烟雾测试。
package windsurf

import (
	"context"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func TestWindsurfSessionAdapter_Platform(t *testing.T) {
	a := &WindsurfSessionAdapter{}
	if got := a.Platform(); got != "windsurf" {
		t.Errorf("Platform()=%q want windsurf", got)
	}
}

func TestWindsurfSessionAdapter_RejectsAPIKey(t *testing.T) {
	a := &WindsurfSessionAdapter{}
	for _, ct := range a.AcceptableCredentialTypes() {
		if ct == provider.CredentialTypeAPIKey {
			t.Error("AcceptableCredentialTypes 不应包含 apikey")
		}
	}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "windsurf-default",
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-x"},
	})
	if err == nil || !strings.Contains(err.Error(), "apikey") {
		t.Errorf("apikey 凭据应被拒绝，err=%v", err)
	}
}

func TestWindsurfSessionAdapter_RejectsEmptyValue(t *testing.T) {
	a := &WindsurfSessionAdapter{}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "windsurf-default",
		Credential:      provider.Credential{Type: provider.CredentialTypeSessionToken, Value: ""},
	})
	if err == nil {
		t.Error("空 Credential.Value 应被拒绝")
	}
}

func TestWindsurfSessionAdapter_RejectsEmptyModelID(t *testing.T) {
	a := &WindsurfSessionAdapter{}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "",
		Credential:      provider.Credential{Type: provider.CredentialTypeSessionToken, Value: "codeium-token"},
	})
	if err == nil {
		t.Error("空 UpstreamModelID 应被拒绝")
	}
}

func TestWindsurfSessionAdapter_HappyPath_InjectsAuthorization(t *testing.T) {
	a := &WindsurfSessionAdapter{}
	req, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "windsurf-default",
		InboundBody:     []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: "ws-token",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer ws-token" {
		t.Errorf("Authorization=%q want Bearer ws-token", got)
	}
}
