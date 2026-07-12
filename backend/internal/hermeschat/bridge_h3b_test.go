package hermeschat

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// stubCatalog 以一个固定的只读目录实现 ToolCatalogProvider。
type stubCatalog struct {
	entries []map[string]any
}

func (s stubCatalog) ToolCatalog() []map[string]any { return s.entries }

func TestPrepareRequestBindsOperatorAndInjectsCatalogForAdminSession(t *testing.T) {
	// 回归:对于 ADMIN 会话(已设置 operator),PrepareRequest 必须(a)把 operator 绑定
	// 到 internal_token 的 request_id,使 runner 的工具回调能解析到此 operator;并且(b)把
	// 只读工具目录注入 runner body。变异:若跳过绑定,会话式工具循环会 fail closed(内部端点
	// 拒绝该会话);若跳过目录注入,LLM 就没有工具。我们断言绑定可解析,且 body 携带了目录。
	store := newBridgeStore()
	store.nextConversationID = 2001
	bindings := NewSessionBindings(func() time.Time { return time.Unix(1700000000, 0).UTC() })
	catalog := stubCatalog{entries: []map[string]any{
		{"name": "audit_lookup", "description": "read audit events", "input_schema": map[string]any{}},
	}}
	bridge := mustBridgeWithOptions(t, store, WithSessionBindings(bindings), WithToolCatalog(catalog))

	prepared, err := bridge.PrepareRequest(context.Background(), Request{
		TenantID: 7, UserID: 42, RequestID: "req-admin",
		Body:     []byte(`{"messages":[{"role":"user","content":"why is account 5 unhealthy"}]}`),
		Operator: SessionOperator{AdminActorTokenID: 99, Role: "platform_admin"},
	})
	if err != nil {
		t.Fatalf("PrepareRequest: %v", err)
	}
	if !prepared.BoundOperator {
		t.Fatalf("admin session not marked BoundOperator")
	}

	// (a) operator 绑定可在该 request_id 下解析出来。
	op, ok := bindings.Lookup("req-admin")
	if !ok {
		t.Fatalf("operator binding not registered for admin session")
	}
	if op.TenantID != 7 || op.ActorUserID != 42 || op.AdminActorTokenID != 99 || op.Role != "platform_admin" {
		t.Fatalf("bound operator=%+v want tenant 7 user 42 token 99 platform_admin", op)
	}

	// (b) runner body 携带了目录。
	var body map[string]any
	if err := json.Unmarshal(prepared.Body, &body); err != nil {
		t.Fatalf("runner body json: %v", err)
	}
	raw, ok := body["tool_catalog"].([]any)
	if !ok || len(raw) != 1 {
		t.Fatalf("tool_catalog=%v want one read-only entry", body["tool_catalog"])
	}
	entry, _ := raw[0].(map[string]any)
	if entry["name"] != "audit_lookup" {
		t.Fatalf("catalog[0].name=%v want audit_lookup", entry["name"])
	}
}

func TestPrepareRequestDoesNotBindOrInjectForEndUserSession(t *testing.T) {
	// 回归(安全):终端用户路径的会话(无 operator)绝不能被绑定,也绝不能收到工具目录
	//——会话式工具循环仅限 admin。变异:若去掉 operator 闸门,无特权会话也会获得绑定 + 目录
	// 并能驱动诊断工具。我们传入零值的 Operator,并断言 body 中没有绑定、没有目录。
	store := newBridgeStore()
	store.nextConversationID = 3001
	bindings := NewSessionBindings(func() time.Time { return time.Unix(1700000000, 0).UTC() })
	catalog := stubCatalog{entries: []map[string]any{{"name": "audit_lookup"}}}
	bridge := mustBridgeWithOptions(t, store, WithSessionBindings(bindings), WithToolCatalog(catalog))

	prepared, err := bridge.PrepareRequest(context.Background(), Request{
		TenantID: 7, UserID: 42, RequestID: "req-enduser",
		Body: []byte(`{"messages":[{"role":"user","content":"hi"}]}`),
		// Operator 保持零值 => 不是 admin 会话。
	})
	if err != nil {
		t.Fatalf("PrepareRequest: %v", err)
	}
	if prepared.BoundOperator {
		t.Fatalf("end-user session was marked BoundOperator")
	}
	if _, ok := bindings.Lookup("req-enduser"); ok {
		t.Fatalf("end-user session got an operator binding (tool loop would be reachable)")
	}
	var body map[string]any
	if err := json.Unmarshal(prepared.Body, &body); err != nil {
		t.Fatalf("runner body json: %v", err)
	}
	if _, ok := body["tool_catalog"]; ok {
		t.Fatalf("end-user session received a tool catalog: %v", body["tool_catalog"])
	}
}

func TestKnobB_ToolLoopDisabledSuppressesCatalogInjection(t *testing.T) {
	// 本测试捕获的缺陷(KNOB B,bridge 侧闸门):若 WithToolLoopEnabled(false) 没有抑制
	// 目录注入,那么在 HUAKAI_HERMES_LLM_TOOLLOOP_ENABLED=false 时,LLM 仍会被告知有可调用
	// 的工具。在循环被禁用时,ADMIN 会话的 runner body 必须不含 tool_catalog——即便目录提供
	// 方已接线且 operator 仍被绑定(此时内部工具端点会作为协同的强制执行一方返回 403)。
	//
	// 变异检查(已跑且确认变红):去掉 PrepareRequest 目录注入块中的 `b.toolLoopEnabled &&`
	// 守卫(回退为 `if b.toolCatalog != nil`),目录就会照样被注入——“无 tool_catalog” 断言
	// 随之变红。
	store := newBridgeStore()
	store.nextConversationID = 4001
	bindings := NewSessionBindings(func() time.Time { return time.Unix(1700000000, 0).UTC() })
	catalog := stubCatalog{entries: []map[string]any{
		{"name": "audit_lookup", "description": "read audit events", "input_schema": map[string]any{}},
	}}
	// 目录提供方已接线,但工具循环被禁用。
	bridge := mustBridgeWithOptions(t, store,
		WithSessionBindings(bindings), WithToolCatalog(catalog), WithToolLoopEnabled(false))

	prepared, err := bridge.PrepareRequest(context.Background(), Request{
		TenantID: 7, UserID: 42, RequestID: "req-loop-off",
		Body:     []byte(`{"messages":[{"role":"user","content":"why is account 5 unhealthy"}]}`),
		Operator: SessionOperator{AdminActorTokenID: 99, Role: "platform_admin"},
	})
	if err != nil {
		t.Fatalf("PrepareRequest: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(prepared.Body, &body); err != nil {
		t.Fatalf("runner body json: %v", err)
	}
	if _, ok := body["tool_catalog"]; ok {
		t.Fatalf("tool loop disabled but catalog injected: %v", body["tool_catalog"])
	}
}

func TestKnobB_ToolLoopEnabledByDefaultInjectsCatalog(t *testing.T) {
	// 阳性对照:在工具循环启用(默认)时,同样的 admin 会话 + 已接线目录确实会注入
	// tool_catalog。这证明上面的抑制是由 kill-switch 引起的,而非会话本身损坏,并且默认值
	//(NewBridge 设置 toolLoopEnabled=true)在未设置时行为零变。
	store := newBridgeStore()
	store.nextConversationID = 4101
	bindings := NewSessionBindings(func() time.Time { return time.Unix(1700000000, 0).UTC() })
	catalog := stubCatalog{entries: []map[string]any{{"name": "audit_lookup"}}}
	// 不传 WithToolLoopEnabled => 默认启用。
	bridge := mustBridgeWithOptions(t, store, WithSessionBindings(bindings), WithToolCatalog(catalog))

	prepared, err := bridge.PrepareRequest(context.Background(), Request{
		TenantID: 7, UserID: 42, RequestID: "req-loop-default-on",
		Body:     []byte(`{"messages":[{"role":"user","content":"hi"}]}`),
		Operator: SessionOperator{AdminActorTokenID: 99, Role: "platform_admin"},
	})
	if err != nil {
		t.Fatalf("PrepareRequest: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(prepared.Body, &body); err != nil {
		t.Fatalf("runner body json: %v", err)
	}
	if _, ok := body["tool_catalog"]; !ok {
		t.Fatalf("default-enabled tool loop did not inject catalog: %v", body)
	}
}
