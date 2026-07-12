package observability

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementrecovery"
)

var ErrSettlerNotConfigured = errors.New("observability: billing settler not configured")

type BillingPersisterHandler struct {
	settler billing.Settler
	timeout time.Duration
}

func NewBillingPersisterHandler(settler billing.Settler, timeout time.Duration) *BillingPersisterHandler {
	return &BillingPersisterHandler{settler: settler, timeout: timeout}
}

func (h *BillingPersisterHandler) ID() eventbus.HandlerID {
	return eventbus.HandlerBillingPersister
}

func (h *BillingPersisterHandler) Tier() eventbus.Tier {
	return eventbus.TierHigh
}

func (h *BillingPersisterHandler) Order() int {
	return 10
}

func (h *BillingPersisterHandler) Critical() bool {
	return true
}

func (h *BillingPersisterHandler) Timeout() time.Duration {
	return h.timeout
}

// DLQKind 返 post_delivery_settlement — billing persister 失败的 DLQ 行
// 必须被 settlementrecovery worker 拿到重放,而不是被 generic usage_record
// worker 当成普通 usage record 重写。
//
// 配合 DLQPayload 提供可重放 settlementrecovery.Payload 格式 payload。
func (h *BillingPersisterHandler) DLQKind() dlq.EventKind {
	return dlq.EventKindPostDeliverySettlement
}

// DLQPayload 实现 eventbus.CustomDLQPayloadProvider — eventbus 失败时把
// RequestCompletionEvent 转 settlementrecovery.Payload(Source =
// eventbus_billing_handler),encoded JSON 进 DLQ 行 payload 列,供
// settlementrecovery worker 重调 Settler.Settle。
//
// 失败时返 (nil, err)让 eventbus 回退到 default dlqPayload — 保 observability
// 元数据不丢,但 worker 无法重放(行最终会进 quarantine 待人工)。
func (h *BillingPersisterHandler) DLQPayload(event eventbus.RequestCompletionEvent, _ error) ([]byte, error) {
	payload := settlementrecovery.FromCompletionEvent(settlementrecovery.SourceEventbusBillingHandler, event)
	return payload.Encode()
}

func (h *BillingPersisterHandler) Handle(ctx context.Context, event eventbus.RequestCompletionEvent) error {
	if h == nil || h.settler == nil {
		return ErrSettlerNotConfigured
	}
	req := event.SettleRequest
	if req.TenantID == 0 {
		req.TenantID = event.TenantID
	}
	if req.ClaimID == 0 {
		req.ClaimID = event.ClaimID
	}
	if req.AccountID == 0 {
		req.AccountID = event.AccountID
	}
	if req.AuditRequestID == "" {
		req.AuditRequestID = event.RequestID
	}
	_, err := h.settler.Settle(ctx, req)
	if err != nil {
		return fmt.Errorf("observability: billing persister settle: %w", err)
	}
	return nil
}
