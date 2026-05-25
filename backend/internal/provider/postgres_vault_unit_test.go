// 包 provider — postgres_vault 私有 mapCredential 单测（不需要真 DB）。
//
// 已有 postgres_vault_test.go 走 integration_pg 构建标签 + 真实 DB 路径；
// 本文件覆盖纯函数 mapSession，不与 DB 集成测试冲突。
package provider

import (
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestMapCredential_SessionHappyPath(t *testing.T) {
	raw := []byte(`{"session_token":"sess-abc","extra":{"cookie":"c=1","user_agent":"x"}}`)
	cred, err := mapCredential("session", raw)
	if err != nil {
		t.Fatal(err)
	}
	if cred.Type != CredentialTypeSessionToken {
		t.Errorf("Type=%q want session_token", cred.Type)
	}
	if cred.Value != "sess-abc" {
		t.Errorf("Value=%q want sess-abc", cred.Value)
	}
	if cred.Extra["cookie"] != "c=1" || cred.Extra["user_agent"] != "x" {
		t.Errorf("Extra=%v want cookie+user_agent 透传", cred.Extra)
	}
}

func TestMapCredential_SessionWithoutExtra(t *testing.T) {
	raw := []byte(`{"session_token":"sess-only"}`)
	cred, err := mapCredential("session", raw)
	if err != nil {
		t.Fatal(err)
	}
	if cred.Type != CredentialTypeSessionToken {
		t.Errorf("Type=%q want session_token", cred.Type)
	}
	if cred.Value != "sess-only" {
		t.Errorf("Value=%q want sess-only", cred.Value)
	}
	// extra 为空时不应分配 map
	if cred.Extra != nil {
		t.Errorf("Extra 应为 nil 当 JSONB 无 extra 字段，得到 %v", cred.Extra)
	}
}

func TestMapCredential_SessionEmptyTokenRejected(t *testing.T) {
	raw := []byte(`{"session_token":""}`)
	_, err := mapCredential("session", raw)
	if !errors.Is(err, ErrCredentialFormat) {
		t.Errorf("空 session_token 应返回 ErrCredentialFormat，得到 %v", err)
	}
}

func TestMapCredential_SessionMalformedJSONRejected(t *testing.T) {
	raw := []byte(`{not json`)
	_, err := mapCredential("session", raw)
	if !errors.Is(err, ErrCredentialFormat) {
		t.Errorf("格式错误 JSON 应返回 ErrCredentialFormat，得到 %v", err)
	}
}

func TestMapCredential_UnknownAccountTypeStillRejected(t *testing.T) {
	// 回归：未知 account_type 仍返回 ErrCredentialFormat（防止未来误改 mapCredential 默认分支）
	_, err := mapCredential("totally_unknown", []byte(`{}`))
	if !errors.Is(err, ErrCredentialFormat) {
		t.Errorf("未知 account_type 应返回 ErrCredentialFormat，得到 %v", err)
	}
}

func TestMapRuntimeMaterialFromAccountCredentials(t *testing.T) {
	cred := mapRuntimeMaterial(credentialstore.RuntimeMaterial{
		Kind:  credentialstore.RuntimeUpstreamPassthrough,
		Value: "Bearer oauth-token",
		Extra: map[string]string{"auth_header": "Authorization"},
	})
	if cred.Type != CredentialTypeUpstreamPassthrough || cred.Value != "Bearer oauth-token" {
		t.Fatalf("cred=%+v", cred)
	}
	if cred.Extra["auth_header"] != "Authorization" {
		t.Fatalf("extra=%v", cred.Extra)
	}
}
