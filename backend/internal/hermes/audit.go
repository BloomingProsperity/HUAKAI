package hermes

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbhermes "github.com/BloomingProsperity/HUAKAI/internal/db/hermes"
)

const (
	ActionEnable             = "hermes.enable"
	ActionDisable            = "hermes.disable"
	ActionProfileCreate      = "hermes.profile.create"
	ActionProfileRotate      = "hermes.profile.rotate"
	ActionProfileDelete      = "hermes.profile.delete"
	ActionChatStart          = "hermes.chat.start"
	ActionMessageSend        = "hermes.message.send"
	ActionConversationDelete = "hermes.conversation.delete"

	// 只读诊断工具统一使用 hermes.tool.<name> 动作写入 Hermes 日志。
	ActionToolCredentialDiagnose    = "hermes.tool.credential_diagnose"
	ActionToolAccountHealthDiagnose = "hermes.tool.account_health_diagnose"
	ActionToolRequestDiagnose       = "hermes.tool.request_diagnose"
	ActionToolDLQInspect            = "hermes.tool.dlq_inspect"
	ActionToolAuditLookup           = "hermes.tool.audit_lookup"
	ActionToolLogAnalyze            = "hermes.tool.log_analyze"
)

func (s *Service) RecordAudit(ctx context.Context, fields AuditFields) error {
	if s == nil || s.store == nil {
		return ErrMisconfigured
	}
	return recordAuditWithStore(ctx, s.store, fields)
}

func recordAuditWithStore(ctx context.Context, store Store, fields AuditFields) error {
	if store == nil {
		return ErrMisconfigured
	}
	if err := validateAuditActor(fields); err != nil {
		return err
	}
	if !validAction(fields.Action) {
		return fmt.Errorf("%w: unknown audit action", ErrInvalidInput)
	}
	if fields.Result != AuditResultSuccess && fields.Result != AuditResultFailure {
		return fmt.Errorf("%w: unknown audit result", ErrInvalidInput)
	}
	args := SanitizeArgs(fields.SanitizedArgs)
	raw, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("%w: audit args must be json encodable", ErrInvalidInput)
	}
	category := strings.TrimSpace(fields.LogCategory)
	if category == "" {
		if fields.Result == AuditResultFailure {
			category = "error"
		} else {
			category = "operation"
		}
	}
	if !validLogCategory(category) {
		return fmt.Errorf("%w: unknown log category", ErrInvalidInput)
	}
	_, err = store.InsertAuditEvent(ctx, dbhermes.InsertAuditEventParams{
		Ts:            pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		TenantID:      fields.TenantID,
		ActorSource:   fields.ActorSource,
		ActorID:       fields.ActorID,
		ActorRole:     fields.ActorRole,
		Action:        fields.Action,
		SanitizedArgs: raw, Result: fields.Result,
		CorrelationID: stringPtr(fields.CorrelationID), RequestID: stringPtr(fields.RequestID),
		LogCategory: category,
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrAuditRecordFailed, err)
	}
	return nil
}

func validateAuditActor(fields AuditFields) error {
	if fields.TenantID <= 0 || fields.ActorID <= 0 {
		return fmt.Errorf("%w: tenant_id and actor_id must be positive", ErrInvalidInput)
	}
	if fields.ActorSource != "token" && fields.ActorSource != "session" {
		return fmt.Errorf("%w: unknown actor source", ErrInvalidInput)
	}
	if fields.ActorRole != "platform_admin" && fields.ActorRole != "tenant_operator" {
		return fmt.Errorf("%w: unknown actor role", ErrInvalidInput)
	}
	return nil
}

func validLogCategory(category string) bool {
	switch category {
	case "operation", "financial", "security", "error", "access", "recovery":
		return true
	default:
		return false
	}
}

func SanitizeArgs(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		if sensitiveKey(k) {
			out[k] = "[REDACTED]"
			continue
		}
		out[k] = sanitizeValue(v)
	}
	return out
}

// sanitizeValue 递归进入某个值,使得嵌套在 collection 任意深处的敏感 key
// 仍能被 redact。常见的 map[string]any / []any 形态直接处理以提升速度;
// 其余所有 map / slice / array 类型(例如
// map[string]int64、[]map[string]any、[N]any)都通过反射遍历,
// 这样 typed collection 里某个敏感 key 下的 secret 也无法绕过 redact 漏出。
// 标量及不支持的类型(chan/func 等)原样返回。
func sanitizeValue(v any) any {
	switch typed := v.(type) {
	case nil:
		return nil
	case map[string]any:
		return SanitizeArgs(typed)
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, sanitizeValue(item))
		}
		return out
	default:
		return sanitizeReflect(v)
	}
}

// sanitizeReflect 通过反射处理任意 map / slice / array 类型。
// 对于 string key 的 map,每个 key 都会用 sensitiveKey 检查(因此例如
// map[string]int64{"api_key": 7} 会 redact 其值);非 string key 的 map
// 仍会递归处理其值。slice 与 array 逐元素递归。其余任何
// 类型(标量、struct、pointer、chan、func)原样返回——
// audit args 是 JSON 形态,实践中不会出现 struct/pointer,
// 原样返回也保持了此前对非 collection 的行为。
func sanitizeReflect(v any) any {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Map:
		out := make(map[string]any, rv.Len())
		stringKeys := rv.Type().Key().Kind() == reflect.String
		iter := rv.MapRange()
		for iter.Next() {
			key := iter.Key()
			keyStr := mapKeyString(key)
			if stringKeys && sensitiveKey(key.String()) {
				out[keyStr] = "[REDACTED]"
				continue
			}
			out[keyStr] = sanitizeValue(iter.Value().Interface())
		}
		return out
	case reflect.Slice, reflect.Array:
		// []byte 是数据,而非 audit 值的 collection;交给
		// JSON encoder 走 base64,而不是把它展开成逐字节的整数。
		if rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() == reflect.Uint8 {
			return v
		}
		out := make([]any, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out = append(out, sanitizeValue(rv.Index(i).Interface()))
		}
		return out
	default:
		return v
	}
}

// mapKeyString 把反射得到的 map key 渲染成字符串,使得 sanitize 后的输出
// 是统一的 map[string]any(反正 JSON object 的 key 本就都是字符串)。
func mapKeyString(key reflect.Value) string {
	if key.Kind() == reflect.String {
		return key.String()
	}
	return fmt.Sprintf("%v", key.Interface())
}

func sensitiveKey(key string) bool {
	k := strings.ToLower(key)
	k = strings.ReplaceAll(k, "-", "_")
	k = strings.ReplaceAll(k, ".", "_")
	noSep := strings.ReplaceAll(k, "_", "")
	return strings.Contains(k, "api_key") ||
		strings.Contains(noSep, "apikey") ||
		strings.Contains(k, "token") ||
		strings.Contains(k, "password") ||
		strings.Contains(k, "secret") ||
		// "credentials"(复数)是 renew_trigger mutating 工具的原始
		// new-credential payload 参数——无论其内部形态如何都整体 redact,
		// 使轮换出来的凭据材料绝不落入 audit row,即便它是以原始字符串
		// 形式提供(此时嵌套 key 的递归也无能为力)。
		// 这里专门匹配复数的 "credentials",从而让单数的、
		// 非密的诊断字段(credential_id / credential_version /
		// credential_state / credential_ok)仍能保留进工具摘要。
		strings.Contains(k, "credentials")
}

func validAction(action string) bool {
	switch action {
	case ActionEnable, ActionDisable, ActionProfileCreate, ActionProfileRotate, ActionProfileDelete, ActionChatStart, ActionMessageSend, ActionConversationDelete,
		ActionToolCredentialDiagnose, ActionToolAccountHealthDiagnose, ActionToolRequestDiagnose,
		ActionToolDLQInspect, ActionToolAuditLookup, ActionToolLogAnalyze:
		return true
	default:
		return false
	}
}

// ToolAuditAction 把 hermesops 工具名映射到它对应的 hermes_audit_events action。
// 对未知工具返回 ("", false),使 handler fail closed,
// 而不是用未在白名单内的 action 写入一条 audit row。
func ToolAuditAction(toolName string) (string, bool) {
	switch toolName {
	case "credential_diagnose":
		return ActionToolCredentialDiagnose, true
	case "account_health_diagnose":
		return ActionToolAccountHealthDiagnose, true
	case "request_diagnose":
		return ActionToolRequestDiagnose, true
	case "dlq_inspect":
		return ActionToolDLQInspect, true
	case "audit_lookup":
		return ActionToolAuditLookup, true
	case "log_analyze":
		return ActionToolLogAnalyze, true
	default:
		return "", false
	}
}

func stringPtr(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	v := value
	return &v
}
