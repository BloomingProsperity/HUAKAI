package gatewayhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/cache_routing"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

func newChatExecution(d ChatHandlerDeps, r *http.Request, ident auth.Identity, validated chatValidatedRequest, startedAt time.Time) *chatExecution {
	return &chatExecution{
		d:              d,
		r:              r,
		ctx:            r.Context(),
		startedAt:      startedAt,
		ident:          ident,
		body:           validated.Body,
		req:            validated.Request,
		clientProtocol: validated.ClientProtocol,
		clientAdapter:  validated.ClientAdapter,
		requestID:      validated.RequestID,
	}
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
		writeJSONError(w, http.StatusInternalServerError, "registry_unknown_error", err.Error())
		return false
	}
	ex.resolved = resolved

	plan, err := ex.d.Router.Plan(ex.ctx, router.PlanInput{
		Context: router.RequestContext{
			TenantID:  ex.ident.TenantID,
			UserID:    ex.ident.UserID,
			APIKeyID:  ex.ident.APIKeyID,
			RequestID: ex.requestID,
		},
		Model: router.ResolvedModel{
			PublicAlias:     resolved.PublicAlias,
			InternalModelID: resolved.CanonicalModelID,
			ProviderModelID: resolved.ProviderModelID,
			ContextWindow:   resolved.ContextWindow,
			Capabilities:    resolved.Capabilities,
			PricingClass:    resolved.PricingClass,
			ProtocolFamily:  resolved.ProtocolFamily,
			PoolCandidates:  resolved.PoolCandidates,
			SnapshotVersion: resolved.SnapshotVersion,
		},
		Features: router.RequestFeatures{Stream: ex.req.Stream},
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "router_plan_error", err.Error())
		return false
	}
	if len(plan.Attempts) == 0 {
		writeJSONError(w, http.StatusInternalServerError, "router_plan_error", "router returned no attempts")
		return false
	}
	ex.plan = plan
	ex.attempt = plan.Attempts[0]
	ex.routeID = plan.SnapshotVersion
	if ex.attempt.Reason != "" {
		ex.routeID = fmt.Sprintf("%s:%s", ex.routeID, ex.attempt.Reason)
	}
	ex.upstreamModelID = ex.resolved.ProviderModelID
	if ex.upstreamModelID == "" {
		ex.upstreamModelID = ex.req.Model
	}
	ex.cacheVendor = pool.VendorFromProtocolFamily(ex.resolved.ProtocolFamily)
	ex.promptHash = cache_routing.ComputePromptHash(ex.body)
	return true
}

func (ex *chatExecution) prepareClaimAndAccount(w http.ResponseWriter) bool {
	if ex.reserveRes == nil && !ex.reserveClaim(w) {
		return false
	}
	return ex.selectPoolAccount(w)
}

func (ex *chatExecution) reserveClaim(w http.ResponseWriter) bool {
	ex.idempotencyHeader = ex.r.Header.Get("Idempotency-Key")
	ex.logicalRequestID = ex.idempotencyHeader
	if ex.logicalRequestID == "" {
		ex.logicalRequestID = uuid.NewString()
	}
	ex.payloadHash = normalizedPayloadHash(ex.body)
	predictedCost, err := ex.predictedCompletionCost()
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "pricing_unavailable", err.Error())
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
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "reserve_error", err.Error())
		return false
	}
	if reserveRes.IdempotencyHit {
		writeJSONError(w, http.StatusConflict, "replay_without_cache",
			"idempotent request hit but replay cache is Phase E scope")
		return false
	}
	ex.reserveRes = reserveRes
	return true
}

func (ex *chatExecution) selectPoolAccount(w http.ResponseWriter) bool {
	// 同一 prompt prefix 固定到同一账号，提高 vendor prompt cache 命中率。
	ex.promptHash = cache_routing.ComputePromptHash(ex.body)
	selRes, err := ex.d.Selector.Select(ex.ctx, pool.SelectionRequest{
		TenantID:        ex.ident.TenantID,
		UserID:          ex.ident.UserID,
		APIKeyID:        ex.ident.APIKeyID,
		PoolGroupID:     ex.attempt.PoolGroupID,
		RequestedModel:  ex.req.Model,
		EndpointFamily:  ex.d.effectiveEndpointFamily(),
		ClaimID:         ex.reserveRes.ClaimID,
		AttemptSeq:      1,
		CapabilityFlags: ex.attempt.RequiredCapabilities,
		SessionHash:     ex.promptHash,
		Vendor:          pool.VendorFromProtocolFamily(ex.resolved.ProtocolFamily),
	})
	if errors.Is(err, pool.ErrNoEligibleAccount) || errors.Is(err, pool.ErrNoSlotAvailable) || errors.Is(err, pool.ErrAllChannelsDegraded) {
		if abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "pool_no_capacity", ex.requestID); abortErr != nil {
			w.Header().Set("X-Huakai-Abort-Failed", abortErr.Error())
		}
		w.Header().Set("Retry-After", "5")
		writeJSONError(w, http.StatusServiceUnavailable, "no_capacity", err.Error())
		return false
	}
	if err != nil {
		_ = ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "pool_select_error", ex.requestID)
		writeJSONError(w, http.StatusInternalServerError, "pool_select_error", err.Error())
		return false
	}
	if selRes != nil && selRes.WaitPlan != nil {
		if abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "queue_wait", ex.requestID); abortErr != nil {
			w.Header().Set("X-Huakai-Abort-Failed", abortErr.Error())
		}
		w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfterSecondsForWaitPlan(selRes.WaitPlan)))
		writeJSONError(w, http.StatusTooManyRequests, "queue_wait", "pool returned wait plan; retry later")
		return false
	}
	if selRes == nil || selRes.AccountID == 0 {
		_ = ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "pool_select_no_account", ex.requestID)
		writeJSONError(w, http.StatusServiceUnavailable, "no_capacity", "pool returned no account")
		return false
	}
	ex.selRes = selRes
	ex.acquiredAccountID = selRes.AccountID
	ex.acquisitionToken = selRes.AcquisitionToken
	return true
}

func retryAfterSecondsForWaitPlan(plan *pool.WaitPlan) int {
	if plan == nil || plan.TimeoutMS <= 0 {
		return 1
	}
	return (plan.TimeoutMS + 999) / 1000
}

func (ex *chatExecution) resolveCredential(w http.ResponseWriter) bool {
	cred, accInfo, err := ex.d.CredentialVault.Resolve(ex.ctx, ex.ident.TenantID, ex.acquiredAccountID)
	if err != nil {
		_ = ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "credential_resolve_error", ex.requestID)
		status := http.StatusInternalServerError
		if errors.Is(err, provider.ErrAccountNotFound) {
			status = http.StatusServiceUnavailable
		}
		writeJSONError(w, status, "credential_resolve_error", "upstream credential unavailable")
		return false
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
		SessionHash:          ex.promptHash,
	}
	ex.healthKey, ex.healthKeyOK = channelHealthKey(ex.ident.TenantID, accInfo)
	return true
}

func (ex *chatExecution) dispatchBufferedEnvelope(w http.ResponseWriter) (*proto.HCSF, bool) {
	upstreamAttemptStartedAt := time.Now()
	seed := requestMetaSeed(ex.r, ex.ident, ex.clientProtocol, ex.resolved.ProtocolFamily, ex.routeID, ex.requestID, ex.acquiredAccountID, ex.acquisitionToken)
	seedCtx := proto.ContextWithRequestMetaSeed(ex.ctx, seed)
	if hcsfDispatchEnabled() {
		return ex.dispatchCanonicalBuffered(w, seedCtx, upstreamAttemptStartedAt)
	}
	return ex.dispatchRawBuffered(w, seed, seedCtx, upstreamAttemptStartedAt)
}

func (ex *chatExecution) dispatchCanonicalBuffered(w http.ResponseWriter, seedCtx context.Context, startedAt time.Time) (*proto.HCSF, bool) {
	canonicalReq, _, err := ex.clientAdapter.RequestToCanonical(seedCtx, ex.body)
	if err != nil {
		_ = ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "invalid_request_body", ex.requestID)
		writeJSONError(w, http.StatusBadRequest, "invalid_request_body", err.Error())
		return nil, false
	}
	if canonicalReq == nil {
		_ = ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "invalid_request_body", ex.requestID)
		writeJSONError(w, http.StatusBadRequest, "invalid_request_body", "client adapter returned nil canonical envelope")
		return nil, false
	}
	enrichCanonicalRequestMeta(canonicalReq, ex.upstreamModelID, ex.accInfo.Platform, ex.idempotencyHeader, ex.promptHash)
	canonicalReq.RequestMeta.EndpointFamily = ex.resolved.ProtocolFamily
	setAccountingModelRequested(canonicalReq, ex.req.Model)
	setAccountingModelRouteDecided(canonicalReq, ex.forwardReq.Model)
	gateway.ApplyForwardRequestHopChain(canonicalReq, ex.forwardReq)

	dispatcher := hcsfDispatcher(ex.d)
	if dispatcher == nil {
		_ = ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "non_streaming_not_yet_wired", ex.requestID)
		writeJSONError(w, http.StatusServiceUnavailable, "non_streaming_not_yet_wired",
			fmt.Sprintf("dispatcher lacks HCSF dispatch support for client_protocol=%q protocol_family=%q", ex.clientProtocol, ex.resolved.ProtocolFamily))
		return nil, false
	}
	dispatchCtx := gateway.ContextWithHCSFDispatchInput(seedCtx, gateway.HCSFDispatchInput{
		ProtocolFamily:  ex.resolved.ProtocolFamily,
		UpstreamModelID: ex.upstreamModelID,
		Account:         ex.accInfo,
		Credential:      ex.cred,
		RawBody:         ex.body,
	})
	bufferedEnv, err := dispatcher.DispatchHCSF(dispatchCtx, canonicalReq)
	if err != nil {
		_ = ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "upstream_dispatch_error", ex.requestID)

		// 把上游真实 status / header / body 透传到 client + health classification,
		// 保留 401/429 retry 语义 + cooldown / rate-limit 信号 (codex review P1
		// 2026-05-19; 之前总是 502 + status=0 跟流式路径行为分叉).
		var upstreamErr *gateway.UpstreamHTTPError
		clientStatus := http.StatusBadGateway
		healthStatus := 0
		var healthHeader http.Header
		var classifyBody []byte
		if errors.As(err, &upstreamErr) {
			clientStatus = upstreamErr.StatusCode
			healthStatus = upstreamErr.StatusCode
			healthHeader = upstreamErr.Header
			classifyBody = upstreamErr.Body
		} else {
			classifyBody = []byte(err.Error())
		}

		if ex.healthKeyOK {
			classification, _ := gateway.Classify(healthStatus, healthHeader, classifyBody, ex.accInfo.Platform)
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, signalFromDispatchError(err, classification), healthStatus, time.Since(startedAt), ex.requestID, nil)
		}
		writeJSONError(w, clientStatus, "upstream_dispatch_error", err.Error())
		return nil, false
	}
	return ex.finalizeBufferedEnvelope(w, bufferedEnv, 0, startedAt)
}

func (ex *chatExecution) finalizeBufferedEnvelope(w http.ResponseWriter, env *proto.HCSF, statusCode int, startedAt time.Time) (*proto.HCSF, bool) {
	if env == nil || env.BufferedResponse == nil {
		_ = ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, ex.reserveRes.ClaimID, "upstream_empty_response", ex.requestID)
		if ex.healthKeyOK {
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, channelhealth.SignalChannelError, statusCode, time.Since(startedAt), ex.requestID, nil)
		}
		writeJSONError(w, http.StatusBadGateway, "upstream_empty_response", "dispatcher returned no buffered HCSF response")
		return nil, false
	}
	setAccountingModelRequested(env, ex.req.Model)
	setAccountingModelRouteDecided(env, ex.forwardReq.Model)
	fillAccountingModelUpstreamReported(env)
	env.Accounting.HopChain = gateway.BuildHopChain(ex.forwardReq, "", ex.startedAt, time.Now())
	if ex.healthKeyOK {
		recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, channelhealth.SignalSuccess, http.StatusOK, time.Since(startedAt), ex.requestID, nil)
	}
	return env, true
}
