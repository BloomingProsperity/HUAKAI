package geminihttp

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/upstreamfeedback"
)

type countTokensAttemptFailure struct {
	decision       gateway.AttemptRetryDecision
	classification gateway.Classification
}

type countTokensAttemptOutcome struct {
	done    bool
	failure *countTokensAttemptFailure
}

func (relay *countTokensRelay) runCountTokens(
	w http.ResponseWriter,
	ctx context.Context,
	requestID string,
	model string,
	body []byte,
	ident auth.Identity,
	resolved registry.Resolved,
	plan router.RoutePlan,
) {
	budget := effectiveCountTokensAttemptBudget(plan)
	authFailoverUsed := false
	attemptCap := budget
	excludedAccounts := make(map[int64]struct{})
	for i := 0; i < attemptCap; i++ {
		planIdx := i
		if planIdx >= len(plan.Attempts) {
			planIdx = len(plan.Attempts) - 1
		}
		attempt := plan.Attempts[planIdx]
		upstreamModelID := firstNonEmpty(attempt.UpstreamModelID, resolved.ProviderModelID, model)
		selRes, ok := relay.selectAccount(w, ctx, ident, model, resolved, attempt, i+1, excludedAccounts)
		if !ok {
			return
		}
		cred, accInfo, ok := relay.resolveCredential(w, ctx, ident, selRes.AccountID)
		if !ok {
			return
		}
		outcome := relay.dispatchCountTokensAttempt(
			w,
			ctx,
			requestID,
			ident.TenantID,
			resolved.ProtocolFamily,
			upstreamModelID,
			body,
			cred,
			accInfo,
		)
		if outcome.done || outcome.failure == nil {
			return
		}
		if outcome.failure.decision.SwitchAccount && accInfo.AccountID > 0 {
			excludedAccounts[accInfo.AccountID] = struct{}{}
		}
		retry, consumeAuthBudget := shouldRetryCountTokensFailure(
			plan,
			outcome.failure,
			i+1 >= attemptCap,
			authFailoverUsed,
		)
		if !retry || (relay.retryBudget != nil && !relay.retryBudget.Allow(ident.TenantID)) {
			writeCountTokensAttemptFailure(w, outcome.failure)
			return
		}
		if consumeAuthBudget {
			authFailoverUsed = true
			if i+1 >= attemptCap {
				attemptCap++
			}
		}
	}
}

func (relay *countTokensRelay) dispatchCountTokensAttempt(
	w http.ResponseWriter,
	ctx context.Context,
	requestID string,
	tenantID int64,
	protocolFamily string,
	upstreamModelID string,
	body []byte,
	cred provider.Credential,
	accInfo provider.AccountInfo,
) countTokensAttemptOutcome {
	startedAt := time.Now()
	attempt := upstreamfeedback.Attempt{
		TenantID:       tenantID,
		Account:        accInfo,
		ProtocolFamily: protocolFamily,
		ModelKey:       upstreamModelID,
		RequestID:      requestID,
		StartedAt:      startedAt,
	}
	res, err := relay.d.Dispatcher.Dispatch(ctx, gateway.DispatchInput{
		ProtocolFamily:  protocolFamily,
		EndpointPath:    "/v1beta/models/" + url.PathEscape(upstreamModelID) + ":countTokens",
		UpstreamModelID: upstreamModelID,
		InboundBody:     body,
		Account:         accInfo,
		Credential:      cred,
	})
	if err != nil {
		decision := normalizeCountTokensDispatchDecision(gateway.ClassifyAttemptDispatchError(err))
		if relay.feedback != nil {
			decision = normalizeCountTokensDispatchDecision(
				relay.feedback.ObserveDispatchError(ctx, attempt, err),
			)
		}
		return countTokensAttemptOutcome{failure: &countTokensAttemptFailure{decision: decision}}
	}
	if res == nil || res.UpstreamReader == nil {
		if relay.feedback != nil {
			relay.feedback.ObserveChannelError(ctx, attempt, 0)
		}
		return countTokensAttemptOutcome{failure: &countTokensAttemptFailure{
			decision: retryableCountTokensDecision("upstream_empty_response", http.StatusBadGateway),
		}}
	}
	if res.Close != nil {
		defer func() { _ = res.Close() }()
	}
	raw, readErr := readUpstreamBody(res.UpstreamReader)
	if readErr != nil {
		if relay.feedback != nil {
			relay.feedback.ObserveChannelError(ctx, attempt, res.StatusCode)
		}
		writeJSONError(w, http.StatusBadGateway, clienterr.CodeUpstreamReadError, clienterr.MessageFor(clienterr.CodeUpstreamReadError))
		return countTokensAttemptOutcome{done: true}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		failure := observeCountTokensHTTPFailure(relay, ctx, attempt, res, raw)
		return countTokensAttemptOutcome{failure: failure}
	}
	if strings.TrimSpace(string(raw)) == "" {
		if relay.feedback != nil {
			relay.feedback.ObserveChannelError(ctx, attempt, res.StatusCode)
		}
		writeJSONError(w, http.StatusBadGateway, clienterr.CodeUpstreamEmptyResponse, clienterr.MessageFor(clienterr.CodeUpstreamEmptyResponse))
		return countTokensAttemptOutcome{done: true}
	}
	if relay.feedback != nil {
		relay.feedback.ObserveSuccess(ctx, attempt, res.StatusCode, res.Headers)
	}
	if ct := res.Headers.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
	return countTokensAttemptOutcome{done: true}
}

func observeCountTokensHTTPFailure(
	relay *countTokensRelay,
	ctx context.Context,
	attempt upstreamfeedback.Attempt,
	res *gateway.DispatchResult,
	raw []byte,
) *countTokensAttemptFailure {
	if relay.feedback != nil {
		observed := relay.feedback.ObserveHTTPError(ctx, attempt, res.StatusCode, res.Headers, raw)
		return &countTokensAttemptFailure{
			decision:       observed.Decision,
			classification: observed.Classification,
		}
	}
	decision, classification, err := gateway.ClassifyAttemptHTTPError(
		res.StatusCode,
		res.Headers,
		raw,
		attempt.Account.Platform,
	)
	if err != nil {
		decision = gateway.AttemptRetryDecision{
			ClientStatus: http.StatusBadGateway,
			AbortReason:  "upstream_error",
		}
	}
	return &countTokensAttemptFailure{decision: decision, classification: classification}
}

func retryableCountTokensDecision(reason string, status int) gateway.AttemptRetryDecision {
	return gateway.AttemptRetryDecision{
		RetryableBeforeDelivery: true,
		SwitchAccount:           true,
		SwitchPool:              true,
		ClientStatus:            status,
		AbortReason:             reason,
	}
}

func normalizeCountTokensDispatchDecision(decision gateway.AttemptRetryDecision) gateway.AttemptRetryDecision {
	if !decision.RetryableBeforeDelivery && decision.TransportClass == gateway.TransportErrorLocalDispatch {
		decision.ClientStatus = http.StatusBadGateway
		decision.AbortReason = "upstream_dispatch_error"
	}
	if decision.AbortReason == "" {
		decision.AbortReason = "upstream_dispatch_error"
	}
	return decision
}

func shouldRetryCountTokensFailure(
	plan router.RoutePlan,
	failure *countTokensAttemptFailure,
	finalAttempt bool,
	authFailoverUsed bool,
) (bool, bool) {
	if failure == nil {
		return false, false
	}
	if failure.decision.CountsAgainstAuthFailoverBudget && !authFailoverUsed {
		return true, true
	}
	if finalAttempt || !failure.decision.RetryableBeforeDelivery {
		return false, false
	}
	endClass := gateway.EndClassFromAttempt(failure.classification, failure.decision)
	for _, allowed := range plan.RetryableEndClasses {
		if strings.TrimSpace(allowed) == string(endClass) {
			return true, false
		}
	}
	return false, false
}

func effectiveCountTokensAttemptBudget(plan router.RoutePlan) int {
	if len(plan.Attempts) == 0 {
		return 0
	}
	if os.Getenv("HUAKAI_ATTEMPT_RETRY_ENABLED") == "0" {
		return 1
	}
	budget := plan.AttemptBudget
	if budget <= 0 || budget > len(plan.Attempts) {
		budget = len(plan.Attempts)
	}
	if budget < 1 {
		return 1
	}
	return budget
}

func writeCountTokensAttemptFailure(w http.ResponseWriter, failure *countTokensAttemptFailure) {
	status := http.StatusBadGateway
	code := clienterr.CodeUpstreamDispatchError
	if failure != nil {
		if failure.decision.ClientStatus > 0 {
			status = failure.decision.ClientStatus
		}
		if failure.classification.Class != "" {
			code = "upstream_" + string(failure.classification.Class)
		}
		if failure.classification.RetryAfterMs > 0 {
			w.Header().Set("Retry-After", strconv.FormatInt((failure.classification.RetryAfterMs+999)/1000, 10))
		}
	}
	writeJSONError(w, status, code, "upstream request failed")
}
