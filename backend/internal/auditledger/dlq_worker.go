package auditledger

import (
	"context"
	"errors"
	"fmt"

	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
)

func NewDLQHandler(auditLedger Ledger) dlq.Handler {
	return func(ctx context.Context, rec dlq.Record) error {
		if auditLedger == nil {
			return dlq.ErrStoreNotConfigured
		}
		entry, err := decodeLedgerEntryFromDLQPayload(rec.Payload)
		if err != nil {
			return fmt.Errorf("auditledger: decode dlq payload: %w", err)
		}
		if rec.TenantID != entry.TenantID {
			return fmt.Errorf("auditledger: dlq tenant mismatch: record=%d payload=%d", rec.TenantID, entry.TenantID)
		}
		if entry.TenantScopeRef != "" && entry.TenantScopeRef != TenantScopeRef(entry.TenantID) {
			return fmt.Errorf("auditledger: dlq tenant_scope_ref mismatch: payload=%q expected=%q", entry.TenantScopeRef, TenantScopeRef(entry.TenantID))
		}
		if rec.IdempotencyKey != "audit_ledger:"+entry.RequestID {
			return fmt.Errorf("auditledger: dlq idempotency/request_id mismatch: key=%q request_id=%q", rec.IdempotencyKey, entry.RequestID)
		}
		entry.TenantScopeRef = ""
		prepared, err := PrepareEntry(ctx, entry)
		if err != nil {
			return err
		}

		if _, err := auditLedger.GetByRequestID(ctx, prepared.requestID); err == nil || errors.Is(err, ErrLedgerEntryCorrupt) {
			return nil
		} else if !errors.Is(err, ErrLedgerEntryNotFound) {
			return err
		}

		if _, err := auditLedger.Append(ctx, prepared); err != nil {
			if errors.Is(err, ErrDuplicateRequestID) {
				return nil
			}
			return err
		}
		return nil
	}
}
