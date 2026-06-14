package gatewayhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/affinityrules"
	"github.com/BloomingProsperity/HUAKAI/internal/apikeymodelallow"
	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	l2cache "github.com/BloomingProsperity/HUAKAI/internal/cache"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/modelfallback"
	"github.com/BloomingProsperity/HUAKAI/internal/moderation"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/protosse"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/quotaenforce"
	"github.com/BloomingProsperity/HUAKAI/internal/rate"
	"github.com/BloomingProsperity/HUAKAI/internal/recentreq"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/sessioncap"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementrecovery"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/BloomingProsperity/HUAKAI/internal/toolpricing"
	"github.com/BloomingProsperity/HUAKAI/internal/warmupintercept"
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

	// AffinityRules optionally derives the sticky key from request header/body
	// signals before the legacy session-hash cascade. Nil or empty preserves
	// the existing behavior. TODO: persist this per binding/channel once the
	// registry schema owns CRED-205..212/216 configuration.
	AffinityRules affinityrules.AffinityRuleSet

	ClaimGate             billing.ClaimGate
	QuotaReserver         quotaenforce.Reserver
	RateTables            billing.RateTableSource
	PricingRatioResolver  pricingRatioResolver
	CacheOverrideStore    cacheOverrideResolver
	Selector              pool.Selector
	CredentialVault       provider.CredentialVault
	Dispatcher            *gateway.UpstreamDispatcher
	CanonicalDispatcher   HCSFDispatcher
	Forwarder             *gateway.StreamForwarder
	ResponseCache         l2cache.Store
	Settler               billing.Settler
	ReplayStore           billing.ReplayStore
	BillingPolicyResolver *billing.PolicyResolver
	CompletionBus         *eventbus.Bus
	AuditRefPolicy        *eventbus.AuditRefPolicy
	AuditLedger           auditledger.Ledger
	AuditLedgerDLQ        auditledger.DLQEnqueuer
	ModerationScreener    moderation.Screener
	// SettleRecoveryDLQ 是 post-delivery settle 失败(流式响应已发给客户端
	// 但 Tx2 settlement 未确认提交)的 durable 兜底 enqueue;nil 时 stream
	// path 失败只 log,money path 灰区无可补救。生产部署必须 wire 上
	// dlq.Service(见 cmd/gateway/routes.go SettleRecoveryDLQ: d.dlqService)。
	SettleRecoveryDLQ      settlementrecovery.Enqueuer
	Signer                 *sign.Signer
	ChannelHealth          channelHealthRecorder
	ModelCooldowns         modelRateLimitRecorder
	RateService            rate.Service
	RetryBudget            retryBudgetGate
	CredentialHotRefresher CredentialHotRefresher
	ModelFallbackSettings  modelfallback.SettingsReader
	// PlatformSettings provides access to platform-wide feature flags.
	// Required for warmup_intercept_enabled gate (SUB2-EGRESS-04).
	PlatformSettings     platformSettingsReader
	BillingPolicyVersion string
	RequestClass         string
	ClientIPResolver     *clientip.Resolver

	// SessionCapRegistry is used to register session hashes at dispatch
	// success (SUB2-EGRESS-02). nil is safe (registration is skipped).
	SessionCapRegistry *sessioncap.Registry

	// RecentReqRing records per-account request outcomes for incident triage
	// (MGMT-RECENTREQ-01). nil is safe (recording is skipped).
	RecentReqRing *recentreq.Ring

	// EndpointFamily 标记 billing 字段；空字符串退化为 "chat"。
	// /v1/chat/completions: "chat"
	// /v1/messages:         "messages"
	EndpointFamily string

	// CacheScope 决定 L2 缓存键 principal 隔离粒度(tenant|apikey|user); 空 → "apikey"。
	CacheScope string

	// ToolPricingTable holds the per-(tenant, model) tool-call surcharge price
	// table (NAPI-BILLING-01 Stage A). nil = default-off: no surcharge is added
	// regardless of tool call counts. Configure via runtime settings to enable.
	ToolPricingTable toolpricing.Table
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
	// modelFallbackEnabled mirrors resolver.Enabled() for this request; it gates
	// the opt-in ROUTE-023 context-window pre-check feed in the dispatch path
	// (default off => no pre-check, matching sub2api/new-api/CLIProxyAPI).
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

	cred         provider.Credential
	accInfo      provider.AccountInfo
	forwardReq   gateway.ForwardRequest
	healthKey    channelhealth.ChannelKey
	healthKeyOK  bool
	protocolLoss json.RawMessage
}

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

// NativeClientRequest is a prevalidated native client-protocol request. The
// raw HTTP body remains on r.Body and is read by the shared gateway pipeline.
type NativeClientRequest struct {
	Model          string
	Action         string
	Stream         bool
	ClientProtocol proto.ClientProtocol
	ClientAdapter  proto.ClientAdapter
	EndpointFamily string
}

// NativeClientGateway exposes the shared chat execution pipeline for native
// path-scoped client protocols such as Gemini v1beta. It avoids body rewrites:
// model and stream come from the native URL/action, while the body is passed to
// the selected ClientAdapter as originally received.
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

func (ex *chatExecution) runSingleModel(w http.ResponseWriter, fallbackAttempts int) modelRunResult {
	// SUB2-EGRESS-04: intercept Claude Code throwaway requests before billing.
	// Gate is opt-in (default off); when off this block is a true no-op.
	// Source: sub2api gateway_handler.go:359-369 / 613-623
	if warmupInterceptEnabled(ex.ctx, ex.d.PlatformSettings) {
		isClaudeUA := warmupintercept.IsClaudeCodeUserAgent(ex.r.UserAgent())
		maxTok := 0
		if ex.req.MaxTokens != nil {
			maxTok = *ex.req.MaxTokens
		}
		if kind, ok := warmupintercept.Detect(isClaudeUA, ex.req.Model, maxTok, ex.req.Stream, ex.body); ok {
			slog.InfoContext(ex.ctx, "warmup_intercept.intercepted",
				"kind", int(kind),
				"model", ex.req.Model,
				"request_id", ex.requestID,
			)
			if ex.req.Stream {
				warmupintercept.WriteStream(w, kind, ex.req.Model)
			} else {
				warmupintercept.WriteNonStream(w, kind, ex.req.Model)
			}
			return modelRunResult{DeliveryStarted: true}
		}
	}
	if !ex.prepareRoute(w) {
		return modelRunResult{DeliveryStarted: responseStarted(w)}
	}
	if !ex.screenModerationInput(w) {
		return modelRunResult{DeliveryStarted: responseStarted(w)}
	}
	if !ex.reserveClaim(w) {
		return modelRunResult{DeliveryStarted: responseStarted(w)}
	}
	if fallbackAttempts > 0 {
		setModelFallbackHeaders(w, ex.req.Model, fallbackAttempts)
	}
	if !ex.req.Stream {
		handled, proceed := ex.serveL2CacheIfAvailable(w)
		if handled || !proceed {
			return modelRunResult{DeliveryStarted: responseStarted(w)}
		}
	}
	failedAccounts := make(map[int64]struct{})
	authFailoverUsed := false
	budget := effectiveAttemptBudget(ex.plan)
	maxAttempts := len(ex.plan.Attempts)
	// attemptCap 起始=普通 attempt 预算; 当 401 的 auth-failover 子预算落在本应最后的 slot 时 +1,
	// 给 auth-failover 一个独立额外 attempt(至多一次, 由 !authFailoverUsed 限定)。
	attemptCap := budget
	for i := 0; i < attemptCap; i++ {
		planIdx := i
		if planIdx >= maxAttempts {
			planIdx = maxAttempts - 1 // auth-failover 额外 slot 复用最后一个 pool plan(池排除已 401 账号选新号)
		}
		outcome := ex.runAttempt(w, attemptInput{
			Plan:             ex.plan.Attempts[planIdx],
			AttemptSeq:       i + 1,
			ExcludedAccounts: failedAccounts,
			ReplayableBody:   true,
			FinalAttempt:     i+1 >= attemptCap,
		})
		if outcome.Success != nil {
			return modelRunResult{Success: &outcome, DeliveryStarted: outcome.DeliveryStarted}
		}
		if outcome.DeliveryStarted || (outcome.Failure != nil && outcome.Failure.DeliveredToClient) {
			return modelRunResult{DeliveryStarted: true}
		}
		if outcome.AccountID != 0 && outcome.Failure != nil {
			if outcome.Failure.Decision.RefreshIntent == gateway.RefreshOAuthHotPath {
				ex.triggerCredentialHotRefresh(outcome.AccountID)
			}
			if outcome.Failure.Decision.SwitchAccount {
				failedAccounts[outcome.AccountID] = struct{}{}
			}
		}
		if outcome.Failure == nil {
			continue
		}
		retry, consumeAuthBudget := shouldRetryAttemptFailure(outcome.Failure, ex.plan, true, i+1 >= attemptCap, authFailoverUsed)
		if !retry {
			return modelRunResult{Failure: outcome.Failure, AllowFallback: outcome.Failure.Decision.RetryableBeforeDelivery}
		}
		if ex.d.RetryBudget != nil && !ex.d.RetryBudget.Allow(ex.ident.TenantID) {
			return modelRunResult{Failure: outcome.Failure}
		}
		if consumeAuthBudget {
			authFailoverUsed = true
			if i+1 >= attemptCap {
				attemptCap++ // auth-failover 落在本应最后的 slot: 额外给一次换号重试(独立子预算)
			}
		}
		clearRetryableAttemptFailureHeaders(w)
		ex.prepareNextAttemptAfterAbort()
	}
	return modelRunResult{}
}

func (ex *chatExecution) triggerCredentialHotRefresh(accountID int64) {
	if ex == nil || ex.d.CredentialHotRefresher == nil || accountID == 0 {
		return
	}
	tenantID := ex.ident.TenantID
	vendor := ex.accInfo.Platform
	if vendor == "" {
		vendor = pool.VendorFromProtocolFamily(ex.resolved.ProtocolFamily)
	}
	requestID := ex.requestID
	refresher := ex.d.CredentialHotRefresher
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), credentialHotRefreshTimeout)
		defer cancel()
		if err := refresher.RefreshHotPath(ctx, tenantID, accountID, vendor); err != nil {
			logInternalError(ctx, requestID, "credential_hot_refresh_failed", err)
		}
	}()
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

func hcsfDispatcher(d ChatHandlerDeps) HCSFDispatcher {
	if d.CanonicalDispatcher != nil {
		return d.CanonicalDispatcher
	}
	if d.Dispatcher == nil {
		return nil
	}
	return d.Dispatcher
}

func protocolAdapterForBuffered(f *gateway.StreamForwarder, protocolFamily string) (proto.UpstreamAdapter, error) {
	var adapters gateway.ProtocolAdapterRegistry
	if f != nil {
		adapters = f.ProtocolAdapters
	}
	if adapters == nil {
		adapters = gateway.BuildDefaultProtocolAdapterRegistry()
	}
	return adapters.For(protocolFamily)
}

const maxRawBufferedUpstreamBodyBytes = 1 << 20

var errRawBufferedUpstreamBodyTooLarge = errors.New("gatewayhttp: upstream buffered response exceeds 1MiB limit")

func readRawBufferedUpstreamBody(r io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxRawBufferedUpstreamBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxRawBufferedUpstreamBodyBytes {
		// 超限时保留截断 body，供 caller 对非 2xx 上游响应继续做错误分类。
		return raw[:maxRawBufferedUpstreamBodyBytes], errRawBufferedUpstreamBodyTooLarge
	}
	return raw, nil
}

func (ex *chatExecution) dispatchRawBuffered(w http.ResponseWriter, seed proto.RequestMetaSeed, seedCtx context.Context, startedAt time.Time) (*proto.HCSF, *classifiedAttemptFailure, bool) {
	transportSelection := transportSelectionForDispatch(ex.accInfo, ex.resolved.ProtocolFamily)
	dispatchRes, err := ex.d.Dispatcher.Dispatch(ex.ctx, gateway.DispatchInput{
		ProtocolFamily:       ex.resolved.ProtocolFamily,
		UpstreamModelID:      ex.upstreamModelID,
		InboundBody:          ex.upstreamInboundBody(ex.body),
		BodyControls:         ex.activeDispatchBodyControls(),
		InboundBetaTokens:    ex.clientBetaTokens(),
		Account:              transportSelection.account,
		Credential:           ex.cred,
		TransportMode:        transportSelection.mode,
		NonStreamingBuffered: true,
	})
	if err != nil {
		classification, _ := gateway.Classify(0, nil, []byte(err.Error()), ex.accInfo.Platform)
		decision := gateway.ClassifyAttemptDispatchError(err)
		if !decision.RetryableBeforeDelivery && decision.TransportClass == gateway.TransportErrorLocalDispatch {
			decision.ClientStatus = http.StatusBadGateway
		}
		if decision.AbortReason == "" {
			decision.AbortReason = "upstream_dispatch_error"
		}
		abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, decision.AbortReason, ex.requestID, 0, nil)
		if ex.healthKeyOK {
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, signalFromDispatchError(err, classification), 0, time.Since(startedAt), ex.requestID, nil)
		}
		failure := classifiedFailureFromDecision("", clienterr.MessageFor(clienterr.CodeUpstreamDispatchError), classification, decision, err)
		return nil, degradeFailureIfAbortFailed(ex.ctx, ex.requestID, failure, abortErr), false
	}
	if dispatchRes == nil || dispatchRes.UpstreamReader == nil {
		abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "upstream_empty_response", ex.requestID, 0, nil)
		if ex.healthKeyOK {
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, channelhealth.SignalChannelError, 0, time.Since(startedAt), ex.requestID, nil)
		}
		failure := retryableLocalAttemptFailure(http.StatusBadGateway, clienterr.CodeUpstreamEmptyResponse, clienterr.MessageFor(clienterr.CodeUpstreamEmptyResponse), "upstream_empty_response", gateway.UpstreamError5xx, nil)
		return nil, degradeFailureIfAbortFailed(ex.ctx, ex.requestID, failure, abortErr), false
	}
	defer closeDispatchResult(dispatchRes)
	raw, readErr := readRawBufferedUpstreamBody(dispatchRes.UpstreamReader)
	oversizedNon2xx := errors.Is(readErr, errRawBufferedUpstreamBodyTooLarge) && (dispatchRes.StatusCode < 200 || dispatchRes.StatusCode >= 300)
	if readErr != nil && !oversizedNon2xx {
		code := clienterr.CodeUpstreamReadError
		if errors.Is(readErr, errRawBufferedUpstreamBodyTooLarge) {
			code = clienterr.CodeUpstreamResponseTooLarge
		}
		if abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, code, ex.requestID, 0, nil); abortErr != nil {
			setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
		}
		if ex.healthKeyOK {
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, channelhealth.SignalChannelError, dispatchRes.StatusCode, time.Since(startedAt), ex.requestID, nil)
		}
		writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusBadGateway, code, readErr)
		return nil, nil, false
	}
	if dispatchRes.StatusCode < 200 || dispatchRes.StatusCode >= 300 {
		decision, classification, classifyErr := gateway.ClassifyAttemptHTTPError(dispatchRes.StatusCode, dispatchRes.Headers, raw, ex.accInfo.Platform)
		if classifyErr != nil {
			classification, _ = gateway.Classify(dispatchRes.StatusCode, dispatchRes.Headers, raw, ex.accInfo.Platform)
			decision = gateway.AttemptRetryDecision{ClientStatus: clientStatusForUpstreamError(dispatchRes.StatusCode, classification.Class), AbortReason: "upstream_error"}
		}
		decision.ClientStatus = ex.remapClientStatusForUpstream(dispatchRes.StatusCode, decision.ClientStatus)
		recordModelCooldownOnUpstream404(ex.ctx, ex.d, ex.ident.TenantID, ex.acquiredAccountID, ex.upstreamModelID, dispatchRes.StatusCode, ex.requestID)
		abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, decision.AbortReason, ex.requestID, 0, nil)
		if ex.healthKeyOK {
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, signalFromClassification(dispatchRes.StatusCode, classification), dispatchRes.StatusCode, time.Since(startedAt), ex.requestID, rateLimitResetFromClassification(classification, time.Now()))
		}
		failure := classifiedFailureFromDecision("", clienterr.MessageFor(clienterr.CodeUpstreamDispatchError), classification, decision, nil)
		return nil, degradeFailureIfAbortFailed(ex.ctx, ex.requestID, failure, abortErr), false
	}
	ex.updateSessionWindowFromHeaders(dispatchRes.Headers)
	upstreamAdapter, err := protocolAdapterForBuffered(ex.d.Forwarder, ex.resolved.ProtocolFamily)
	if err != nil {
		if abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "upstream_adapter_error", ex.requestID, 0, nil); abortErr != nil {
			setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
		}
		writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusBadGateway, clienterr.CodeUpstreamAdapterError, err)
		return nil, nil, false
	}
	bufferedEnv, _, err := upstreamAdapter.ProviderResponseToCanonical(seedCtx, raw)
	if err != nil {
		if reconstructedEnv, _, ok := protosse.ReconstructBufferedFromSSE(upstreamAdapter, raw); ok && reconstructedEnv != nil {
			bufferedEnv = reconstructedEnv
		} else {
			if abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "canonical_response_error", ex.requestID, 0, nil); abortErr != nil {
				setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
			}
			if ex.healthKeyOK {
				recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, channelhealth.SignalChannelError, dispatchRes.StatusCode, time.Since(startedAt), ex.requestID, nil)
			}
			writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusBadGateway, clienterr.CodeCanonicalResponseError, err)
			return nil, nil, false
		}
	}
	if bufferedEnv != nil {
		_ = seed.ApplyToRequestMeta(&bufferedEnv.RequestMeta)
		enrichCanonicalRequestMeta(bufferedEnv, ex.upstreamModelID, ex.accInfo.Platform, ex.idempotencyHeader, ex.sessionHash)
	}
	return ex.finalizeBufferedEnvelope(w, bufferedEnv, dispatchRes.StatusCode, startedAt)
}

func (ex *chatExecution) updateSessionWindowFromHeaders(headers http.Header) {
	if ex == nil || ex.d.RateService == nil || ex.acquiredAccountID <= 0 {
		return
	}
	if err := ex.d.RateService.UpdateSessionWindow(ex.ctx, ex.acquiredAccountID, headers); err != nil {
		logInternalError(ex.ctx, ex.requestID, "session_window_update_failed", err)
	}
}

// NewMessagesHandler 是 /v1/messages 端点 handler。它复用 chat completions
// 管线，只把 billing endpoint family 标为 messages。
func NewMessagesHandler(d ChatHandlerDeps) http.HandlerFunc {
	if d.EndpointFamily == "" {
		d.EndpointFamily = "messages"
	}
	return NewChatCompletionsHandler(d)
}

// NewResponsesHandler 是 /v1/responses 端点 handler。它复用同一条
// auth/routing/billing/forwarding pipeline，仅把 billing endpoint family 标为
// openai_responses；真实上下游协议仍由 registry 的 ProtocolFamily 决定。
func NewResponsesHandler(d ChatHandlerDeps) http.HandlerFunc {
	if d.EndpointFamily == "" {
		d.EndpointFamily = "openai_responses"
	}
	return NewChatCompletionsHandler(d)
}

// platformSettingsReader is the minimal interface for reading a single platform setting.
// *platformsettings.Service satisfies this interface.
type platformSettingsReader interface {
	Get(ctx context.Context, key platformsettings.SettingKey) (platformsettings.StoredSetting, error)
}

// warmupInterceptEnabled reports whether warmup interception is enabled.
// Returns false (safe default) when PlatformSettings is nil or the setting is absent/invalid.
func warmupInterceptEnabled(ctx context.Context, settings platformSettingsReader) bool {
	if settings == nil {
		return false
	}
	s, err := settings.Get(ctx, platformsettings.KeyWarmupInterceptEnabled)
	if err != nil {
		return false
	}
	return s.Value == "true"
}

// clientBetaTokens 惰性解析并缓存客户端 anthropic-beta 请求头(DM-03)。
// 产出只被 anthropic 族出站 adapter 消费(与凭据 beta 合并去重;OAuth 池
// 账号侧另有白名单);attempt 重试不重复解析。
func (ex *chatExecution) clientBetaTokens() []string {
	if ex == nil || ex.r == nil {
		return nil
	}
	if !ex.inboundBetaTokensParsed {
		ex.inboundBetaTokensParsed = true
		ex.inboundBetaTokens = provider.ParseInboundBetaTokens(ex.r.Header.Values("Anthropic-Beta"))
	}
	return ex.inboundBetaTokens
}

// clientTailMessageRole 解析请求体最后一条消息的角色,供输入审核区分
// "新用户轮"与"agent 工具循环重发轮"(DM-16)。按客户端协议取字段:
// chat/anthropic=messages[].role;gemini=contents[].role;responses=input
// (字符串=用户输入;数组取尾项 role,无 role 的工具输出项归 "tool")。
// 解析失败返回 ""(未知,审核按首轮处理)。
func clientTailMessageRole(clientProtocol proto.ClientProtocol, body []byte) string {
	if len(body) == 0 {
		return ""
	}
	switch clientProtocol {
	case proto.ClientProtocolOpenAIResponses:
		var req struct {
			Input json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(body, &req); err != nil || len(req.Input) == 0 {
			return ""
		}
		if req.Input[0] == '"' {
			return "user"
		}
		var items []struct {
			Role string `json:"role"`
		}
		if err := json.Unmarshal(req.Input, &items); err != nil || len(items) == 0 {
			return ""
		}
		if role := strings.TrimSpace(items[len(items)-1].Role); role != "" {
			return strings.ToLower(role)
		}
		return "tool"
	case proto.ClientProtocolGemini:
		var req struct {
			Contents []struct {
				Role string `json:"role"`
			} `json:"contents"`
		}
		if err := json.Unmarshal(body, &req); err != nil || len(req.Contents) == 0 {
			return ""
		}
		return strings.ToLower(strings.TrimSpace(req.Contents[len(req.Contents)-1].Role))
	default:
		var req struct {
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(body, &req); err != nil || len(req.Messages) == 0 {
			return ""
		}
		return strings.ToLower(strings.TrimSpace(req.Messages[len(req.Messages)-1].Role))
	}
}

// classifyPoolSelectFailure maps a pool.Selector error to its HTTP failure and
// claim abort (incl. the SEC-249/250 per-key rate-limit 429). nil err → nil.
// Kept here rather than in its own file to stay within the gatewayhttp
// package's file-count budget.
func (ex *chatExecution) classifyPoolSelectFailure(w http.ResponseWriter, err error) *classifiedAttemptFailure {
	if err == nil {
		return nil
	}
	abort := func(reason string) error {
		return ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, reason, ex.requestID, 0, ex.protocolLoss)
	}
	switch {
	case errors.Is(err, pool.ErrKeyRateLimited):
		if e := abort("key_rate_limited"); e != nil && w != nil {
			setAbortFailedHeader(w, ex.ctx, ex.requestID, e)
		}
		f := terminalLocalAttemptFailure(http.StatusTooManyRequests, clienterr.CodeKeyRateLimited, clienterr.MessageFor(clienterr.CodeKeyRateLimited), "key_rate_limited", err)
		f.RetryAfterSeconds = 1
		return f
	case errors.Is(err, pool.ErrNoEligibleAccount), errors.Is(err, pool.ErrNoSlotAvailable), errors.Is(err, pool.ErrAllChannelsDegraded):
		f := retryableLocalAttemptFailure(http.StatusServiceUnavailable, clienterr.CodeNoCapacity, clienterr.MessageFor(clienterr.CodeNoCapacity), "pool_no_capacity", gateway.UpstreamError5xx, err)
		f.RetryAfterSeconds = 5
		return degradeFailureIfAbortFailed(ex.ctx, ex.requestID, f, abort("pool_no_capacity"))
	case errors.Is(err, pool.ErrClaimRace):
		if e := abort("claim_race"); e != nil && w != nil {
			setAbortFailedHeader(w, ex.ctx, ex.requestID, e)
		}
		f := terminalLocalAttemptFailure(http.StatusConflict, clienterr.CodeClaimRace, clienterr.MessageFor(clienterr.CodeClaimRace), "claim_race", err)
		f.RetryAfterSeconds = 1
		return f
	default:
		f := retryableLocalAttemptFailure(http.StatusInternalServerError, clienterr.CodePoolSelectError, clienterr.MessageFor(clienterr.CodePoolSelectError), "pool_select_error", gateway.UpstreamError5xx, err)
		return degradeFailureIfAbortFailed(ex.ctx, ex.requestID, f, abort("pool_select_error"))
	}
}
