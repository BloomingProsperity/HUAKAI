package redact

import "testing"

func TestIsSafeField_AllowlistCore(t *testing.T) {
	allowed := []string{
		"request_id", "trace_id", "tenant_id", "key_id_hash", "account_id_hash",
		"model_requested", "model_actual", "upstream_model_reported",
		"token_count_input", "token_count_output", "token_count_total",
		"cache_hit_ratio", "cache_creation_tokens", "cache_read_tokens",
		"cost_usd_cents", "latency_ms_total", "latency_ms_first_token",
		"status_code", "error_class", "error_code",
		"pool_id", "route_id", "provider", "ingress_path",
		"client_protocol", "protocol_family", "evidence_label",
	}
	for _, name := range allowed {
		if !IsSafeField(name) {
			t.Errorf("expected %q to be safe", name)
		}
	}
}

func TestIsSafeField_RejectUserContentFields(t *testing.T) {
	// 这些字段任何情况都不允许写 system_log。
	forbidden := []string{
		"prompt", "completion", "messages", "content",
		"text", "tool_input", "tool_output", "tool_result",
		"system", "instructions", "thinking", "reasoning_summary",
		"user_email", "user_name", "api_key", "bearer_token",
		"authorization", "x-api-key",
	}
	for _, name := range forbidden {
		if IsSafeField(name) {
			t.Errorf("forbidden field %q must not be safe", name)
		}
	}
}

func TestIsSafeField_EmptyAndUnknownReject(t *testing.T) {
	if IsSafeField("") {
		t.Error("empty field name must be unsafe")
	}
	if IsSafeField("random_future_field") {
		t.Error("unknown field must be unsafe (allowlist strict)")
	}
}

func TestSystemLogSafeFieldsSnapshot_IsCopy(t *testing.T) {
	snap := SystemLogSafeFieldsSnapshot()
	if len(snap) == 0 {
		t.Fatal("snapshot must not be empty")
	}
	// 尝试污染 snapshot 不应影响内部 allowlist。
	snap["polluted_field"] = struct{}{}
	if IsSafeField("polluted_field") {
		t.Error("mutating snapshot must not affect internal allowlist")
	}
}
