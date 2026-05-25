package gatewayhttp

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
)

func TestReplayCaptureWriterPreservesFlushAndCapturesWrittenBytes(t *testing.T) {
	underlying := newFlushCountingResponseWriter(3)
	capture := newStreamingIdempotencyReplayCaptureWriter(underlying, 64)

	n, err := capture.Write([]byte("abcdef"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 3 {
		t.Fatalf("Write n=%d want 3", n)
	}
	capture.Flush()
	if underlying.flushes != 1 {
		t.Fatalf("flushes=%d want 1", underlying.flushes)
	}
	if got := string(capture.body()); got != "abc" {
		t.Fatalf("captured body=%q want abc", got)
	}
	if got := underlying.body.String(); got != "abc" {
		t.Fatalf("underlying body=%q want abc", got)
	}
}

func TestReplayCaptureWriterStopsAtLimitWithoutShortWrite(t *testing.T) {
	underlying := newFlushCountingResponseWriter(0)
	capture := newStreamingIdempotencyReplayCaptureWriter(underlying, 4)

	if n, err := capture.Write([]byte("abc")); err != nil || n != 3 {
		t.Fatalf("first Write n=%d err=%v want n=3 nil", n, err)
	}
	if n, err := capture.Write([]byte("defgh")); err != nil || n != 5 {
		t.Fatalf("over-limit Write n=%d err=%v want n=5 nil", n, err)
	}
	if n, err := capture.Write([]byte("ijkl")); err != nil || n != 4 {
		t.Fatalf("post-limit Write n=%d err=%v want n=4 nil", n, err)
	}
	if !capture.overLimit() {
		t.Fatal("capture must be marked over-limit")
	}
	if got := capture.body(); got != nil {
		t.Fatalf("capture body=%q want nil after over-limit", string(got))
	}
	if got := underlying.body.String(); got != "abcdefghijkl" {
		t.Fatalf("underlying body=%q want full pass-through", got)
	}
}

func TestReplayBodyCaptureReturnsNonNilEmptyBodyUnlessOverLimit(t *testing.T) {
	empty := newIdempotencyReplayBodyCapture(8)
	empty.append([]byte{})

	body := empty.body()
	if body == nil {
		t.Fatal("empty capture body is nil; want non-nil empty slice")
	}
	if len(body) != 0 {
		t.Fatalf("empty capture body len=%d want 0", len(body))
	}

	overLimit := newIdempotencyReplayBodyCapture(1)
	overLimit.append([]byte("ab"))
	if body := overLimit.body(); body != nil {
		t.Fatalf("over-limit capture body=%q want nil", string(body))
	}
}

func TestServeIdempotentReplayAddsNoCacheForEventStreamWithParameters(t *testing.T) {
	store := billing.NewMemoryReplayStore()
	if err := store.Record(context.Background(), 7, 99, http.StatusOK, "text/event-stream; charset=utf-8", []byte("data: ok\n\n"), 0); err != nil {
		t.Fatalf("Record: %v", err)
	}
	ex := &chatExecution{
		ctx:   context.Background(),
		ident: auth.Identity{TenantID: 7},
		d:     ChatHandlerDeps{ReplayStore: store},
	}

	rec := httptest.NewRecorder()
	if ok := ex.serveIdempotentReplay(rec, 99); !ok {
		t.Fatal("serveIdempotentReplay returned false")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control=%q want no-cache", got)
	}
	if got := rec.Header().Get("Connection"); got != "" {
		t.Fatalf("Connection=%q want empty", got)
	}
}

type flushCountingResponseWriter struct {
	header      http.Header
	body        bytes.Buffer
	flushes     int
	maxPerWrite int
	status      int
}

func newFlushCountingResponseWriter(maxPerWrite int) *flushCountingResponseWriter {
	return &flushCountingResponseWriter{
		header:      make(http.Header),
		maxPerWrite: maxPerWrite,
	}
}

func (w *flushCountingResponseWriter) Header() http.Header {
	return w.header
}

func (w *flushCountingResponseWriter) Write(p []byte) (int, error) {
	if w.maxPerWrite > 0 && len(p) > w.maxPerWrite {
		p = p[:w.maxPerWrite]
	}
	return w.body.Write(p)
}

func (w *flushCountingResponseWriter) WriteHeader(statusCode int) {
	w.status = statusCode
}

func (w *flushCountingResponseWriter) Flush() {
	w.flushes++
}
