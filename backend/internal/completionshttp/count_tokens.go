package completionshttp

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/apikeymodelallow"
	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
	fallbackexec "github.com/BloomingProsperity/HUAKAI/internal/bindingfallback/executor"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/servingcapability"
)

func NewCountTokensHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !configuredForCountTokens(d) {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "count_tokens handler dependency unset")
			return
		}
		ident, ok := resolveAuth(w, d.Auth, r)
		if !ok {
			return
		}
		body, req, ok := validateCountTokensRequest(w, r)
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
			requestID:      requestID,
			endpointFamily: endpointFamilyCountTokens,
			upstreamPath:   upstreamCountTokensPath,
			payloadHash:    bodyHash(body),
		}
		if !ex.prepareRoute(w, req.Model) {
			return
		}
		if ex.resolved.ProtocolFamily == registrydefault.ProtocolAnthropicClaudeSession {
			writeJSONError(w, http.StatusNotImplemented, "count_tokens_not_supported_for_protocol", "count_tokens is not enabled for Claude session serving")
			return
		}
		ex.runCountTokens(w, req.Model)
	}
}

func (ex *execution) runCountTokens(w http.ResponseWriter, requestedModel string) {
	budget := fallbackexec.NormalBudget(ex.plan)
	authFailoverUsed := false
	var coordinator bindingfallback.Coordinator
	for i := 0; i < budget; i++ {
		planIdx := i
		if planIdx >= len(ex.plan.Attempts) {
			planIdx = len(ex.plan.Attempts) - 1
		}
		outcome := ex.runCountTokensAttempt(w, ex.plan.Attempts[planIdx], i+1, requestedModel)
		if outcome.done {
			return
		}
		if ex.selRes != nil {
			ex.excludeAccount(ex.selRes.AccountID)
		}
		if failure := outcome.failure; failure != nil && failure.AuthFailoverEligible && failure.RetryPermitted {
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
			target := ex.runCountTokensAttempt(w, phase.Attempts[0], i+2, requestedModel)
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

func (ex *execution) runCountTokensAttempt(w http.ResponseWriter, attempt router.AttemptPlan, attemptSeq int, requestedModel string) attemptOutcome {
	ex.activateAttempt(attempt, requestedModel)
	ex.reserveRes = nil
	ex.selRes = nil
	if failure := ex.selectAccount(w, attemptSeq, requestedModel); failure != nil {
		return attemptOutcome{failure: failure}
	}
	if !ex.resolveCredential(w) {
		ex.releaseCountTokensSelection()
		return attemptOutcome{done: true}
	}
	if err := servingcapability.ValidateRuntimeAccountCompatibility(ex.resolved.ProtocolFamily, ex.cred, ex.accInfo); err != nil {
		ex.releaseCountTokensSelection()
		return attemptOutcome{failure: fallbackexec.CredentialCompatibilityFailure()}
	}
	outcome := ex.dispatchCountTokens(w)
	ex.releaseCountTokensSelection()
	return outcome
}

func (ex *execution) releaseCountTokensSelection() {
	if ex.selRes == nil || ex.selRes.Release == nil {
		return
	}
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ex.ctx), 2*time.Second)
	defer cancel()
	_ = ex.selRes.Release(releaseCtx)
}

func (ex *execution) dispatchCountTokens(w http.ResponseWriter) attemptOutcome {
	res, err := ex.d.Dispatcher.Dispatch(ex.ctx, gateway.DispatchInput{
		ProtocolFamily:    ex.resolved.ProtocolFamily,
		EndpointPath:      ex.upstreamPath,
		UpstreamModelID:   ex.upstreamModelID,
		InboundBody:       ex.body,
		Account:           ex.accInfo,
		Credential:        ex.cred,
		InboundBetaTokens: provider.ParseInboundBetaTokens(ex.r.Header.Values("Anthropic-Beta")),
	})
	if err != nil {
		ex.observeDispatchError(err)
		return attemptOutcome{failure: fallbackexec.DispatchFailure(err)}
	}
	if res == nil || res.UpstreamReader == nil {
		ex.observeChannelError(0)
		return attemptOutcome{failure: fallbackexec.EmptyResponseFailure()}
	}
	defer closeDispatchResult(res)
	raw, readErr := readUpstreamBody(res.UpstreamReader)
	if readErr != nil {
		ex.observeChannelError(res.StatusCode)
		return attemptOutcome{failure: fallbackexec.ReadFailure(readErr)}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		observed := ex.observeHTTPError(res, raw)
		return attemptOutcome{failure: fallbackexec.UpstreamFailureFromDecision(res.StatusCode, raw, observed.Decision, observed.Classification)}
	}
	if strings.TrimSpace(string(raw)) == "" {
		ex.observeChannelError(res.StatusCode)
		return attemptOutcome{failure: fallbackexec.EmptyResponseFailure()}
	}
	ex.observeSuccess(res)
	copyAllowedHeaders(w.Header(), res.Headers)
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
	return attemptOutcome{done: true}
}
