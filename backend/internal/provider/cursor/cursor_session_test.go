// 包 cursor — CursorSessionAdapter 烟雾测试。仅覆盖 adapter 协议契约
// （Platform / AcceptableCredentialTypes / BuildRequest 拒 apikey 与必填校验），
// 真实 endpoint / vendor header 行为待 OCAW 抓包后扩充。
package cursor

import (
	"context"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func TestCursorSessionAdapter_Platform(t *testing.T) {
	a := &CursorSessionAdapter{}
	if got := a.Platform(); got != "cursor" {
		t.Errorf("Platform()=%q want cursor", got)
	}
}

func TestCursorSessionAdapter_RejectsAPIKey(t *testing.T) {
	a := &CursorSessionAdapter{}
	for _, ct := range a.AcceptableCredentialTypes() {
		if ct == provider.CredentialTypeAPIKey {
			t.Error("AcceptableCredentialTypes 不应包含 apikey")
		}
	}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "claude-3.5-sonnet",
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-x"},
	})
	if err == nil || !strings.Contains(err.Error(), "apikey") {
		t.Errorf("apikey 凭据应被拒绝，err=%v", err)
	}
}

func TestCursorSessionAdapter_RejectsEmptyValue(t *testing.T) {
	a := &CursorSessionAdapter{}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "claude-3.5-sonnet",
		Credential:      provider.Credential{Type: provider.CredentialTypeSessionToken, Value: "   "},
	})
	if err == nil {
		t.Error("空白 Credential.Value 应被拒绝")
	}
}

func TestCursorSessionAdapter_RejectsEmptyModelID(t *testing.T) {
	a := &CursorSessionAdapter{}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "",
		Credential:      provider.Credential{Type: provider.CredentialTypeSessionToken, Value: "sess-token"},
	})
	if err == nil {
		t.Error("空 UpstreamModelID 应被拒绝")
	}
}

func TestCursorSessionAdapter_HappyPath_InjectsAuthorization(t *testing.T) {
	a := &CursorSessionAdapter{}
	req, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "claude-3.5-sonnet",
		InboundBody:     []byte(`{"model":"claude-3.5-sonnet"}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: "cursor-session-token",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer cursor-session-token" {
		t.Errorf("Authorization=%q want Bearer cursor-session-token", got)
	}
}
