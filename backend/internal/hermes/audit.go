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
	ActionChatStart          = "hermes.chat.start"
	ActionMessageSend        = "hermes.message.send"
	ActionConversationDelete = "hermes.conversation.delete"

	// WAVE H3 read-only diagnostic tool actions (hermes.tool.<name>). The
	// tool-execute handler mirrors each invocation into hermes_audit_events
	// under these. The matching DB CHECK is extended in migration 0145; H4
	// mutating tools add their own actions (validAction + CHECK) when built.
	ActionToolCredentialDiagnose    = "hermes.tool.credential_diagnose"
	ActionToolAccountHealthDiagnose = "hermes.tool.account_health_diagnose"
	ActionToolRequestDiagnose       = "hermes.tool.request_diagnose"
	ActionToolDLQInspect            = "hermes.tool.dlq_inspect"
	ActionToolAuditLookup           = "hermes.tool.audit_lookup"
	ActionToolLogAnalyze            = "hermes.tool.log_analyze"
)

func (s *Service) RecordAudit(ctx context.Context, tenantID, actorUserID int64, action string, sanitizedArgs map[string]any, result, correlationID, requestID string) error {
	if s == nil || s.store == nil {
		return ErrMisconfigured
	}
	return recordAuditWithStore(ctx, s.store, AuditFields{
		TenantID: tenantID, ActorUserID: actorUserID, Action: action,
		SanitizedArgs: sanitizedArgs, Result: result,
		CorrelationID: correlationID, RequestID: requestID,
	})
}

func recordAuditWithStore(ctx context.Context, store Store, fields AuditFields) error {
	if store == nil {
		return ErrMisconfigured
	}
	if err := validateTenantUser(fields.TenantID, fields.ActorUserID); err != nil {
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
	_, err = store.InsertAuditEvent(ctx, dbhermes.InsertAuditEventParams{
		Ts:       pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		TenantID: fields.TenantID, ActorUserID: fields.ActorUserID, Action: fields.Action,
		SanitizedArgs: raw, Result: fields.Result,
		CorrelationID: stringPtr(fields.CorrelationID), RequestID: stringPtr(fields.RequestID),
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrAuditRecordFailed, err)
	}
	return nil
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

// sanitizeValue recurses into a value so a sensitive key nested anywhere inside
// a collection is still redacted. The common map[string]any / []any shapes are
// handled directly for speed; ALL other map / slice / array kinds (e.g.
// map[string]int64, []map[string]any, [N]any) are walked via reflection so a
// secret under a sensitive key in a typed collection cannot slip through
// unredacted. Scalars and unsupported kinds (chan/func/etc.) are returned as-is.
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

// sanitizeReflect handles arbitrary map / slice / array kinds via reflection. A
// map with string keys has each key checked against sensitiveKey (so e.g.
// map[string]int64{"api_key": 7} redacts the value); maps with non-string keys
// still have their values recursed. Slices and arrays recurse per element. Any
// other kind (scalar, struct, pointer, chan, func) is returned unchanged — the
// audit args are JSON-shaped, so structs/pointers do not occur in practice and
// returning them verbatim preserves the prior non-collection behavior.
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
		// []byte is data, not a collection of audit values; leave it for the
		// JSON encoder to base64 rather than exploding it into per-byte ints.
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

// mapKeyString renders a reflected map key as a string so the sanitized output
// is a uniform map[string]any (JSON object keys are always strings anyway).
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
		// "credentials" (PLURAL) is the raw new-credential payload arg of the
		// renew_trigger mutating tool — redact the whole value regardless of its
		// inner shape so rotated material never lands in an audit row, even when
		// supplied as a raw string (where the nested-key recursion can't help).
		// Matched on the plural "credentials" specifically so the SINGULAR,
		// non-secret diagnostic fields (credential_id / credential_version /
		// credential_state / credential_ok) still survive into tool summaries.
		strings.Contains(k, "credentials")
}

func validAction(action string) bool {
	switch action {
	case ActionEnable, ActionDisable, ActionProfileCreate, ActionProfileRotate, ActionChatStart, ActionMessageSend, ActionConversationDelete,
		ActionToolCredentialDiagnose, ActionToolAccountHealthDiagnose, ActionToolRequestDiagnose,
		ActionToolDLQInspect, ActionToolAuditLookup, ActionToolLogAnalyze:
		return true
	default:
		return false
	}
}

// ToolAuditAction maps a hermesops tool name to its hermes_audit_events action.
// Returns ("", false) for an unknown tool so the handler fails closed rather
// than recording an audit row under an unwhitelisted action.
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
