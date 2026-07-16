package completionshttp

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/apikeymodelallow"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
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
	budget := effectiveAttemptBudget(ex.plan)
	authFailoverUsed := false
	attemptCap := budget
	for i := 0; i < attemptCap; i++ {
		planIdx := i
		if planIdx >= len(ex.plan.Attempts) {
			planIdx = len(ex.plan.Attempts) - 1
		}
		ex.activateAttempt(ex.plan.Attempts[planIdx], requestedModel)
		if !ex.selectAccount(w, i+1, requestedModel) || !ex.resolveCredential(w) {
			return
		}
		outcome := ex.dispatchCountTokens(w)
		if outcome.Done {
			return
		}
		if outcome.Failure == nil {
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

func (ex *execution) dispatchCountTokens(w http.ResponseWriter) attemptOutcome {
	startedAt := time.Now()
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
		decision := dispatchFailureDecision(err)
		if ex.d.Feedback != nil {
			decision = normalizeDispatchFailureDecision(
				ex.d.Feedback.ObserveDispatchError(ex.ctx, ex.feedbackAttempt(startedAt), err),
			)
		}
		return attemptOutcome{Failure: &attemptFailure{Decision: decision}}
	}
	if res == nil || res.UpstreamReader == nil {
		ex.observeChannelError(startedAt, 0)
		return attemptOutcome{Failure: &attemptFailure{
			Decision: retryableAttemptDecision("upstream_empty_response", http.StatusBadGateway),
		}}
	}
	defer closeDispatchResult(res)
	raw, readErr := readUpstreamBody(res.UpstreamReader)
	if readErr != nil {
		ex.observeChannelError(startedAt, res.StatusCode)
		writeJSONError(w, http.StatusBadGateway, clienterr.CodeUpstreamReadError, clienterr.MessageFor(clienterr.CodeUpstreamReadError))
		return attemptOutcome{Done: true}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return attemptOutcome{Failure: ex.observeHTTPFailure(startedAt, res, raw)}
	}
	ex.observeSuccess(startedAt, res)
	copyAllowedHeaders(w.Header(), res.Headers)
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
	return attemptOutcome{Done: true}
}
