package hermes

import (
	"context"
	"encoding/json"
	"fmt"
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

func sanitizeValue(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		return SanitizeArgs(typed)
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, sanitizeValue(item))
		}
		return out
	default:
		return v
	}
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
		strings.Contains(k, "secret")
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
