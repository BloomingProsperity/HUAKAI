// 包 kiro — KiroSessionAdapter 烟雾测试。
package kiro

import (
	"context"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func TestKiroSessionAdapter_Platform(t *testing.T) {
	a := &KiroSessionAdapter{}
	if got := a.Platform(); got != "kiro" {
		t.Errorf("Platform()=%q want kiro", got)
	}
}

func TestKiroSessionAdapter_RejectsAPIKey(t *testing.T) {
	a := &KiroSessionAdapter{}
	for _, ct := range a.AcceptableCredentialTypes() {
		if ct == provider.CredentialTypeAPIKey {
			t.Error("AcceptableCredentialTypes 不应包含 apikey")
		}
	}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "kiro-default",
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-x"},
	})
	if err == nil || !strings.Contains(err.Error(), "apikey") {
		t.Errorf("apikey 凭据应被拒绝，err=%v", err)
	}
}

func TestKiroSessionAdapter_RejectsEmptyValue(t *testing.T) {
	a := &KiroSessionAdapter{}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "kiro-default",
		Credential:      provider.Credential{Type: provider.CredentialTypeSessionToken, Value: ""},
	})
	if err == nil {
		t.Error("空 Credential.Value 应被拒绝")
	}
}

func TestKiroSessionAdapter_RejectsEmptyModelID(t *testing.T) {
	a := &KiroSessionAdapter{}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "",
		Credential:      provider.Credential{Type: provider.CredentialTypeSessionToken, Value: "cognito-token"},
	})
	if err == nil {
		t.Error("空 UpstreamModelID 应被拒绝")
	}
}

func TestKiroSessionAdapter_HappyPath_InjectsAuthorization(t *testing.T) {
	a := &KiroSessionAdapter{}
	req, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "kiro-default",
		InboundBody:     []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: "cognito-id-token",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer cognito-id-token" {
		t.Errorf("Authorization=%q want Bearer cognito-id-token", got)
	}
}
