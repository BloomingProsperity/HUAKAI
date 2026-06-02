package dispatcher

import (
	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/pool/router"
)

type AccountSnapshot = router.AccountSnapshot
type ModelRateLimit = router.ModelRateLimit
type AccountSource = router.AccountSource
type SelectionRequest = router.SelectionRequest
type SelectionResult = router.SelectionResult
type Selector = router.Selector
type SlotManager = router.SlotManager
type AcquireResult = router.AcquireResult
type ReleaseFunc = router.ReleaseFunc

var (
	ErrNoEligibleAccount      = router.ErrNoEligibleAccount
	ErrNoSlotAvailable        = router.ErrNoSlotAvailable
	ErrSlotManagerUnavailable = router.ErrSlotManagerUnavailable
	ErrPASRPreMutationFail    = router.ErrPASRPreMutationFail
	ErrPASRPostMutationFail   = router.ErrPASRPostMutationFail
)

func NewIdempotentRelease(token uuid.UUID, fn ReleaseFunc) ReleaseFunc {
	return router.NewIdempotentRelease(token, fn)
}
