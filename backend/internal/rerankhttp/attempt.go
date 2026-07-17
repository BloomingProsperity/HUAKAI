package rerankhttp

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/servingcapability"
	"github.com/BloomingProsperity/HUAKAI/internal/upstreamfeedback"
)

func (ex *execution) selectAccount(w http.ResponseWriter, attemptSeq int) bool {
	sel, err := ex.d.Selector.Select(ex.ctx, pool.SelectionRequest{
		TenantID:         ex.ident.TenantID,
		UserID:           ex.ident.UserID,
		APIKeyID:         ex.ident.APIKeyID,
		PoolGroupID:      ex.attempt.PoolGroupID,
		RequestedModel:   ex.req.Model,
		ModelCooldownKey: ex.upstreamModelID,
		ProtocolFamily:   ex.resolved.ProtocolFamily,
		EndpointFamily:   endpointFamilyRerank,
		ClaimID:          ex.reserveRes.ClaimID,
		AttemptSeq:       attemptSeq,
		ExcludedAccounts: ex.excludedAccounts,
		CapabilityFlags:  ex.attempt.RequiredCapabilities,
		SessionHash:      ex.payloadHash,
		Vendor:           pool.VendorFromProtocolFamily(ex.resolved.ProtocolFamily),
		UserGroup:        ex.ident.UserGroup,
	})
	if err != nil || sel == nil || sel.AccountID == 0 || sel.WaitPlan != nil {
		ex.abort(w, "pool_select_no_account", 0)
		writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodeNoCapacity, clienterr.MessageFor(clienterr.CodeNoCapacity))
		return false
	}
	ex.selRes = sel
	return true
}

func (ex *execution) resolveCredential(w http.ResponseWriter) bool {
	cred, acc, err := ex.d.CredentialVault.Resolve(ex.ctx, ex.ident.TenantID, ex.selRes.AccountID)
	if err != nil {
		ex.abort(w, "credential_resolve_error", 0)
		writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodeCredentialResolveError, clienterr.MessageFor(clienterr.CodeCredentialResolveError))
		return false
	}
	if acc.AccountID == 0 {
		acc.AccountID = ex.selRes.AccountID
	}
	ex.cred = cred
	ex.accInfo = acc
	return true
}

func (ex *execution) credentialCompatibilityFailure(w http.ResponseWriter) *attemptFailure {
	if err := servingcapability.ValidateRuntimeAccountCompatibility(ex.resolved.ProtocolFamily, ex.cred, ex.accInfo); err == nil {
		return nil
	}
	decision := gateway.CredentialProtocolIncompatibleDecision()
	return &attemptFailure{
		Decision:   decision,
		AbortErr:   ex.abortWithError(w, decision.AbortReason, 0),
		ClientCode: clienterr.CodeCredentialResolveError,
	}
}

type attemptFailure struct {
	Decision       gateway.AttemptRetryDecision
	Classification gateway.Classification
	AbortErr       error
	ClientCode     string
}

type attemptOutcome struct {
	Done    bool
	Failure *attemptFailure
}

func (ex *execution) dispatchAndSettle(w http.ResponseWriter, attemptSeq int) attemptOutcome {
	startedAt := time.Now()
	res, err := ex.d.Dispatcher.Dispatch(ex.ctx, gateway.DispatchInput{
		ProtocolFamily:  ex.resolved.ProtocolFamily,
		EndpointPath:    upstreamRerankPath,
		UpstreamModelID: ex.upstreamModelID,
		InboundBody:     ex.body,
		Account:         ex.accInfo,
		Credential:      ex.cred,
	})
	if err != nil {
		decision := dispatchFailureDecision(err)
		if ex.d.Feedback != nil {
			decision = normalizeDispatchFailureDecision(
				ex.d.Feedback.ObserveDispatchError(ex.ctx, ex.feedbackAttempt(startedAt), err),
			)
		}
		return attemptOutcome{Failure: &attemptFailure{
			Decision: decision,
			AbortErr: ex.abortWithError(w, decision.AbortReason, 0),
		}}
	}
	if res == nil || res.UpstreamReader == nil {
		ex.observeChannelError(startedAt, 0)
		decision := retryableAttemptDecision("upstream_empty_response", http.StatusBadGateway)
		return attemptOutcome{Failure: &attemptFailure{
			Decision: decision,
			AbortErr: ex.abortWithError(w, decision.AbortReason, 0),
		}}
	}
	defer closeDispatchResult(res)
	return ex.finishUpstreamResponse(w, res, attemptSeq, startedAt)
}

func (ex *execution) finishUpstreamResponse(w http.ResponseWriter, res *gateway.DispatchResult, attemptSeq int, startedAt time.Time) attemptOutcome {
	raw, readErr := readUpstreamBody(res.UpstreamReader)
	if readErr != nil {
		ex.observeChannelError(startedAt, res.StatusCode)
		ex.abort(w, clienterr.CodeUpstreamReadError, 0)
		writeJSONError(w, http.StatusBadGateway, clienterr.CodeUpstreamReadError, clienterr.MessageFor(clienterr.CodeUpstreamReadError))
		return attemptOutcome{Done: true}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		failure := ex.observeHTTPFailure(startedAt, res, raw)
		failure.AbortErr = ex.abortWithError(w, failure.Decision.AbortReason, 0)
		return attemptOutcome{Failure: failure}
	}
	if strings.TrimSpace(string(raw)) == "" {
		ex.observeChannelError(startedAt, res.StatusCode)
		decision := retryableAttemptDecision("upstream_empty_response", http.StatusBadGateway)
		return attemptOutcome{Failure: &attemptFailure{
			Decision: decision,
			AbortErr: ex.abortWithError(w, decision.AbortReason, 0),
		}}
	}
	ex.observeSuccess(startedAt, res)
	_ = ex.settleSuccessfulResponse(w, res, raw, attemptSeq)
	return attemptOutcome{Done: true}
}

func (ex *execution) settleSuccessfulResponse(w http.ResponseWriter, res *gateway.DispatchResult, raw []byte, attemptSeq int) bool {
	sbctx, scancel := ex.billingCtx()
	defer scancel()
	if _, err := ex.d.Settler.Settle(sbctx, ex.settleRequest(ex.costSnapshot, attemptSeq)); err != nil {
		writeJSONError(w, http.StatusInternalServerError, clienterr.CodeSettleError, clienterr.MessageFor(clienterr.CodeSettleError))
		return false
	}
	copyAllowedHeaders(w.Header(), res.Headers)
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
	return true
}

func (ex *execution) feedbackAttempt(startedAt time.Time) upstreamfeedback.Attempt {
	return upstreamfeedback.Attempt{
		TenantID:       ex.ident.TenantID,
		Account:        ex.accInfo,
		ProtocolFamily: ex.resolved.ProtocolFamily,
		ModelKey:       ex.upstreamModelID,
		RequestID:      ex.requestID,
		StartedAt:      startedAt,
	}
}

func (ex *execution) observeHTTPFailure(startedAt time.Time, res *gateway.DispatchResult, raw []byte) *attemptFailure {
	if ex.d.Feedback != nil {
		observed := ex.d.Feedback.ObserveHTTPError(
			ex.ctx,
			ex.feedbackAttempt(startedAt),
			res.StatusCode,
			res.Headers,
			raw,
		)
		return &attemptFailure{
			Decision:       observed.Decision,
			Classification: observed.Classification,
		}
	}
	decision, classification, err := gateway.ClassifyAttemptHTTPError(
		res.StatusCode,
		res.Headers,
		raw,
		ex.classificationProvider(),
	)
	if err != nil {
		decision = gateway.AttemptRetryDecision{
			ClientStatus: http.StatusBadGateway,
			AbortReason:  "upstream_error",
		}
	}
	return &attemptFailure{Decision: decision, Classification: classification}
}

func (ex *execution) classificationProvider() string {
	if strings.EqualFold(strings.TrimSpace(ex.resolved.ProtocolFamily), "bedrock_invoke") {
		return "bedrock"
	}
	return ex.accInfo.Platform
}

func (ex *execution) observeChannelError(startedAt time.Time, statusCode int) {
	if ex.d.Feedback != nil {
		ex.d.Feedback.ObserveChannelError(ex.ctx, ex.feedbackAttempt(startedAt), statusCode)
	}
}

func (ex *execution) observeSuccess(startedAt time.Time, res *gateway.DispatchResult) {
	if ex.d.Feedback != nil {
		ex.d.Feedback.ObserveSuccess(ex.ctx, ex.feedbackAttempt(startedAt), res.StatusCode, res.Headers)
	}
}

func (ex *execution) excludeAccount(accountID int64) {
	if accountID <= 0 {
		return
	}
	if ex.excludedAccounts == nil {
		ex.excludedAccounts = make(map[int64]struct{})
	}
	ex.excludedAccounts[accountID] = struct{}{}
}

func (ex *execution) prepareNextAttempt() {
	ex.reserveRes = nil
	ex.selRes = nil
	ex.accInfo = provider.AccountInfo{}
	ex.cred = provider.Credential{}
}

func retryableAttemptDecision(reason string, status int) gateway.AttemptRetryDecision {
	return gateway.AttemptRetryDecision{
		RetryableBeforeDelivery: true,
		SwitchAccount:           true,
		SwitchPool:              true,
		ClientStatus:            status,
		AbortReason:             reason,
	}
}

func dispatchFailureDecision(err error) gateway.AttemptRetryDecision {
	return normalizeDispatchFailureDecision(gateway.ClassifyAttemptDispatchError(err))
}

func normalizeDispatchFailureDecision(decision gateway.AttemptRetryDecision) gateway.AttemptRetryDecision {
	if !decision.RetryableBeforeDelivery && decision.TransportClass == gateway.TransportErrorLocalDispatch {
		decision.ClientStatus = http.StatusBadGateway
		decision.AbortReason = "upstream_dispatch_error"
	}
	if decision.AbortReason == "" {
		decision.AbortReason = "upstream_dispatch_error"
	}
	return decision
}

func shouldRetryFailure(plan router.RoutePlan, failure *attemptFailure, finalAttempt, authFailoverUsed bool) (bool, bool) {
	if failure == nil || failure.AbortErr != nil {
		return false, false
	}
	if failure.Decision.CountsAgainstAuthFailoverBudget && !authFailoverUsed {
		return true, true
	}
	if finalAttempt || !failure.Decision.RetryableBeforeDelivery {
		return false, false
	}
	endClass := gateway.EndClassFromAttempt(failure.Classification, failure.Decision)
	for _, allowed := range plan.RetryableEndClasses {
		if strings.TrimSpace(allowed) == string(endClass) {
			return true, false
		}
	}
	return false, false
}

func effectiveAttemptBudget(plan router.RoutePlan) int {
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

func writeAttemptFailure(w http.ResponseWriter, failure *attemptFailure) {
	status := http.StatusBadGateway
	code := clienterr.CodeUpstreamDispatchError
	if failure != nil {
		if failure.Decision.ClientStatus > 0 {
			status = failure.Decision.ClientStatus
		}
		if failure.ClientCode != "" {
			code = failure.ClientCode
		} else if failure.Classification.Class != "" {
			code = "upstream_" + string(failure.Classification.Class)
		}
		if failure.Classification.RetryAfterMs > 0 {
			w.Header().Set("Retry-After", strconv.FormatInt((failure.Classification.RetryAfterMs+999)/1000, 10))
		}
	}
	writeJSONError(w, status, code, "upstream request failed")
}

func closeDispatchResult(res *gateway.DispatchResult) {
	if res != nil && res.Close != nil {
		_ = res.Close()
	}
}
