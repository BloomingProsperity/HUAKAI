package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
)

const settlementLedgerTimeout = 2 * time.Second

// SettlementLedgerSettler 在账务提交后补齐协议共享的六跳日志链。
// 写日志失败不撤销已经交付的响应和已提交账务，而是持久化脱敏恢复意图。
type SettlementLedgerSettler struct {
	inner    billing.Settler
	ledger   auditledger.Ledger
	recovery auditledger.DLQEnqueuer
	onError  ReceiptHookErrorHandler
	now      func() time.Time
}

func NewSettlementLedgerSettler(
	inner billing.Settler,
	ledger auditledger.Ledger,
	recovery auditledger.DLQEnqueuer,
	onError ReceiptHookErrorHandler,
) billing.Settler {
	if inner == nil || ledger == nil || auditledger.IsNoopLedger(ledger) {
		return inner
	}
	return &SettlementLedgerSettler{
		inner:    inner,
		ledger:   ledger,
		recovery: recovery,
		onError:  onError,
		now:      func() time.Time { return time.Now().UTC() },
	}
}

func (s *SettlementLedgerSettler) Settle(ctx context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
	if s == nil || s.inner == nil {
		return nil, billing.ErrPoolNotConfigured
	}
	result, err := s.inner.Settle(ctx, req)
	if err != nil {
		return result, err
	}
	s.ensureEntry(ctx, req)
	return result, nil
}

func (s *SettlementLedgerSettler) CommitCacheHit(ctx context.Context, req billing.SettleRequest) error {
	if s == nil || s.inner == nil {
		return billing.ErrPoolNotConfigured
	}
	if err := s.inner.CommitCacheHit(ctx, req); err != nil {
		return err
	}
	s.ensureEntry(ctx, req)
	return nil
}

func (s *SettlementLedgerSettler) Abort(ctx context.Context, tenantID, claimID int64, reason, auditRequestID string, observedInputTokens int64, protocolLoss json.RawMessage) error {
	if s == nil || s.inner == nil {
		return billing.ErrPoolNotConfigured
	}
	return s.inner.Abort(ctx, tenantID, claimID, reason, auditRequestID, observedInputTokens, protocolLoss)
}

func (s *SettlementLedgerSettler) Refund(ctx context.Context, req billing.RefundRequest) (*billing.RefundResult, error) {
	if s == nil || s.inner == nil {
		return nil, billing.ErrPoolNotConfigured
	}
	return s.inner.Refund(ctx, req)
}

func (s *SettlementLedgerSettler) ensureEntry(ctx context.Context, req billing.SettleRequest) {
	requestID := strings.TrimSpace(req.AuditRequestID)
	if requestID == "" || s == nil || s.ledger == nil {
		return
	}

	existing, err := s.ledger.GetByRequestID(ctx, requestID)
	switch {
	case err == nil:
		if existing.TenantID != req.TenantID {
			s.report(ctx, requestID, fmt.Errorf("audit: existing settlement ledger tenant mismatch: request_id=%q tenant_id=%d existing_tenant_id=%d", requestID, req.TenantID, existing.TenantID))
		}
		return
	case !errors.Is(err, auditledger.ErrLedgerEntryNotFound):
		s.report(ctx, requestID, fmt.Errorf("audit: lookup settlement ledger: %w", err))
		return
	}

	accountID := req.AccountID
	if accountID == 0 {
		accountID = req.ProviderAccountID
	}
	routeID := strings.TrimSpace(req.AuditRouteID)
	if routeID == "" {
		routeID = strings.TrimSpace(req.SnapshotVersion)
	}
	poolID := ""
	if req.AuditPoolGroupID > 0 {
		poolID = strconv.FormatInt(req.AuditPoolGroupID, 10)
	}
	completedAt := s.now()
	hops := gateway.BuildHopChain(gateway.ForwardRequest{
		TenantID:         req.TenantID,
		AccountID:        accountID,
		AcquisitionToken: req.AcquisitionToken,
		RequestID:        requestID,
		RouteID:          routeID,
		PoolID:           poolID,
		Model:            req.UpstreamModel,
		RequestedModel:   req.RequestedModel,
		Provider:         req.Provider,
	}, sanitizedProviderEndpoint(req.AuditProviderEndpoint), req.RequestedAt, completedAt)
	prepared, err := auditledger.PrepareEntry(ctx, auditledger.LedgerEntry{
		Timestamp: completedAt.Format(time.RFC3339Nano),
		RequestID: requestID,
		TenantID:  req.TenantID,
		HopChain:  hops,
	})
	if err != nil {
		s.report(ctx, requestID, fmt.Errorf("audit: prepare settlement ledger: %w", err))
		return
	}
	if _, err = s.ledger.Append(ctx, prepared); err == nil {
		return
	}
	if errors.Is(err, auditledger.ErrDuplicateRequestID) {
		existing, lookupErr := s.ledger.GetByRequestID(ctx, requestID)
		if lookupErr == nil && existing.TenantID == req.TenantID {
			return
		}
		if lookupErr != nil {
			err = fmt.Errorf("audit: duplicate settlement ledger lookup: %w", lookupErr)
		} else {
			err = fmt.Errorf("audit: duplicate settlement ledger tenant mismatch: request_id=%q tenant_id=%d existing_tenant_id=%d", requestID, req.TenantID, existing.TenantID)
		}
	}

	recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(nonNilContext(ctx)), settlementLedgerTimeout)
	defer cancel()
	if _, recoveryErr := auditledger.EnqueuePreparedEntryToDLQ(recoveryCtx, s.recovery, prepared, err); recoveryErr != nil {
		s.report(ctx, requestID, fmt.Errorf("audit: append settlement ledger: %w; enqueue recovery: %v", err, recoveryErr))
		return
	}
	s.report(ctx, requestID, fmt.Errorf("audit: append settlement ledger deferred: %w", err))
}

func sanitizedProviderEndpoint(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "?#") {
		return ""
	}
	return raw
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func (s *SettlementLedgerSettler) report(ctx context.Context, requestID string, err error) {
	if s != nil && s.onError != nil && err != nil {
		s.onError(ctx, requestID, err)
	}
}
