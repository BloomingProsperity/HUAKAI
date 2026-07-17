package imageshttp

import (
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/httpkeepalive"
	"github.com/BloomingProsperity/HUAKAI/internal/imagepricing"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/relaybody"
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
		EndpointFamily:   endpointFamilyImages,
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
		Decision:            decision,
		AbortErr:            ex.abortWithError(w, decision.AbortReason, 0),
		SideEffectRetrySafe: true,
		ClientCode:          clienterr.CodeCredentialResolveError,
	}
}

type attemptFailure struct {
	Decision            gateway.AttemptRetryDecision
	Classification      gateway.Classification
	AbortErr            error
	KeepaliveWritten    bool
	SideEffectRetrySafe bool
	ClientCode          string
}

type attemptOutcome struct {
	Done    bool
	Failure *attemptFailure
}

func (ex *execution) dispatchAndSettle(w http.ResponseWriter, attemptSeq int) attemptOutcome {
	// 把出站 body 的 model 改写成解析后的上游 id。JSON 只改 body 不动 CT(保持原行为=adapter默认);
	// multipart(edits/variations)才设 InboundContentType(顺带补上 image 原先缺失的 CT)。
	inboundBody, inboundCT, isMultipart := relaybody.RewriteModel(ex.body, ex.r.Header.Get("Content-Type"), ex.upstreamModelID)
	dispatchInput := gateway.DispatchInput{
		ProtocolFamily:  ex.resolved.ProtocolFamily,
		EndpointPath:    ex.endpoint.Path(),
		UpstreamModelID: ex.upstreamModelID,
		InboundBody:     inboundBody,
		Account:         ex.accInfo,
		Credential:      ex.cred,
	}
	if isMultipart {
		dispatchInput.InboundContentType = inboundCT
	}
	// 图片同步 API 常在 Dispatch(等上游生成完再回 header)一步就阻塞数十秒,期间对客户端零字节;
	// 起 keepalive 保活避开反代空闲超时,Stop 在下方任何写 w 之前(含错误路径)。
	startedAt := time.Now()
	dispatchKeepalive := httpkeepalive.Start(w, ex.d.NonStreamKeepAliveInterval)
	res, err := ex.d.Dispatcher.Dispatch(ex.ctx, dispatchInput)
	dispatchKeepalive.Stop()
	if err != nil {
		decision := dispatchFailureDecision(err)
		if ex.d.Feedback != nil {
			decision = normalizeDispatchFailureDecision(
				ex.d.Feedback.ObserveDispatchError(ex.ctx, ex.feedbackAttempt(startedAt), err),
			)
		}
		return attemptOutcome{Failure: &attemptFailure{
			Decision:         decision,
			AbortErr:         ex.abortWithError(w, decision.AbortReason, 0),
			KeepaliveWritten: dispatchKeepalive.Wrote(),
		}}
	}
	if res == nil || res.UpstreamReader == nil {
		ex.observeChannelError(startedAt, 0)
		decision := retryableAttemptDecision("upstream_empty_response", http.StatusBadGateway)
		return attemptOutcome{Failure: &attemptFailure{
			Decision:         decision,
			AbortErr:         ex.abortWithError(w, decision.AbortReason, 0),
			KeepaliveWritten: dispatchKeepalive.Wrote(),
		}}
	}
	defer closeDispatchResult(res)
	return ex.finishUpstreamResponse(w, res, attemptSeq, startedAt, dispatchKeepalive.Wrote())
}

func (ex *execution) finishUpstreamResponse(
	w http.ResponseWriter,
	res *gateway.DispatchResult,
	attemptSeq int,
	startedAt time.Time,
	dispatchKeepaliveWritten bool,
) attemptOutcome {
	// 若上游改在 body 读阶段才慢(early header + 延迟 body),读全 body 也起 keepalive 兜住;
	// Stop 在下方任何写 w 之前。与 Dispatch 处的保活互补,覆盖两种慢点形态。
	readKeepalive := httpkeepalive.Start(w, ex.d.NonStreamKeepAliveInterval)
	raw, readErr := readUpstreamBody(res.UpstreamReader)
	readKeepalive.Stop()
	keepaliveWritten := dispatchKeepaliveWritten || readKeepalive.Wrote()
	if readErr != nil {
		ex.observeChannelError(startedAt, res.StatusCode)
		ex.abort(w, clienterr.CodeUpstreamReadError, 0)
		writeJSONError(w, http.StatusBadGateway, clienterr.CodeUpstreamReadError, clienterr.MessageFor(clienterr.CodeUpstreamReadError))
		return attemptOutcome{Done: true}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		failure := ex.observeHTTPFailure(startedAt, res, raw)
		failure.AbortErr, failure.SideEffectRetrySafe = ex.abortHTTPFailure(w, failure.Decision.AbortReason, raw)
		failure.KeepaliveWritten = keepaliveWritten
		return attemptOutcome{Failure: failure}
	}
	if strings.TrimSpace(string(raw)) == "" {
		ex.observeChannelError(startedAt, res.StatusCode)
		ex.abort(w, "upstream_empty_response", 0)
		writeJSONError(w, http.StatusBadGateway, clienterr.CodeUpstreamEmptyResponse, clienterr.MessageFor(clienterr.CodeUpstreamEmptyResponse))
		return attemptOutcome{Done: true}
	}
	ex.observeSuccess(startedAt, res)
	// family 专属响应翻译(replicate_image:prediction → OpenAI 形)必须在
	// settle/写客户端之前;翻译失败按上游错误处理,绝不 settle 计费。
	raw, ok := ex.translateUpstreamResponseForFamily(w, raw)
	if !ok {
		return attemptOutcome{Done: true}
	}
	_ = ex.settleSuccessfulResponse(w, res, raw, attemptSeq)
	return attemptOutcome{Done: true}
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
	ex.deliveredImageCount = 0
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
	if failure == nil || failure.AbortErr != nil || failure.KeepaliveWritten {
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

func (ex *execution) retrySafeForFamily(failure *attemptFailure) bool {
	if ex.resolved.ProtocolFamily != replicateImageFamily {
		return true
	}
	if failure == nil {
		return false
	}
	if !failure.SideEffectRetrySafe {
		return false
	}
	if failure.Decision.CountsAgainstAuthFailoverBudget {
		return true
	}
	return failure.Classification.Class == gateway.ErrorClassRateLimited
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

func (ex *execution) settleSuccessfulResponse(w http.ResponseWriter, res *gateway.DispatchResult, raw []byte, attemptSeq int) bool {
	tokens := tokenImageUsage{}
	actualCost := ex.predictedCost
	costSnapshot := ex.costSnapshot
	pending := ex.pending
	if ex.scheme == imagepricing.SchemeTokenImage {
		usage, ok := parseTokenImageUsage(raw)
		if !ok {
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
