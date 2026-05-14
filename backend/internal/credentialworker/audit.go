package credentialworker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func (s *Scheduler) recordAudit(ctx context.Context, account db.ListAccountsForRefreshRow, outcome auth.Outcome, scope string, cause error) error {
	now := s.now().UTC()
	requestID := fmt.Sprintf("cred-refresh-%d-%d-%d-%s", account.ID, now.UnixNano(), s.seq.Add(1), outcome)
	entry := &auth.RefreshAuditEntry{
		TenantID:          account.TenantID,
		ProviderAccountID: account.ID,
		Outcome:           outcome,
		StormScope:        scope,
		RequestID:         requestID,
		OccurredAt:        now,
	}
	if cause != nil {
		entry.ErrorClass = fmt.Sprintf("%T", cause)
		entry.ErrorMessageRedacted = auth.SanitizeOAuthMessage(cause.Error())
	}
	auditErr := s.auditWriter.WriteRefreshAudit(ctx, entry)
	_, ledgerErr := s.AuditLedger.Append(ctx, auditledger.LedgerEntry{
		LedgerID:  fmt.Sprintf("ledger-%s", requestID),
		Timestamp: now.Format(time.RFC3339Nano),
		RequestID: requestID,
		TenantID:  account.TenantID,
	})
	return errors.Join(auditErr, ledgerErr)
}

type dbAuditWriter struct {
	queries *db.Queries
}

func (w dbAuditWriter) WriteRefreshAudit(ctx context.Context, entry *auth.RefreshAuditEntry) error {
	if w.queries == nil || entry == nil {
		return nil
	}
	return w.queries.InsertOAuthRefreshAuditEvent(ctx, db.InsertOAuthRefreshAuditEventParams{
		TenantID:                 entry.TenantID,
		ProviderAccountID:        entry.ProviderAccountID,
		Outcome:                  string(entry.Outcome),
		StormScope:               stringPtr(entry.StormScope),
		OldTokenFingerprint:      stringPtr(entry.OldRefreshTokenFingerprint),
		NewTokenFingerprint:      stringPtr(entry.NewRefreshTokenFingerprint),
		MimicryComponentsApplied: entry.ComponentsApplied,
		RequestID:                stringPtr(entry.RequestID),
		ClientProtocol:           stringPtr(entry.ClientProtocol),
		Model:                    stringPtr(entry.Model),
		ErrorClass:               stringPtr(entry.ErrorClass),
		ErrorMessageRedacted:     stringPtr(entry.ErrorMessageRedacted),
		OccurredAt:               entry.OccurredAt,
	})
}

func stringPtr(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
