package hermeschat

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// stubCatalog implements ToolCatalogProvider with a fixed read-only catalog.
type stubCatalog struct {
	entries []map[string]any
}

func (s stubCatalog) ReadOnlyToolCatalog() []map[string]any { return s.entries }

func TestPrepareRequestBindsOperatorAndInjectsCatalogForAdminSession(t *testing.T) {
	// Regression: for an ADMIN session (operator set) PrepareRequest must (a) bind
	// the operator to the internal_token's request_id so the runner's tool
	// callbacks resolve to this operator, and (b) inject the read-only tool
	// catalog into the runner body. Mutation: if the bind is skipped, the
	// conversational tool loop fails closed (the internal endpoint rejects the
	// session); if the catalog injection is skipped, the LLM has no tools. We
	// assert the binding resolves AND the body carries the catalog.
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

	// (a) The operator binding resolves under the request_id.
	op, ok := bindings.Lookup("req-admin")
	if !ok {
		t.Fatalf("operator binding not registered for admin session")
	}
	if op.TenantID != 7 || op.ActorUserID != 42 || op.AdminActorTokenID != 99 || op.Role != "platform_admin" {
		t.Fatalf("bound operator=%+v want tenant 7 user 42 token 99 platform_admin", op)
	}

	// (b) The runner body carries the catalog.
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
	// Regression (SAFETY): an end-user-path session (NO operator) must NOT be bound
	// and must NOT receive a tool catalog — the conversational tool loop is
	// admin-only. Mutation: if the operator gate were dropped, an unprivileged
	// session would get a binding + catalog and could drive diagnostic tools. We
	// pass a zero-value Operator and assert no binding + no catalog in the body.
	store := newBridgeStore()
	store.nextConversationID = 3001
	bindings := NewSessionBindings(func() time.Time { return time.Unix(1700000000, 0).UTC() })
	catalog := stubCatalog{entries: []map[string]any{{"name": "audit_lookup"}}}
	bridge := mustBridgeWithOptions(t, store, WithSessionBindings(bindings), WithToolCatalog(catalog))

	prepared, err := bridge.PrepareRequest(context.Background(), Request{
		TenantID: 7, UserID: 42, RequestID: "req-enduser",
		Body: []byte(`{"messages":[{"role":"user","content":"hi"}]}`),
		// Operator left zero-valued => not an admin session.
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
	// Defect this catches (KNOB B, bridge-side gate): if WithToolLoopEnabled(false)
	// did NOT suppress catalog injection, the LLM would still be told about callable
	// tools while HUAKAI_HERMES_LLM_TOOLLOOP_ENABLED=false. With the loop disabled,
	// an ADMIN session must get NO tool_catalog in the runner body — even though the
	// catalog provider is wired and the operator is still bound (the internal tool
	// endpoint then 403s as the cooperating enforcing half).
	//
	// Mutation check (run + RED confirmed): drop the `b.toolLoopEnabled &&` guard in
	// PrepareRequest's catalog-injection block (revert to `if b.toolCatalog != nil`)
	// and the catalog is injected anyway — the "no tool_catalog" assertion goes RED.
	store := newBridgeStore()
	store.nextConversationID = 4001
	bindings := NewSessionBindings(func() time.Time { return time.Unix(1700000000, 0).UTC() })
	catalog := stubCatalog{entries: []map[string]any{
		{"name": "audit_lookup", "description": "read audit events", "input_schema": map[string]any{}},
	}}
	// Catalog provider wired, but the tool loop is disabled.
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
	// Positive control: with the tool loop ENABLED (default), the SAME admin session
	// + wired catalog DOES inject the tool_catalog. This proves the suppression
	// above is caused by the kill-switch, not a broken session, and that the default
	// (NewBridge sets toolLoopEnabled=true) is zero behavior change when unset.
	store := newBridgeStore()
	store.nextConversationID = 4101
	bindings := NewSessionBindings(func() time.Time { return time.Unix(1700000000, 0).UTC() })
	catalog := stubCatalog{entries: []map[string]any{{"name": "audit_lookup"}}}
	// No WithToolLoopEnabled => default-enabled.
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
