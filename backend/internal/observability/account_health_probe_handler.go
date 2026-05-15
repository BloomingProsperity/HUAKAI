package observability

import (
	"context"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
)

type AccountHealthSignal struct {
	EventID   string
	TenantID  int64
	AccountID int64
	ClaimID   int64
	Model     string
	At        time.Time
}

type AccountHealthProbeHandler struct {
	timeout time.Duration
	probe   func(context.Context, AccountHealthSignal) error
}

func NewAccountHealthProbeHandler(timeout time.Duration, probe func(context.Context, AccountHealthSignal) error) *AccountHealthProbeHandler {
	return &AccountHealthProbeHandler{timeout: timeout, probe: probe}
}

func (h *AccountHealthProbeHandler) ID() eventbus.HandlerID {
	return eventbus.HandlerAccountHealthProbe
}

func (h *AccountHealthProbeHandler) Tier() eventbus.Tier {
	return eventbus.TierMed
}

func (h *AccountHealthProbeHandler) Order() int {
	return 40
}

func (h *AccountHealthProbeHandler) Critical() bool {
	return false
}

func (h *AccountHealthProbeHandler) Timeout() time.Duration {
	return h.timeout
}

func (h *AccountHealthProbeHandler) DLQKind() dlq.EventKind {
	return dlq.EventKindAccountHealth
}

func (h *AccountHealthProbeHandler) Handle(ctx context.Context, event eventbus.RequestCompletionEvent) error {
	if h == nil || h.probe == nil {
		return nil
	}
	return h.probe(ctx, AccountHealthSignal{
		EventID:   event.ID,
		TenantID:  event.TenantID,
		AccountID: event.AccountID,
		ClaimID:   event.ClaimID,
		Model:     event.RequestedModel,
		At:        time.Now().UTC(),
	})
}
