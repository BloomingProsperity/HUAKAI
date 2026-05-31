package gatewayhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/cache_routing"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

func newChatExecution(d ChatHandlerDeps, r *http.Request, ident auth.Identity, validated chatValidatedRequest, startedAt time.Time) *chatExecution {
	return &chatExecution{
		d:                                d,
		r:                                r,
		ctx:                              r.Context(),
		startedAt:                        startedAt,
		ident:                            ident,
		body:                             validated.Body,
		req:                              validated.Request,
		clientProtocol:                   validated.ClientProtocol,
		clientAdapter:                    validated.ClientAdapter,
		requestID:                        validated.RequestID,
		clientRequestID:                  validated.ClientRequestID,
		streamInputOnlyInterruptedPolicy: d.BillingPolicyResolver.ResolveStreamInputOnlyInterruptedPolicy(r.Context(), ident.TenantID),
	}
}

func (ex *chatExecution) refreshRequestSessionHashes() {
	ex.promptHash = cache_routing.ComputePromptHash(ex.body)
	ex.sessionHash = requestSessionHash(ex.clientProtocol, ex.body, ex.promptHash)
}

func requestSessionHash(clientProtocol proto.ClientProtocol, rawBody []byte, promptHash string) string {
	if promptHash != "" {
		return promptHash
	}
	if clientProtocol == proto.ClientProtocolOpenAIResponses {
		if previousID := openAIResponsesPreviousResponseID(rawBody); previousID != "" {
			return previousID
		}
	}
	return promptHash
}

func openAIResponsesPreviousResponseID(rawBody []byte) string {
	var req struct {
		PreviousResponseID string `json:"previous_response_id"`
	}
	if err := json.Unmarshal(rawBody, &req); err != nil {
		return ""
	}
	return req.PreviousResponseID
}

func (ex *chatExecution) prepareRoute(w http.ResponseWriter) bool {
	resolved, err := ex.d.Registry.ResolveModel(ex.ctx, ex.req.Model, ex.ident.TenantID)
	if errors.Is(err, registry.ErrRegistryBackend) {
		writeJSONError(w, http.StatusServiceUnavailable, "registry_backend_error",
			"registry backend transient failure")
		return false
	}
	if errors.Is(err, registry.ErrUnknownModel) ||
		errors.Is(err, registry.ErrModelDisabled) ||
		errors.Is(err, registry.ErrTenantNoAccess) {
		writeJSONError(w, http.StatusNotFound, "model_not_available", "model not available")
		return false
	}
	if err != nil {
		writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusInternalServerError, clienterr.CodeRegistryUnknownError, err)
		return false
	}
	ex.resolved = resolved
	resolvedModel := routerResolvedModelFromRegistry(resolved)

	plan, err := ex.d.Router.Plan(ex.ctx, router.PlanInput{
		Context: router.RequestContext{
			TenantID:  ex.ident.TenantID,
			UserID:    ex.ident.UserID,
			APIKeyID:  ex.ident.APIKeyID,
			RequestID: ex.requestID,
		},
		Model:    resolvedModel,
		Features: router.RequestFeatures{Stream: ex.req.Stream},
	})
	if err != nil {
		writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusInternalServerError, clienterr.CodeRouterPlanError, err)
		return false
	}
	if len(plan.Attempts) == 0 {
		writeJSONError(w, http.StatusInternalServerError, "router_plan_error", "router returned no attempts")
		return false
	}
	ex.plan = plan
	ex.activateRouteAttempt(plan.Attempts[0])
	ex.cacheVendor = pool.VendorFromProtocolFamily(ex.resolved.ProtocolFamily)
	ex.refreshRequestSessionHashes()
	return true
}

func routerResolvedModelFromRegistry(resolved registry.Resolved) router.ResolvedModel {
	return router.ResolvedModel{
		PublicAlias:     resolved.PublicAlias,
		InternalModelID: resolved.CanonicalModelID,
		ProviderModelID: resolved.ProviderModelID,
		ContextWindow:   resolved.ContextWindow,
		Capabilities:    resolved.Capabilities,
		PricingClass:    resolved.PricingClass,
		ProtocolFamily:  resolved.ProtocolFamily,
		PoolCandidates:  resolved.PoolCandidates,
		PoolMetadata:    routerPoolMetadataFromRegistry(resolved),
		SnapshotVersion: resolved.SnapshotVersion,
	}
}

func routerPoolMetadataFromRegistry(resolved registry.Resolved) []router.PoolCandidateMeta {
	if len(resolved.BindingMetadata) == 0 {
		return nil
	}
	defaultProviderModelID := resolved.DefaultProviderModelID
	if defaultProviderModelID == "" {
		defaultProviderModelID = resolved.ProviderModelID
	}
	out := make([]router.PoolCandidateMeta, 0, len(resolved.BindingMetadata))
	for _, binding := range resolved.BindingMetadata {
		if binding.PoolGroupID == 0 {
			continue
		}
		providerModelID := defaultProviderModelID
		if binding.ProviderModelIDOverride != nil && *binding.ProviderModelIDOverride != "" {
			providerModelID = *binding.ProviderModelIDOverride
		}
		out = append(out, router.PoolCandidateMeta{
			PoolGroupID:     binding.PoolGroupID,
			ProviderModelID: providerModelID,
		})
	}
	return out
}

func (ex *chatExecution) activateRouteAttempt(attempt router.AttemptPlan) {
	ex.attempt = attempt
	ex.routeID = ex.plan.SnapshotVersion
	if attempt.Reason != "" {
		ex.routeID = fmt.Sprintf("%s:%s", ex.routeID, attempt.Reason)
	}
	ex.upstreamModelID = attempt.UpstreamModelID
	if ex.upstreamModelID == "" {
		ex.upstreamModelID = ex.resolved.ProviderModelID
	}
	if ex.upstreamModelID == "" {
		ex.upstreamModelID = ex.req.Model
	}
}

func (ex *chatExecution) prepareClaimAndAccount(w http.ResponseWriter, in attemptInput) (bool, *classifiedAttemptFailure) {
	if ex.reserveRes == nil && !ex.reserveClaim(w) {
		return false, nil
	}
	if failure := ex.selectPoolAccount(w, in); failure != nil {
		return false, failure
	}
	return true, nil
}

func (ex *chatExecution) reserveClaim(w http.ResponseWriter) bool {
	ex.ensureIdempotencyState()
	predictedCost, err := ex.predictedCompletionCost()
	if err != nil {
		writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusServiceUnavailable, clienterr.CodePricingUnavailable, err)
		return false
	}

	reserveRes, err := ex.d.ClaimGate.Reserve(ex.ctx, billing.ReserveRequest{
		TenantID:                   ex.ident.TenantID,
		APIKeyID:                   ex.ident.APIKeyID,
		UserID:                     ex.ident.UserID,
		LogicalRequestID:           ex.logicalRequestID,
		EndpointFamily:             ex.d.effectiveEndpointFamily(),
		NormalizedPayloadHash:      ex.payloadHash,
		RequestedModel:             ex.req.Model,
		PoolingGroupID:             ex.attempt.PoolGroupID,
		BillingPolicyVersion:       ex.d.BillingPolicyVersion,
		RequestClass:               ex.d.RequestClass,
		PredictedCost:              predictedCost,
		IdempotencyKeyClientHeader: ex.idempotencyHeader,
	})
	if errors.Is(err, billing.ErrFingerprintConflict) || (reserveRes != nil && reserveRes.FingerprintConflict) {
		writeJSONError(w, http.StatusConflict, "idempotency_conflict",
			"same logical_request_id with different normalized payload")
		return false
	}
	if errors.Is(err, billing.ErrInsufficientBalance) {
		writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusPaymentRequired, clienterr.CodeReserveError, err)
		return false
	}
	if err != nil {
		writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusInternalServerError, clienterr.CodeReserveError, err)
		return false
	}
	if reserveRes.IdempotencyHit {
		// 同 idempotency-key 的重试: 按原 claim_id 从持久重放表取回原始响应
		// 重放 — 路由无关、不受 L2 response cache 淘汰影响。 取不到才回 409。
		if ex.serveIdempotentReplay(w, reserveRes.ClaimID) {
			return false
		}
		writeJSONError(w, http.StatusConflict, "replay_without_cache",
			"idempotent request hit but stored response unavailable; retry the request")
		return false
	}
	ex.reserveRes = reserveRes
	return true
}

func (ex *chatExecution) ensureIdempotencyState() {
	if ex == nil || ex.r == nil {
		return
	}
	header := ex.r.Header.Get("Idempotency-Key")
	if ex.idempotencyHeader == "" {
		ex.idempotencyHeader = header
	}
	if ex.logicalRequestID == "" {
		ex.logicalRequestID = header
		if ex.logicalRequestID == "" {
			ex.logicalRequestID = uuid.NewString()
		}
	}
	if ex.payloadHash == "" {
		ex.payloadHash = normalizedPayloadHash(ex.body)
	}
}

func (ex *chatExecution) selectPoolAccount(w http.ResponseWriter, in attemptInput) *classifiedAttemptFailure {
	// 同一 prompt prefix 固定到同一账号，提高 vendor prompt cache 命中率。
	ex.refreshRequestSessionHashes()
	attemptSeq := in.AttemptSeq
	if attemptSeq <= 0 {
		attemptSeq = ex.activeAttemptSeq()
	}
	excludedAccounts := in.ExcludedAccounts
	if excludedAccounts == nil {
		excludedAccounts = map[int64]struct{}{}
	}
	selRes, err := ex.d.Selector.Select(ex.ctx, pool.SelectionRequest{
		TenantID:         ex.ident.TenantID,
		UserID:           ex.ident.UserID,
		APIKeyID:         ex.ident.APIKeyID,
		PoolGroupID:      ex.attempt.PoolGroupID,
		RequestedModel:   ex.req.Model,
		ProtocolFamily:   ex.resolved.ProtocolFamily,
		EndpointFamily:   ex.d.effectiveEndpointFamily(),
		ClaimID:          ex.reserveRes.ClaimID,
		AttemptSeq:       attemptSeq,
		ExcludedAccounts: excludedAccounts,
		CapabilityFlags:  ex.attempt.RequiredCapabilities,
		SessionHash:      ex.sessionHash,
		Vendor:           pool.VendorFromProtocolFamily(ex.resolved.ProtocolFamily),
	})
	if errors.Is(err, pool.ErrNoEligibleAccount) || errors.Is(err, pool.ErrNoSlotAvailable) || errors.Is(err, pool.ErrAllChannelsDegraded) {
		abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "pool_no_capacity", ex.requestID, 0, ex.protocolLoss)
		failure := retryableLocalAttemptFailure(http.StatusServiceUnavailable, clienterr.CodeNoCapacity, clienterr.MessageFor(clienterr.CodeNoCapacity), "pool_no_capacity", gateway.UpstreamError5xx, err)
		failure.RetryAfterSeconds = 5
		return degradeFailureIfAbortFailed(ex.ctx, ex.requestID, failure, abortErr)
	}
	if errors.Is(err, pool.ErrClaimRace) {
		// claim 被并发路径抢占移出 reserving — 这是预期内
		// 竞态, 不是 internal error。 返 409 + Retry-After 让 client 幂等重试。
		if abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "claim_race", ex.requestID, 0, ex.protocolLoss); abortErr != nil && w != nil {
			setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
		}
		failure := terminalLocalAttemptFailure(http.StatusConflict, clienterr.CodeClaimRace, clienterr.MessageFor(clienterr.CodeClaimRace), "claim_race", err)
		failure.RetryAfterSeconds = 1
		return failure
	}
	if err != nil {
		abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "pool_select_error", ex.requestID, 0, ex.protocolLoss)
		failure := retryableLocalAttemptFailure(http.StatusInternalServerError, clienterr.CodePoolSelectError, clienterr.MessageFor(clienterr.CodePoolSelectError), "pool_select_error", gateway.UpstreamError5xx, err)
		return degradeFailureIfAbortFailed(ex.ctx, ex.requestID, failure, abortErr)
	}
	if selRes != nil && selRes.WaitPlan != nil {
		abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "queue_wait", ex.requestID, 0, ex.protocolLoss)
		failure := retryableLocalAttemptFailure(http.StatusTooManyRequests, clienterr.CodeQueueWait, clienterr.MessageFor(clienterr.CodeQueueWait), "queue_wait", gateway.UpstreamRateLimit, nil)
		failure.RetryAfterSeconds = retryAfterSecondsForWaitPlan(selRes.WaitPlan)
		return degradeFailureIfAbortFailed(ex.ctx, ex.requestID, failure, abortErr)
	}
	if selRes == nil || selRes.AccountID == 0 {
		abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "pool_select_no_account", ex.requestID, 0, ex.protocolLoss)
		failure := retryableLocalAttemptFailure(http.StatusServiceUnavailable, clienterr.CodeNoCapacity, clienterr.MessageFor(clienterr.CodeNoCapacity), "pool_select_no_account", gateway.UpstreamError5xx, nil)
		return degradeFailureIfAbortFailed(ex.ctx, ex.requestID, failure, abortErr)
	}
	ex.selRes = selRes
	ex.acquiredAccountID = selRes.AccountID
	ex.acquisitionToken = selRes.AcquisitionToken
	return nil
}

func retryAfterSecondsForWaitPlan(plan *pool.WaitPlan) int {
	if plan == nil || plan.TimeoutMS <= 0 {
		return 1
	}
	return (plan.TimeoutMS + 999) / 1000
}

func (ex *chatExecution) resolveCredential() *classifiedAttemptFailure {
	cred, accInfo, err := ex.d.CredentialVault.Resolve(ex.ctx, ex.ident.TenantID, ex.acquiredAccountID)
	if err != nil {
		abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "credential_resolve_error", ex.requestID, 0, ex.protocolLoss)
		status := http.StatusInternalServerError
		if errors.Is(err, provider.ErrAccountNotFound) {
			status = http.StatusServiceUnavailable
		}
		failure := retryableLocalAttemptFailure(status, clienterr.CodeCredentialResolveError, clienterr.MessageFor(clienterr.CodeCredentialResolveError), "credential_resolve_error", gateway.UpstreamError5xx, err)
		return degradeFailureIfAbortFailed(ex.ctx, ex.requestID, failure, abortErr)
	}
	if accInfo.AccountID == 0 {
		accInfo.AccountID = ex.acquiredAccountID
	}
	ex.cred = cred
	ex.accInfo = accInfo
	ex.forwardReq = gateway.ForwardRequest{
		TenantID:             ex.ident.TenantID,
		AccountID:            ex.acquiredAccountID,
		AcquisitionToken:     ex.acquisitionToken,
		RequestID:            ex.requestID,
		RouteID:              ex.routeID,
		PoolID:               fmt.Sprintf("%d", ex.attempt.PoolGroupID),
		IngressPath:          ex.r.URL.Path,
		ProtocolFamily:       ex.resolved.ProtocolFamily,
		ClientProtocol:       string(ex.clientProtocol),
		Model:                ex.upstreamModelID,
		RequestedModel:       ex.req.Model,
		Provider:             accInfo.Platform,
		RoutingReasonPayload: ex.selRes.RoutingReasonJSON,
		SessionHash:          ex.sessionHash,
	}
	ex.healthKey, ex.healthKeyOK = channelHealthKey(ex.ident.TenantID, accInfo)
	return nil
}

func (ex *chatExecution) dispatchBufferedEnvelope(w http.ResponseWriter) (*proto.HCSF, *classifiedAttemptFailure, bool) {
	upstreamAttemptStartedAt := time.Now()
	seed := requestMetaSeed(ex.r, ex.ident, ex.clientProtocol, ex.resolved.ProtocolFamily, ex.routeID, ex.requestID, ex.acquiredAccountID, ex.acquisitionToken)
	seedCtx := proto.ContextWithRequestMetaSeed(ex.ctx, seed)
	if hcsfDispatchEnabled() {
		return ex.dispatchCanonicalBuffered(w, seedCtx, upstreamAttemptStartedAt)
	}
	return ex.dispatchRawBuffered(w, seed, seedCtx, upstreamAttemptStartedAt)
}

func (ex *chatExecution) dispatchCanonicalBuffered(w http.ResponseWriter, seedCtx context.Context, startedAt time.Time) (*proto.HCSF, *classifiedAttemptFailure, bool) {
	canonicalReq, requestLosses, err := ex.clientAdapter.RequestToCanonical(seedCtx, ex.body)
	// 请求翻译损失之前被丢弃(_)。先快照供下方 dispatch-internal abort 携带证据;
	// canonicalReq 非空时再折入其 CapabilityGraph —— DispatchHCSF 用 cloneHCSF 把请求侧
	// ProtocolLoss 带进响应 env(upstream_dispatcher_hcsf.go:144/153),使成功路径的
	// billing 快照也累积请求侧证据(S1-025-fu item 2;非流式 buffered 路径原本整段丢失)。
	ex.protocolLoss = protocolLossJSONFromEntries(requestLosses)
	if err != nil {
		if abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "invalid_request_body", ex.requestID, 0, ex.protocolLoss); abortErr != nil {
			setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
		}
		writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusBadRequest, clienterr.CodeInvalidRequestBody, err)
		return nil, nil, false
	}
	if canonicalReq == nil {
		if abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "invalid_request_body", ex.requestID, 0, ex.protocolLoss); abortErr != nil {
			setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
		}
		writeJSONError(w, http.StatusBadRequest, clienterr.CodeInvalidRequestBody, clienterr.MessageFor(clienterr.CodeInvalidRequestBody))
		return nil, nil, false
	}
	if len(requestLosses) > 0 {
		canonicalReq.CapabilityGraph.ProtocolLoss = append(canonicalReq.CapabilityGraph.ProtocolLoss, requestLosses...)
	}
	enrichCanonicalRequestMeta(canonicalReq, ex.upstreamModelID, ex.accInfo.Platform, ex.idempotencyHeader, ex.sessionHash)
	canonicalReq.RequestMeta.EndpointFamily = ex.resolved.ProtocolFamily
	setAccountingModelRequested(canonicalReq, ex.req.Model)
	setAccountingModelRouteDecided(canonicalReq, ex.forwardReq.Model)
	gateway.ApplyForwardRequestHopChain(canonicalReq, ex.forwardReq)

	dispatcher := hcsfDispatcher(ex.d)
	if dispatcher == nil {
		if abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "non_streaming_not_yet_wired", ex.requestID, 0, ex.protocolLoss); abortErr != nil {
			setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
		}
		err := fmt.Errorf("dispatcher lacks HCSF dispatch support for client_protocol=%q protocol_family=%q", ex.clientProtocol, ex.resolved.ProtocolFamily)
		writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusServiceUnavailable, clienterr.CodeNonStreamingNotYetWired, err)
		return nil, nil, false
	}
	dispatchCtx := gateway.ContextWithHCSFDispatchInput(seedCtx, gateway.HCSFDispatchInput{
		ProtocolFamily:  ex.resolved.ProtocolFamily,
		UpstreamModelID: ex.upstreamModelID,
		Account:         ex.accInfo,
		Credential:      ex.cred,
		RawBody:         ex.body,
	})
	bufferedEnv, err := dispatcher.DispatchHCSF(dispatchCtx, canonicalReq)
	// DispatchHCSF 内 MarshalToProviderRequest 会原地往 canonicalReq.CapabilityGraph.ProtocolLoss
	// 追加 canonical→upstream marshal 损失(addMarshalLossRaw)。下方 dispatch-error 与
	// finalizeBufferedEnvelope 的 empty-response abort 都走 ex.protocolLoss 快照,必须在此刷新,
	// 否则只剩 dispatch 前的请求翻译损失,marshal 证据整段丢失(S1-025-fu review R1)。
	// 成功路径稍后由 billing 快照从 bufferedEnv(已含 marshal+resp 损失)覆盖,无重复累加。
	ex.protocolLoss = protocolLossJSONFromEnv(canonicalReq)
	if err != nil {
		// 上游真实 status / header / body 只用于 retry 与 health classification;
		// client 只拿脱敏后的 code/message。
		var upstreamErr *gateway.UpstreamHTTPError
		clientStatus := http.StatusBadGateway
		healthStatus := 0
		var classifyBody []byte
		var classification gateway.Classification
		var decision gateway.AttemptRetryDecision
		if errors.As(err, &upstreamErr) {
			clientStatus = upstreamErr.StatusCode
			healthStatus = upstreamErr.StatusCode
			classifyBody = upstreamErr.Body
			decision, classification, _ = gateway.ClassifyAttemptHTTPError(upstreamErr.StatusCode, upstreamErr.Header, upstreamErr.Body, ex.accInfo.Platform)
		} else {
			classifyBody = []byte(err.Error())
			classification, _ = gateway.Classify(0, nil, classifyBody, ex.accInfo.Platform)
			decision = gateway.ClassifyAttemptDispatchError(err)
			if decision.ClientStatus != 0 {
				clientStatus = decision.ClientStatus
			}
		}
		if decision.ClientStatus == 0 {
			decision.ClientStatus = clientStatus
		}
		if decision.AbortReason == "" {
			decision.AbortReason = "upstream_dispatch_error"
		}
		abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, decision.AbortReason, ex.requestID, 0, ex.protocolLoss)

		if ex.healthKeyOK {
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, signalFromDispatchError(err, classification), healthStatus, time.Since(startedAt), ex.requestID, rateLimitResetFromClassification(classification, time.Now()))
		}
		code := "upstream_dispatch_error"
		if upstreamErr != nil {
			code = ""
		}
		failure := classifiedFailureFromDecision(code, clienterr.MessageFor(clienterr.CodeUpstreamDispatchError), classification, decision, err)
		return nil, degradeFailureIfAbortFailed(ex.ctx, ex.requestID, failure, abortErr), false
	}
	return ex.finalizeBufferedEnvelope(w, bufferedEnv, 0, startedAt)
}

func (ex *chatExecution) finalizeBufferedEnvelope(w http.ResponseWriter, env *proto.HCSF, statusCode int, startedAt time.Time) (*proto.HCSF, *classifiedAttemptFailure, bool) {
	if env == nil || env.BufferedResponse == nil {
		abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "upstream_empty_response", ex.requestID, 0, ex.protocolLoss)
		if ex.healthKeyOK {
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, channelhealth.SignalChannelError, statusCode, time.Since(startedAt), ex.requestID, nil)
		}
		failure := retryableLocalAttemptFailure(http.StatusBadGateway, clienterr.CodeUpstreamEmptyResponse, clienterr.MessageFor(clienterr.CodeUpstreamEmptyResponse), "upstream_empty_response", gateway.UpstreamError5xx, nil)
		return nil, degradeFailureIfAbortFailed(ex.ctx, ex.requestID, failure, abortErr), false
	}
	setAccountingModelRequested(env, ex.req.Model)
	setAccountingModelRouteDecided(env, ex.forwardReq.Model)
	fillAccountingModelUpstreamReported(env)
	env.Accounting.HopChain = gateway.BuildHopChain(ex.forwardReq, "", ex.startedAt, time.Now())
	if ex.healthKeyOK {
		recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, channelhealth.SignalSuccess, http.StatusOK, time.Since(startedAt), ex.requestID, nil)
	}
	return env, nil, true
}
