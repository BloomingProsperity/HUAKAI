package imageshttp

import (
	"io"
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
	fallbackexec "github.com/BloomingProsperity/HUAKAI/internal/bindingfallback/executor"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/httpkeepalive"
	"github.com/BloomingProsperity/HUAKAI/internal/imagepricing"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/relaybody"
	"github.com/BloomingProsperity/HUAKAI/internal/servingcapability"
	"github.com/BloomingProsperity/HUAKAI/internal/upstreamfeedback"
)

func (ex *execution) selectAccount(w http.ResponseWriter, attemptSeq int) *fallbackexec.Failure {
	sel, err := ex.d.Selector.Select(ex.ctx, pool.SelectionRequest{
		TenantID:             ex.ident.TenantID,
		UserID:               ex.ident.UserID,
		APIKeyID:             ex.ident.APIKeyID,
		PoolGroupID:          ex.attempt.PoolGroupID,
		RequestedModel:       ex.req.Model,
		ProviderModelID:      ex.upstreamModelID,
		ModelCooldownKey:     ex.upstreamModelID,
		ProtocolFamily:       ex.resolved.ProtocolFamily,
		EndpointFamily:       endpointFamilyImages,
		ClaimID:              ex.reserveRes.ClaimID,
		AttemptSeq:           attemptSeq,
		CapabilityFlags:      ex.attempt.RequiredCapabilities,
		SessionHash:          ex.payloadHash,
		RequestID:            ex.requestID,
		Vendor:               pool.VendorFromProtocolFamily(ex.resolved.ProtocolFamily),
		UserGroup:            ex.ident.UserGroup,
		SelectionMode:        ex.attempt.SelectionMode,
		BindingID:            ex.attempt.BindingID,
		BindingRPMLimit:      ex.attempt.BindingRPMLimit,
		BindingTPMLimit:      ex.attempt.BindingTPMLimit,
		MaxParallelRequests:  ex.attempt.MaxParallelRequests,
		EstimatedInputTokens: estimatePromptTokens(ex.req.PromptText()),
		ModelContextWindow:   ex.resolved.ContextWindow,
		ExcludedAccounts:     ex.excludedAccounts,
	})
	if err != nil || sel == nil || sel.AccountID == 0 || sel.WaitPlan != nil {
		failureErr := err
		if failureErr == nil {
			failureErr = pool.ErrNoEligibleAccount
			if sel != nil && sel.WaitPlan != nil {
				failureErr = pool.ErrNoSlotAvailable
			}
		}
		failure := fallbackexec.PoolFailure(failureErr)
		if !ex.abort(w, failure.AbortReason, 0) {
			return fallbackexec.AbortFailure()
		}
		return failure
	}
	if ex.classTransition != nil && bindingfallback.NormalizeClass(string(ex.attempt.FallbackClass)) != bindingfallback.ClassNormal {
		sel.RoutingReasonJSON = bindingfallback.AnnotateRoutingReason(sel.RoutingReasonJSON, *ex.classTransition)
	}
	ex.selRes = sel
	return nil
}

func (ex *execution) resolveCredential(w http.ResponseWriter) bool {
	cred, acc, err := ex.d.CredentialVault.Resolve(ex.ctx, ex.ident.TenantID, ex.selRes.AccountID)
	if err != nil {
		if !ex.abort(w, "credential_resolve_error", 0) {
			fallbackexec.WriteHTTP(w, fallbackexec.AbortFailure())
		} else {
			writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodeCredentialResolveError, clienterr.MessageFor(clienterr.CodeCredentialResolveError))
		}
		return false
	}
	if acc.AccountID == 0 {
		acc.AccountID = ex.selRes.AccountID
	}
	ex.cred = cred
	ex.accInfo = acc
	return true
}

// credentialCompatibilityFailure 发网前校验凭据形态与协议族匹配(oauth 号不能打
// api-key 直连等)。不匹配=本号静态必败:退预留、经授权换号子预算换下一个号,
// 绝不带着错配凭据发网(上游 401 白烧一轮还可能触发风控)。
func (ex *execution) credentialCompatibilityFailure(w http.ResponseWriter) *fallbackexec.Failure {
	if err := servingcapability.ValidateRuntimeAccountCompatibility(ex.resolved.ProtocolFamily, ex.cred, ex.accInfo); err == nil {
		return nil
	}
	failure := fallbackexec.CredentialCompatibilityFailure()
	if !ex.abort(w, failure.AbortReason, 0) {
		return fallbackexec.AbortFailure()
	}
	return failure
}

// failAttempt 统一失败收尾:keepalive 已向客户端写过字节(deliveryStarted)后
// 绝不换号重试(响应已开始交付),但仍要把终态错误体写完——否则客户端只收到
// 一串保活换行就被挂断,拿不到任何错误说明。
func (ex *execution) failAttempt(w http.ResponseWriter, failure *fallbackexec.Failure) attemptOutcome {
	if ex.deliveryStarted {
		fallbackexec.WriteHTTP(w, failure)
		return attemptOutcome{done: true}
	}
	return attemptOutcome{failure: failure}
}

func (ex *execution) dispatchAndSettle(w http.ResponseWriter, attemptSeq int) attemptOutcome {
	// 把出站 body 的 model 改写成解析后的上游 id。JSON 只改 body 不动 CT(保持原行为=adapter默认);
	// multipart(edits/variations)才设 InboundContentType(顺带补上 image 原先缺失的 CT)。
	originalContentType := ex.r.Header.Get("Content-Type")
	inboundBody, inboundCT, _ := relaybody.RewriteModel(ex.body, originalContentType, ex.upstreamModelID)
	dispatchInput := gateway.DispatchInput{
		ProtocolFamily:  ex.resolved.ProtocolFamily,
		EndpointPath:    ex.endpoint.Path(),
		UpstreamModelID: ex.upstreamModelID,
		InboundBody:     inboundBody,
		Account:         ex.accInfo,
		Credential:      ex.cred,
	}
	if _, isMultipart := multipartBoundary(originalContentType); isMultipart {
		dispatchInput.InboundContentType = inboundCT
	}
	// 图片同步 API 常在 Dispatch(等上游生成完再回 header)一步就阻塞数十秒,期间对客户端零字节;
	// 起 keepalive 保活避开反代空闲超时,Stop 在下方任何写 w 之前(含错误路径)。
	dispatchKeepalive := httpkeepalive.Start(w, ex.d.NonStreamKeepAliveInterval)
	res, err := ex.d.Dispatcher.Dispatch(ex.ctx, dispatchInput)
	dispatchKeepalive.Stop()
	ex.deliveryStarted = dispatchKeepalive.Started()
	if err != nil {
		ex.observeDispatchError(err)
		failure := fallbackexec.DispatchFailure(err)
		if !ex.abort(w, failure.AbortReason, 0) {
			failure = fallbackexec.AbortFailure()
		}
		return ex.failAttempt(w, failure)
	}
	if res == nil || res.UpstreamReader == nil {
		ex.observeChannelError(0)
		failure := fallbackexec.EmptyResponseFailure()
		if !ex.abort(w, failure.AbortReason, 0) {
			failure = fallbackexec.AbortFailure()
		}
		return ex.failAttempt(w, failure)
	}
	defer func() {
		if res.Close != nil {
			_ = res.Close()
		}
	}()
	return ex.finishUpstreamResponse(w, res, attemptSeq)
}

func (ex *execution) finishUpstreamResponse(w http.ResponseWriter, res *gateway.DispatchResult, attemptSeq int) attemptOutcome {
	// 若上游改在 body 读阶段才慢(early header + 延迟 body),读全 body 也起 keepalive 兜住;
	// Stop 在下方任何写 w 之前。与 Dispatch 处的保活互补,覆盖两种慢点形态。
	readKeepalive := httpkeepalive.Start(w, ex.d.NonStreamKeepAliveInterval)
	raw, readErr := readUpstreamBody(res.UpstreamReader)
	readKeepalive.Stop()
	ex.deliveryStarted = ex.deliveryStarted || readKeepalive.Started()
	if readErr != nil {
		ex.observeChannelError(res.StatusCode)
		failure := fallbackexec.ReadFailure(readErr)
		if !ex.abort(w, failure.AbortReason, 0) {
			failure = fallbackexec.AbortFailure()
		}
		return ex.failAttempt(w, failure)
	}
	// 非 2xx 必须先于空 body 判定:400/401 常带空 body,先判空会把终态客户端错误
	// 伪装成可重试的 empty_response(400 换号重试、401 失去授权换号语义)。
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		observed := ex.observeHTTPError(res, raw)
		failure := fallbackexec.UpstreamFailureFromDecision(res.StatusCode, raw, observed.Decision, observed.Classification)
		abortErr, sideEffectRetrySafe := ex.abortHTTPFailure(w, failure.AbortReason, raw)
		if abortErr != nil {
			// abort 失败=预留状态不明,绝不再开下一 attempt(防双份扣费);仍按上游
			// 语义回客户端,X-Huakai-Abort-Failed 头已由 abort 助手落下。
			failure.RetryPermitted = false
			failure.AuthFailoverEligible = false
			failure.SideEffectRetrySafe = false
		} else if !ex.familyRetrySafe(failure, sideEffectRetrySafe) {
			// family 已产生上游侧付费副作用(如 Replicate prediction 未确认取消),
			// 换号重试=第二个号再建付费任务(重复扣费),降为终态。
			failure.RetryPermitted = false
			failure.AuthFailoverEligible = false
			failure.SideEffectRetrySafe = false
		}
		return ex.failAttempt(w, failure)
	}
	if strings.TrimSpace(string(raw)) == "" {
		failure := fallbackexec.EmptyResponseFailure()
		if !ex.abort(w, failure.AbortReason, 0) {
			failure = fallbackexec.AbortFailure()
		}
		return ex.failAttempt(w, failure)
	}
	// family 专属响应翻译(replicate_image:prediction → OpenAI 形)必须在
	// settle/写客户端之前;翻译失败按上游错误处理,绝不 settle 计费。
	raw, ok := ex.translateUpstreamResponseForFamily(w, raw)
	if !ok {
		return attemptOutcome{done: true}
	}
	if ex.resolved.ProtocolFamily == openAICodexFamily {
		res.Headers.Set("Content-Type", "application/json")
	}
	_ = ex.settleSuccessfulResponse(w, res, raw, attemptSeq)
	return attemptOutcome{done: true}
}

func (ex *execution) settleSuccessfulResponse(w http.ResponseWriter, res *gateway.DispatchResult, raw []byte, attemptSeq int) bool {
	tokens := tokenImageUsage{}
	actualCost := ex.predictedCost
	costSnapshot := ex.costSnapshot
	pending := ex.pending
	if ex.scheme == imagepricing.SchemeTokenImage {
		usage, ok := parseTokenImageUsage(raw)
		if !ok {
			ex.observeChannelError(res.StatusCode)
			ex.abort(w, "usage_missing", 0)
			writeJSONError(w, http.StatusBadGateway, clienterr.CodeCanonicalResponseError, clienterr.MessageFor(clienterr.CodeCanonicalResponseError))
			return false
		}
		var err error
		tokens = usage
		actualCost, costSnapshot, pending, err = ex.tokenImageCost(usage)
		if err != nil {
			ex.abort(w, "pricing_unavailable", int64(usage.InputTokens))
			writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodePricingUnavailable, clienterr.MessageFor(clienterr.CodePricingUnavailable))
			return false
		}
	} else if ex.scheme == imagepricing.SchemePerImage {
		// per_image settle 时按解析账号平台 + 实际交付张数重算:
		// (1) 重定价——预扣用协议族 vendor 估的 predictedCost,选号后真账号平台
		//     (providerForPricing 此刻含 accInfo.Platform)可能价不同,沿用预估
		//     会误扣/少收(与 token/per-second 路径在 settle 重算对称);
		// (2) 交付数对账——billableImageCount 在上游交付少于请求数时返回交付数
		//     (Replicate num_outputs 被忽略只回 1 张),绝不按请求数多收。
		var err error
		actualCost, costSnapshot, pending, err = ex.perImageCost(ex.billableImageCount())
		if err != nil {
			ex.abort(w, "pricing_unavailable", 0)
			writeJSONError(w, http.StatusServiceUnavailable, clienterr.CodePricingUnavailable, clienterr.MessageFor(clienterr.CodePricingUnavailable))
			return false
		}
	}
	settleReq := ex.settleRequest(tokens, actualCost, costSnapshot, attemptSeq, pending)
	ex.observeSuccess(res)
	copyAllowedHeaders(w.Header(), res.Headers)
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	written, writeErr := w.Write(raw)
	fullyWritten := written >= len(raw)
	if !fullyWritten && writeErr == nil {
		writeErr = io.ErrShortWrite
	}
	if !fullyWritten {
		ex.abortAfterResponseWriteFailure("client_response_write_error", int64(tokens.InputTokens), writeErr)
		return false
	}
	if writeErr != nil {
		ex.observeResponseWriteUncertainty(writeErr)
	}
	// 小图片 JSON 可能仍在 net/http 写缓冲中；结算前刷新，使业务体尽早对客户端可见。
	// 完整 Write 后的刷新错误属于交付不确定，按已交付保守结算，不能释放预留。
	if flushErr := http.NewResponseController(w).Flush(); flushErr != nil {
		ex.observeResponseWriteUncertainty(flushErr)
	}
	ibctx, icancel := ex.billingCtx()
	defer icancel()
	if err := ex.settleDeliveredResponse(ibctx, settleReq); err != nil {
		return false
	}
	return true
}

// feedbackAttempt 组装喂给账号健康 FSM 的一次尝试上下文。
func (ex *execution) feedbackAttempt() upstreamfeedback.Attempt {
	attempt := upstreamfeedback.Attempt{
		TenantID:       ex.ident.TenantID,
		Account:        ex.accInfo,
		ProtocolFamily: ex.resolved.ProtocolFamily,
		ModelKey:       ex.upstreamModelID,
		RequestID:      ex.requestID,
		StartedAt:      ex.startedAt,
	}
	if binding, ok := ex.resolved.BindingForAttempt(ex.attempt.BindingID, ex.attempt.PoolGroupID); ok {
		attempt.StatusCodeMapping = binding.StatusCodeMapping
	}
	return attempt
}

// observeDispatchError/observeChannelError/observeHTTPError/observeSuccess 把上游结果喂账号健康
// FSM(upstreamfeedback→channelhealth.ApplySignal),坏号据此冷却→下次选号自动跳过(自动换号)。
// 决策/路由归 fallbackexec；Observer 追加账号客户端投影与健康副作用，nil 时仍用同一静态分类。
func (ex *execution) observeDispatchError(err error) {
	if ex.d.Feedback != nil {
		_ = ex.d.Feedback.ObserveDispatchError(ex.ctx, ex.feedbackAttempt(), err)
	}
}

func (ex *execution) observeChannelError(statusCode int) {
	if ex.d.Feedback != nil {
		ex.d.Feedback.ObserveChannelError(ex.ctx, ex.feedbackAttempt(), statusCode)
	}
}

func (ex *execution) observeHTTPError(res *gateway.DispatchResult, raw []byte) upstreamfeedback.HTTPFailure {
	attempt := ex.feedbackAttempt()
	if ex.d.Feedback != nil {
		return ex.d.Feedback.ObserveHTTPError(ex.ctx, attempt, res.StatusCode, res.Headers, raw)
	}
	return upstreamfeedback.ClassifyHTTPError(attempt, res.StatusCode, res.Headers, raw)
}

func (ex *execution) observeSuccess(res *gateway.DispatchResult) {
	if ex.d.Feedback != nil {
		ex.d.Feedback.ObserveSuccess(ex.ctx, ex.feedbackAttempt(), res.StatusCode, res.Headers)
	}
}

// excludeAccount 把本次失败账号加入本请求排除集,重试选号经 SelectionRequest.ExcludedAccounts
// 被 pool/router gates+pasr 跳过,避免重试打到同一坏号。
func (ex *execution) excludeAccount(accountID int64) {
	if accountID <= 0 {
		return
	}
	if ex.excludedAccounts == nil {
		ex.excludedAccounts = make(map[int64]struct{})
	}
	ex.excludedAccounts[accountID] = struct{}{}
}
