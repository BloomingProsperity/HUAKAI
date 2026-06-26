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
	"github.com/BloomingProsperity/HUAKAI/internal/rate/precheck"
)

// VendorFromProtocolFamily 把 protocol family 归一到 vendor 字面量,供
// (a) selector/dispatcher 按 vendor 切片 metric,(b) gatewayhttp cacheVendor →
// providerForPricing 在选号前命中 rate table 的 providers.<vendor> 节点
// (先例:imageshttp pricingVendorForFamily 对 replicate 的修复),
// (c) settle 的 Provider 留档。两条硬规则:
//   - 4-vendor 真实账号集合的历史标签锁定不得改动:openai_codex→"codex"
//     (防 ChatGPT Plus/Codex CLI 反转场景被 openai 切片双计,历史 bug)、
//     gemini_advanced_session→"gemini" 同理;
//   - 其余族 vendor == registrydefault 注册 adapter 的 Platform() 串——与
//     选号后 accInfo.Platform 同值,行为零漂移。漂移/漏配由注册表驱动守卫
//     TestVendorCoversEveryRegisteredProtocolFamily(族集对称第 9 站)锁死。
func VendorFromProtocolFamily(pf string) string {
	switch pf {
	// —— 4-vendor 真实账号集合(标签锁定,勿动)——
	case "anthropic_messages":
		return "anthropic"
	case "openai_chat", "openai_responses":
		return "openai"
	case "openai_codex":
		return "codex"
	case "gemini_messages", "gemini_advanced_session":
		return "gemini"
	// —— 其余注册族:vendor == 注册 platform ——
	case "bedrock_invoke":
		return "bedrock"
	case "openrouter_chat":
		return "openrouter"
	case "grok_chat":
		return "grok"
	case "kimi_chat":
		return "kimi"
	case "deepseek_chat":
		return "deepseek"
	case "mistral_chat":
		return "mistral"
	case "groqcloud_chat":
		return "groqcloud"
	case "together_chat":
		return "together"
	case "perplexity_chat":
		return "perplexity"
	case "fireworks_chat":
		return "fireworks"
	case "qwen_chat":
		return "qwen"
	case "glm_chat":
		return "glm"
	case "yi_chat":
		return "yi"
	case "baichuan_chat":
		return "baichuan"
	case "doubao_chat":
		return "doubao"
	case "ernie_chat":
		return "ernie"
	case "step_chat":
		return "step"
	case "hunyuan_chat":
		return "hunyuan"
	case "minimax_chat":
		return "minimax"
	case "cohere_chat":
		return "cohere"
	case "ollama_chat", "ollama_native":
		return "ollama"
	case "dify_chat":
		return "dify"
	case "replicate_image":
		return "replicate"
	case "vertex_gemini", "vertex_anthropic":
		return "vertex"
	case "gemini_code_assist":
		return "gemini_code_assist"
	case "cursor_session":
		return "cursor"
	case "copilot_session":
		return "copilot"
	case "antigravity_session":
		return "antigravity"
	case "kiro_session":
		return "kiro"
	case "windsurf_session":
		return "windsurf"
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

// RatePrecheckCounter 是由 RatePrecheckGate(读)和 RecordingSelector(写)
// 共享的内存 RPM/TPM 预算跟踪器。ROUTE-121。
type RatePrecheckCounter = precheck.Counter

// NewRatePrecheckCounter 构建一个带默认 1 分钟窗口和墙钟的预算跟踪器。
// 当限流器被禁用时,其用法是 nil-safe 的。
func NewRatePrecheckCounter() *RatePrecheckCounter { return precheck.New(0, nil) }

// NewRecordingSelector 包装 inner,使一次成功的 Select 消费该账号 RPM/TPM
// 预算中的一个请求(及其估算的输入 token)(ROUTE-121)。counter 为 nil
// 时它是透明的直通。
func NewRecordingSelector(inner Selector, counter *precheck.Counter) Selector {
	return router.NewRecordingSelector(inner, counter)
}

// NewKeyRateLimitSelector 用「每个已认证 API key 的 RPM/TPM 预算」包装 inner,
// 在选号前强制执行(SEC-249/250)。rpm/tpm <= 0 = 无限制(inert)。
// 超预算的选号返回 ErrKeyRateLimited(-> HTTP 429)。
func NewKeyRateLimitSelector(inner Selector, counter *precheck.Counter, rpm, tpm int64) Selector {
	return router.NewKeyRateLimitSelector(inner, counter, rpm, tpm)
}

// NewBindingRateLimitSelector 用「每个 binding 的 RPM/TPM 预算」包装 inner,
// 在选号前强制执行。limit 随每个请求携带在 SelectionRequest 上(每个 binding 有
// 自己的 model_pool_bindings.rpm_limit/tpm_limit);counter 为 nil / BindingID<=0 /
// limit 为零时它是透明的直通。超预算返回 ErrBindingRateLimited(-> 429)。
func NewBindingRateLimitSelector(inner Selector, counter *precheck.Counter) Selector {
	return router.NewBindingRateLimitSelector(inner, counter)
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
