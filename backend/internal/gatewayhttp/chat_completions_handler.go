package gatewayhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/affinityrules"
	"github.com/BloomingProsperity/HUAKAI/internal/apikeymodelallow"
	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/authcooldown"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
	l2cache "github.com/BloomingProsperity/HUAKAI/internal/cache"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/modelfallback"
	"github.com/BloomingProsperity/HUAKAI/internal/moderation"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/quotaenforce"
	"github.com/BloomingProsperity/HUAKAI/internal/rate"
	"github.com/BloomingProsperity/HUAKAI/internal/recentreq"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/sessioncap"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementintent"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementrecovery"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/BloomingProsperity/HUAKAI/internal/toolpricing"
)

type authResolver interface {
	Resolve(ctx context.Context, req *http.Request) (auth.Identity, error)
}

type pricingRatioResolver interface {
	Resolve(ctx context.Context, tenantID, poolGroupID int64) (decimal.Decimal, error)
}

type cacheOverrideResolver interface {
	ResolveMultiplier(tenantID int64, model string) decimal.Decimal
}

type ChatHandlerDeps struct {
	Auth     authResolver
	Registry registry.Registry
	Router   router.Router

	// AffinityRules 可选地在旧的 session-hash 级联之前，从请求头/请求体
	// 信号推导出粘滞 key。为 nil 或为空时保持既有行为。TODO: 待 registry
	// schema 接管 CRED-205..212/216 配置后，按 binding/channel 持久化此项。
	AffinityRules affinityrules.AffinityRuleSet

	ClaimGate            billing.ClaimGate
	QuotaReserver        quotaenforce.Reserver
	RateTables           billing.RateTableSource
	PricingRatioResolver pricingRatioResolver
	CacheOverrideStore   cacheOverrideResolver
	Selector             pool.Selector
	// QueueWaiter 执行 WaitPlan 的有界等待。nil 时 handler 构造阶段补默认执行器。
	QueueWaiter QueueWaiter
	// QueueWaitNow 为 queue_wait 请求级预算提供可注入时钟;nil 时使用 time.Now。
	QueueWaitNow        func() time.Time
	CredentialVault     provider.CredentialVault
	Dispatcher          *gateway.UpstreamDispatcher
	CanonicalDispatcher HCSFDispatcher
	Forwarder           *gateway.StreamForwarder
	ResponseCache       l2cache.Store
	Settler             billing.Settler
	// SettlementIntents 关闭时静默禁用，开启但 Store 缺失时只告警并 fail-open。
	SettlementIntents       settlementintent.Store
	SettlementIntentEnabled bool
	ReplayStore             billing.ReplayStore
	BillingPolicyResolver   *billing.PolicyResolver
	CompletionBus           *eventbus.Bus
	AuditRefPolicy          *eventbus.AuditRefPolicy
	AuditLedger             auditledger.Ledger
	AuditLedgerDLQ          auditledger.DLQEnqueuer
	ModerationScreener      moderation.Screener
	// SettleRecoveryDLQ 持久化已交付但 Tx2 未确认的结算；生产必须接入 DLQ 服务。
	SettleRecoveryDLQ      settlementrecovery.Enqueuer
	Signer                 *sign.Signer
	ChannelHealth          channelHealthRecorder
	ModelCooldowns         modelRateLimitRecorder
	RateService            rate.Service
	RetryBudget            retryBudgetGate
	CredentialHotRefresher CredentialHotRefresher
	ModelFallbackSettings  modelfallback.SettingsReader
	// NonStreamKeepAliveInterval:非流式 buffered 长响应期间每隔此时长写裸换行保活,避开反代空闲超时;0=关。
	NonStreamKeepAliveInterval time.Duration
	// PlatformSettings 提供对平台级 feature flag 的读取访问。
	// warmup_intercept_enabled 开关需要它。
	PlatformSettings     platformSettingsReader
	BillingPolicyVersion string
	RequestClass         string
	ClientIPResolver     *clientip.Resolver

	// SessionCapRegistry 在 dispatch 成功时登记 session hash；nil 时跳过。
	SessionCapRegistry *sessioncap.Registry

	// RecentReqRing 记录账号级近期请求；nil 时跳过。
	RecentReqRing *recentreq.Ring

	// EndpointFamily 标记 billing 端点族，空值退化为 "chat"。
	EndpointFamily string

	// CacheScope 决定 L2 缓存键 principal 隔离粒度(tenant|apikey|user); 空 → "apikey"。
	CacheScope string

	// ToolPricingTable 查询租户/模型工具附加费；nil 保持不加费的安全默认。
	// 生产由 HUAKAI_TOOL_SURCHARGE_ENABLED 决定是否注入带默认价的 source。
	ToolPricingTable toolpricing.Source

	// AuthCooldown 接收热刷新结果，永久失效时升级 HardDisabled；nil 安全。
	AuthCooldown *authcooldown.Store
}

type channelHealthRecorder interface {
	ApplySignal(context.Context, channelhealth.Signal) (channelhealth.Record, error)
	ForceCooldown(context.Context, channelhealth.ChannelKey, time.Time, string) (channelhealth.Record, error)
}

type modelRateLimitRecorder interface {
	RecordModelRateLimit(context.Context, rate.ModelCooldownInput) error
}

type retryBudgetGate interface {
	Allow(tenantID int64) bool
}

type CredentialHotRefresher interface {
	RefreshHotPath(ctx context.Context, tenantID, accountID int64, vendorName string) error
}

// HCSFDispatcher 是 non-streaming HCSF 主链路；默认开启，可由 env 开关关闭。
type HCSFDispatcher interface {
	DispatchHCSF(ctx context.Context, envelope *proto.HCSF) (*proto.HCSF, error)
}

// effectiveEndpointFamily 返回 d.EndpointFamily 若非空，否则 "chat"。
func (d ChatHandlerDeps) effectiveEndpointFamily() string {
	if d.EndpointFamily == "" {
		return "chat"
	}
	return d.EndpointFamily
}

// effectiveCacheScope 返回 d.CacheScope 若非空，否则安全默认 "apikey"。
func (d ChatHandlerDeps) effectiveCacheScope() string {
	if d.CacheScope == "" {
		return "apikey"
	}
	return d.CacheScope
}

type chatExecution struct {
	d         ChatHandlerDeps
	r         *http.Request
	ctx       context.Context
	startedAt time.Time

	ident           auth.Identity
	body            []byte
	req             chatRequest
	clientProtocol  proto.ClientProtocol
	clientAdapter   proto.ClientAdapter
	requestID       string
	clientRequestID string
	clientSessionID string

	resolved          registry.Resolved
	plan              router.RoutePlan
	attempt           router.AttemptPlan
	routeID           string
	currentAttemptSeq int
	// modelFallbackEnabled 对本次请求镜像 resolver.Enabled()；它控制
	// dispatch 路径上 opt-in 的 ROUTE-023 上下文窗口预检喂入
	// (默认关 => 不做预检，与各上游参考实现一致)。
	modelFallbackEnabled bool

	idempotencyHeader                string
	inboundBetaTokens                []string
	inboundBetaTokensParsed          bool
	logicalRequestID                 string
	payloadHash                      string
	promptHash                       string
	sessionHash                      string
	moderationScreened               bool
	reserveRes                       *billing.ReserveResult
	settlementIntent                 *settlementintent.Tracker
	streamInputOnlyInterruptedPolicy billing.StreamInputOnlyInterruptedPolicy
	balanceEnforcementMode           billing.BalanceEnforcementMode

	selRes            *pool.SelectionResult
	acquiredAccountID int64
	acquisitionToken  uuid.UUID
	upstreamModelID   string
	cacheVendor       string
	cacheKey          string

	groupRatioCacheSet                   bool
	groupRatioCacheTenantID              int64
	groupRatioCachePoolGroupID           int64
	groupRatioCache                      decimal.Decimal
	groupRatioCachePendingReconciliation bool

	cred                        provider.Credential
	accInfo                     provider.AccountInfo
	forwardReq                  gateway.ForwardRequest
	healthKey                   channelhealth.ChannelKey
	healthKeyOK, officialDirect bool
	protocolLoss                json.RawMessage

	queueWaitSpentMS int
	queueWaitNow     func() time.Time
	classTransition  *bindingClassTransition
}

type bindingClassTransition = bindingfallback.Transition

type modelRunResult struct {
	Success         *attemptOutcome
	Failure         *classifiedAttemptFailure
	DeliveryStarted bool
	AllowFallback   bool
}

const (
	headerHuakaiModelFallback    = "X-Huakai-Model-Fallback"
	headerHuakaiFallbackAttempts = "X-Huakai-Fallback-Attempts"
)

const (
	credentialHotRefreshDedupeWindow = 30 * time.Second
	credentialHotRefreshTimeout      = 30 * time.Second
)

type credentialHotRefreshKey struct {
	tenantID  int64
	accountID int64
}

// credentialHotRefreshPurgeThreshold 触发惰性清理的 until map 大小阈值,防高账号 churn 下
// 已过期项无限堆积。
const credentialHotRefreshPurgeThreshold = 1024

type dedupingCredentialHotRefresher struct {
	inner  CredentialHotRefresher
	window time.Duration
	now    func() time.Time
	mu     sync.Mutex
	until  map[credentialHotRefreshKey]time.Time
}

func newDedupingCredentialHotRefresher(inner CredentialHotRefresher, window time.Duration) CredentialHotRefresher {
	return &dedupingCredentialHotRefresher{
		inner: inner, window: window, now: time.Now,
		until: make(map[credentialHotRefreshKey]time.Time),
	}
}

func (r *dedupingCredentialHotRefresher) RefreshHotPath(ctx context.Context, tenantID, accountID int64, vendorName string) error {
	if r == nil || r.inner == nil || !r.admit(tenantID, accountID) {
		return nil
	}
	return r.inner.RefreshHotPath(ctx, tenantID, accountID, vendorName)
}

func (r *dedupingCredentialHotRefresher) admit(tenantID, accountID int64) bool {
	if tenantID == 0 || accountID == 0 {
		return false
	}
	now := r.now()
	key := credentialHotRefreshKey{tenantID: tenantID, accountID: accountID}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.until) > credentialHotRefreshPurgeThreshold {
		// 惰性清理已过期项(map 超阈值时才扫,均摊 O(1)),防无限增长。
		for k, t := range r.until {
			if !now.Before(t) {
				delete(r.until, k)
			}
		}
	}
	if until, ok := r.until[key]; ok && now.Before(until) {
		return false
	}
	r.until[key] = now.Add(r.window)
	return true
}

func NewChatCompletionsHandler(d ChatHandlerDeps) http.HandlerFunc {
	if d.CredentialHotRefresher != nil {
		d.CredentialHotRefresher = newDedupingCredentialHotRefresher(d.CredentialHotRefresher, credentialHotRefreshDedupeWindow)
	}
	ensureChatQueueWaiter(&d)
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		requestStartedAt := time.Now()

		if !chatHandlerConfigured(d) {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured",
				"chat handler dependency unset")
			return
		}

		ident, err := d.Auth.Resolve(ctx, r)
		if errors.Is(err, auth.ErrAuthMisconfigured) {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "auth tables unavailable")
			return
		}
		if errors.Is(err, auth.ErrAuthBackend) {
			writeJSONError(w, http.StatusServiceUnavailable, "auth_backend_error", "auth backend transient failure")
			return
		}
		if errors.Is(err, auth.ErrForbidden) {
			writeJSONError(w, http.StatusForbidden, "forbidden", "api key policy forbids this request")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized", "invalid bearer")
			return
		}

		validated, ok := validateChatCompletionsRequest(w, r, ctx)
		if !ok {
			return
		}
		if !apikeymodelallow.AllowsCSV(ident.AllowedModels, validated.Request.Model) {
			writeJSONError(w, http.StatusForbidden, "model_not_allowed", "api key is not allowed to use this model")
			return
		}
		ctx = context.WithValue(ctx, middleware.RequestIDKey, validated.RequestID)
		r = r.WithContext(ctx)
		w.Header().Set(middleware.RequestIDHeader, validated.RequestID)
		exec := newChatExecution(d, r, ident, validated, requestStartedAt)
		exec.runWithModelFallback(newDeliveryTracker(w))
	}
}

// NativeClientRequest 是一个已预校验的原生客户端协议请求。原始 HTTP
// body 仍保留在 r.Body 上，由共享的 gateway pipeline 读取。
type NativeClientRequest struct {
	Model          string
	Action         string
	Stream         bool
	ClientProtocol proto.ClientProtocol
	ClientAdapter  proto.ClientAdapter
	EndpointFamily string
}

// NativeClientGateway 为原生的、按路径限定的客户端协议(如 Gemini v1beta)
// 暴露共享的 chat 执行 pipeline。它避免对 body 做改写：model 和 stream 来自
// 原生 URL/action，而 body 按原样收到后原封不动传给所选的 ClientAdapter。
type NativeClientGateway struct {
	d ChatHandlerDeps
}

func NewNativeClientGateway(d ChatHandlerDeps) *NativeClientGateway {
	if d.CredentialHotRefresher != nil {
		d.CredentialHotRefresher = newDedupingCredentialHotRefresher(d.CredentialHotRefresher, credentialHotRefreshDedupeWindow)
	}
	return &NativeClientGateway{d: d}
}

func (g *NativeClientGateway) ServeNativeClient(w http.ResponseWriter, r *http.Request, native NativeClientRequest) {
	if g == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "native gateway dependency unset")
		return
	}
	d := g.d
	if native.EndpointFamily != "" {
		d.EndpointFamily = native.EndpointFamily
	}
	ctx := r.Context()
	requestStartedAt := time.Now()

	if !chatHandlerConfigured(d) {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured",
			"chat handler dependency unset")
		return
	}
	if native.Model == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_model", "model path segment required")
		return
	}
	if native.ClientProtocol == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_client_protocol", "client protocol required")
		return
	}
	clientAdapter := native.ClientAdapter
	if clientAdapter == nil {
		var ok bool
		clientAdapter, ok = proto.DefaultClientAdapterRegistry().Lookup(native.ClientProtocol)
		if !ok {
			writeJSONError(w, http.StatusServiceUnavailable, "adapter_unregistered",
				"client adapter not registered for protocol "+string(native.ClientProtocol))
			return
		}
	}

	ident, err := d.Auth.Resolve(ctx, r)
	if errors.Is(err, auth.ErrAuthMisconfigured) {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "auth tables unavailable")
		return
	}
	if errors.Is(err, auth.ErrAuthBackend) {
		writeJSONError(w, http.StatusServiceUnavailable, "auth_backend_error", "auth backend transient failure")
		return
	}
	if errors.Is(err, auth.ErrForbidden) {
		writeJSONError(w, http.StatusForbidden, "forbidden", "api key policy forbids this request")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized", "invalid bearer")
		return
	}

	body, ok := readChatRequestBody(w, r, ctx)
	if !ok {
		return
	}
	if !rejectRemovedBodyFields(w, body) {
		return
	}
	if !apikeymodelallow.AllowsCSV(ident.AllowedModels, native.Model) {
		writeJSONError(w, http.StatusForbidden, "model_not_allowed", "api key is not allowed to use this model")
		return
	}
	requestID := uuid.NewString()
	clientRequestID := r.Header.Get(middleware.RequestIDHeader)
	ctx = context.WithValue(ctx, middleware.RequestIDKey, requestID)
	r = r.WithContext(ctx)
	w.Header().Set(middleware.RequestIDHeader, requestID)

	validated := chatValidatedRequest{
		Body: body,
		Request: chatRequest{
			Model:  native.Model,
			Stream: native.Stream,
		},
		ClientProtocol:  native.ClientProtocol,
		ClientAdapter:   clientAdapter,
		RequestID:       requestID,
		ClientRequestID: clientRequestID,
	}
	exec := newChatExecution(d, r, ident, validated, requestStartedAt)
	exec.runWithModelFallback(newDeliveryTracker(w))
}

func (ex *chatExecution) runWithModelFallback(w *deliveryTracker) {
	resolver := modelfallback.FromSettings(ex.ctx, ex.d.ModelFallbackSettings)
	ex.modelFallbackEnabled = resolver.Enabled()
	originalModel := ex.req.Model
	triedModels := []string{originalModel}
	baseLogicalRequestID := ""
	fallbackAttempts := 0
	for {
		result := ex.runSingleModel(w, fallbackAttempts)
		if result.Success != nil {
			writeAttemptSuccess(w, *result.Success)
			return
		}
		if result.DeliveryStarted || w.started() {
			return
		}
		clearModelFallbackHeaders(w)
		if result.Failure == nil {
			return
		}
		if baseLogicalRequestID == "" {
			baseLogicalRequestID = ex.logicalRequestID
		}
		if !result.AllowFallback || !resolver.Enabled() || fallbackAttempts >= resolver.MaxDepth() {
			writeAttemptFailure(w, result.Failure)
			return
		}
		class := modelfallback.ClassForFailure(result.Failure.ClientCode, result.Failure.EndClass, result.Failure.Classification.Class, result.Failure.AbortReason)
		nextModel := resolver.Resolve(originalModel, class, triedModels)
		if nextModel == "" {
			writeAttemptFailure(w, result.Failure)
			return
		}
		fallbackAttempts++
		triedModels = append(triedModels, nextModel)
		clearRetryableAttemptFailureHeaders(w)
		ex.prepareNextModelFallback(nextModel, baseLogicalRequestID)
	}
}

func (ex *chatExecution) screenModerationInput(w http.ResponseWriter) bool {
	if ex == nil || ex.d.ModerationScreener == nil || ex.moderationScreened {
		return true
	}
	ex.ensureIdempotencyState()
	res, err := ex.d.ModerationScreener.Screen(ex.ctx, moderation.ScreenRequest{
		TenantID:    ex.ident.TenantID,
		APIKeyID:    ex.ident.APIKeyID,
		UserID:      ex.ident.UserID,
		RequestID:   ex.requestID,
		PayloadHash: ex.payloadHash,
		Body:        ex.body,
		TailRole:    clientTailMessageRole(ex.clientProtocol, ex.body),
	})
	if err != nil {
		logInternalError(ex.ctx, ex.requestID, clienterr.CodeContentPolicyViolation, err)
	}
	if err != nil || moderationDecisionBlocks(res.Decision) {
		writeJSONError(w, http.StatusForbidden, clienterr.CodeContentPolicyViolation, clienterr.MessageFor(clienterr.CodeContentPolicyViolation))
		return false
	}
	ex.moderationScreened = true
	return true
}

func moderationDecisionBlocks(decision moderation.Decision) bool {
	switch decision {
	case "", moderation.DecisionPass:
		return false
	case moderation.DecisionBlockKeyword, moderation.DecisionBlockHash, moderation.DecisionBlockBackend:
		return true
	default:
		return true
	}
}

func (ex *chatExecution) prepareNextModelFallback(model, baseLogicalRequestID string) {
	ex.req.Model = model
	ex.resolved = registry.Resolved{}
	ex.plan = router.RoutePlan{}
	ex.attempt = router.AttemptPlan{}
	ex.routeID = ""
	ex.currentAttemptSeq = 0
	ex.logicalRequestID = modelfallback.DeriveLogicalRequestID(baseLogicalRequestID, model)
	ex.reserveRes = nil
	ex.cacheVendor = ""
	ex.cacheKey = ""
	ex.protocolLoss = nil
	ex.classTransition = nil
	ex.prepareNextAttemptAfterAbort()
}

func responseStarted(w http.ResponseWriter) bool {
	if tracker, ok := w.(*deliveryTracker); ok {
		return tracker.started()
	}
	return false
}

func setModelFallbackHeaders(w http.ResponseWriter, model string, attempts int) {
	w.Header().Set(headerHuakaiModelFallback, model)
	w.Header().Set(headerHuakaiFallbackAttempts, strconv.Itoa(attempts))
}

func clearModelFallbackHeaders(w http.ResponseWriter) {
	w.Header().Del(headerHuakaiModelFallback)
	w.Header().Del(headerHuakaiFallbackAttempts)
}

func chatHandlerConfigured(d ChatHandlerDeps) bool {
	return d.Registry != nil && d.Router != nil && d.Auth != nil &&
		d.Selector != nil && d.ClaimGate != nil && d.Settler != nil &&
		d.CredentialVault != nil && d.Dispatcher != nil && d.Forwarder != nil
}

func hcsfDispatchEnabled() bool {
	return os.Getenv("HUAKAI_DISPATCH_HCSF") != "0"
}
