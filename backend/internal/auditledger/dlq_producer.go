package auditledger

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
)

type DLQEnqueuer interface {
	Enqueue(context.Context, dlq.Event) (int64, error)
}

func EnqueuePreparedEntryToDLQ(ctx context.Context, enqueuer DLQEnqueuer, prepared PreparedEntry, cause error) (string, error) {
	if enqueuer == nil {
		return "", dlq.ErrStoreNotConfigured
	}
	entry := prepared.AsLedgerEntry()
	payload, err := json.Marshal(prepared)
	if err != nil {
		return "", fmt.Errorf("auditledger: marshal prepared entry for dlq: %w", err)
	}
	reason := "audit ledger append failed"
	if cause != nil {
		reason = reason + ": " + cause.Error()
	}
	id, err := enqueuer.Enqueue(ctx, dlq.Event{
		EventKind:      dlq.EventKindAuditLedgerEntry,
		TenantID:       entry.TenantID,
		IdempotencyKey: "audit_ledger:" + entry.RequestID,
		SourceTable:    "audit_ledger",
		ReplicaStatus:  dlq.ReplicaStatusNone,
		Payload:        payload,
		FailureReason:  reason,
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("audit_ledger_dlq:%d", id), nil
}
