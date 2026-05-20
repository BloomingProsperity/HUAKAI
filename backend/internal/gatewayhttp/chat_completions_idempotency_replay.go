package gatewayhttp

import (
	"context"
	"net/http"
)

// maxIdempotencyReplayBodyBytes 是持久重放记录存储的响应体上限; 超限的响应不
// 存重放 (后续同 key 重试回退 409 而非重放), 防大响应撑爆重放表。
const maxIdempotencyReplayBodyBytes = 1 << 20 // 1 MiB

// recordIdempotencyReplay best-effort 存一条幂等重放记录: 仅当请求带
// Idempotency-Key 且 ReplayStore 已配置。 写失败不影响已成功的响应 —— 仅意味
// 后续同 key 重试回退 409 而非重放。
func (ex *chatExecution) recordIdempotencyReplay(claimID int64, status int, body []byte) {
	if ex.idempotencyHeader == "" || ex.d.ReplayStore == nil || claimID == 0 {
		return
	}
	if len(body) > maxIdempotencyReplayBodyBytes {
		return
	}
	_ = ex.d.ReplayStore.Record(ex.ctx, ex.ident.TenantID, claimID, status, "application/json", body, 0)
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
	_ = d.ReplayStore.Record(ctx, in.Ident.TenantID, in.ReserveResult.ClaimID,
		http.StatusOK, "application/json", in.Entry.Body, 0)
}

// serveIdempotentReplay 处理同 Idempotency-Key 的重试 (ClaimGate 返
// IdempotencyHit): 按原 claim_id 从持久重放表取回原始响应重放 —— 路由无关、
// 不受 L2 response cache 淘汰影响。 取不到返 false 让 caller 回 409。
func (ex *chatExecution) serveIdempotentReplay(w http.ResponseWriter, claimID int64) bool {
	if ex.d.ReplayStore == nil || claimID == 0 {
		return false
	}
	rec, ok, err := ex.d.ReplayStore.Lookup(ex.ctx, ex.ident.TenantID, claimID)
	if err != nil || !ok || rec == nil {
		return false
	}
	contentType := rec.ContentType
	if contentType == "" {
		contentType = "application/json"
	}
	status := rec.ResponseStatus
	if status == 0 {
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-HUAKAI-Idempotent-Replay", "hit")
	w.WriteHeader(status)
	_, _ = w.Write(rec.ResponseBody)
	return true
}
