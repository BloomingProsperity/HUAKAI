package audit

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
)

type ReceiptAppender interface {
	AppendReceipt(ctx context.Context, receipt *CostReceipt) error
}

type receiptFormatterService interface {
	DeriveReceipt(ctx context.Context, requestID string) (*CostReceipt, error)
	SignReceipt(ctx context.Context, receipt *CostReceipt) (*CostReceipt, error)
}

type ReceiptHookErrorHandler func(ctx context.Context, requestID string, err error)

type ReceiptHookOption func(*ReceiptHookHandler)

// ReceiptHookHandler 在 Tx2 成功后从既有账务事实派生并写入 receipt snapshot。
type ReceiptHookHandler struct {
	formatter receiptFormatterService
	storage   ReceiptAppender
	onError   ReceiptHookErrorHandler
}

func NewReceiptHookHandler(formatter receiptFormatterService, storage ReceiptAppender, opts ...ReceiptHookOption) *ReceiptHookHandler {
	h := &ReceiptHookHandler{formatter: formatter, storage: storage}
	for _, opt := range opts {
		if opt != nil {
			opt(h)
		}
	}
	return h
}

func WithReceiptHookErrorHandler(fn ReceiptHookErrorHandler) ReceiptHookOption {
	return func(h *ReceiptHookHandler) {
		h.onError = fn
	}
}

func (h *ReceiptHookHandler) AppendSettledReceipt(ctx context.Context, req billing.SettleRequest) error {
	if h == nil {
		return nil
	}
	if h.formatter == nil {
		return ErrReceiptFormatterNil
	}
	if h.storage == nil {
		return ErrReceiptStorageRequired
	}
	requestID := strings.TrimSpace(req.AuditRequestID)
	if requestID == "" {
		return nil
	}
	receipt, err := h.formatter.DeriveReceipt(ctx, requestID)
	if err != nil {
		return fmt.Errorf("audit: derive receipt after settle: %w", err)
	}
	signed, err := h.formatter.SignReceipt(ctx, receipt)
	if err != nil {
		return fmt.Errorf("audit: sign receipt after settle: %w", err)
	}
	if err := h.storage.AppendReceipt(ctx, signed); err != nil {
		if errors.Is(err, ErrReceiptDuplicate) {
			return nil
		}
		return fmt.Errorf("audit: append receipt after settle: %w", err)
	}
	return nil
}

func (h *ReceiptHookHandler) report(ctx context.Context, requestID string, err error) {
	if h == nil || h.onError == nil || err == nil {
		return
	}
	h.onError(ctx, strings.TrimSpace(requestID), err)
}

// ReceiptHookSettler 包装真实 settler；账务成功后 best-effort 写 receipt。
type ReceiptHookSettler struct {
	inner billing.Settler
	hook  *ReceiptHookHandler
}

func NewReceiptHookSettler(inner billing.Settler, hook *ReceiptHookHandler) billing.Settler {
	if hook == nil {
		return inner
	}
	return &ReceiptHookSettler{inner: inner, hook: hook}
}

func (s *ReceiptHookSettler) Settle(ctx context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
	if s == nil || s.inner == nil {
		return nil, billing.ErrPoolNotConfigured
	}
	res, err := s.inner.Settle(ctx, req)
	if err != nil {
		return res, err
	}
	if s.hook != nil {
		if hookErr := s.hook.AppendSettledReceipt(ctx, req); hookErr != nil {
			s.hook.report(ctx, req.AuditRequestID, hookErr)
		}
	}
	return res, nil
}

func (s *ReceiptHookSettler) Abort(ctx context.Context, tenantID, claimID int64, reason, auditRequestID string) error {
	if s == nil || s.inner == nil {
		return billing.ErrPoolNotConfigured
	}
	return s.inner.Abort(ctx, tenantID, claimID, reason, auditRequestID)
}

func (s *ReceiptHookSettler) CommitCacheHit(ctx context.Context, req billing.SettleRequest) error {
	if s == nil || s.inner == nil {
		return billing.ErrPoolNotConfigured
	}
	if err := s.inner.CommitCacheHit(ctx, req); err != nil {
		return err
	}
	// L2 cache 命中走 CommitCacheHit 而非 Settle; CommitCacheHit 现在写了
	// provider-less usage_records 行, receipt 源能 join 到 → 补跑 receipt hook
	// 生成 user_cost_receipts, 与正常结算一致。
	if s.hook != nil {
		if hookErr := s.hook.AppendSettledReceipt(ctx, req); hookErr != nil {
			s.hook.report(ctx, req.AuditRequestID, hookErr)
		}
	}
	return nil
}

func (s *ReceiptHookSettler) Refund(ctx context.Context, req billing.RefundRequest) (*billing.RefundResult, error) {
	if s == nil || s.inner == nil {
		return nil, billing.ErrPoolNotConfigured
	}
	return s.inner.Refund(ctx, req)
}
