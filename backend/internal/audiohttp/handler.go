package audiohttp

import (
	"context"
	"errors"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/apikeymodelallow"
	"github.com/BloomingProsperity/HUAKAI/internal/audiopricing"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
	fallbackexec "github.com/BloomingProsperity/HUAKAI/internal/bindingfallback/executor"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/quotaenforce"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementrecovery"
	"github.com/BloomingProsperity/HUAKAI/internal/upstreamfeedback"
)

const (
	endpointFamilyAudio   = "audio"
	maxJSONBodyBytes      = 1 << 20
	maxMultipartBodyBytes = 25 << 20
	maxUpstreamBodyBytes  = 32 << 20
	maxSpeechInputRunes   = 4096
)

type authResolver interface {
	Resolve(context.Context, *http.Request) (auth.Identity, error)
}

type pricingRatioResolver interface {
	Resolve(ctx context.Context, tenantID, poolGroupID int64) (decimal.Decimal, error)
}

type dispatcher interface {
	Dispatch(context.Context, gateway.DispatchInput) (*gateway.DispatchResult, error)
}

type Deps struct {
	Auth                  authResolver
	Registry              registry.Registry
	Router                router.Router
	ClaimGate             billing.ClaimGate
	QuotaReserver         quotaenforce.Reserver
	RateTables            billing.RateTableSource
	PricingRatioResolver  pricingRatioResolver
	Selector              pool.Selector
	CredentialVault       provider.CredentialVault
	Dispatcher            dispatcher
	Settler               billing.Settler
	BillingPolicyResolver *billing.PolicyResolver
	BillingPolicyVersion  string
	RequestClass          string
	// SettleRecoveryDLQ 交付后结算失败的 durable 兜底队列(防掉钱)。nil 时退回原行为。
	SettleRecoveryDLQ settlementrecovery.Enqueuer
	// Feedback 把上游结果喂账号健康 FSM(坏号冷却→下次选号自动跳过=自动换号)。nil 时 no-op。
	Feedback *upstreamfeedback.Observer
	// RetryBudget 每租户重试预算限流,防重试风暴(nil 不限)。
	RetryBudget retryBudgetGate
}

type execution struct {
	d           Deps
	r           *http.Request
	ctx         context.Context
	startedAt   time.Time
	endpoint    audioEndpoint
	ident       auth.Identity
	body        []byte
	contentType string
	req         audioRequest
	requestID   string

	resolved          registry.Resolved
	plan              router.RoutePlan
	attempt           router.AttemptPlan
	upstreamModelID   string
	payloadHash       string
	logicalRequestID  string
	idempotencyKey    string
	reserveRes        *billing.ReserveResult
	selRes            *pool.SelectionResult
	accInfo           provider.AccountInfo
	cred              provider.Credential
	catalog           *audiopricing.Catalog
	scheme            audiopricing.Scheme
	charCount         int
	estimatedDuration durationEstimate
	predictedCost     decimal.Decimal
	costSnapshot      string
	pending           bool
	classTransition   *bindingfallback.Transition
	// excludedAccounts 本请求已失败账号,重试选号经 SelectionRequest.ExcludedAccounts 跳过。
	excludedAccounts map[int64]struct{}
}

func NewSpeechHandler(d Deps) http.HandlerFunc {
	return newHandler(d, audioEndpointSpeech)
}

func NewTranscriptionHandler(d Deps) http.HandlerFunc {
	return newHandler(d, audioEndpointTranscriptions)
}

func NewTranslationHandler(d Deps) http.HandlerFunc {
	return newHandler(d, audioEndpointTranslations)
}

func newHandler(d Deps, endpoint audioEndpoint) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !configured(d) {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "audio handler dependency unset")
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
		body, contentType, req, ok := validateAudioRequest(w, r, endpoint)
		if !ok {
			return
		}
		if !apikeymodelallow.AllowsCSV(ident.AllowedModels, req.Model) {
			writeJSONError(w, http.StatusForbidden, "model_not_allowed", "api key is not allowed to use this model")
			return
		}
		requestID := uuid.NewString()
		ctx = context.WithValue(ctx, middleware.RequestIDKey, requestID)
		r = r.WithContext(ctx)
		w.Header().Set(middleware.RequestIDHeader, requestID)
		ex := &execution{
			d:           d,
			r:           r,
			ctx:         ctx,
			startedAt:   time.Now().UTC(),
			endpoint:    endpoint,
			ident:       ident,
			body:        body,
			contentType: contentType,
			req:         req,
			requestID:   requestID,
			payloadHash: bodyHash(body),
		}
		if endpoint == audioEndpointSpeech {
			ex.charCount = utf8.RuneCountInString(req.Input)
		} else {
			estimate, err := estimateDuration(req.File)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid_audio_file", "audio file duration could not be estimated")
				return
			}
			ex.estimatedDuration = estimate
		}
		if !ex.prepareRoute(w) {
			return
		}
		ex.run(w)
	}
}

func configured(d Deps) bool {
	return d.Auth != nil && d.Registry != nil && d.Router != nil &&
		d.ClaimGate != nil && d.RateTables != nil && d.Selector != nil &&
		d.CredentialVault != nil && d.Dispatcher != nil && d.Settler != nil
}

func (ex *execution) prepareRoute(w http.ResponseWriter) bool {
	resolved, err := ex.d.Registry.ResolveModel(ex.ctx, ex.req.Model, ex.ident.TenantID)
	if errors.Is(err, registry.ErrRegistryBackend) {
		writeJSONError(w, http.StatusServiceUnavailable, "registry_backend_error", "registry backend transient failure")
		return false
	}
	if errors.Is(err, registry.ErrUnknownModel) || errors.Is(err, registry.ErrModelDisabled) || errors.Is(err, registry.ErrTenantNoAccess) {
		writeJSONError(w, http.StatusNotFound, "model_not_available", "model not available")
		return false
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, clienterr.CodeRegistryUnknownError, clienterr.MessageFor(clienterr.CodeRegistryUnknownError))
		return false
	}
	if !hasAudioEndpointCapability(resolved.Capabilities, ex.endpoint) {
		writeJSONError(w, http.StatusNotFound, "model_not_available", "model does not support this audio operation")
		return false
	}
	ex.resolved = resolved
	plan, err := ex.d.Router.Plan(ex.ctx, router.PlanInput{
		Context: router.RequestContext{TenantID: ex.ident.TenantID, UserID: ex.ident.UserID, APIKeyID: ex.ident.APIKeyID, RequestID: ex.requestID},
		Model:   routerResolvedModel(resolved),
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, clienterr.CodeRouterPlanError, clienterr.MessageFor(clienterr.CodeRouterPlanError))
		return false
	}
	if len(plan.Attempts) == 0 {
		writeJSONError(w, http.StatusInternalServerError, clienterr.CodeRouterPlanError, "router returned no attempts")
		return false
	}
	requireAudioEndpointCapability(&plan, ex.endpoint)
	ex.plan = plan
	ex.activateAttempt(plan.Attempts[0])
	return true
}

func (ex *execution) run(w http.ResponseWriter) {
	budget := fallbackexec.NormalBudget(ex.plan)
	authFailoverUsed := false
	var coordinator bindingfallback.Coordinator
	for i := 0; i < budget; i++ {
		planIdx := i
		if planIdx >= len(ex.plan.Attempts) {
			planIdx = len(ex.plan.Attempts) - 1
		}
		outcome := ex.runAttempt(w, ex.plan.Attempts[planIdx], i+1)
		if outcome.done {
			return
		}
		if ex.selRes != nil {
			ex.excludeAccount(ex.selRes.AccountID)
		}
		// 上游 401/发网前凭据错配走授权换号子预算:整请求最多一次、预算末位也放行
		// (必要时扩一格),不进 Coordinator 的跨类终态门(auth 对类别转移保持
		// fail-closed)。第二次授权失败落到 Coordinator 原地终止,防 401 风暴烧号。
		if f := outcome.failure; f != nil && f.AuthFailoverEligible && f.RetryPermitted {
			if !authFailoverUsed && (ex.d.RetryBudget == nil || ex.d.RetryBudget.Allow(ex.ident.TenantID)) {
				authFailoverUsed = true
				if i+1 >= budget {
					budget++
				}
				continue
			}
		}
		decision, phase := fallbackexec.ObserveFailure(&coordinator, outcome.failure, ex.plan, i+1 < budget, false, true)
		switch decision.Action {
		case bindingfallback.ActionContinuePrimary:
			if ex.d.RetryBudget != nil && !ex.d.RetryBudget.Allow(ex.ident.TenantID) {
				fallbackexec.WriteHTTP(w, outcome.failure)
				return
			}
			continue
		case bindingfallback.ActionTransition:
			ex.classTransition = &decision.Transition
			target := ex.runAttempt(w, phase.Attempts[0], i+2)
			if !target.done {
				fallbackexec.WriteHTTP(w, target.failure)
			}
			return
		default:
			fallbackexec.WriteHTTP(w, outcome.failure)
			return
		}
	}
}

type attemptOutcome struct {
	failure *fallbackexec.Failure
	done    bool
}

func (ex *execution) runAttempt(w http.ResponseWriter, attempt router.AttemptPlan, attemptSeq int) attemptOutcome {
	// validateAudioRequest 已把 JSON/multipart 限长读入不可变 []byte；每个 attempt
	// 都从该缓冲重新生成出站 body，因此跨账号/跨类不会复用已消费的流。
	ex.activateAttempt(attempt)
	ex.reserveRes = nil
	ex.selRes = nil
	ex.accInfo = provider.AccountInfo{}
	if err := ex.preparePricing(); err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodePricingUnavailable, clienterr.MessageFor(clienterr.CodePricingUnavailable))
		return attemptOutcome{done: true}
	}
	if !ex.reserve(w) {
		return attemptOutcome{done: true}
	}
	// 幂等重放续用被 abort 的 claim 时,billing 返回的 attempt_seq 是权威值
	// (跨请求单调),选号与结算必须用同一个,否则 settle 身份对不上。
	attemptSeq = authoritativeAttemptSeq(ex.reserveRes, attemptSeq)
	if failure := ex.selectAccount(w, attemptSeq); failure != nil {
		return attemptOutcome{failure: failure}
	}
	if !ex.resolveCredential(w) {
		return attemptOutcome{done: true}
	}
	if failure := ex.credentialCompatibilityFailure(w); failure != nil {
		return attemptOutcome{failure: failure}
	}
	return ex.dispatchAndSettle(w, attemptSeq)
}

func (ex *execution) activateAttempt(attempt router.AttemptPlan) {
	ex.attempt = attempt
	ex.upstreamModelID = attempt.UpstreamModelID
	if ex.upstreamModelID == "" {
		ex.upstreamModelID = ex.resolved.ProviderModelID
	}
	if ex.upstreamModelID == "" {
		ex.upstreamModelID = ex.req.Model
	}
}

type retryBudgetGate interface {
	Allow(tenantID int64) bool
}
