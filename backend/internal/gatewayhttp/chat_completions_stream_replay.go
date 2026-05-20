package gatewayhttp

import "net/http"

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
