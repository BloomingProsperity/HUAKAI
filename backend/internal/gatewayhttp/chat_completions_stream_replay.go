package gatewayhttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/chatpipe"
	"github.com/BloomingProsperity/HUAKAI/internal/httpkeepalive"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/protosse"
)

// streamingIdempotencyReplayCaptureWriter 捕获已成功写给客户端的 SSE 字节,
// 同时保留 ResponseWriter/Flusher 行为给 forwarder 热路径使用。
type streamingIdempotencyReplayCaptureWriter struct {
	http.ResponseWriter
	capture *idempotencyReplayBodyCapture
	status  int
}

func newStreamingIdempotencyReplayCaptureWriter(w http.ResponseWriter, limit int) *streamingIdempotencyReplayCaptureWriter {
	return &streamingIdempotencyReplayCaptureWriter{
		ResponseWriter: w,
		capture:        newIdempotencyReplayBodyCapture(limit),
	}
}

func (w *streamingIdempotencyReplayCaptureWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if n > 0 {
		if w.status == 0 {
			w.status = http.StatusOK
		}
		captured := n
		if captured > len(p) {
			captured = len(p)
		}
		w.capture.append(p[:captured])
	}
	return n, err
}

func (w *streamingIdempotencyReplayCaptureWriter) WriteHeader(statusCode int) {
	if w.status == 0 {
		w.status = statusCode
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *streamingIdempotencyReplayCaptureWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *streamingIdempotencyReplayCaptureWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *streamingIdempotencyReplayCaptureWriter) statusCode() int {
	if w == nil || w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *streamingIdempotencyReplayCaptureWriter) overLimit() bool {
	return w != nil && w.capture != nil && w.capture.overLimit
}

func (w *streamingIdempotencyReplayCaptureWriter) body() []byte {
	if w == nil || w.capture == nil {
		return nil
	}
	return w.capture.body()
}

type idempotencyReplayBodyCapture struct {
	limit     int
	buf       []byte
	overLimit bool
}

func newIdempotencyReplayBodyCapture(limit int) *idempotencyReplayBodyCapture {
	if limit < 0 {
		limit = 0
	}
	return &idempotencyReplayBodyCapture{limit: limit}
}

func (c *idempotencyReplayBodyCapture) append(p []byte) {
	if c == nil || c.overLimit || len(p) == 0 {
		return
	}
	if len(c.buf)+len(p) > c.limit {
		c.overLimit = true
		c.buf = nil
		return
	}
	c.buf = append(c.buf, p...)
}

func (c *idempotencyReplayBodyCapture) body() []byte {
	if c == nil || c.overLimit {
		return nil
	}
	// pgx 会把 typed nil []byte 编码成 SQL NULL，而 response_body 是 NOT NULL 列。
	// 空流必须以非 nil 空切片落库，否则 INSERT 失败、claim 已 commit 却无记录，重试会变成 409。
	return append([]byte{}, c.buf...)
}

func (ex *chatExecution) shouldCaptureStreamingIdempotencyReplay() bool {
	return ex.idempotencyHeader != "" &&
		ex.d.ReplayStore != nil &&
		ex.reserveRes != nil &&
		ex.reserveRes.ClaimID != 0
}

func hcsfDispatcher(d ChatHandlerDeps) HCSFDispatcher {
	if d.CanonicalDispatcher != nil {
		return d.CanonicalDispatcher
	}
	if d.Dispatcher == nil {
		return nil
	}
	return d.Dispatcher
}

func protocolAdapterForBuffered(f *gateway.StreamForwarder, protocolFamily string) (proto.UpstreamAdapter, error) {
	var adapters gateway.ProtocolAdapterRegistry
	if f != nil {
		adapters = f.ProtocolAdapters
	}
	if adapters == nil {
		adapters = gateway.BuildDefaultProtocolAdapterRegistry()
	}
	return adapters.For(protocolFamily)
}

const maxRawBufferedUpstreamBodyBytes = 1 << 20

var errRawBufferedUpstreamBodyTooLarge = errors.New("gatewayhttp: upstream buffered response exceeds 1MiB limit")

func readRawBufferedUpstreamBody(r io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxRawBufferedUpstreamBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxRawBufferedUpstreamBodyBytes {
		// 超限时保留截断 body，供调用方对非 2xx 上游响应继续做错误分类。
		return raw[:maxRawBufferedUpstreamBodyBytes], errRawBufferedUpstreamBodyTooLarge
	}
	return raw, nil
}

func (ex *chatExecution) dispatchRawBuffered(w http.ResponseWriter, seed proto.RequestMetaSeed, seedCtx context.Context, startedAt time.Time) (*proto.HCSF, *classifiedAttemptFailure, bool) {
	dispatchAccount, transportMode := gateway.ResolveDispatchTransport(ex.accInfo, ex.resolved.ProtocolFamily)
	dispatchRes, err := ex.d.Dispatcher.Dispatch(ex.ctx, gateway.DispatchInput{
		ProtocolFamily:  ex.resolved.ProtocolFamily,
		UpstreamModelID: ex.upstreamModelID,
		// R7 身份改写(默认关 + fail-open,只动 dispatch 专用拷贝、不动 ex.body)。
		InboundBody:          chatpipe.OutboundDispatchBody(ex.officialDirect, ex.resolved.ProtocolFamily, ex.upstreamInboundBody(ex.body), ex.identityRewrite),
		BodyControls:         ex.activeDispatchBodyControls(),
		InboundBetaTokens:    ex.clientBetaTokens(),
		Account:              dispatchAccount,
		Credential:           ex.cred,
		TransportMode:        transportMode,
		NonStreamingBuffered: true,
	})
	if err != nil {
		classification, _ := gateway.Classify(0, nil, []byte(err.Error()), ex.errorClassProvider())
		decision := gateway.ClassifyAttemptDispatchError(err)
		if !decision.RetryableBeforeDelivery && decision.TransportClass == gateway.TransportErrorLocalDispatch {
			decision.ClientStatus = http.StatusBadGateway
		}
		if decision.AbortReason == "" {
			decision.AbortReason = "upstream_dispatch_error"
		}
		abortErr := ex.abortReservation(ex.reserveRes.ClaimID, decision.AbortReason, 0, nil)
		if ex.healthKeyOK {
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, signalFromDispatchError(err, classification), 0, time.Since(startedAt), ex.requestID, nil, gateway.AuthFailureClassFromClassification(classification))
		}
		failure := classifiedFailureFromDecision("", clienterr.MessageFor(clienterr.CodeUpstreamDispatchError), classification, decision, err)
		return nil, degradeFailureIfAbortFailed(ex.ctx, ex.requestID, failure, abortErr), false
	}
	if dispatchRes == nil || dispatchRes.UpstreamReader == nil {
		abortErr := ex.abortReservation(ex.reserveRes.ClaimID, "upstream_empty_response", 0, nil)
		if ex.healthKeyOK {
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, channelhealth.SignalChannelError, 0, time.Since(startedAt), ex.requestID, nil, 0)
		}
		failure := retryableLocalAttemptFailure(http.StatusBadGateway, clienterr.CodeUpstreamEmptyResponse, clienterr.MessageFor(clienterr.CodeUpstreamEmptyResponse), "upstream_empty_response", gateway.UpstreamError5xx, nil)
		return nil, degradeFailureIfAbortFailed(ex.ctx, ex.requestID, failure, abortErr), false
	}
	defer closeDispatchResult(dispatchRes)
	if dispatchRes.StatusCode >= 200 && dispatchRes.StatusCode < 300 && ex.shouldAggregateForcedStreamingBuffered() {
		return ex.dispatchForcedStreamingBuffered(w, dispatchRes, seed, seedCtx, startedAt)
	}
	rawKeepalive := httpkeepalive.Start(w, ex.d.NonStreamKeepAliveInterval) // buffered 读慢接缝保活(见 Deps 字段注释)
	raw, readErr := readRawBufferedUpstreamBody(dispatchRes.UpstreamReader)
	rawKeepalive.Stop()
	oversizedNon2xx := errors.Is(readErr, errRawBufferedUpstreamBodyTooLarge) && (dispatchRes.StatusCode < 200 || dispatchRes.StatusCode >= 300)
	if readErr != nil && !oversizedNon2xx {
		code := clienterr.CodeUpstreamReadError
		if errors.Is(readErr, errRawBufferedUpstreamBodyTooLarge) {
			code = clienterr.CodeUpstreamResponseTooLarge
		}
		if abortErr := ex.abortReservation(ex.reserveRes.ClaimID, code, 0, nil); abortErr != nil {
			setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
		}
		if ex.healthKeyOK {
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, channelhealth.SignalChannelError, dispatchRes.StatusCode, time.Since(startedAt), ex.requestID, nil, 0)
		}
		writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusBadGateway, code, readErr)
		return nil, nil, false
	}
	if dispatchRes.StatusCode < 200 || dispatchRes.StatusCode >= 300 {
		decision, classification, classifyErr := gateway.ClassifyAttemptHTTPError(dispatchRes.StatusCode, dispatchRes.Headers, raw, ex.errorClassProvider())
		if classifyErr != nil {
			classification, _ = gateway.Classify(dispatchRes.StatusCode, dispatchRes.Headers, raw, ex.errorClassProvider())
			decision = gateway.AttemptRetryDecision{ClientStatus: clientStatusForUpstreamError(dispatchRes.StatusCode, classification.Class), AbortReason: "upstream_error"}
		}
		decision.ClientStatus = ex.remapClientStatusForUpstream(dispatchRes.StatusCode, decision.ClientStatus)
		agentTaskInvalid := ex.classifyAgentTaskInvalid(dispatchRes.StatusCode, raw, &decision)
		var policyOutcome upstreamErrorPolicyOutcome
		suppressHealth := false
		if !agentTaskInvalid {
			policyOutcome = ex.applyUpstreamErrorCooldown(&gateway.UpstreamHTTPError{
				StatusCode: dispatchRes.StatusCode,
				Body:       raw,
				Header:     dispatchRes.Headers,
			}, classification, true)
			suppressHealth = ex.applyAccountErrorPolicy(&decision, classification, policyOutcome)
		}
		abortErr := ex.abortReservation(ex.reserveRes.ClaimID, decision.AbortReason, 0, nil)
		if ex.healthKeyOK && !agentTaskInvalid && !policyOutcome.ModelScoped && !suppressHealth {
			recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, gateway.SignalFromClassification(dispatchRes.StatusCode, classification), dispatchRes.StatusCode, time.Since(startedAt), ex.requestID, rateLimitResetFromClassification(classification, time.Now()), gateway.AuthFailureClassFromClassification(classification))
		}
		failure := classifiedFailureFromDecision("", clienterr.MessageFor(clienterr.CodeUpstreamDispatchError), classification, decision, nil)
		failure.AgentTaskInvalid = agentTaskInvalid
		failure.FallbackSignal = bindingFallbackSignalFromUpstream(dispatchRes.StatusCode, raw, classification, decision)
		return nil, degradeFailureIfAbortFailed(ex.ctx, ex.requestID, failure, abortErr), false
	}
	ex.updateSessionWindowFromHeaders(dispatchRes.Headers)
	upstreamAdapter, err := protocolAdapterForBuffered(ex.d.Forwarder, ex.resolved.ProtocolFamily)
	if err != nil {
		if abortErr := ex.abortReservation(ex.reserveRes.ClaimID, "upstream_adapter_error", 0, nil); abortErr != nil {
			setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
		}
		writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusBadGateway, clienterr.CodeUpstreamAdapterError, err)
		return nil, nil, false
	}
	bufferedEnv, _, err := upstreamAdapter.ProviderResponseToCanonical(seedCtx, raw)
	if err != nil {
		if reconstructedEnv, _, ok := protosse.ReconstructBufferedFromSSE(upstreamAdapter, raw); ok && reconstructedEnv != nil {
			bufferedEnv = reconstructedEnv
		} else {
			if abortErr := ex.abortReservation(ex.reserveRes.ClaimID, "canonical_response_error", 0, nil); abortErr != nil {
				setAbortFailedHeader(w, ex.ctx, ex.requestID, abortErr)
			}
			if ex.healthKeyOK {
				recordChannelHealthSignal(ex.ctx, ex.d, ex.healthKey, channelhealth.SignalChannelError, dispatchRes.StatusCode, time.Since(startedAt), ex.requestID, nil, 0)
			}
			writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusBadGateway, clienterr.CodeCanonicalResponseError, err)
			return nil, nil, false
		}
	}
	if bufferedEnv != nil {
		_ = seed.ApplyToRequestMeta(&bufferedEnv.RequestMeta)
		enrichCanonicalRequestMeta(bufferedEnv, ex.upstreamModelID, ex.accInfo.Platform, ex.idempotencyHeader, ex.sessionHash)
	}
	return ex.finalizeBufferedEnvelope(w, bufferedEnv, dispatchRes.StatusCode, startedAt)
}

func (ex *chatExecution) updateSessionWindowFromHeaders(headers http.Header) {
	if ex == nil || ex.d.RateService == nil || ex.acquiredAccountID <= 0 {
		return
	}
	// 这些头的语义只属于 Anthropic 车道，避免兼容网关伪造同名头污染其它账号。
	if !strings.EqualFold(strings.TrimSpace(ex.accInfo.Platform), "anthropic") {
		return
	}
	if err := ex.d.RateService.UpdateSessionWindow(ex.ctx, ex.acquiredAccountID, headers); err != nil {
		logInternalError(ex.ctx, ex.requestID, "session_window_update_failed", err)
	}
}
