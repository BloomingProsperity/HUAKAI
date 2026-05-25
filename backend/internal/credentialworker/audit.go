package credentialworker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	dbauth "github.com/BloomingProsperity/HUAKAI/internal/db/auth"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// ErrAuditWriterMissing 表示 production 缺审计 writer (queries nil) — 不再
// silent return nil 让审计字段悄悄丢 (RR-W5-002 修复)。
var ErrAuditWriterMissing = errors.New("credentialworker: audit writer queries unset; production must wire dbauth.Queries")

// recordAudit:
//   - 优先走同事务路径 (s.txPool + s.auditSigner + dbauth queries 全配齐 →
//     pgx.BeginFunc + dbauth.New(tx).InsertOAuthRefreshAuditEvent +
//     auditledger.AppendInTransaction);任一步失败 BeginFunc 自动 rollback,
//     audit row 与 ledger 行同生死,RR-W5-002 D1/D4 fail-closed。
//   - 缺一时回退老 2-step 路径 (auditWriter + AuditLedger.Append),仅留
//     dev/test 用 — production wiring.go 必须 gate 全装 (RR-W5-002 步骤 3)。
func (s *Scheduler) recordAudit(ctx context.Context, account dbbilling.ListAccountsForRefreshRow, outcome auth.Outcome, scope string, cause error) error {
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
	prepared, prepareErr := auditledger.PrepareEntry(ctx, auditledger.LedgerEntry{
		LedgerID:  fmt.Sprintf("ledger-%s", requestID),
		Timestamp: now.Format(time.RFC3339Nano),
		RequestID: requestID,
		TenantID:  account.TenantID,
	})
	healthChange, hasHealthChange := s.providerAccountHealthChange(account.ID, account.TenantID, outcome, now)

	// 同事务路径:txPool + auditSigner + auditQueries 全配齐才走。
	if s.txPool != nil && s.auditSigner != nil && s.auditQueries != nil {
		if prepareErr != nil {
			return fmt.Errorf("credentialworker: prepare ledger entry: %w", prepareErr)
		}
		params := refreshAuditParams(entry)
		err := pgx.BeginFunc(ctx, s.txPool, func(tx pgx.Tx) error {
			if hasHealthChange {
				if err := updateProviderAccountHealth(ctx, tx, healthChange); err != nil {
					return err
				}
			}
			if err := dbauth.New(tx).InsertOAuthRefreshAuditEvent(ctx, params); err != nil {
				return fmt.Errorf("audit insert: %w", err)
			}
			if _, err := auditledger.AppendInTransaction(ctx, tx, s.auditSigner, prepared); err != nil {
				return fmt.Errorf("ledger append: %w", err)
			}
			return nil
		})
		if err == nil && hasHealthChange {
			s.maybeLogProviderAccountHealthAlert(ctx, healthChange, outcome)
		}
		return err
	}

	// Legacy 2-step path (dev/test).production wiring 必须把 tx 三件套装上。
	var healthErr error
	if hasHealthChange && s.healthStore != nil {
		healthErr = s.healthStore.UpdateProviderAccountHealth(ctx, healthChange)
	}
	auditErr := s.auditWriter.WriteRefreshAudit(ctx, entry)
	var ledgerErr error
	if prepareErr != nil {
		ledgerErr = prepareErr
	} else {
		_, ledgerErr = s.AuditLedger.Append(ctx, prepared)
	}
	err := errors.Join(healthErr, auditErr, ledgerErr)
	if err == nil && hasHealthChange {
		s.maybeLogProviderAccountHealthAlert(ctx, healthChange, outcome)
	}
	return err
}

func (s *Scheduler) recordAuditString(ctx context.Context, account dbbilling.ListAccountsForRefreshRow, outcome string, scope string, cause error) error {
	return s.recordAudit(ctx, account, auth.RefreshAuditOutcome(outcome), scope, cause)
}

// refreshAuditParams 把 auth.RefreshAuditEntry 转 sqlc InsertOAuthRefreshAuditEventParams,
// 同事务/legacy 路径共用。
func refreshAuditParams(entry *auth.RefreshAuditEntry) dbauth.InsertOAuthRefreshAuditEventParams {
	return dbauth.InsertOAuthRefreshAuditEventParams{
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
	}
}

type dbAuditWriter struct {
	queries *dbauth.Queries
}

// WriteRefreshAudit:queries nil 不再 silent return nil — RR-W5-002 步骤 2,
// production 误用 nil writer 时必须显式失败,防 audit fail-closed 静默丢字段。
func (w dbAuditWriter) WriteRefreshAudit(ctx context.Context, entry *auth.RefreshAuditEntry) error {
	if w.queries == nil {
		return ErrAuditWriterMissing
	}
	if entry == nil {
		return nil
	}
	return w.queries.InsertOAuthRefreshAuditEvent(ctx, refreshAuditParams(entry))
}

func stringPtr(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
