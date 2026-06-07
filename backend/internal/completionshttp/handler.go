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
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/quotaenforce"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
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
			d:              d,
			r:              r,
			ctx:            ctx,
			startedAt:      time.Now().UTC(),
			ident:          ident,
			body:           body,
			req:            req,
			promptTexts:    prompts,
			requestID:      requestID,
			endpointFamily: endpointFamilyCompletions,
			upstreamPath:   upstreamCompletionsPath,
			payloadHash:    bodyHash(body),
			inputEstimate:  estimateInputTokens(prompts),
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
	budget := ex.plan.AttemptBudget
	if budget <= 0 || budget > len(ex.plan.Attempts) {
		budget = len(ex.plan.Attempts)
	}
	for i := 0; i < budget; i++ {
		ex.activateAttempt(ex.plan.Attempts[i], ex.req.Model)
		if !ex.reserve(w) || !ex.selectAccount(w, i+1, ex.req.Model) || !ex.resolveCredential(w) {
			return
		}
		switch ex.dispatchCompletionsAndSettle(w, i+1) {
		case attemptDone:
			return
		case attemptRetryable:
			if i+1 < budget {
				continue
			}
			writeJSONError(w, http.StatusBadGateway, clienterr.CodeUpstreamDispatchError, clienterr.MessageFor(clienterr.CodeUpstreamDispatchError))
			return
		}
	}
}
