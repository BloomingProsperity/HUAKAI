package completionshttp

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/apikeymodelallow"
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
	"github.com/BloomingProsperity/HUAKAI/internal/settlementintent"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementrecovery"
	"github.com/BloomingProsperity/HUAKAI/internal/upstreamfeedback"
)

const (
	endpointFamilyCompletions = "completions"
	endpointFamilyCountTokens = "messages_count_tokens"
	upstreamCompletionsPath   = "/v1/completions"
	upstreamCountTokensPath   = "/v1/messages/count_tokens"
	maxRequestBodyBytes       = 2 << 20
	maxUpstreamBodyBytes      = 16 << 20
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
	Auth                    authResolver
	Registry                registry.Registry
	Router                  router.Router
	ClaimGate               billing.ClaimGate
	QuotaReserver           quotaenforce.Reserver
	RateTables              billing.RateTableSource
	PricingRatioResolver    pricingRatioResolver
	Selector                pool.Selector
	CredentialVault         provider.CredentialVault
	Dispatcher              dispatcher
	Settler                 billing.Settler
	SettlementIntents       settlementintent.Store
	SettlementIntentEnabled bool
	BillingPolicyResolver   *billing.PolicyResolver
	BillingPolicyVersion    string
	RequestClass            string
	// SettleRecoveryDLQ 是流式交付后(响应已发给客户端)settle 失败时的 durable 兜底队列。
	// 镜像 gatewayhttp chat 路径的同名依赖。nil 时退回原行为(仅置 X-Huakai-Settle-Failed 头)，
	// 不破坏现有 wiring；生产由 cmd/gateway/routes.go 注入 d.dlqService。
	SettleRecoveryDLQ settlementrecovery.Enqueuer
	// Feedback 把上游结果喂账号健康 FSM(坏号冷却→下次选号自动跳过=自动换号)。
	// nil 时健康观测为 no-op,不破坏 wiring;生产由 cmd/gateway/routes.go 注入。
	Feedback *upstreamfeedback.Observer
	// RetryBudget 每租户重试预算限流,防重试风暴(nil 不限)。
	RetryBudget retryBudgetGate
}

type execution struct {
	d              Deps
	r              *http.Request
	ctx            context.Context
	startedAt      time.Time
	ident          auth.Identity
	body           []byte
	req            completionRequest
	promptTexts    []string
	requestID      string
	endpointFamily string
	upstreamPath   string

	resolved         registry.Resolved
	plan             router.RoutePlan
	attempt          router.AttemptPlan
	upstreamModelID  string
	payloadHash      string
	logicalRequestID string
	idempotencyKey   string
	inputEstimate    int
	reserveRes       *billing.ReserveResult
	selRes           *pool.SelectionResult
	accInfo          provider.AccountInfo
	cred             provider.Credential
	classTransition  *bindingfallback.Transition
	// excludedAccounts 本请求内已失败的账号,重试选号经 SelectionRequest.ExcludedAccounts 跳过。
	excludedAccounts map[int64]struct{}
	settlementIntent *settlementintent.Tracker
}

func NewCompletionsHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !configuredForCompletions(d) {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "completions handler dependency unset")
			return
		}
		ident, ok := resolveAuth(w, d.Auth, r)
		if !ok {
			return
		}
		body, req, prompts, ok := validateCompletionRequest(w, r)
		if !ok {
			return
		}
		if !apikeymodelallow.AllowsCSV(ident.AllowedModels, req.Model) {
			writeJSONError(w, http.StatusForbidden, "model_not_allowed", "api key is not allowed to use this model")
			return
		}
		requestID := uuid.NewString()
		ctx := context.WithValue(r.Context(), middleware.RequestIDKey, requestID)
		r = r.WithContext(ctx)
		w.Header().Set(middleware.RequestIDHeader, requestID)
		ex := &execution{
			d:                d,
			r:                r,
			ctx:              ctx,
			startedAt:        time.Now().UTC(),
			ident:            ident,
			body:             body,
			req:              req,
			promptTexts:      prompts,
			requestID:        requestID,
			endpointFamily:   endpointFamilyCompletions,
			upstreamPath:     upstreamCompletionsPath,
			payloadHash:      bodyHash(body),
			inputEstimate:    estimateInputTokens(prompts),
			settlementIntent: settlementintent.NewTracker(d.SettlementIntents, d.SettlementIntentEnabled),
		}
		if !ex.prepareRoute(w, req.Model) {
			return
		}
		ex.runCompletions(w)
	}
}

func configuredForCompletions(d Deps) bool {
	return d.Auth != nil && d.Registry != nil && d.Router != nil &&
		d.ClaimGate != nil && d.RateTables != nil && d.Selector != nil &&
		d.CredentialVault != nil && d.Dispatcher != nil && d.Settler != nil
}

func configuredForCountTokens(d Deps) bool {
	return d.Auth != nil && d.Registry != nil && d.Router != nil &&
		d.Selector != nil && d.CredentialVault != nil && d.Dispatcher != nil
}

func resolveAuth(w http.ResponseWriter, resolver authResolver, r *http.Request) (auth.Identity, bool) {
	ident, err := resolver.Resolve(r.Context(), r)
	if errors.Is(err, auth.ErrAuthMisconfigured) {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "auth tables unavailable")
		return auth.Identity{}, false
	}
	if errors.Is(err, auth.ErrAuthBackend) {
		writeJSONError(w, http.StatusServiceUnavailable, "auth_backend_error", "auth backend transient failure")
		return auth.Identity{}, false
	}
	if errors.Is(err, auth.ErrForbidden) {
		writeJSONError(w, http.StatusForbidden, "forbidden", "api key policy forbids this request")
		return auth.Identity{}, false
	}
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized", "invalid bearer")
		return auth.Identity{}, false
	}
	return ident, true
}

func (ex *execution) prepareRoute(w http.ResponseWriter, model string) bool {
	resolved, err := ex.d.Registry.ResolveModel(ex.ctx, model, ex.ident.TenantID)
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
	ex.plan = plan
	return true
}

func (ex *execution) activateAttempt(attempt router.AttemptPlan, requestedModel string) {
	ex.attempt = attempt
	ex.upstreamModelID = attempt.UpstreamModelID
	if ex.upstreamModelID == "" {
		ex.upstreamModelID = ex.resolved.ProviderModelID
	}
	if ex.upstreamModelID == "" {
		ex.upstreamModelID = requestedModel
	}
}

func (ex *execution) runCompletions(w http.ResponseWriter) {
	budget := fallbackexec.NormalBudget(ex.plan)
	authFailoverUsed := false
	var coordinator bindingfallback.Coordinator
	for i := 0; i < budget; i++ {
		planIdx := i
		if planIdx >= len(ex.plan.Attempts) {
			planIdx = len(ex.plan.Attempts) - 1
		}
		outcome := ex.runPaidAttempt(w, ex.plan.Attempts[planIdx], i+1, ex.req.Model)
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
			target := ex.runPaidAttempt(w, phase.Attempts[0], i+2, ex.req.Model)
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

func (ex *execution) runPaidAttempt(w http.ResponseWriter, attempt router.AttemptPlan, attemptSeq int, requestedModel string) attemptOutcome {
	ex.activateAttempt(attempt, requestedModel)
	ex.reserveRes = nil
	ex.selRes = nil
	if !ex.reserve(w) {
		return attemptOutcome{done: true}
	}
	// 幂等重放续用被 abort 的 claim 时,billing 返回的 attempt_seq 是权威值
	// (跨请求单调),选号与结算必须用同一个,否则 settle 身份对不上。
	attemptSeq = authoritativeAttemptSeq(ex.reserveRes, attemptSeq)
	if failure := ex.selectAccount(w, attemptSeq, requestedModel); failure != nil {
		return attemptOutcome{failure: failure}
	}
	if !ex.resolveCredential(w) {
		return attemptOutcome{done: true}
	}
	if failure := ex.credentialCompatibilityFailure(w); failure != nil {
		return attemptOutcome{failure: failure}
	}
	return ex.dispatchCompletionsAndSettle(w, attemptSeq)
}

type retryBudgetGate interface {
	Allow(tenantID int64) bool
}
