package replicate

import (
	"context"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func TestNewCancelRequestDefaultEndpointAndAuth(t *testing.T) {
	req, err := NewCancelRequest(context.Background(), provider.Credential{
		Type:  provider.CredentialTypeAPIKey,
		Value: "r8_secret",
	}, "pred-abc")
	if err != nil {
		t.Fatalf("NewCancelRequest: %v", err)
	}
	if req.Method != "POST" {
		t.Fatalf("method=%s want POST", req.Method)
	}
	if got := req.URL.String(); got != "https://api.replicate.com/v1/predictions/pred-abc/cancel" {
		t.Fatalf("url=%q want default predictions cancel endpoint", got)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer r8_secret" {
		t.Fatalf("auth=%q want Bearer(与 BuildRequest 同口径)", got)
	}
}

func TestNewCancelRequestPassthroughBaseURLAndHeader(t *testing.T) {
	// MUTATION: cancel 不走 EndpointForCredential / applyCredentialAuth(自拼
	// 官方 host 或硬编码 Bearer)→ 自托管代理凭据的 cancel 打到官方端点或
	// 鉴权头错误 → 两断言 RED。
	req, err := NewCancelRequest(context.Background(), provider.Credential{
		Type:  provider.CredentialTypeUpstreamPassthrough,
		Value: "Token xyz",
		Extra: map[string]string{
			"base_url":    "https://relay.example.com",
			"auth_header": "X-Api-Key",
		},
	}, "pred-1")
	if err != nil {
		t.Fatalf("NewCancelRequest: %v", err)
	}
	if got := req.URL.String(); got != "https://relay.example.com/v1/predictions/pred-1/cancel" {
		t.Fatalf("url=%q want base_url 覆盖后的 cancel 端点", got)
	}
	if got := req.Header.Get("X-Api-Key"); got != "Token xyz" {
		t.Fatalf("X-Api-Key=%q want 透传凭据原值", got)
	}
}

func TestNewCancelRequestRejectsNonTokenID(t *testing.T) {
	// MUTATION: 删 id 白名单校验 → "/"、"?"、".." 注入路径段/query(且 passthrough
	// base_url 重组会把 escape 塌缩,escape 救不了)→ err==nil → RED。
	for _, bad := range []string{"a/b?c", "../predictions", "id with space", "id#frag"} {
		if _, err := NewCancelRequest(context.Background(), provider.Credential{
			Type:  provider.CredentialTypeAPIKey,
			Value: "r8",
		}, bad); err == nil {
			t.Fatalf("id=%q 应被白名单拒绝", bad)
		}
	}
}

func TestNewCancelRequestRejectsEmptyIDOrCredential(t *testing.T) {
	if _, err := NewCancelRequest(context.Background(), provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "r8"}, "  "); err == nil {
		t.Fatal("空 prediction id 应报错")
	}
	if _, err := NewCancelRequest(context.Background(), provider.Credential{Type: provider.CredentialTypeAPIKey}, "pred-1"); err == nil {
		t.Fatal("空凭据应报错")
	}
}

func TestPredictionMetaFromResponse(t *testing.T) {
	meta := PredictionMetaFromResponse([]byte(`{"id":" pred-7 ","status":"processing","output":null}`))
	if meta.ID != "pred-7" || meta.Status != "processing" {
		t.Fatalf("meta=%+v want id=pred-7 status=processing(含 trim)", meta)
	}
	if meta := PredictionMetaFromResponse([]byte("not json")); meta.ID != "" || meta.Status != "" {
		t.Fatalf("malformed 响应应得零值 meta,got %+v", meta)
	}
}

func TestCancelWorthwhile(t *testing.T) {
	for status, want := range map[string]bool{
		"starting":   true,
		"processing": true,
		"":           true, // 未知状态保守发:宁多一次幂等 cancel,不留计费泄漏
		"failed":     false,
		"canceled":   false,
		"succeeded":  false,
	} {
		if got := CancelWorthwhile(status); got != want {
			t.Fatalf("CancelWorthwhile(%q)=%v want %v", status, got, want)
		}
	}
}

func TestBuildRequestPreferWaitSecondsOverride(t *testing.T) {
	in := provider.BuildInput{
		UpstreamModelID: "owner/name",
		InboundBody:     []byte(`{"prompt":"x"}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeAPIKey,
			Value: "r8",
			Extra: map[string]string{"prefer_wait_seconds": "15"},
		},
	}
	req, err := (&Adapter{}).BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	// MUTATION: 忽略 prefer_wait_seconds 覆盖(恒 wait=60)→ RED。
	if got := req.Header.Get("Prefer"); got != "wait=15" {
		t.Fatalf("Prefer=%q want wait=15(凭据级覆盖)", got)
	}
	if got := req.Header.Get("Cancel-After"); got != "15s" {
		t.Fatalf("Cancel-After=%q want 15s(必须与等待窗口同值)", got)
	}
}

func TestBuildRequestPreferWaitSecondsInvalidFallsBackToDefault(t *testing.T) {
	// fail-safe:非法值回默认 60 + 告警,绝不让一个垃圾配置值把整账号打成 502
	//(评审定级:此为调优旋钮,默认永远合法,爆炸半径 > typo 检出收益)。
	// MUTATION: 非法值 fail-loud(BuildRequest 报错)→ err!=nil → RED;
	// 非法值被原样透传进 Prefer 头 → wait!=60 → RED。
	for _, bad := range []string{"0", "61", "abc", "-5"} {
		in := provider.BuildInput{
			UpstreamModelID: "owner/name",
			InboundBody:     []byte(`{"prompt":"x"}`),
			Credential: provider.Credential{
				Type:  provider.CredentialTypeAPIKey,
				Value: "r8",
				Extra: map[string]string{"prefer_wait_seconds": bad},
			},
		}
		req, err := (&Adapter{}).BuildRequest(context.Background(), in)
		if err != nil {
			t.Fatalf("prefer_wait_seconds=%q 应 fail-safe 回默认,got err=%v", bad, err)
		}
		if got := req.Header.Get("Prefer"); got != "wait=60" {
			t.Fatalf("prefer_wait_seconds=%q → Prefer=%q want wait=60", bad, got)
		}
		if got := req.Header.Get("Cancel-After"); got != "60s" {
			t.Fatalf("prefer_wait_seconds=%q → Cancel-After=%q want 60s", bad, got)
		}
	}
}
