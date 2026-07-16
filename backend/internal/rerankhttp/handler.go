package rerankhttp

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
)

const (
	endpointFamilyRerank = "rerank"
	upstreamRerankPath   = "/v1/rerank"
	maxRequestBodyBytes  = 2 << 20
	maxUpstreamBodyBytes = 16 << 20
	maxRerankDocuments   = 1000
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
}

type execution struct {
	d         Deps
	r         *http.Request
	ctx       context.Context
	startedAt time.Time
	ident     auth.Identity
	body      []byte
	req       rerankRequest
	requestID string

	resolved         registry.Resolved
	plan             router.RoutePlan
	attempt          router.AttemptPlan
	upstreamModelID  string
	payloadHash      string
	logicalRequestID string
	idempotencyKey   string
	searchUnits      int
	predictedCost    decimal.Decimal
	costSnapshot     string
	pending          bool
	reserveRes       *billing.ReserveResult
	selRes           *pool.SelectionResult
	accInfo          provider.AccountInfo
	cred             provider.Credential
	classTransition  *bindingfallback.Transition
}

func NewRerankHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !configured(d) {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "rerank handler dependency unset")
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

		body, req, ok := validateRequest(w, r)
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
			d:             d,
			r:             r,
			ctx:           ctx,
			startedAt:     time.Now().UTC(),
			ident:         ident,
			body:          body,
			req:           req,
			requestID:     requestID,
			payloadHash:   bodyHash(body),
			searchUnits:   searchUnitsForDocuments(len(req.Documents)),
			predictedCost: decimal.Zero,
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

func (ex *execution) run(w http.ResponseWriter) {
	budget := fallbackexec.NormalBudget(ex.plan)
	var coordinator bindingfallback.Coordinator
	for i := 0; i < budget; i++ {
		outcome := ex.runAttempt(w, ex.plan.Attempts[i], i+1)
		if outcome.done {
			return
		}
		decision, phase := fallbackexec.ObserveFailure(&coordinator, outcome.failure, ex.plan, i+1 < budget, false, true)
		switch decision.Action {
		case bindingfallback.ActionContinuePrimary:
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
	ex.activateAttempt(attempt)
	ex.reserveRes = nil
	ex.selRes = nil
	if !ex.reserve(w) {
		return attemptOutcome{done: true}
	}
	if failure := ex.selectAccount(w, attemptSeq); failure != nil {
		return attemptOutcome{failure: failure}
	}
	if !ex.resolveCredential(w) {
		return attemptOutcome{done: true}
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
