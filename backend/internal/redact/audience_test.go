package redact

import (
	"reflect"
	"testing"
)

func TestFieldsForAudience_PublicFields(t *testing.T) {
	fields := FieldsForAudience(AudiencePublic)
	assertHasFields(t, fields, "request_id", "model_requested", "model_actual", "token_count_total", "status_code", "signature", "merkle_root", "pubkey_fp")
	assertLacksFields(t, fields, "tenant_id", "account_id_hash", "hop_chain")
}

func TestFieldsForAudience_TenantOperatorFields(t *testing.T) {
	fields := FieldsForAudience(AudienceTenantOperator)
	assertHasFields(t, fields, "request_id", "tenant_id", "route_id", "pool_id", "cache_hit_ratio", "latency_ms_total", "latency_ms_first_token", "latency_ms_tta", "error_class", "hop_chain")
	assertLacksFields(t, fields, "account_id_hash", "latency_ms_upstream", "provider")
}

func TestFieldsForAudience_PlatformAdminFields(t *testing.T) {
	fields := FieldsForAudience(AudiencePlatformAdmin)
	assertHasFields(t, fields, "account_id_hash", "upstream_model_reported", "error_code", "retry_reason", "provider", "ingress_path", "client_protocol", "latency_ms_upstream", "hop_chain")
	assertLacksFields(t, fields, "trace_id", "key_id_hash", "cost_usd_cents")
}

func TestFieldsForAudience_InternalFields(t *testing.T) {
	fields := FieldsForAudience(AudienceInternal)
	assertHasFields(t, fields, "trace_id", "key_id_hash", "cost_usd_cents", "protocol_family", "evidence_label", "attempt", "signature", "hop_chain")
	assertLacksFields(t, fields, "prompt", "api_key", "authorization")
}

func TestFieldsForAudience_Hierarchy(t *testing.T) {
	assertSubset(t, FieldsForAudience(AudiencePublic), FieldsForAudience(AudienceTenantOperator))
	assertSubset(t, FieldsForAudience(AudienceTenantOperator), FieldsForAudience(AudiencePlatformAdmin))
	assertSubset(t, FieldsForAudience(AudiencePlatformAdmin), FieldsForAudience(AudienceInternal))
}

func TestFieldsForAudience_ReturnsCopy(t *testing.T) {
	fields := FieldsForAudience(AudiencePublic)
	fields["account_id_hash"] = struct{}{}
	if _, ok := FieldsForAudience(AudiencePublic)["account_id_hash"]; ok {
		t.Fatal("修改返回值不应污染内部 audienceLevel")
	}
}

func TestFieldsForAudience_UnknownAudienceEmpty(t *testing.T) {
	if got := FieldsForAudience(Audience("unknown")); len(got) != 0 {
		t.Fatalf("未知 audience 应返回空集合，got=%v", got)
	}
}

func TestRedactForAudience_PublicAllowsOnlyPublicFields(t *testing.T) {
	in := map[string]any{
		"request_id": "req_1", "model_requested": "gpt-x", "signature": "sig", "merkle_root": "root",
		"tenant_id": "ten_1", "account_id_hash": "acct_hash", "prompt": "secret",
	}
	got := RedactForAudience(in, AudiencePublic)
	want := map[string]any{"request_id": "req_1", "model_requested": "gpt-x", "signature": "sig", "merkle_root": "root"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("public redaction got=%v want=%v", got, want)
	}
}

func TestRedactForAudience_TenantOperatorAllowsTenantFields(t *testing.T) {
	in := map[string]any{
		"request_id": "req_2", "tenant_id": "ten_1", "pool_id": "pool_1",
		"latency_ms_total": 33, "account_id_hash": "acct_hash", "error_code": "429",
	}
	got := RedactForAudience(in, AudienceTenantOperator)
	want := map[string]any{"request_id": "req_2", "tenant_id": "ten_1", "pool_id": "pool_1", "latency_ms_total": 33}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tenant redaction got=%v want=%v", got, want)
	}
}

func TestRedactForAudience_PlatformAdminAllowsAdminFields(t *testing.T) {
	hops := []map[string]any{{"name": "pool", "ts": "2026-05-14T00:00:00Z", "account_id_hash": "acct_hash"}}
	in := map[string]any{
		"request_id": "req_3", "tenant_id": "ten_1", "account_id_hash": "acct_hash",
		"provider": "openai", "latency_ms_upstream": 21, "hop_chain": hops, "messages": []string{"secret"},
	}
	got := RedactForAudience(in, AudiencePlatformAdmin)
	want := map[string]any{"request_id": "req_3", "tenant_id": "ten_1", "account_id_hash": "acct_hash", "provider": "openai", "latency_ms_upstream": 21, "hop_chain": hops}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("admin redaction got=%v want=%v", got, want)
	}
}

func TestRedactForAudience_InternalPassesDebugButDropsForbidden(t *testing.T) {
	in := map[string]any{"debug_detail": "kept in memory", "trace_id": "tr_1", "api_key": "sk", "Authorization": "Bearer x"}
	got := RedactForAudience(in, AudienceInternal)
	want := map[string]any{"debug_detail": "kept in memory", "trace_id": "tr_1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("internal redaction got=%v want=%v", got, want)
	}
}

func TestRedactForAudience_ForbiddenFieldsDroppedForEveryAudience(t *testing.T) {
	for _, aud := range []Audience{AudiencePublic, AudienceTenantOperator, AudiencePlatformAdmin, AudienceInternal} {
		got := RedactForAudience(map[string]any{
			"request_id": "req_4", "prompt": "p", "completion": "c", "messages": []string{"m"},
			"content": "body", "api_key": "sk", "authorization": "bearer",
		}, aud)
		assertLacksAnyKeys(t, got, "prompt", "completion", "messages", "content", "api_key", "authorization")
	}
}

func TestRedactForAudience_TenantHopChainDropsAccountHash(t *testing.T) {
	in := map[string]any{"hop_chain": []map[string]any{
		{"name": "route", "ts": "t1", "account_id_hash": "hidden"},
		{"hop_name": "provider", "timestamp": "t2", "provider": "hidden-too"},
	}}
	got := RedactForAudience(in, AudienceTenantOperator)
	want := map[string]any{"hop_chain": []map[string]any{
		{"name": "route", "ts": "t1"},
		{"hop_name": "provider", "timestamp": "t2"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tenant hop_chain got=%v want=%v", got, want)
	}
}

func TestRedactForAudience_TenantDropsUnsupportedHopChainShape(t *testing.T) {
	got := RedactForAudience(map[string]any{"hop_chain": "route acct_hash"}, AudienceTenantOperator)
	if _, ok := got["hop_chain"]; ok {
		t.Fatalf("tenant 不应透出无法结构化脱敏的 hop_chain: %v", got)
	}
}

func TestRedactForAudience_UnknownAudienceRejectsAll(t *testing.T) {
	got := RedactForAudience(map[string]any{"request_id": "req_5", "tenant_id": "ten_1"}, Audience("unknown"))
	if len(got) != 0 {
		t.Fatalf("未知 audience 应拒绝所有字段，got=%v", got)
	}
}

func TestRedactForAudience_NilEntry(t *testing.T) {
	if RedactForAudience(nil, AudiencePublic) != nil {
		t.Fatal("nil entry 应返回 nil")
	}
}

func TestRedactForAudience_EmptyEntry(t *testing.T) {
	got := RedactForAudience(map[string]any{}, AudiencePlatformAdmin)
	if len(got) != 0 {
		t.Fatalf("empty entry 应返回 empty map，got=%v", got)
	}
}

func TestDroppedFieldsForAudience_Sorted(t *testing.T) {
	in := map[string]any{"request_id": "req_6", "tenant_id": "ten_1", "api_key": "sk", "account_id_hash": "acct"}
	got := DroppedFieldsForAudience(in, AudiencePublic)
	want := []string{"account_id_hash", "api_key", "tenant_id"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dropped got=%v want=%v", got, want)
	}
}

func TestDroppedFieldsForAudience_NilEntry(t *testing.T) {
	if DroppedFieldsForAudience(nil, AudienceInternal) != nil {
		t.Fatal("nil entry 应返回 nil")
	}
}

func assertHasFields(t *testing.T, fields map[string]struct{}, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, ok := fields[name]; !ok {
			t.Fatalf("字段集合缺少 %q", name)
		}
	}
}

func assertLacksFields(t *testing.T, fields map[string]struct{}, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, ok := fields[name]; ok {
			t.Fatalf("字段集合不应包含 %q", name)
		}
	}
}

func assertSubset(t *testing.T, small, large map[string]struct{}) {
	t.Helper()
	for name := range small {
		if _, ok := large[name]; !ok {
			t.Fatalf("%q 不在上级字段集合中", name)
		}
	}
}

func assertLacksAnyKeys(t *testing.T, got map[string]any, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, ok := got[name]; ok {
			t.Fatalf("输出不应包含 %q: %v", name, got)
		}
	}
}
