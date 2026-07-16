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

type retryBudgetGate interface {
	Allow(tenantID int64) bool
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
	SettleRecoveryDLQ     settlementrecovery.Enqueuer
	BillingPolicyResolver *billing.PolicyResolver
	BillingPolicyVersion  string
	RequestClass          string
	Feedback              *upstreamfeedback.Observer
	RetryBudget           retryBudgetGate
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
	excludedAccounts  map[int64]struct{}
	catalog           *audiopricing.Catalog
	scheme            audiopricing.Scheme
	charCount         int
	estimatedDuration durationEstimate
	predictedCost     decimal.Decimal
	costSnapshot      string
	pending           bool
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
	budget := effectiveAttemptBudget(ex.plan)
	authFailoverUsed := false
	attemptCap := budget
	for i := 0; i < attemptCap; i++ {
		planIdx := i
		if planIdx >= len(ex.plan.Attempts) {
			planIdx = len(ex.plan.Attempts) - 1
		}
		ex.activateAttempt(ex.plan.Attempts[planIdx])
		if err := ex.preparePricing(); err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodePricingUnavailable, clienterr.MessageFor(clienterr.CodePricingUnavailable))
			return
		}
		if !ex.reserve(w) {
			return
		}
		attemptSeq := authoritativeAttemptSeq(ex.reserveRes, i+1)
		if !ex.selectAccount(w, attemptSeq) || !ex.resolveCredential(w) {
			return
		}
		outcome := attemptOutcome{Failure: ex.credentialCompatibilityFailure(w)}
		if outcome.Failure == nil {
			outcome = ex.dispatchAndSettle(w, attemptSeq)
		}
		if outcome.Done || outcome.Failure == nil {
			return
		}
		if outcome.Failure.Decision.SwitchAccount && ex.accInfo.AccountID > 0 {
			ex.excludeAccount(ex.accInfo.AccountID)
		}
		retry, consumeAuthBudget := shouldRetryFailure(
			ex.plan,
			outcome.Failure,
			i+1 >= attemptCap,
			authFailoverUsed,
		)
		if !retry || (ex.d.RetryBudget != nil && !ex.d.RetryBudget.Allow(ex.ident.TenantID)) {
			writeAttemptFailure(w, outcome.Failure)
			return
		}
		if consumeAuthBudget {
			authFailoverUsed = true
			if i+1 >= attemptCap {
				attemptCap++
			}
		}
		ex.prepareNextAttempt()
	}
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
