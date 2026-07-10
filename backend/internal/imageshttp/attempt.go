package imageshttp

import (
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/httpkeepalive"
	"github.com/BloomingProsperity/HUAKAI/internal/imagepricing"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/relaybody"
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

func (ex *execution) dispatchAndSettle(w http.ResponseWriter, attemptSeq int) bool {
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
	dispatchKeepalive := httpkeepalive.Start(w, ex.d.NonStreamKeepAliveInterval)
	res, err := ex.d.Dispatcher.Dispatch(ex.ctx, dispatchInput)
	dispatchKeepalive.Stop()
	if err != nil {
		ex.abort(w, "upstream_dispatch_error", 0)
		writeJSONError(w, http.StatusBadGateway, clienterr.CodeUpstreamDispatchError, clienterr.MessageFor(clienterr.CodeUpstreamDispatchError))
		return false
	}
	if res == nil || res.UpstreamReader == nil {
		ex.abort(w, "upstream_empty_response", 0)
		writeJSONError(w, http.StatusBadGateway, clienterr.CodeUpstreamEmptyResponse, clienterr.MessageFor(clienterr.CodeUpstreamEmptyResponse))
		return false
	}
	defer func() {
		if res.Close != nil {
			_ = res.Close()
		}
	}()
	return ex.finishUpstreamResponse(w, res, attemptSeq)
}

func (ex *execution) finishUpstreamResponse(w http.ResponseWriter, res *gateway.DispatchResult, attemptSeq int) bool {
	// 若上游改在 body 读阶段才慢(early header + 延迟 body),读全 body 也起 keepalive 兜住;
	// Stop 在下方任何写 w 之前。与 Dispatch 处的保活互补,覆盖两种慢点形态。
	readKeepalive := httpkeepalive.Start(w, ex.d.NonStreamKeepAliveInterval)
	raw, readErr := readUpstreamBody(res.UpstreamReader)
	readKeepalive.Stop()
	if readErr != nil {
		ex.abort(w, clienterr.CodeUpstreamReadError, 0)
		writeJSONError(w, http.StatusBadGateway, clienterr.CodeUpstreamReadError, clienterr.MessageFor(clienterr.CodeUpstreamReadError))
		return false
	}
	if strings.TrimSpace(string(raw)) == "" {
		ex.abort(w, "upstream_empty_response", 0)
		writeJSONError(w, http.StatusBadGateway, clienterr.CodeUpstreamEmptyResponse, clienterr.MessageFor(clienterr.CodeUpstreamEmptyResponse))
		return false
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		decision, _, err := gateway.ClassifyAttemptHTTPError(res.StatusCode, res.Headers, raw, ex.accInfo.Platform)
		reason := decision.AbortReason
		if err != nil || reason == "" {
			reason = "upstream_error"
		}
		ex.abort(w, reason, 0)
		writeJSONError(w, http.StatusBadGateway, clienterr.CodeUpstreamDispatchError, clienterr.MessageFor(clienterr.CodeUpstreamDispatchError))
		return false
	}
	// family 专属响应翻译(replicate_image:prediction → OpenAI 形)必须在
	// settle/写客户端之前;翻译失败按上游错误处理,绝不 settle 计费。
	raw, ok := ex.translateUpstreamResponseForFamily(w, raw)
	if !ok {
		return false
	}
	return ex.settleSuccessfulResponse(w, res, raw, attemptSeq)
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
	ibctx, icancel := ex.billingCtx()
	defer icancel()
	if _, err := ex.d.Settler.Settle(ibctx, ex.settleRequest(tokens, actualCost, costSnapshot, attemptSeq, pending)); err != nil {
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
