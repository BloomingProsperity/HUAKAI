package gatewayhttp

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementintent"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementrecovery"
)

// maxIdempotencyReplayBodyBytes 是持久重放记录存储的响应体上限; 超限的响应不
// 存重放 (后续同 key 重试回退 409 而非重放), 防大响应撑爆重放表。
const maxIdempotencyReplayBodyBytes = 1 << 20 // 1 MiB

const (
	idempotencyReplayContentTypeJSON  = "application/json"
	idempotencyReplayContentTypeSSE   = "text/event-stream"
	idempotencyReplayRecordTimeout    = 5 * time.Second
	idempotencyReplayRecordFailedCode = "idempotency_replay_record_failed"
)

// recordIdempotencyReplay best-effort 存一条幂等重放记录: 仅当请求带
// Idempotency-Key 且 ReplayStore 已配置。 写失败不影响已成功的响应 —— 仅意味
// 后续同 key 重试回退 409 而非重放。
func (ex *chatExecution) recordIdempotencyReplay(claimID int64, status int, body []byte) {
	ex.recordIdempotencyReplayWithContentType(claimID, status, idempotencyReplayContentTypeJSON, body)
}

// recordStreamingIdempotencyReplay 存流式 SSE 的原始客户端字节。
func (ex *chatExecution) recordStreamingIdempotencyReplay(claimID int64, status int, body []byte) {
	ex.recordIdempotencyReplayWithContentType(claimID, status, idempotencyReplayContentTypeSSE, body)
}

// recordIdempotencyReplayWithContentType 是 JSON / SSE 共用的 best-effort
// 持久重放写入入口。
func (ex *chatExecution) recordIdempotencyReplayWithContentType(claimID int64, status int, contentType string, body []byte) {
	if ex.idempotencyHeader == "" || ex.d.ReplayStore == nil || claimID == 0 {
		return
	}
	if len(body) > maxIdempotencyReplayBodyBytes {
		return
	}
	contentType = normalizeIdempotencyReplayContentType(contentType)
	// replay 写入发生在响应已形成之后, 不能被客户端读完断连取消; 仅保留短
	// 超时避免异常存储写入拖住 handler。
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ex.ctx), idempotencyReplayRecordTimeout)
	defer cancel()
	if err := ex.d.ReplayStore.Record(recordCtx, ex.ident.TenantID, claimID, status, contentType, body, 0); err != nil {
		logInternalError(recordCtx, ex.requestID, idempotencyReplayRecordFailedCode, err)
	}
}

// recordCacheHitReplay 是 serveL2CacheHit 用的 best-effort 重放记录写入 ——
// 缓存命中也是成功响应, 其 claim 同样需要可重放。
func recordCacheHitReplay(ctx context.Context, d ChatHandlerDeps, in l2CacheHitInput) {
	if in.IdempotencyHeader == "" || d.ReplayStore == nil || in.ReserveResult == nil {
		return
	}
	if len(in.Entry.Body) > maxIdempotencyReplayBodyBytes {
		return
	}
	if err := d.ReplayStore.Record(ctx, in.Ident.TenantID, in.ReserveResult.ClaimID,
		http.StatusOK, idempotencyReplayContentTypeJSON, in.Entry.Body, 0); err != nil {
		logInternalError(ctx, in.RequestID, idempotencyReplayRecordFailedCode, err)
	}
}

// serveIdempotentReplay 处理同 Idempotency-Key 的重试 (ClaimGate 返
// IdempotencyHit): 按原 claim_id 从持久重放表取回原始响应重放 —— 路由无关、
// 不受 L2 response cache 淘汰影响。 取不到返 false 让调用方回 409。
func (ex *chatExecution) serveIdempotentReplay(w http.ResponseWriter, claimID int64) bool {
	if ex.d.ReplayStore == nil || claimID == 0 {
		return false
	}
	rec, ok, err := ex.d.ReplayStore.Lookup(ex.ctx, ex.ident.TenantID, claimID)
	if err != nil {
		// 存储故障 (PG 不可用等) 不能伪装成 409 客户端
		// 冲突 — 返 503 让客户端正确退避重试。
		writeLoggedJSONError(ex.ctx, ex.requestID, w, http.StatusServiceUnavailable, clienterr.CodeReplayLookupFailed, err)
		return true
	}
	if !ok || rec == nil {
		return false
	}
	contentType := normalizeIdempotencyReplayContentType(rec.ContentType)
	status := rec.ResponseStatus
	if status == 0 {
		status = http.StatusOK
	}
	isSSE := isIdempotencyReplayEventStream(contentType)
	w.Header().Set("Content-Type", contentType)
	if isSSE {
		w.Header().Set("Cache-Control", "no-cache")
	}
	// X-HUAKAI-Idempotency-Hit 是 openapi.yaml 公布的契约头。
	w.Header().Set("X-HUAKAI-Idempotency-Hit", "true")
	w.WriteHeader(status)
	_, _ = w.Write(rec.ResponseBody)
	if isSSE {
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}
	return true
}

func isIdempotencyReplayEventStream(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err == nil {
		return strings.EqualFold(mediaType, idempotencyReplayContentTypeSSE)
	}
	base, _, _ := strings.Cut(contentType, ";")
	return strings.EqualFold(strings.TrimSpace(base), idempotencyReplayContentTypeSSE)
}

// normalizeIdempotencyReplayContentType 把持久化值收敛到 HUAKAI 实际支持的两种
// 重放合同。即便数据库记录被误写或篡改为 text/html，也只能按 JSON 数据交付，
// 不能借响应头把重放体提升为浏览器可执行文档。
func normalizeIdempotencyReplayContentType(contentType string) string {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil {
		return idempotencyReplayContentTypeJSON
	}
	switch {
	case strings.EqualFold(mediaType, idempotencyReplayContentTypeSSE):
		return idempotencyReplayContentTypeSSE
	case strings.EqualFold(mediaType, idempotencyReplayContentTypeJSON):
		return idempotencyReplayContentTypeJSON
	default:
		return idempotencyReplayContentTypeJSON
	}
}

func settleCompletionWithRecovery(
	ctx context.Context,
	d ChatHandlerDeps,
	event eventbus.RequestCompletionEvent,
	source settlementrecovery.Source,
) (*billing.SettleResult, bool, settlementrecovery.FailureEvidence, error) {
	var res *billing.SettleResult
	var err error
	if source != "" {
		if validationErr := validateMoneyPathAuditRefForSource(ctx, d, event, string(source)); validationErr != nil {
			logMoneyPathAuditRefError(ctx, event, validationErr, string(source), false)
			err = fmt.Errorf("post-delivery settlement deferred: %w", validationErr)
		} else {
			res, err = settleCompletion(ctx, d, event)
		}
	} else {
		res, err = settleCompletion(ctx, d, event)
	}
	if err == nil {
		return res, false, settlementrecovery.FailureEvidence{}, nil
	}
	if source == "" {
		return res, false, settlementrecovery.FailureEvidence{}, err
	}
	payload := settlementrecovery.FromCompletionEvent(source, event)
	evidence, enqueueErr := settlementrecovery.EnqueueFailure(
		ctx,
		d.SettleRecoveryDLQ,
		payload,
		err,
		"gatewayhttp.settle_recovery",
	)
	return res, enqueueErr == nil, evidence, err
}

func toSettlementIntentEvidence(evidence settlementrecovery.FailureEvidence) settlementintent.RecoveryEvidence {
	return settlementintent.RecoveryEvidence{
		Payload:      evidence.Payload,
		FailureClass: evidence.FailureClass,
	}
}
