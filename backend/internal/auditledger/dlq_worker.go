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
		if rec.IdempotencyKey != "audit_ledger:"+entry.RequestID {
			return fmt.Errorf("auditledger: dlq idempotency/request_id mismatch: key=%q request_id=%q", rec.IdempotencyKey, entry.RequestID)
		}
		if entry.TenantScopeRef != "" && entry.TenantScopeRef != TenantScopeRef(entry.TenantID) {
			return fmt.Errorf("auditledger: dlq tenant_scope_ref mismatch: payload=%q expected=%q", entry.TenantScopeRef, TenantScopeRef(entry.TenantID))
		}
		entry.TenantScopeRef = ""
		prepared, err := PrepareEntry(ctx, entry)
		if err != nil {
			return err
		}

		existing, err := auditLedger.GetByRequestID(ctx, prepared.requestID)
		if err == nil || errors.Is(err, ErrLedgerEntryCorrupt) {
			if err := ensureDLQDuplicateBelongsToRecordTenant(existing, rec); err != nil {
				return err
			}
			return nil
		}
		if !errors.Is(err, ErrLedgerEntryNotFound) {
			return err
		}

		if _, err := auditLedger.Append(ctx, prepared); err != nil {
			if errors.Is(err, ErrDuplicateRequestID) {
				existing, lookupErr := auditLedger.GetByRequestID(ctx, prepared.requestID)
				if lookupErr != nil && !errors.Is(lookupErr, ErrLedgerEntryCorrupt) {
					return lookupErr
				}
				if err := ensureDLQDuplicateBelongsToRecordTenant(existing, rec); err != nil {
					return err
				}
				return nil
			}
			return err
		}
		return nil
	}
}

func ensureDLQDuplicateBelongsToRecordTenant(existing LedgerEntry, rec dlq.Record) error {
	if existing.TenantID != rec.TenantID {
		return fmt.Errorf("auditledger: duplicate request_id tenant mismatch: request_id=%q record_tenant=%d existing_tenant=%d", existing.RequestID, rec.TenantID, existing.TenantID)
	}
	return nil
}
