package redact

import (
	"sort"
	"strings"
)

// Audience 表示信任链查询结果的可见受众。
type Audience string

const (
	AudiencePublic         Audience = "public"
	AudienceTenantOperator Audience = "tenant_operator"
	AudiencePlatformAdmin  Audience = "platform_admin"
	AudienceInternal       Audience = "internal"
)

var publicAudienceFields = fieldSet(
	"request_id",
	"model_requested",
	"model_actual",
	"token_count_total",
	"status_code",
	"signature",
	"merkle_root",
	"pubkey_fp",
)

var tenantOperatorAudienceFields = mergeFieldSets(publicAudienceFields, fieldSet(
	"tenant_id",
	"route_id",
	"pool_id",
	"cache_hit_ratio",
	"latency_ms_total",
	"latency_ms_first_token",
	"latency_ms_tta",
	"error_class",
	"hop_chain",
))

var platformAdminAudienceFields = mergeFieldSets(tenantOperatorAudienceFields, fieldSet(
	"account_id_hash",
	"upstream_model_reported",
	"error_code",
	"retry_reason",
	"provider",
	"ingress_path",
	"client_protocol",
	"latency_ms_upstream",
))

var internalAudienceFields = mergeFieldSets(SystemLogSafeFieldsSnapshot(), platformAdminAudienceFields)

// audienceLevel 是每个外发受众的字段级 allowlist。
//
// Internal 仍保留一份可枚举集合，便于层级测试和诊断；RedactForAudience 对
// Internal 不按该集合过滤，只剔除显式禁字段。
var audienceLevel = map[Audience]map[string]struct{}{
	AudiencePublic:         publicAudienceFields,
	AudienceTenantOperator: tenantOperatorAudienceFields,
	AudiencePlatformAdmin:  platformAdminAudienceFields,
	AudienceInternal:       internalAudienceFields,
}

var forbiddenAudienceFields = fieldSet(
	"prompt",
	"completion",
	"messages",
	"content",
	"text",
	"tool_input",
	"tool_output",
	"tool_result",
	"system",
	"instructions",
	"thinking",
	"reasoning_summary",
	"user_email",
	"user_name",
	"api_key",
	"x-api-key",
	"authorization",
	"bearer_token",
	"token",
	"password",
	"secret",
	"request_body",
	"response_body",
)

// FieldsForAudience 返回受众字段 allowlist 的拷贝，避免调用方污染内部集合。
func FieldsForAudience(aud Audience) map[string]struct{} {
	fields, ok := audienceLevel[aud]
	if !ok {
		return map[string]struct{}{}
	}
	return cloneFieldSet(fields)
}

// RedactForAudience 按受众裁剪 entry，并返回新的 map。
//
// public / tenant_operator / platform_admin 使用严格 audience allowlist；
// internal 用于 SRE in-memory 调试，不做 audience 字段裁剪，但仍拒绝内容、
// credential、body 等显式禁字段。
func RedactForAudience(entry map[string]any, aud Audience) map[string]any {
	if entry == nil {
		return nil
	}
	out := make(map[string]any, len(entry))
	for k, v := range entry {
		if isForbiddenAudienceField(k) {
			continue
		}
		if aud == AudienceInternal {
			out[k] = v
			continue
		}
		fields, ok := audienceLevel[aud]
		if !ok {
			continue
		}
		if _, allowed := fields[k]; !allowed {
			continue
		}
		if aud == AudienceTenantOperator && k == "hop_chain" {
			clean, keep := redactTenantHopChain(v)
			if !keep {
				continue
			}
			out[k] = clean
			continue
		}
		out[k] = v
	}
	return out
}

// DroppedFieldsForAudience 返回按受众过滤后丢弃的顶层字段名，按字典序排序。
func DroppedFieldsForAudience(entry map[string]any, aud Audience) []string {
	if entry == nil {
		return nil
	}
	redacted := RedactForAudience(entry, aud)
	dropped := make([]string, 0, len(entry)-len(redacted))
	for k := range entry {
		if _, ok := redacted[k]; !ok {
			dropped = append(dropped, k)
		}
	}
	sort.Strings(dropped)
	return dropped
}

func fieldSet(names ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, name := range names {
		out[name] = struct{}{}
	}
	return out
}

func mergeFieldSets(sets ...map[string]struct{}) map[string]struct{} {
	out := map[string]struct{}{}
	for _, set := range sets {
		for name := range set {
			out[name] = struct{}{}
		}
	}
	return out
}

func cloneFieldSet(in map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for name := range in {
		out[name] = struct{}{}
	}
	return out
}

func isForbiddenAudienceField(name string) bool {
	_, ok := forbiddenAudienceFields[strings.ToLower(name)]
	return ok
}

func redactTenantHopChain(value any) (any, bool) {
	switch hops := value.(type) {
	case []map[string]any:
		out := make([]map[string]any, 0, len(hops))
		for _, hop := range hops {
			out = append(out, publicHopFields(hop))
		}
		return out, true
	case []any:
		out := make([]any, 0, len(hops))
		for _, hop := range hops {
			clean, ok := redactTenantHopChain(hop)
			if !ok {
				return nil, false
			}
			out = append(out, clean)
		}
		return out, true
	case map[string]any:
		return publicHopFields(hops), true
	default:
		return nil, false
	}
}

func publicHopFields(hop map[string]any) map[string]any {
	out := map[string]any{}
	for _, name := range []string{"hop", "hop_name", "name", "ts", "timestamp"} {
		if value, ok := hop[name]; ok {
			out[name] = value
		}
	}
	return out
}
