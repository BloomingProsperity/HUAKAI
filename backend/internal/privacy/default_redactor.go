package privacy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

const (
	defaultMaxString = 256
	redactedValue    = "[REDACTED]"
)

type RedactorOption func(*AllowlistRedactor)

type AllowlistRedactor struct {
	maxString         int
	panicOnLongString bool
}

func NewAllowlistRedactor(opts ...RedactorOption) *AllowlistRedactor {
	r := &AllowlistRedactor{maxString: defaultMaxString}
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}
	if r.maxString <= 0 {
		r.maxString = defaultMaxString
	}
	return r
}

func WithMaxString(n int) RedactorOption {
	return func(r *AllowlistRedactor) { r.maxString = n }
}

func WithPanicOnLongString(enabled bool) RedactorOption {
	return func(r *AllowlistRedactor) { r.panicOnLongString = enabled }
}

func (r *AllowlistRedactor) SanitizePayload(_ context.Context, payload any) ([]byte, error) {
	value, err := normalizePayload(payload)
	if err != nil {
		return nil, err
	}
	clean, blocked := r.sanitizeValue("", value)
	raw, err := json.Marshal(clean)
	if err != nil {
		return nil, err
	}
	if blocked {
		return raw, ErrUnsafePayload
	}
	return raw, nil
}

func (r *AllowlistRedactor) SanitizeError(_ context.Context, err error) (string, error) {
	if err == nil {
		return "", nil
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "timeout") || strings.Contains(text, "deadline"):
		return "network_timeout", nil
	case strings.Contains(text, "rate") && strings.Contains(text, "limit"):
		return "upstream_rate_limit", nil
	case strings.Contains(text, "forbidden") || strings.Contains(text, "permission") || strings.Contains(text, "unauthorized"):
		return "upstream_forbidden", nil
	case strings.Contains(text, "panic"):
		return "panic", nil
	case strings.Contains(text, "invalid") || strings.Contains(text, "malformed") || strings.Contains(text, "bad request"):
		return "invalid_request", nil
	case strings.Contains(text, "credential") || strings.Contains(text, "decrypt") || strings.Contains(text, "key unavailable"):
		return "credential_error", nil
	case strings.Contains(text, "upstream"):
		return "upstream_error", nil
	default:
		return "internal_error", nil
	}
}

func (r *AllowlistRedactor) AllowlistField(field string) bool {
	return allowlistField(field)
}

func normalizePayload(payload any) (any, error) {
	if payload == nil {
		return map[string]any{}, nil
	}
	switch v := payload.(type) {
	case json.RawMessage:
		return normalizeBytes([]byte(v))
	case []byte:
		return normalizeBytes(v)
	case string:
		if strings.TrimSpace(v) == "" {
			return map[string]any{}, nil
		}
		value, err := normalizeBytes([]byte(v))
		if err != nil {
			if errors.Is(err, ErrUnsafePayload) {
				return nil, err
			}
			return nil, ErrFreeformString
		}
		return value, nil
	case map[string]any:
		return v, nil
	case map[string]string:
		out := make(map[string]any, len(v))
		for k, val := range v {
			out[k] = val
		}
		return out, nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return normalizeBytes(raw)
}

func normalizeBytes(raw []byte) (any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, nil
	}
	return StrictDecodeJSON(raw)
}

func (r *AllowlistRedactor) sanitizeValue(key string, value any) (any, bool) {
	if sensitiveKey(key) {
		return redactedValue, true
	}
	if key != "" && !r.AllowlistField(key) {
		return redactedValue, true
	}
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v)+1)
		blocked := false
		for k, child := range v {
			if sensitiveKey(k) || !r.AllowlistField(k) {
				blocked = true
				continue
			}
			clean, childBlocked := r.sanitizeValue(k, child)
			if childBlocked {
				blocked = true
			}
			out[k] = clean
		}
		if blocked {
			out["redaction_result"] = RedactionResultBlocked
		}
		return out, blocked
	case []any:
		out := make([]any, len(v))
		blocked := false
		for i, child := range v {
			clean, childBlocked := r.sanitizeValue(key, child)
			if childBlocked {
				blocked = true
			}
			out[i] = clean
		}
		return out, blocked
	case string:
		return r.sanitizeString(key, v)
	case json.Number, bool, nil:
		return v, false
	default:
		rv := reflect.ValueOf(value)
		switch rv.Kind() {
		case reflect.Float64, reflect.Float32, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return value, false
		default:
			return fmt.Sprint(value), false
		}
	}
}

func (r *AllowlistRedactor) sanitizeString(key, value string) (any, bool) {
	value = strings.TrimSpace(value)
	blocked := containsForbiddenString(value)
	if (strings.EqualFold(key, "stack") || strings.EqualFold(key, "body_envelope")) && !blocked {
		return value, false
	}
	if len(value) > r.maxString {
		if r.panicOnLongString {
			panic("privacy: string field exceeded max length")
		}
		sum := sha256.Sum256([]byte(value))
		prefix := value
		if len(prefix) > r.maxString {
			prefix = prefix[:r.maxString]
		}
		return map[string]any{
			"value":             prefix,
			"truncated":         true,
			"redacted_hash_ref": hex.EncodeToString(sum[:8]),
		}, true
	}
	if blocked {
		return redactedValue, true
	}
	return value, false
}

func allowlistField(field string) bool {
	k := strings.ToLower(strings.TrimSpace(field))
	if k == "" {
		return false
	}
	if _, ok := exactAllowlist[k]; ok {
		return true
	}
	for _, suffix := range []string{
		"_id", "_id_ref", "_scope_ref", "_fingerprint", "_version", "_class", "_family",
		"_count", "_tokens", "_micro_usd", "_microcents", "_cents", "_ms", "_pct", "_state",
		"_status", "_type", "_kind", "_at", "_until", "_seconds", "_hours", "_bytes",
	} {
		if strings.HasSuffix(k, suffix) {
			return !sensitiveKey(k)
		}
	}
	return false
}

var exactAllowlist = map[string]struct{}{
	"account": {}, "account_credential_id": {}, "account_id_hash": {}, "actor": {}, "actor_id": {}, "actor_id_ref": {}, "actor_role": {}, "actor_type": {}, "adjustment_refs": {},
	"amount_cents": {}, "attempt_count": {}, "attempts": {}, "auth_mode": {}, "balance_cents": {}, "batch_id": {},
	"billing_event_id": {}, "body_envelope": {}, "cache_hit": {}, "cache_read_input_tokens": {}, "cache_write_input_tokens": {}, "channel": {},
	"channel_id": {}, "claim_id": {}, "component": {}, "cooldown_hours": {}, "cooldown_until": {}, "cost_rate_version": {},
	"cost_total_micro_usd": {}, "created_at": {}, "credential_id": {}, "credential_version": {}, "credentials_present": {}, "currency_code": {},
	"decision_ref": {}, "delivered_token_count": {}, "detail": {}, "duration_ms": {}, "endpoint": {}, "endpoint_family": {}, "ended_at": {}, "error_class": {}, "event_class": {}, "event_id": {},
	"event_type": {}, "failed_attempts": {}, "failure_class": {}, "failure_count": {}, "failure_reason_class": {},
	"feature_refs": {}, "handler_id": {}, "hop": {}, "hop_chain": {}, "hop_index": {}, "hop_kind": {}, "id": {}, "input_tokens": {},
	"latency_bucket": {}, "latency_p99_ms": {}, "ledger_id": {}, "manual_override_actor_id": {}, "max_redemptions": {},
	"message_count": {}, "metadata_keys": {}, "model": {}, "model_chain": {}, "model_verdict": {}, "new_state": {}, "occurred_at": {}, "outbox_event_id": {},
	"outcome": {}, "output_tokens": {}, "panic_class": {}, "payload": {}, "policy_version": {}, "pool_group_id": {}, "previous_state": {},
	"priority": {}, "provider": {}, "provider_account_id": {}, "provider_family": {}, "protocol_family": {}, "pubkey_fingerprint": {},
	"ramp_failure_count": {}, "ramp_stage_pct": {}, "rate_limit_hits": {}, "reason": {}, "reason_class": {}, "receipt_id": {}, "receipt_sequence": {},
	"redaction_result": {}, "redemption_id": {}, "refund_microcents": {}, "request_id": {}, "requested": {}, "requested_model": {},
	"route_decided": {}, "route_id": {}, "safe": {}, "schema_version": {}, "score": {}, "severity": {}, "single_use_per_user": {},
	"source_ip_hash": {}, "stack": {}, "started_at": {}, "state": {}, "status": {}, "status_class": {}, "stream": {}, "subject": {}, "substitution_refund_micro_usd": {}, "tenant_id": {},
	"tenant_scope": {}, "tenant_scope_ref": {}, "to": {}, "total_attempts": {}, "trace_id": {}, "type": {}, "unredeemed_capacity": {},
	"upstream_5xx_hits": {}, "upstream_reported": {}, "user_id": {}, "vendor": {}, "verdict": {}, "voucher_redeemed_micro_usd": {}, "window_summary": {}, "ts": {},
}

func sensitiveKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	if k == "" {
		return false
	}
	for _, safe := range []string{
		"account_credential_id", "credential_id", "credential_version", "credential_fingerprint", "credentials_present",
		"token_count", "tokens", "cache_read_tokens", "cache_write_tokens", "body_envelope",
		"source_ip_hash", "pubkey_fingerprint", "refresh_token_fingerprint", "payload_fingerprint",
	} {
		if strings.Contains(k, safe) {
			return false
		}
	}
	for _, marker := range []string{
		"access_token", "refresh_token", "id_token", "bearer", "cookie", "password",
		"secret", "authorization", "api_key", "apikey", "credential_bytes", "credentials",
		"prompt", "completion", "raw_body", "raw_request", "raw_response", "upstream_body",
		"tool_input", "tool_output", "tool_result", "html_body", "message_content",
	} {
		if strings.Contains(k, marker) {
			return true
		}
	}
	if k == "body" || k == "content" || strings.HasSuffix(k, "_content") || k == "message" || k == "details" || k == "evidence" {
		return true
	}
	return k == "token" || strings.HasSuffix(k, "_token")
}

func containsForbiddenRawData(raw []byte) bool {
	var v any
	if err := json.Unmarshal(raw, &v); err == nil {
		return containsForbiddenValue(v)
	}
	return containsForbiddenString(string(raw))
}

func containsForbiddenValue(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		for k, child := range v {
			if sensitiveKey(k) || containsForbiddenValue(child) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if containsForbiddenValue(child) {
				return true
			}
		}
	case string:
		return containsForbiddenString(v)
	}
	return false
}

func containsForbiddenString(value string) bool {
	text := strings.ToLower(value)
	for _, marker := range []string{
		"sk-", "toolu_", "aiv_", "gho_", "ant-", "bearer ", "authorization:",
		"access_token", "refresh_token", "id_token", "cookie=", "cookie:",
		"credential", "raw user prompt", "prompt sentinel", "prompt_sentinel",
		"completion sentinel", "completion_sentinel", "password", "secret=",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}
