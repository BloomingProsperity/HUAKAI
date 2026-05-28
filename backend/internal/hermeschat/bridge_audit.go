package hermeschat

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbhermes "github.com/BloomingProsperity/HUAKAI/internal/db/hermes"
	legacydlq "github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
)

type auditDLQ interface {
	Enqueue(context.Context, legacydlq.Event) (int64, error)
}

func (b *Bridge) recordMessageAudit(ctx context.Context, store hermes.Store, prepared PreparedRequest, conversationID int64, now time.Time) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("hermes audit panic: %v", recovered)
		}
	}()
	args := hermes.SanitizeArgs(map[string]any{
		"conversation_id": conversationID,
		"message_role":    "assistant",
	})
	raw, err := json.Marshal(args)
	if err != nil {
		return err
	}
	_, err = store.InsertAuditEvent(ctx, dbhermes.InsertAuditEventParams{
		Ts:            pgtype.Timestamptz{Time: now.UTC(), Valid: true},
		TenantID:      prepared.TenantID,
		ActorUserID:   prepared.UserID,
		Action:        hermes.ActionMessageSend,
		SanitizedArgs: raw,
		Result:        hermes.AuditResultSuccess,
		CorrelationID: stringPtr(prepared.CorrelationID),
		RequestID:     stringPtr(prepared.RequestID),
	})
	if err != nil {
		return fmt.Errorf("%w: %w", hermes.ErrAuditRecordFailed, err)
	}
	return nil
}

func (b *Bridge) warnAuditFailure(ctx context.Context, prepared PreparedRequest, conversationID int64, auditErr error) {
	if b.logger != nil {
		b.logger.Warnf("hermes audit message send failed: tenant_id=%d user_id=%d conversation_id=%d request_id=%s err=%v",
			prepared.TenantID, prepared.UserID, conversationID, prepared.RequestID, auditErr)
	}
	if b.auditDLQ == nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"action":          hermes.ActionMessageSend,
		"tenant_id":       prepared.TenantID,
		"actor_user_id":   prepared.UserID,
		"conversation_id": conversationID,
		"message_role":    "assistant",
		"result":          hermes.AuditResultSuccess,
		"correlation_id":  prepared.CorrelationID,
		"request_id":      prepared.RequestID,
	})
	keyParts := []string{
		"hermes.message.send", strconv.FormatInt(prepared.TenantID, 10),
		strconv.FormatInt(conversationID, 10), strings.TrimSpace(prepared.RequestID),
	}
	key := strings.Join(keyParts, ":")
	if strings.HasSuffix(key, ":") {
		key += strconv.FormatInt(b.now().UTC().UnixNano(), 10)
	}
	_, err := b.auditDLQ.Enqueue(ctx, legacydlq.Event{
		TenantID: prepared.TenantID, EventKind: legacydlq.EventKindAuditEventReplica,
		Lane: legacydlq.LaneHigh, Payload: payload, FailureReason: auditErr.Error(),
		IdempotencyKey: key, SourceTable: "hermes_audit_events", SourceID: conversationID,
	})
	if err != nil && b.logger != nil {
		b.logger.Warnf("hermes audit message send dlq enqueue failed: tenant_id=%d conversation_id=%d request_id=%s err=%v",
			prepared.TenantID, conversationID, prepared.RequestID, err)
	}
}

func stringPtr(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	v := value
	return &v
}
