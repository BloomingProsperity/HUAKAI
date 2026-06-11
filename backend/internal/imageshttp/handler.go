package imageshttp

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
	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/imagepricing"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/quotaenforce"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

const (
	endpointFamilyImages = "images"
	maxRequestBodyBytes  = 4 << 20
	maxUpstreamBodyBytes = 32 << 20
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
	ClientIPResolver      *clientip.Resolver
}

type execution struct {
	d         Deps
	r         *http.Request
	ctx       context.Context
	startedAt time.Time
	endpoint  imageEndpoint
	ident     auth.Identity
	body      []byte
	req       imageRequest
	requestID string

	resolved         registry.Resolved
	plan             router.RoutePlan
	attempt          router.AttemptPlan
	upstreamModelID  string
	payloadHash      string
	logicalRequestID string
	idempotencyKey   string
	reserveRes       *billing.ReserveResult
	selRes           *pool.SelectionResult
	accInfo          provider.AccountInfo
	cred             provider.Credential

	catalog       *imagepricing.Catalog
	scheme        imagepricing.Scheme
	size          string
	quality       string
	amount        int
	predictedCost decimal.Decimal
	costSnapshot  string
	pending       bool
	// deliveredImageCount 是 family 响应翻译实际交付给客户端的图片张数
	// (0 = 未知/未翻译,按请求 amount 计费)。per_image 计费在 settle 时
	// 用它做"按交付数对账":上游(如 Replicate model-specific num_outputs
	// 被忽略只回 1 张)交付少于请求数时,不得按请求数多收用户钱。
	deliveredImageCount int
}

func NewGenerationsHandler(d Deps) http.HandlerFunc {
	return newHandler(d, imageEndpointGenerations)
}

func NewEditsHandler(d Deps) http.HandlerFunc {
	return newHandler(d, imageEndpointEdits)
}

func NewVariationsHandler(d Deps) http.HandlerFunc {
	return newHandler(d, imageEndpointVariations)
}

func newHandler(d Deps, endpoint imageEndpoint) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !configured(d) {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "images handler dependency unset")
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
		body, req, ok := validateRequest(w, r, endpoint)
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
			req:         req,
			requestID:   requestID,
			payloadHash: bodyHash(body),
			amount:      req.Amount(),
			quality:     req.NormalizedQuality(),
		}
		if !ex.prepareRoute(w) || !ex.validateFamilyConstraints(w) || !ex.preparePricing(w) {
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
	ex.activateAttempt(plan.Attempts[0])
	return true
}

func (ex *execution) run(w http.ResponseWriter) {
	if !ex.reserve(w) || !ex.selectAccount(w, 1) || !ex.resolveCredential(w) {
		return
	}
	_ = ex.dispatchAndSettle(w, 1)
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
