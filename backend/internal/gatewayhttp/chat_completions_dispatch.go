package gatewayhttp

import (
	"context"
	"errors"
	"expvar"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

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
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/chatpipe"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/clientgate"
	"github.com/BloomingProsperity/HUAKAI/internal/httpkeepalive"
	"github.com/BloomingProsperity/HUAKAI/internal/payloadhash"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/protosse"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
	"github.com/BloomingProsperity/HUAKAI/internal/quotaenforce"
	"github.com/BloomingProsperity/HUAKAI/internal/rate"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/servingcapability"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementintent"
	"github.com/BloomingProsperity/HUAKAI/internal/tokenestimate"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

var quotaReserveFailedOpenTotal = expvar.NewInt("quota_reserve_failed_open_total")

// quotaDeniedTotal 计 token/cost/requests 等配额硬拒次数(区别于 fail-open),
// 供运营观测配额拦截命中率。
var quotaDeniedTotal = expvar.NewInt("quota_denied_total")

func newChatExecution(d ChatHandlerDeps, r *http.Request, ident auth.Identity, validated chatValidatedRequest, startedAt time.Time) *chatExecution {
	queueWaitNow := d.QueueWaitNow
	if queueWaitNow == nil {
		queueWaitNow = time.Now
	}
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
		settlementIntent:                 settlementintent.NewTracker(d.SettlementIntents, d.SettlementIntentEnabled),
		streamInputOnlyInterruptedPolicy: d.BillingPolicyResolver.ResolveStreamInputOnlyInterruptedPolicy(r.Context(), ident.TenantID),
		balanceEnforcementMode:           d.BillingPolicyResolver.ResolveBalanceEnforcementMode(r.Context(), ident.TenantID),
		queueWaitNow:                     queueWaitNow,
	}
}

func requestClientSessionID(r *http.Request, validated chatValidatedRequest) string {
	return chatpipe.RequestClientSessionID(r, validated.ClientProtocol, validated.Body)
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
	ex.sessionHash = chatpipe.RequestSessionHash(ex.clientProtocol, ex.body, ex.promptHash, ex.clientSessionID)
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

func (ex *chatExecution) prepareRoute(w http.ResponseWriter) bool {
	// resolveModelWithEffortSuffix 把感知 registry 的 effort 后缀规范化折入解析过程。
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

	// 在任何尝试之前,从原始请求体一次性推导出请求的能力需求——它们在重试间
	// 是稳定的——这样 Router 就能要求匹配的账号 capability_flags
	// (vision/tools/json/audio)。Stream 仍由已解析的请求标志驱动。
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
	if ex != nil && ex.officialDirect && ex.resolved.ProtocolFamily == "anthropic_claude_session" {
		return gateway.DispatchBodyControls{}
	}
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

// activeBindingRateLimits 取命中 binding 的 per-binding RPM/TPM 限额(model_pool_bindings.rpm_limit/
// tpm_limit),透传给 BindingRateLimitSelector 做预算闸。无 binding 或未设限额 → 0(该维度不限)。
func (ex *chatExecution) activeBindingRateLimits() (bindingID, rpm, tpm int64) {
	binding, ok := ex.activeBindingMetadata()
	if !ok {
		return 0, 0, 0
	}
	return binding.BindingID, deref32OrZero(binding.RPMLimit), deref32OrZero(binding.TPMLimit)
}

func deref32OrZero(p *int32) int64 {
	if p == nil {
		return 0
	}
	return int64(*p)
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
	ex.settlementIntent.InsertPending(ex.ctx, ex.ident.TenantID, ex.requestID, ex.logicalRequestID, reserveRes.ClaimID, reserveRes.AttemptSeq, ex.ident.APIKeyID, ex.payloadHash, predictedCost)
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
		abortErr := ex.abortReservation(reserveRes.ClaimID, "quota_denied", 0, ex.protocolLoss)
		if abortErr != nil {
			setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
		}
		quotaDeniedTotal.Add(1)
		logInternalError(ex.ctx, ex.requestID, clienterr.CodeInsufficientBalance, err)
		writeInsufficientQuotaErrorRetryable(w, quotaenforce.DenyRetryAfter(result, err), quotaenforce.DenyWindowKind(result, err))
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
		ex.payloadHash = payloadhash.Sum(ex.body)
	}
}

func (ex *chatExecution) selectPoolAccount(w http.ResponseWriter, in attemptInput) *classifiedAttemptFailure {
	// 同一 prompt prefix 固定到同一账号，提高 vendor prompt cache 命中率。
	ex.refreshRequestSessionHashes()
	selReq := ex.buildPoolSelectionRequest(in)
	selRes, err := ex.d.Selector.Select(ex.ctx, selReq)
	// 把任何池选号错误(含 SEC-249/250 的 per-key 限流)映射到对应的 HTTP 失败
	// + claim 终止，集中交给 classifyPoolSelectFailure。
	if failure := ex.classifyPoolSelectFailure(w, err); failure != nil {
		return failure
	}
	if selRes != nil && selRes.WaitPlan != nil {
		return ex.handleQueueWaitPlan(w, selReq, selRes.WaitPlan)
	}
	if selRes == nil || selRes.AccountID == 0 {
		abortErr := ex.abortReservation(ex.reserveRes.ClaimID, "pool_select_no_account", 0, ex.protocolLoss)
		failure := retryableLocalAttemptFailure(http.StatusServiceUnavailable, clienterr.CodeNoCapacity, clienterr.MessageFor(clienterr.CodeNoCapacity), "pool_select_no_account", gateway.UpstreamError5xx, nil)
		// 此分支 err 为 nil(无哨兵携带恢复时刻),给一个默认 Retry-After 修掉"503 却无退避头"缺陷,
		// 避免客户端盲目重试。与无容量错误路径的回退值一致。
		failure.RetryAfterSeconds = noCapacityFallbackRetryAfter
		return degradeFailureIfAbortFailed(ex.ctx, ex.requestID, failure, abortErr)
	}
	ex.acceptPoolSelection(selRes)
	return nil
}

func (ex *chatExecution) buildPoolSelectionRequest(in attemptInput) pool.SelectionRequest {
	attemptSeq := in.AttemptSeq
	if attemptSeq <= 0 {
		attemptSeq = ex.activeAttemptSeq()
	}
	excludedAccounts := in.ExcludedAccounts
	if excludedAccounts == nil {
		excludedAccounts = map[int64]struct{}{}
	}
	// estInput 是 per-key / per-binding / per-account(ROUTE-121)TPM 限流器按 token 累积窗口的增量源,
	// 必须**无条件估算**:此前它只在 model-fallback 开启时算,默认(回退关闭)恒 0 → 三个 TPM 限流器
	// 的窗口永不累积、配置的 TPM 上限被静默绕过(对抗 bug-hunt S2 数据/限额正确性缺陷)。
	// tokenestimate.Estimate 是廉价的单次字符分类扫描。ctxWindow / maxOut 仅服务 ROUTE-023 的
	// context-window 预检(它对 ctxWindow<=0 / estInput<=0 fail-open),故仍随 model-fallback 门控。
	var ctxWindow, maxOut int
	estInput := tokenestimate.Estimate(ex.body, ex.resolved.ProtocolFamily)
	if ex.modelFallbackEnabled {
		ctxWindow = ex.resolved.ContextWindow
		maxOut = derefIntOrZero(ex.req.MaxTokens)
	}
	bindingID, bindingRPM, bindingTPM := ex.activeBindingRateLimits()
	if ex.attempt.BindingID > 0 {
		// 并发上限与 BindingID 都来自同一 AttemptPlan，避免跨 pool fallback 时
		// 把新 attempt 的 K 配到上一条 binding 上。
		bindingID = ex.attempt.BindingID
	}
	return pool.SelectionRequest{
		TenantID:         ex.ident.TenantID,
		UserID:           ex.ident.UserID,
		APIKeyID:         ex.ident.APIKeyID,
		PoolGroupID:      ex.attempt.PoolGroupID,
		RequestedModel:   ex.req.Model,
		ModelCooldownKey: ex.upstreamModelID,
		ProtocolFamily:   ex.resolved.ProtocolFamily,
		EndpointFamily:   ex.d.effectiveEndpointFamily(),
		ClaimID:          ex.reserveRes.ClaimID,
		AttemptSeq:       attemptSeq,
		ExcludedAccounts: excludedAccounts,
		CapabilityFlags:  ex.attempt.RequiredCapabilities,
		SessionHash:      ex.sessionHash,
		Vendor:           pool.VendorFromProtocolFamily(ex.resolved.ProtocolFamily),
		UserGroup:        ex.ident.UserGroup,
		// 命中 binding 的 selection_mode 透传给 selector(priority_weighted 才激活加权,否则均匀)。
		SelectionMode:        ex.activeBindingSelectionMode(),
		ModelContextWindow:   ctxWindow,
		EstimatedInputTokens: estInput,
		MaxOutputTokens:      maxOut,
		// 命中 binding 的 per-binding RPM/TPM 限额透传给 BindingRateLimitSelector(env 门控 + 限额>0 才强制)。
		BindingID:           bindingID,
		BindingRPMLimit:     bindingRPM,
		BindingTPMLimit:     bindingTPM,
		MaxParallelRequests: ex.attempt.MaxParallelRequests,
	}
}

// derefIntOrZero 在 p 非 nil 且为正时返回 *p,否则返回 0。用于把可选的
// 客户端 max_tokens 转换为 context-window 闸门的输出预留,
// 未指定时不做填充。
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
		abortErr := ex.abortReservation(ex.reserveRes.ClaimID, "credential_resolve_error", 0, ex.protocolLoss)
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
	if ex.resolved.ProtocolFamily == registrydefault.ProtocolAnthropicClaudeSession {
		runtimeKind, ok := servingcapability.RuntimeKindForProviderCredential(ex.cred.Type)
		if !ok {
			runtimeKind = string(ex.cred.Type)
		}
		if err := servingcapability.ValidateAccountCompatibility(ex.resolved.ProtocolFamily, accInfo.Platform, accInfo.AccountType, runtimeKind); err != nil {
			abortErr := ex.abortReservation(ex.reserveRes.ClaimID, "credential_protocol_incompatible", 0, ex.protocolLoss)
			failure := classifiedFailureFromDecision(clienterr.CodeCredentialResolveError, clienterr.MessageFor(clienterr.CodeCredentialResolveError), gateway.Classification{}, gateway.AttemptRetryDecision{
				RetryableBeforeDelivery:         true,
				SwitchAccount:                   true,
				ClientStatus:                    http.StatusServiceUnavailable,
				AbortReason:                     "credential_protocol_incompatible",
				CountsAgainstAuthFailoverBudget: true,
			}, err)
			failure.EndClass = gateway.UpstreamError5xx
			return degradeFailureIfAbortFailed(ex.ctx, ex.requestID, failure, abortErr)
		}
	}
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
	if failure := ex.enforceOfficialClient(); failure != nil {
		return failure
	}
	return nil
}

// enforceOfficialClient 对 oauth/session 账号执行客户端准入；拒绝时释放预扣并返回 403。
// API key 等既有账号类型保持原行为。
func (ex *chatExecution) enforceOfficialClient() *classifiedAttemptFailure {
	result := clientgate.DecideWithBody(ex.ctx, ex.d.PlatformSettings, ex.accInfo.AccountType, ex.accInfo.Platform, ex.accInfo.CodexCLIOnly, ex.r, ex.body)
	if result.Decision == clientgate.DecisionOfficialDirect {
		ex.body = result.Body
		ex.officialDirect = true
		return nil
	}
	if result.Decision != clientgate.DecisionReject {
		return nil
	}
	reason := result.Reason
	if reason == "" {
		reason = clientgate.ReasonOfficialClientRequired
	}
	abortErr := ex.abortReservation(ex.reserveRes.ClaimID, reason, 0, ex.protocolLoss)
	failure := terminalLocalAttemptFailure(http.StatusForbidden, clienterr.CodeOfficialClientRequired, clienterr.MessageFor(clienterr.CodeOfficialClientRequired), reason, nil)
	return degradeFailureIfAbortFailed(ex.ctx, ex.requestID, failure, abortErr)
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
		if abortErr := ex.abortReservation(ex.reserveRes.ClaimID, "invalid_request_body", 0, ex.protocolLoss); abortErr != nil {
			setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
		}
		writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusBadRequest, clienterr.CodeInvalidRequestBody, err)
		return nil, nil, false
	}
	if canonicalReq == nil {
		if abortErr := ex.abortReservation(ex.reserveRes.ClaimID, "invalid_request_body", 0, ex.protocolLoss); abortErr != nil {
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
		if abortErr := ex.abortReservation(ex.reserveRes.ClaimID, "non_streaming_not_yet_wired", 0, ex.protocolLoss); abortErr != nil {
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
		OfficialDirect:    ex.officialDirect,
		// R7 三路闭环第三路:HCSF canonical 非流式(默认走)。改写施加在 dispatcher marshal 出的最终上游 body 上(anthropic 往返丢 metadata,入口改 ex.body 流不过去);默认关时空操作字节等价、不污染缓存键。
		IdentityRewrite: func(body []byte) []byte {
			return chatpipe.OutboundDispatchBody(ex.officialDirect, ex.resolved.ProtocolFamily, body, ex.identityRewrite)
		},
	})
	// DispatchHCSF 是 canonical buffered 慢接缝(完整上游往返+聚合),keepalive 保活;Stop 在写 w 前。
	canonicalKeepalive := httpkeepalive.Start(w, ex.d.NonStreamKeepAliveInterval)
	bufferedEnv, err := dispatcher.DispatchHCSF(dispatchCtx, canonicalReq)
	canonicalKeepalive.Stop()
	// DispatchHCSF 内 MarshalToProviderRequest 会原地往 canonicalReq.CapabilityGraph.ProtocolLoss
	// 追加 canonical→upstream marshal 损失(addMarshalLossRaw)。下方 dispatch-error 与
	// finalizeBufferedEnvelope 的 empty-response abort 都走 ex.protocolLoss 快照,必须在此刷新,
	// 否则只剩 dispatch 前的请求翻译损失,marshal 证据整段丢失。
	// 成功路径稍后由 billing 快照从 bufferedEnv(已含 marshal+resp 损失)覆盖,无重复累加。
	ex.protocolLoss = protocolLossJSONFromEnv(canonicalReq)
	if err != nil {
		if errors.Is(err, gateway.ErrUpstreamResponseTooLarge) {
			// 上游 2xx 响应超 1MiB 上限：终止(重试不会让响应变小)，client 拿 upstream_response_too_large
			// 而非把截断字节喂 ProviderResponseToCanonical 后塌成的 opaque upstream_dispatch_error(502)。
			// 与 legacy raw 路径(readRawBufferedUpstreamBody → CodeUpstreamResponseTooLarge)行为一致。
			abortErr := ex.abortReservation(ex.reserveRes.ClaimID, "upstream_response_too_large", 0, ex.protocolLoss)
			if ex.healthKeyOK {
				recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, channelhealth.SignalChannelError, http.StatusOK, time.Since(startedAt), ex.requestID, nil, 0)
			}
			failure := terminalLocalAttemptFailure(http.StatusBadGateway, clienterr.CodeUpstreamResponseTooLarge, clienterr.MessageFor(clienterr.CodeUpstreamResponseTooLarge), "upstream_response_too_large", err)
			return nil, degradeFailureIfAbortFailed(ex.ctx, ex.requestID, failure, abortErr), false
		}
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
			decision, classification, _ = gateway.ClassifyAttemptHTTPError(upstreamErr.StatusCode, upstreamErr.Header, upstreamErr.Body, ex.errorClassProvider())
		} else {
			classifyBody = []byte(err.Error())
			classification, _ = gateway.Classify(0, nil, classifyBody, ex.errorClassProvider())
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
		abortErr := ex.abortReservation(ex.reserveRes.ClaimID, decision.AbortReason, 0, ex.protocolLoss)
		modelScopedRateLimit := false
		if upstreamErr != nil {
			modelScopedRateLimit = ex.applyUpstreamErrorCooldown(upstreamErr, classification, true)
		}

		if ex.healthKeyOK && !modelScopedRateLimit {
			// canonical 缓冲是默认主路径:必须带真实 iron-clad 分级,否则该路径上的铁证 401
			// 永远按 ambiguous 处理、strike 硬禁不可达(审查 S2)。
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, signalFromDispatchError(err, classification), healthStatus, time.Since(startedAt), ex.requestID, rateLimitResetFromClassification(classification, time.Now()), gateway.AuthFailureClassFromClassification(classification))
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

func (ex *chatExecution) shouldAggregateForcedStreamingBuffered() bool {
	if ex == nil {
		return false
	}
	return gateway.ForcedStreamingBufferedFamily(ex.resolved.ProtocolFamily)
}

func (ex *chatExecution) dispatchForcedStreamingBuffered(w http.ResponseWriter, dispatchRes *gateway.DispatchResult, seed proto.RequestMetaSeed, seedCtx context.Context, startedAt time.Time) (*proto.HCSF, *classifiedAttemptFailure, bool) {
	ex.updateSessionWindowFromHeaders(dispatchRes.Headers)
	upstreamAdapter, err := protocolAdapterForBuffered(ex.d.Forwarder, ex.resolved.ProtocolFamily)
	if err != nil {
		if abortErr := ex.abortReservation(ex.reserveRes.ClaimID, "upstream_adapter_error", 0, nil); abortErr != nil {
			setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
		}
		writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusBadGateway, clienterr.CodeUpstreamAdapterError, err)
		return nil, nil, false
	}
	bufferedEnv, _, ok, err := protosse.ReconstructBufferedFromSSEReader(seedCtx, upstreamAdapter, dispatchRes.UpstreamReader, protosse.DefaultBufferedSSEReconstructLimits())
	if err != nil {
		code := clienterr.CodeCanonicalResponseError
		if errors.Is(err, protosse.ErrBufferedSSECanonicalTooLarge) {
			code = clienterr.CodeUpstreamResponseTooLarge
		}
		if abortErr := ex.abortReservation(ex.reserveRes.ClaimID, code, 0, nil); abortErr != nil {
			setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
		}
		if ex.healthKeyOK {
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, channelhealth.SignalChannelError, dispatchRes.StatusCode, time.Since(startedAt), ex.requestID, nil, 0)
		}
		writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusBadGateway, code, err)
		return nil, nil, false
	}
	if !ok || bufferedEnv == nil || bufferedEnv.BufferedResponse == nil {
		if abortErr := ex.abortReservation(ex.reserveRes.ClaimID, "canonical_response_error", 0, nil); abortErr != nil {
			setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
		}
		if ex.healthKeyOK {
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, channelhealth.SignalChannelError, dispatchRes.StatusCode, time.Since(startedAt), ex.requestID, nil, 0)
		}
		writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusBadGateway, clienterr.CodeCanonicalResponseError, errors.New("forced streaming upstream did not return reconstructable SSE"))
		return nil, nil, false
	}
	_ = seed.ApplyToRequestMeta(&bufferedEnv.RequestMeta)
	enrichCanonicalRequestMeta(bufferedEnv, ex.upstreamModelID, ex.accInfo.Platform, ex.idempotencyHeader, ex.sessionHash)
	return ex.finalizeBufferedEnvelope(w, bufferedEnv, dispatchRes.StatusCode, startedAt)
}

func (ex *chatExecution) forceCooldownFromDecision(dec rate.Decision) {
	if ex == nil || ex.d.ChannelHealth == nil || !ex.healthKeyOK {
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
		abortErr := ex.abortReservation(ex.reserveRes.ClaimID, "upstream_empty_response", 0, ex.protocolLoss)
		if ex.healthKeyOK {
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, channelhealth.SignalChannelError, statusCode, time.Since(startedAt), ex.requestID, nil, 0)
		}
		failure := retryableLocalAttemptFailure(http.StatusBadGateway, clienterr.CodeUpstreamEmptyResponse, clienterr.MessageFor(clienterr.CodeUpstreamEmptyResponse), "upstream_empty_response", gateway.UpstreamError5xx, nil)
		return nil, degradeFailureIfAbortFailed(ex.ctx, ex.requestID, failure, abortErr), false
	}
	setAccountingModelRequested(env, ex.req.Model)
	setAccountingModelRouteDecided(env, ex.forwardReq.Model)
	fillAccountingModelUpstreamReported(env)
	env.Accounting.HopChain = gateway.BuildHopChain(ex.forwardReq, "", ex.startedAt, time.Now())
	if ex.healthKeyOK {
		recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, channelhealth.SignalSuccess, http.StatusOK, time.Since(startedAt), ex.requestID, nil, 0)
	}
	return env, nil, true
}
