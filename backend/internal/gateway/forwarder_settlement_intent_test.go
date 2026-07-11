package gateway

import (
	"bytes"
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

// TestForwarderReportsFirstBusinessFrameAfterWriteOnce 守住首帧证据时序：只有完整业务帧
// 已写出后才报告交付，多帧仍只报告一次。
func TestForwarderReportsFirstBusinessFrameAfterWriteOnce(t *testing.T) {
	upstream := sseBytes(
		messageStart("msg-intent"),
		textDelta(0, "first"),
		textDelta(0, "second"),
		messageStop(),
	)
	rec := httptest.NewRecorder()
	f := newForwarder()
	var calls int
	var calledAt time.Time
	bodyLenAtCallback := 0
	f.AfterFirstBusinessFrame = func(at time.Time) {
		calls++
		calledAt = at
		bodyLenAtCallback = rec.Body.Len()
	}

	if _, err := f.Forward(context.Background(), bytes.NewReader(upstream), rec, anthropicForwardRequest(1, 100)); err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if calls != 1 {
		t.Fatalf("首业务帧回调 calls=%d want 1", calls)
	}
	if calledAt.IsZero() {
		t.Fatal("首业务帧回调时间不得为空")
	}
	if bodyLenAtCallback == 0 {
		t.Fatal("首帧报告发生时客户端尚无完整业务字节")
	}
	if rec.Body.Len() == 0 {
		t.Fatal("测试夹具未实际交付业务帧")
	}
}
