package hermeschat

import (
	"context"
	"encoding/json"
	"testing"
)

func TestPrepareRequest签入真实管理员且不再注入私有工具协议(t *testing.T) {
	store := newBridgeStore()
	store.nextConversationID = 2001
	bridge := mustBridgeWithOptions(t, store)

	prepared, err := bridge.PrepareRequest(context.Background(), Request{
		TenantID: 7, UserID: 42, RequestID: "req-admin", Model: "gpt-4o", ModelBaseURL: "https://model.example.com/v1", ModelAPIKey: []byte("sk-test"),
		Body: []byte(`{"messages":[{"role":"user","content":"分析账号 5 的异常"}]}`),
		Operator: SessionOperator{
			TenantID: 7, ActorSource: "token", ActorID: 99, Role: "platform_admin",
		},
	})
	if err != nil {
		t.Fatalf("准备请求失败：%v", err)
	}
	if prepared.ActorSource != "token" || prepared.ActorID != 99 || prepared.ActorRole != "platform_admin" {
		t.Fatalf("准备结果操作者=%s/%d，角色=%q，不符合预期", prepared.ActorSource, prepared.ActorID, prepared.ActorRole)
	}

	var body map[string]any
	if err := json.Unmarshal(prepared.Body, &body); err != nil {
		t.Fatalf("运行器请求体不是合法 JSON：%v", err)
	}
	token, _ := body["mcp_token"].(string)
	claims, err := VerifyInternalToken(token, []byte(testInternalSecret), bridge.now())
	if err != nil {
		t.Fatalf("独立验签失败：%v", err)
	}
	if claims.Purpose != InternalTokenPurposeMCP || claims.TenantID != 7 || claims.UserID != 42 || claims.ActorSource != "token" || claims.ActorID != 99 || claims.ActorRole != "platform_admin" {
		t.Fatalf("令牌声明=%+v，不符合预期", claims)
	}
	if _, exists := body["tool_catalog"]; exists {
		t.Fatalf("运行器请求仍包含已退役的私有工具目录：%v", body["tool_catalog"])
	}
}

func TestPrepareRequest拒绝缺少管理员身份的普通用户(t *testing.T) {
	store := newBridgeStore()
	store.nextConversationID = 3001
	bridge := mustBridgeWithOptions(t, store)
	_, err := bridge.PrepareRequest(context.Background(), Request{
		TenantID: 7, UserID: 42, RequestID: "req-end-user", Model: "gpt-4o", ModelBaseURL: "https://model.example.com/v1", ModelAPIKey: []byte("sk-test"),
		Body: []byte(`{"messages":[{"role":"user","content":"你好"}]}`),
	})
	if err == nil {
		t.Fatal("普通用户缺少管理员身份却进入了 Hermes 聊天")
	}
}
