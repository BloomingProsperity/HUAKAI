package notify

import (
	"context"
	"encoding/json"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
)

const DefaultDeliveryTimeout = 5 * time.Second

type asyncRunner func(func())

type settlerConfig struct {
	async       asyncRunner
	timeout     time.Duration
	recordError func(error)
}

type SettlerOption func(*settlerConfig)

func WithSettlerAsync(async func(func())) SettlerOption {
	return func(cfg *settlerConfig) {
		if async != nil {
			cfg.async = async
		}
	}
}

func WithSettlerTimeout(timeout time.Duration) SettlerOption {
	return func(cfg *settlerConfig) {
		if timeout > 0 {
			cfg.timeout = timeout
		}
	}
}

func WithSettlerDeliveryErrorRecorder(record func(error)) SettlerOption {
	return func(cfg *settlerConfig) {
		cfg.recordError = record
	}
}

type Settler struct {
	next     billing.Settler
	notifier *Notifier
	cfg      settlerConfig
}

func NewSettler(next billing.Settler, notifier *Notifier, opts ...SettlerOption) *Settler {
	cfg := settlerConfig{
		async:   func(fn func()) { go fn() },
		timeout: DefaultDeliveryTimeout,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return &Settler{next: next, notifier: notifier, cfg: cfg}
}

func (s *Settler) Settle(ctx context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
	res, err := s.next.Settle(ctx, req)
	if err != nil || res == nil || s.notifier == nil {
		return res, err
	}
	tenantID := firstPositive(res.TenantID, req.TenantID)
	userID := firstPositive(res.UserID, req.UserID)
	if tenantID <= 0 || userID <= 0 {
		return res, nil
	}
	balance := res.NewUserBalance
	billingEventID := res.BillingEventID
	s.cfg.async(func() {
		notifyCtx, cancel := context.WithTimeout(context.Background(), s.cfg.timeout)
		defer cancel()
		if notifyErr := s.notifier.NotifyLowBalance(notifyCtx, tenantID, userID, balance, billingEventID); notifyErr != nil && s.cfg.recordError != nil {
			s.cfg.recordError(notifyErr)
		}
	})
	return res, nil
}

func (s *Settler) Abort(ctx context.Context, tenantID, claimID int64, reason, auditRequestID string, observedInputTokens int64, protocolLoss json.RawMessage) error {
	return s.next.Abort(ctx, tenantID, claimID, reason, auditRequestID, observedInputTokens, protocolLoss)
}

func (s *Settler) CommitCacheHit(ctx context.Context, req billing.SettleRequest) error {
	return s.next.CommitCacheHit(ctx, req)
}

func (s *Settler) Refund(ctx context.Context, req billing.RefundRequest) (*billing.RefundResult, error) {
	return s.next.Refund(ctx, req)
}

func firstPositive(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

var _ billing.Settler = (*Settler)(nil)
