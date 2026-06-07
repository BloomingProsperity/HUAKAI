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
		ex.runCountTokens(w, req.Model)
	}
}

func (ex *execution) runCountTokens(w http.ResponseWriter, requestedModel string) {
	budget := ex.plan.AttemptBudget
	if budget <= 0 || budget > len(ex.plan.Attempts) {
		budget = len(ex.plan.Attempts)
	}
	for i := 0; i < budget; i++ {
		ex.activateAttempt(ex.plan.Attempts[i], requestedModel)
		if !ex.selectAccount(w, i+1, requestedModel) || !ex.resolveCredential(w) {
			return
		}
		switch ex.dispatchCountTokens(w) {
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

func (ex *execution) dispatchCountTokens(w http.ResponseWriter) int {
	res, err := ex.d.Dispatcher.Dispatch(ex.ctx, gateway.DispatchInput{
		ProtocolFamily:  ex.resolved.ProtocolFamily,
		EndpointPath:    ex.upstreamPath,
		UpstreamModelID: ex.upstreamModelID,
		InboundBody:     ex.body,
		Account:         ex.accInfo,
		Credential:      ex.cred,
	})
	if err != nil {
		return attemptRetryable
	}
	if res == nil || res.UpstreamReader == nil {
		return attemptRetryable
	}
	defer closeDispatchResult(res)
	raw, readErr := readUpstreamBody(res.UpstreamReader)
	if readErr != nil {
		writeJSONError(w, http.StatusBadGateway, clienterr.CodeUpstreamReadError, clienterr.MessageFor(clienterr.CodeUpstreamReadError))
		return attemptDone
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		writeJSONError(w, http.StatusBadGateway, clienterr.CodeUpstreamDispatchError, clienterr.MessageFor(clienterr.CodeUpstreamDispatchError))
		return attemptDone
	}
	copyAllowedHeaders(w.Header(), res.Headers)
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
	return attemptDone
}
