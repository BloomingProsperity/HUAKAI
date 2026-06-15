package gatewayhttp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"expvar"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/affinityrules"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/bodyfeatures"
	"github.com/BloomingProsperity/HUAKAI/internal/cache_routing"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/quotaenforce"
	"github.com/BloomingProsperity/HUAKAI/internal/rate"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/tokenestimate"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

var quotaReserveFailedOpenTotal = expvar.NewInt("quota_reserve_failed_open_total")

// quotaDeniedTotal 计 token/cost/requests 等配额硬拒次数(区别于 fail-open),
// 供运营观测配额拦截命中率。
var quotaDeniedTotal = expvar.NewInt("quota_denied_total")

const (
	clientSessionIDMaxLength = 200
	clientSessionHashPrefix  = "client-session:"
	clientSessionHashDomain  = "huakai:client-session:v1:"
)

var clientSessionIDHeaderPriority = []string{
	"X-Session-ID",
	"X-Amp-Thread-Id",
	"Session-Id",
	"X-Client-Request-Id",
}

var openAIMetadataUserIDSessionSuffixRE = regexp.MustCompile(`_session_([a-f0-9-]+)$`)

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
		clientSessionID:                  requestClientSessionID(r, validated),
		streamInputOnlyInterruptedPolicy: d.BillingPolicyResolver.ResolveStreamInputOnlyInterruptedPolicy(r.Context(), ident.TenantID),
		balanceEnforcementMode:           d.BillingPolicyResolver.ResolveBalanceEnforcementMode(r.Context(), ident.TenantID),
	}
}

func requestClientSessionID(r *http.Request, validated chatValidatedRequest) string {
	if r != nil {
		for _, header := range clientSessionIDHeaderPriority {
			if id := normalizeClientSessionID(r.Header.Get(header)); id != "" {
				return id
			}
		}
	}
	if !isOpenAIClientProtocol(validated.ClientProtocol) {
		return ""
	}
	return openAITopLevelClientSessionID(validated.Body)
}

func isOpenAIClientProtocol(clientProtocol proto.ClientProtocol) bool {
	switch clientProtocol {
	case proto.ClientProtocolOpenAIChat, proto.ClientProtocolOpenAIResponses:
		return true
	default:
		return false
	}
}

func openAITopLevelClientSessionID(rawBody []byte) string {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(rawBody, &top); err != nil || top == nil {
		return ""
	}
	for _, key := range []string{"conversation_id", "session_id"} {
		raw, ok := top[key]
		if !ok {
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			continue
		}
		if id := normalizeClientSessionID(value); id != "" {
			return id
		}
	}
	if raw, ok := top["metadata"]; ok {
		var metadata struct {
			UserID string `json:"user_id"`
		}
		if err := json.Unmarshal(raw, &metadata); err == nil {
			userID := strings.TrimSpace(metadata.UserID)
			if match := openAIMetadataUserIDSessionSuffixRE.FindStringSubmatch(userID); len(match) == 2 {
				return normalizeClientSessionID(match[1])
			}
			return normalizeClientSessionID(metadata.UserID)
		}
	}
	return ""
}

func normalizeClientSessionID(raw string) string {
	id := strings.TrimSpace(raw)
	if id == "" || len(id) > clientSessionIDMaxLength {
		return ""
	}
	for _, r := range id {
		if unicode.IsControl(r) {
			return ""
		}
	}
	return id
}

type dispatchTransportSelection struct {
	account provider.AccountInfo
	mode    transport.TransportMode
}

func transportSelectionForDispatch(account provider.AccountInfo, protocolFamily string) dispatchTransportSelection {
	providerCode := transportProviderForDispatch(account, protocolFamily)
	account.Platform = string(providerCode)
	return dispatchTransportSelection{
		account: account,
		mode:    transportModeForProvider(providerCode, account.AccountType),
	}
}

func transportProviderForDispatch(account provider.AccountInfo, protocolFamily string) transport.ProviderCode {
	switch strings.ToLower(strings.TrimSpace(protocolFamily)) {
	case "openai_codex":
		return transport.ProviderOpenAICodex
	case "gemini_advanced_session":
		return transport.ProviderGeminiAdvanced
	case "antigravity_session":
		return transport.ProviderAntigravity
	case "cursor_session":
		return transport.ProviderCursor
	case "copilot_session":
		return transport.ProviderCopilot
	case "kiro_session":
		return transport.ProviderKiro
	case "windsurf_session":
		return transport.ProviderWindsurf
	}

	providerCode := transport.ProviderCode(strings.ToLower(strings.TrimSpace(account.Platform)))
	switch providerCode {
	case transport.ProviderOpenAI:
		switch strings.ToLower(strings.TrimSpace(account.AccountType)) {
		case credentialstore.AuthModeChatGPTOAuth, credentialstore.AuthModeCodexCLIOAuth, credentialstore.AuthModeCodexWebOAuth:
			return transport.ProviderOpenAICodex
		}
	case transport.ProviderGemini:
		switch strings.ToLower(strings.TrimSpace(account.AccountType)) {
		case credentialstore.AuthModeCodeAssist, credentialstore.AuthModeGoogleOne:
			return transport.ProviderGeminiAdvanced
		case credentialstore.AuthModeAntigravity:
			return transport.ProviderAntigravity
		}
	}
	return providerCode
}

func transportModeForProvider(providerCode transport.ProviderCode, accountType string) transport.TransportMode {
	switch providerCode {
	case transport.ProviderOpenAICodex:
		return transport.TransportModeMimicryChatGPT
	case transport.ProviderGeminiAdvanced:
		return transport.TransportModeMimicryGeminiAdvanced
	case transport.ProviderAntigravity:
		return transport.TransportModeMimicryAntigravity
	case transport.ProviderCursor:
		return transport.TransportModeMimicryCursor
	case transport.ProviderCopilot:
		return transport.TransportModeMimicryCopilot
	case transport.ProviderKiro:
		return transport.TransportModeMimicryKiro
	case transport.ProviderWindsurf:
		return transport.TransportModeMimicryWindsurf
	case transport.ProviderAnthropic:
		switch strings.ToLower(strings.TrimSpace(accountType)) {
		case "oauth", "session", credentialstore.AuthModeClaudeAIOAuth, credentialstore.AuthModeClaudeCode:
			return transport.TransportModeMimicryClaudeCode
		}
	}
	return transport.TransportModeStandard
}

func (ex *chatExecution) refreshRequestSessionHashes() {
	ex.promptHash = cache_routing.ComputePromptHash(ex.body)
	if affinityKey, ok := ex.configuredAffinityKey(); ok {
		ex.sessionHash = affinityKey
		return
	}
	ex.sessionHash = requestSessionHash(ex.clientProtocol, ex.body, ex.promptHash, ex.clientSessionID)
}

func (ex *chatExecution) configuredAffinityKey() (string, bool) {
	if ex == nil || len(ex.d.AffinityRules) == 0 {
		return "", false
	}
	var path, userAgent string
	var header func(string) string
	if ex.r != nil {
		if ex.r.URL != nil {
			path = ex.r.URL.Path
		}
		userAgent = ex.r.UserAgent()
		header = ex.r.Header.Get
	}
	_, affinityKey, matched := ex.d.AffinityRules.Match(affinityrules.MatchRequest{
		Model:     ex.req.Model,
		Path:      path,
		UserAgent: userAgent,
		Header:    header,
		Body:      ex.body,
	})
	return affinityKey, matched
}

func requestSessionHash(clientProtocol proto.ClientProtocol, rawBody []byte, promptHash, clientSessionID string) string {
	if clientSessionID != "" {
		return clientSessionHash(clientSessionID)
	}
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

func clientSessionHash(clientSessionID string) string {
	sum := sha256.Sum256([]byte(clientSessionHashDomain + clientSessionID))
	return clientSessionHashPrefix + hex.EncodeToString(sum[:])
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
	// resolveModelWithEffortSuffix folds registry-aware effort-suffix normalization into resolution.
	resolved, err := ex.resolveModelWithEffortSuffix()
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

	// Derive the request's capability needs from the raw body once, before
	// any attempt — they are stable across retries — so the Router can demand
	// matching account capability_flags (vision/tools/json/audio). Stream
	// stays driven by the parsed request flag.
	wantsVision, wantsToolUse, wantsJSON, wantsAudio := bodyfeatures.Detect(ex.body)
	plan, err := ex.d.Router.Plan(ex.ctx, router.PlanInput{
		Context: router.RequestContext{
			TenantID:  ex.ident.TenantID,
			UserID:    ex.ident.UserID,
			APIKeyID:  ex.ident.APIKeyID,
			RequestID: ex.requestID,
		},
		Model: resolvedModel,
		Features: router.RequestFeatures{
			Stream:       ex.req.Stream,
			WantsVision:  wantsVision,
			WantsToolUse: wantsToolUse,
			WantsJSON:    wantsJSON,
			WantsAudio:   wantsAudio,
		},
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

func (ex *chatExecution) activeBindingMetadata() (registry.BindingMetadata, bool) {
	if ex == nil || len(ex.resolved.BindingMetadata) == 0 {
		return registry.BindingMetadata{}, false
	}
	poolGroupID := ex.attempt.PoolGroupID
	if poolGroupID != 0 {
		for _, binding := range ex.resolved.BindingMetadata {
			if binding.PoolGroupID == poolGroupID {
				return binding, true
			}
		}
	}
	if len(ex.resolved.BindingMetadata) == 1 {
		return ex.resolved.BindingMetadata[0], true
	}
	return registry.BindingMetadata{}, false
}

func (ex *chatExecution) activeDispatchBodyControls() gateway.DispatchBodyControls {
	binding, ok := ex.activeBindingMetadata()
	if !ok {
		return gateway.DispatchBodyControls{}
	}
	return gateway.DispatchBodyControlsFromBinding(binding)
}

func (ex *chatExecution) activeStatusCodeMapping() map[int]int {
	binding, ok := ex.activeBindingMetadata()
	if !ok {
		return nil
	}
	return binding.StatusCodeMapping
}

func (ex *chatExecution) activeForceFormat() bool {
	binding, ok := ex.activeBindingMetadata()
	return ok && binding.ForceFormat
}

func (ex *chatExecution) remapClientStatusForUpstream(upstreamStatus int, currentClientStatus int) int {
	mapping := ex.activeStatusCodeMapping()
	if len(mapping) == 0 {
		return currentClientStatus
	}
	if mapped, ok := mapping[upstreamStatus]; ok && mapped > 0 {
		return gateway.RemapClientStatus(upstreamStatus, map[int]int{upstreamStatus: mapped})
	}
	return currentClientStatus
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
		BalanceEnforcementMode:     ex.balanceEnforcementMode,
	})
	if errors.Is(err, billing.ErrFingerprintConflict) || (reserveRes != nil && reserveRes.FingerprintConflict) {
		writeJSONError(w, http.StatusConflict, "idempotency_conflict",
			"same logical_request_id with different normalized payload")
		return false
	}
	if errors.Is(err, billing.ErrInsufficientBalance) {
		logInternalError(ex.ctx, ex.requestID, clienterr.CodeInsufficientBalance, err)
		writeInsufficientBalanceError(w)
		return false
	}
	if errors.Is(err, billing.ErrClaimRace) {
		w.Header().Set("Retry-After", "1")
		writeJSONError(w, http.StatusConflict, clienterr.CodeClaimRace, clienterr.MessageFor(clienterr.CodeClaimRace))
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
	if !ex.reserveQuota(w, reserveRes, predictedCost) {
		return false
	}
	return true
}

func (ex *chatExecution) reserveQuota(w http.ResponseWriter, reserveRes *billing.ReserveResult, predictedCost decimal.Decimal) bool {
	if ex.d.QuotaReserver == nil || reserveRes == nil {
		return true
	}
	result, err := ex.d.QuotaReserver.Reserve(ex.ctx, quotaenforce.BuildReserveRequest(quotaenforce.ReserveInput{
		TenantID:           ex.ident.TenantID,
		UserID:             ex.ident.UserID,
		APIKeyID:           ex.ident.APIKeyID,
		ClaimID:            reserveRes.ClaimID,
		PoolGroupID:        ex.attempt.PoolGroupID,
		RequestFingerprint: ex.payloadHash,
		RequestedModel:     ex.req.Model,
		// W5:输入 token 估算喂进 token-per-window 配额预检(估算口径与计费预扣
		// estimateInputTokens 一致);未配 token 策略时引擎按 observe 跳过,零影响。
		ReservedTokens: int64(estimateInputTokens(ex.req.Model, ex.body)),
		PredictedCost:  predictedCost,
		At:             time.Now().UTC(),
	}))
	if err == nil && result.Allowed {
		return true
	}
	if quotaenforce.IsDenied(err) || (err == nil && !result.Allowed) {
		abortErr := ex.d.Settler.Abort(ex.ctx, ex.ident.TenantID, reserveRes.ClaimID, "quota_denied", ex.requestID, 0, ex.protocolLoss)
		if abortErr != nil {
			setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
		}
		quotaDeniedTotal.Add(1)
		logInternalError(ex.ctx, ex.requestID, clienterr.CodeInsufficientBalance, err)
		writeInsufficientQuotaErrorRetryable(w, quotaenforce.DenyRetryAfter(result, err))
		return false
	}
	quotaReserveFailedOpenTotal.Add(1)
	slog.WarnContext(ex.ctx, "quota reserve failed open",
		slog.String("request_id", ex.requestID),
		slog.Int64("tenant_id", ex.ident.TenantID),
		slog.Int64("claim_id", reserveRes.ClaimID),
		slog.String("reason", "quota_reserve_infra_error"),
		slog.String("error_type", fmt.Sprintf("%T", err)),
	)
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
	// ROUTE-023 Option B: opt-in context-window pre-check (default off — see commit).
	var ctxWindow, estInput, maxOut int
	if ex.modelFallbackEnabled {
		ctxWindow = ex.resolved.ContextWindow
		estInput = tokenestimate.Estimate(ex.body, ex.resolved.ProtocolFamily)
		maxOut = derefIntOrZero(ex.req.MaxTokens)
	}
	selRes, err := ex.d.Selector.Select(ex.ctx, pool.SelectionRequest{
		TenantID:             ex.ident.TenantID,
		UserID:               ex.ident.UserID,
		APIKeyID:             ex.ident.APIKeyID,
		PoolGroupID:          ex.attempt.PoolGroupID,
		RequestedModel:       ex.req.Model,
		ModelCooldownKey:     ex.upstreamModelID,
		ProtocolFamily:       ex.resolved.ProtocolFamily,
		EndpointFamily:       ex.d.effectiveEndpointFamily(),
		ClaimID:              ex.reserveRes.ClaimID,
		AttemptSeq:           attemptSeq,
		ExcludedAccounts:     excludedAccounts,
		CapabilityFlags:      ex.attempt.RequiredCapabilities,
		SessionHash:          ex.sessionHash,
		Vendor:               pool.VendorFromProtocolFamily(ex.resolved.ProtocolFamily),
		UserGroup:            ex.ident.UserGroup,
		ModelContextWindow:   ctxWindow,
		EstimatedInputTokens: estInput,
		MaxOutputTokens:      maxOut,
	})
	// Map any pool selection error (incl. SEC-249/250 per-key rate limit) to the
	// right HTTP failure + claim abort. Extracted to chat_completions_pool_errors.go.
	if failure := ex.classifyPoolSelectFailure(w, err); failure != nil {
		return failure
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
	// SUB2-EGRESS-02: register the session against the acquired account so
	// the SessionCountGate has an up-to-date view on the next selection.
	if ex.d.SessionCapRegistry != nil && ex.sessionHash != "" {
		ex.d.SessionCapRegistry.Register(ex.acquiredAccountID, ex.sessionHash)
	}
	return nil
}

// derefIntOrZero returns *p when p is non-nil and positive, else 0. Used to
// translate the optional client max_tokens into the context-window gate's
// output reservation without padding when unspecified.
func derefIntOrZero(p *int) int {
	if p == nil || *p < 0 {
		return 0
	}
	return *p
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
	ex.cred = credentialWithNativeStreamMode(cred, ex.clientProtocol, ex.req.Stream)
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

func credentialWithNativeStreamMode(cred provider.Credential, clientProtocol proto.ClientProtocol, stream bool) provider.Credential {
	if clientProtocol != proto.ClientProtocolGemini {
		return cred
	}
	extra := make(map[string]string, len(cred.Extra)+1)
	for k, v := range cred.Extra {
		extra[k] = v
	}
	if stream {
		extra["stream"] = "true"
	} else {
		extra["stream"] = "false"
	}
	cred.Extra = extra
	return cred
}

func (ex *chatExecution) dispatchBufferedEnvelope(w http.ResponseWriter) (*proto.HCSF, *classifiedAttemptFailure, bool) {
	upstreamAttemptStartedAt := time.Now()
	seed := requestMetaSeed(ex.r, ex.ident, ex.clientProtocol, ex.resolved.ProtocolFamily, ex.routeID, ex.requestID, ex.req.Model, ex.acquiredAccountID, ex.acquisitionToken)
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
	// billing 快照也累积请求侧证据(item 2;非流式 buffered 路径原本整段丢失)。
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
	transportSelection := transportSelectionForDispatch(ex.accInfo, ex.resolved.ProtocolFamily)
	dispatchCtx := gateway.ContextWithHCSFDispatchInput(seedCtx, gateway.HCSFDispatchInput{
		ProtocolFamily:    ex.resolved.ProtocolFamily,
		UpstreamModelID:   ex.upstreamModelID,
		Account:           transportSelection.account,
		Credential:        ex.cred,
		TransportMode:     transportSelection.mode,
		RawBody:           ex.upstreamInboundBody(ex.body),
		BodyControls:      ex.activeDispatchBodyControls(),
		InboundBetaTokens: ex.clientBetaTokens(),
	})
	bufferedEnv, err := dispatcher.DispatchHCSF(dispatchCtx, canonicalReq)
	// DispatchHCSF 内 MarshalToProviderRequest 会原地往 canonicalReq.CapabilityGraph.ProtocolLoss
	// 追加 canonical→upstream marshal 损失(addMarshalLossRaw)。下方 dispatch-error 与
	// finalizeBufferedEnvelope 的 empty-response abort 都走 ex.protocolLoss 快照,必须在此刷新,
	// 否则只剩 dispatch 前的请求翻译损失,marshal 证据整段丢失。
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
			decision, classification, _ = gateway.ClassifyAttemptHTTPError(upstreamErr.StatusCode, upstreamErr.Header, upstreamErr.Body, ex.accInfo.Platform)
			recordModelCooldownOnUpstream404(ex.ctx, ex.d, ex.ident.TenantID, ex.acquiredAccountID, ex.upstreamModelID, upstreamErr.StatusCode, ex.requestID)
			ex.forceCooldownFromUpstreamRateLimit(upstreamErr)
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
		if upstreamErr != nil {
			decision.ClientStatus = ex.remapClientStatusForUpstream(upstreamErr.StatusCode, decision.ClientStatus)
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

func (ex *chatExecution) forceCooldownFromUpstreamRateLimit(upstreamErr *gateway.UpstreamHTTPError) {
	if upstreamErr == nil || ex.d.RateService == nil || ex.d.ChannelHealth == nil || !ex.healthKeyOK {
		return
	}
	if !upstreamRateCooldownCandidate(upstreamErr.StatusCode) {
		return
	}
	// 不再因缺 Retry-After 头而早退:很多 provider 的 429/529 不带该头,HandleUpstreamError 对
	// 无头情形会施加默认冷却(defaultCooldown)。早退会让被限流账号永不冷却、被持续命中。
	// 若上游带了 Retry-After,HandleUpstreamError 内部(retryAfterCooldown)会解析并采用。
	dec, err := ex.d.RateService.HandleUpstreamError(ex.ctx, ex.acquiredAccountID, upstreamErr.StatusCode, upstreamErr.Header, upstreamErr.Body)
	if err != nil {
		logInternalError(ex.ctx, ex.requestID, "upstream_rate_cooldown_decision_failed", err)
		return
	}
	if dec.StateChange == rate.StateNoChange || dec.CooldownUntil.IsZero() {
		return
	}
	_, _ = ex.d.ChannelHealth.ForceCooldown(ex.ctx, ex.healthKey, dec.CooldownUntil, string(dec.Reason))
}

func upstreamRateCooldownCandidate(statusCode int) bool {
	switch statusCode {
	case http.StatusTooManyRequests, 529,
		http.StatusRequestTimeout,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
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
