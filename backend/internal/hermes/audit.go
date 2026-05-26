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
	ActionEnable        = "hermes.enable"
	ActionDisable       = "hermes.disable"
	ActionProfileCreate = "hermes.profile.create"
	ActionProfileRotate = "hermes.profile.rotate"
	ActionChatStart     = "hermes.chat.start"
	ActionMessageSend   = "hermes.message.send"
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
	case ActionEnable, ActionDisable, ActionProfileCreate, ActionProfileRotate, ActionChatStart, ActionMessageSend:
		return true
	default:
		return false
	}
}

func stringPtr(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	v := value
	return &v
}
