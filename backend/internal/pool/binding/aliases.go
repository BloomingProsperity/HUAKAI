package binding

import "github.com/BloomingProsperity/HUAKAI/internal/pool/router"

type AccountSnapshot = router.AccountSnapshot
type SelectionRequest = router.SelectionRequest
type GateFailureReason = router.GateFailureReason
type StickyStore = router.StickyStore
type ClaimGate = router.ClaimGate

const GateFailureCredential = router.GateFailureCredential

var ErrClaimRace = router.ErrClaimRace
