package pool

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/pool/binding"
	"github.com/BloomingProsperity/HUAKAI/internal/pool/dispatcher"
	"github.com/BloomingProsperity/HUAKAI/internal/pool/router"
)

func VendorFromProtocolFamily(pf string) string {
	switch pf {
	case "anthropic_messages":
		return "anthropic"
	case "openai_chat", "openai_responses":
		return "openai"
	case "openai_codex":
		return "codex"
	case "gemini_messages", "gemini_advanced_session":
		return "gemini"
	default:
		return ""
	}
}

type DefaultSelector = router.DefaultSelector
type SelectorOption = router.SelectorOption
type AccountRing = router.AccountRing
type PASRSelector = router.PASRSelector
type PASRSelectorConfig = router.PASRSelectorConfig
type PrefixSegment = router.PrefixSegment
type SegmentTable = router.SegmentTable
type SegmentTableConfig = router.SegmentTableConfig
type PASRAgingWorker = router.PASRAgingWorker
type PASRAgingWorkerConfig = router.PASRAgingWorkerConfig
type PASRCacheFeedback = router.PASRCacheFeedback
type PASRMetricsSnapshot = router.PASRMetricsSnapshot

const (
	PASRSegmentSize         = router.PASRSegmentSize
	DefaultSegmentMaxAge    = router.DefaultSegmentMaxAge
	DefaultExtendedCacheTTL = router.DefaultExtendedCacheTTL
	DefaultSegmentTableCap  = router.DefaultSegmentTableCap
	PASRDemoteThreshold     = router.PASRDemoteThreshold
	DefaultAgingInterval    = router.DefaultAgingInterval
)

func DefaultGateChain() GateChain { return router.DefaultGateChain() }

func NewDefaultSelector(accounts AccountSource, opts ...SelectorOption) *DefaultSelector {
	return router.NewDefaultSelector(accounts, opts...)
}

func WithRoutingPolicySource(v RoutingPolicySource) SelectorOption {
	return router.WithRoutingPolicySource(v)
}

func WithStickyStore(v StickyStore) SelectorOption { return router.WithStickyStore(v) }

func WithGateChain(v GateChain) SelectorOption { return router.WithGateChain(v) }

func WithSlotManager(v SlotManager) SelectorOption { return router.WithSlotManager(v) }

func WithClaimGate(v ClaimGate) SelectorOption { return router.WithClaimGate(v) }

func NewAccountRing(accounts []int64, seed uint64) *AccountRing {
	return router.NewAccountRing(accounts, seed)
}

func BuildAccountRingFromSnapshots(snapshots []*AccountSnapshot, seed uint64) *AccountRing {
	return router.BuildAccountRingFromSnapshots(snapshots, seed)
}

func NewPASRSelector(cfg PASRSelectorConfig) (*PASRSelector, error) {
	return router.NewPASRSelector(cfg)
}

func NewSegmentTable(cfg SegmentTableConfig) *SegmentTable { return router.NewSegmentTable(cfg) }

func NewPASRAgingWorker(cfg PASRAgingWorkerConfig) *PASRAgingWorker {
	return router.NewPASRAgingWorker(cfg)
}

func NewPASRCacheFeedback(segments *SegmentTable, now func() time.Time) *PASRCacheFeedback {
	return router.NewPASRCacheFeedback(segments, now)
}

func RegisterPASRCacheFeedback(segments *SegmentTable) { router.RegisterPASRCacheFeedback(segments) }

func IncFirstPick()                            { router.IncFirstPick() }
func IncFailover()                             { router.IncFailover() }
func IncFullRingFallback()                     { router.IncFullRingFallback() }
func IncSegmentCreates()                       { router.IncSegmentCreates() }
func IncCacheHitObs()                          { router.IncCacheHitObs() }
func IncCacheCreationObs()                     { router.IncCacheCreationObs() }
func IncMissObs()                              { router.IncMissObs() }
func IncDemote()                               { router.IncDemote() }
func AddEvictions(n int64)                     { router.AddEvictions(n) }
func SetSegmentCount(n int64)                  { router.SetSegmentCount(n) }
func SnapshotPASRMetrics() PASRMetricsSnapshot { return router.SnapshotPASRMetrics() }

func NewIdempotentRelease(token uuid.UUID, fn ReleaseFunc) ReleaseFunc {
	return router.NewIdempotentRelease(token, fn)
}

// NewRoutingReasonBuilder 重新导出 router 子包构造器，兼容老 importer。
func NewRoutingReasonBuilder(req SelectionRequest) *RoutingReasonBuilder {
	return router.NewRoutingReasonBuilder(req)
}

type AuthCredentialGate = binding.AuthCredentialGate
type DBClaimGate = binding.DBClaimGate
type DBStickyStore = binding.DBStickyStore

func NewAuthCredentialGate(provider auth.TokenProvider) AuthCredentialGate {
	return AuthCredentialGate{Provider: provider}
}

func NewDBClaimGate(q *dbbilling.Queries) *DBClaimGate { return binding.NewDBClaimGate(q) }

func NewDBStickyStore(repo binding.StickyBindingRepo) *DBStickyStore {
	return binding.NewDBStickyStore(repo)
}

func NewDBStickyStoreReadOnly(repo binding.StickyBindingReader) *DBStickyStore {
	return binding.NewDBStickyStoreReadOnly(repo)
}

type DBAccountSource = dispatcher.DBAccountSource
type DBSlotManager = dispatcher.DBSlotManager
type DBRepository = dispatcher.DBRepository
type AccountRepository = dispatcher.AccountRepository
type SlotAcquisitionRepository = dispatcher.SlotAcquisitionRepository
type StickyBindingRepository = dispatcher.StickyBindingRepository
type AuditRepository = dispatcher.AuditRepository
type SelectorDispatcher = dispatcher.SelectorDispatcher
type SelectorDispatcherConfig = dispatcher.SelectorDispatcherConfig
type PASRDispatchSnapshot = dispatcher.PASRDispatchSnapshot

const (
	DefaultLeaseDuration    = dispatcher.DefaultLeaseDuration
	DispatchModeDefault     = dispatcher.DispatchModeDefault
	DispatchModeShadow      = dispatcher.DispatchModeShadow
	DispatchModeCanary      = dispatcher.DispatchModeCanary
	DispatchModePASRPrimary = dispatcher.DispatchModePASRPrimary
	DispatchModePASRStrict  = dispatcher.DispatchModePASRStrict
)

var PASRDispatchVendors = dispatcher.PASRDispatchVendors

func NewDBAccountSource(q *dbbilling.Queries) *DBAccountSource {
	return dispatcher.NewDBAccountSource(q)
}

func NewDBSlotManager(pool *pgxpool.Pool) *DBSlotManager {
	return dispatcher.NewDBSlotManager(pool)
}

func NewDBRepository(q *dbbilling.Queries) *DBRepository { return dispatcher.NewDBRepository(q) }

func NewSelectorDispatcher(cfg SelectorDispatcherConfig) (*SelectorDispatcher, error) {
	return dispatcher.NewSelectorDispatcher(cfg)
}

func IncDispatchVendor(metricKey, vendor string) { dispatcher.IncDispatchVendor(metricKey, vendor) }
func IncDispatchShadowSampled()                  { dispatcher.IncDispatchShadowSampled() }
func IncDispatchShadowMatch()                    { dispatcher.IncDispatchShadowMatch() }
func IncDispatchShadowDiff()                     { dispatcher.IncDispatchShadowDiff() }
func IncDispatchShadowDrop()                     { dispatcher.IncDispatchShadowDrop() }
func IncDispatchShadowPanic()                    { dispatcher.IncDispatchShadowPanic() }
func IncDispatchShadowTimeout()                  { dispatcher.IncDispatchShadowTimeout() }
func IncDispatchShadowPASRErr()                  { dispatcher.IncDispatchShadowPASRErr() }
func IncDispatchCanaryPASRUsed()                 { dispatcher.IncDispatchCanaryPASRUsed() }
func IncDispatchCanaryDefaultUsed()              { dispatcher.IncDispatchCanaryDefaultUsed() }
func IncDispatchCanaryPreMutationFallback()      { dispatcher.IncDispatchCanaryPreMutationFallback() }
func IncDispatchCanaryPostMutationRelease()      { dispatcher.IncDispatchCanaryPostMutationRelease() }
func IncDispatchMode(mode string)                { dispatcher.IncDispatchMode(mode) }
func IncDispatchVendorMode(mode, vendor string)  { dispatcher.IncDispatchVendorMode(mode, vendor) }
func SnapshotPASRDispatchVendor(vendor string) map[string]int64 {
	return dispatcher.SnapshotPASRDispatchVendor(vendor)
}
func SnapshotPASRDispatchMetrics() PASRDispatchSnapshot {
	return dispatcher.SnapshotPASRDispatchMetrics()
}

var _ Selector = (*DefaultSelector)(nil)
var _ Selector = (*PASRSelector)(nil)
var _ Selector = (*SelectorDispatcher)(nil)
