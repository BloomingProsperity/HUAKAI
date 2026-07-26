package gateway

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

// TestForwarderPersistsDeliveryEvidenceBeforeFirstBusinessFrame 守住首帧证据时序：
// 业务字节写出前先持久化一次，多帧仍只调用一次。
func TestForwarderPersistsDeliveryEvidenceBeforeFirstBusinessFrame(t *testing.T) {
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
	f.BeforeFirstBusinessFrame = func(at time.Time) error {
		calls++
		calledAt = at
		bodyLenAtCallback = rec.Body.Len()
		return nil
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
	if bodyLenAtCallback != 0 {
		t.Fatalf("交付证据写入时客户端已有业务字节: %d", bodyLenAtCallback)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("测试夹具未实际交付业务帧")
	}
}

func TestForwarderDeliveryEvidenceFailureWritesNoBusinessFrame(t *testing.T) {
	upstream := sseBytes(
		messageStart("msg-intent-failed"),
		textDelta(0, "blocked"),
		messageStop(),
	)
	rec := httptest.NewRecorder()
	f := newForwarder()
	wantErr := context.DeadlineExceeded
	var calls int
	f.BeforeFirstBusinessFrame = func(time.Time) error {
		calls++
		return wantErr
	}

	draft, err := f.Forward(context.Background(), bytes.NewReader(upstream), rec, anthropicForwardRequest(1, 100))
	if !errors.Is(err, wantErr) {
		t.Fatalf("err=%v want %v", err, wantErr)
	}
	if calls != 1 {
		t.Fatalf("交付硬门 calls=%d want 1", calls)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("交付硬门失败后写出了业务体: %q", rec.Body.String())
	}
	if draft.BusinessFrameDelivered {
		t.Fatal("交付硬门失败不得标记业务帧已交付")
	}
}
