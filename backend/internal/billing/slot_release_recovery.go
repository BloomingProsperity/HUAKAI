package billing

import (
	"context"
	"errors"
	"expvar"
	"fmt"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	slotReleaseRecoveryMetricsName    = "billing_slot_release"
	slotReleaseOutcomeAlreadyReleased = "slot_already_released"
	slotReleasedStatusPrefix          = "released_"
	slotReleaseStatusExpired          = "expired"
	slotReleaseStatusOrphanSwept      = "orphan_swept"
)

type slotReleaseOperation string

const (
	slotReleaseSettle slotReleaseOperation = "settle"
	slotReleaseAbort  slotReleaseOperation = "abort"
)

type poolSlotStatusQuerier interface {
	GetPoolSlotStatusByToken(context.Context, uuid.UUID) (string, error)
}

var (
	slotReleaseRecoveryMetricsOnce sync.Once
	slotReleaseRecoveryMetrics     *expvar.Map
)

// verifyAlreadyReleasedSlot 只接受持久化终态作为释放未命中的幂等证明，避免
// 回收方已递减 in_flight 后由终结事务再次递减。
func verifyAlreadyReleasedSlot(ctx context.Context, q poolSlotStatusQuerier, token uuid.UUID, operation slotReleaseOperation) error {
	status, err := q.GetPoolSlotStatusByToken(ctx, token)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSlotReleaseMissed
		}
		return fmt.Errorf("billing: get pool slot status after release miss: %w", err)
	}
	if !isReleasedPoolSlotStatus(status) {
		return ErrSlotReleaseMissed
	}
	observeAlreadyReleasedSlot(operation)
	return nil
}

func isReleasedPoolSlotStatus(status string) bool {
	if status == slotReleaseStatusExpired || status == slotReleaseStatusOrphanSwept {
		return true
	}
	return len(status) > len(slotReleasedStatusPrefix) && strings.HasPrefix(status, slotReleasedStatusPrefix)
}

func observeAlreadyReleasedSlot(operation slotReleaseOperation) {
	if operation != slotReleaseSettle && operation != slotReleaseAbort {
		return
	}
	slotReleaseRecoveryMetricsOnce.Do(func() {
		if existing := expvar.Get(slotReleaseRecoveryMetricsName); existing != nil {
			slotReleaseRecoveryMetrics, _ = existing.(*expvar.Map)
			return
		}
		slotReleaseRecoveryMetrics = expvar.NewMap(slotReleaseRecoveryMetricsName)
	})
	if slotReleaseRecoveryMetrics == nil {
		return
	}
	key := "operation=" + string(operation) + "|outcome=" + slotReleaseOutcomeAlreadyReleased
	slotReleaseRecoveryMetrics.Add(key, 1)
}
