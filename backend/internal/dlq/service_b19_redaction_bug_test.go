package dlq

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// captureReasonStore 是注入用的 recordStore:记录传给 MarkFailed 的 reason 字符串,
// 用于断言 money-path DLQ 在把 handler 错误落库前对其做了脱敏。
type captureReasonStore struct {
	rec           *Record
	gotReason     string
	markFailedHit int
}

func (f *captureReasonStore) Enqueue(context.Context, Event) (int64, error)      { return 0, nil }
func (f *captureReasonStore) List(context.Context, ListFilter) ([]Record, error) { return nil, nil }
func (f *captureReasonStore) MarkDelivered(context.Context, Record) error        { return nil }
func (f *captureReasonStore) Claim(context.Context, Lane, string, time.Duration) (*Record, error) {
	return f.rec, nil
}
func (f *captureReasonStore) ClaimByID(context.Context, int64, string, time.Duration) (*Record, error) {
	return f.rec, nil
}
func (f *captureReasonStore) MarkFailed(_ context.Context, _ Record, reason string, _ RetryDecision) error {
	f.markFailedHit++
	f.gotReason = reason
	return nil
}

// errB19Secret 模拟一个 handler 错误,其消息里嵌入了凭证派生文本(secretKeyPattern 命中的 "password="）。
// 当前有缺陷的代码把 err.Error() 原样落到 usage_record_dlq.replay_failure_reason,
// 并经 operator List/GetByID API 明文外泄。正确行为:与 obs outbox 路径一致,落库前 RedactString。
var errB19Secret = errors.New("bind ctx: password=hunter2-super-secret rejected by upstream")

// TestB19ReplayRedactsFailureReason 守 money-path DLQ 手动 replay 路径:
// handler 错误在写入 replay_failure_reason 之前必须脱敏,不能把敏感明文落库/外泄给 operator。
// 变异检查:把 service.go 的 err.Error() 换回未脱敏 → gotReason 含 "hunter2" 明文 → 红。
func TestB19ReplayRedactsFailureReason(t *testing.T) {
	store := &captureReasonStore{rec: &Record{EventKind: "k"}}
	s := newReplayService(store, func(context.Context, Record) error { return errB19Secret })

	if _, err := s.Replay(context.Background(), 1, "operator-1"); err == nil {
		t.Fatal("Replay must still surface the handler error")
	}
	if store.markFailedHit != 1 {
		t.Fatalf("MarkFailed should be attempted exactly once; got %d", store.markFailedHit)
	}
	if strings.Contains(store.gotReason, "hunter2") || strings.Contains(store.gotReason, "password") {
		t.Fatalf("replay_failure_reason must be redacted before persistence; got cleartext secret %q", store.gotReason)
	}
	if store.gotReason == "" {
		t.Fatalf("redacted reason must be non-empty so operators still see a marker")
	}
}

// TestB19ProcessClaimRedactsFailureReason 守 money-path DLQ worker 路径(ProcessClaim):
// 与 replay 路径同样必须在落库前脱敏 handler 错误。
// 变异检查:把 service.go:143 的 err.Error() 换回未脱敏 → gotReason 含明文 → 红。
func TestB19ProcessClaimRedactsFailureReason(t *testing.T) {
	store := &captureReasonStore{rec: &Record{EventKind: "k"}}
	s := newReplayService(store, func(context.Context, Record) error { return errB19Secret })

	if _, err := s.ProcessClaim(context.Background(), LaneHigh, "worker-1", 30*time.Second); err != nil {
		t.Fatalf("ProcessClaim should not error when MarkFailed succeeds; got %v", err)
	}
	if store.markFailedHit != 1 {
		t.Fatalf("MarkFailed should be attempted exactly once; got %d", store.markFailedHit)
	}
	if strings.Contains(store.gotReason, "hunter2") || strings.Contains(store.gotReason, "password") {
		t.Fatalf("replay_failure_reason must be redacted before persistence; got cleartext secret %q", store.gotReason)
	}
}

// TestB19RedactFailureReasonPreservesBenign 守脱敏不过度:不含敏感标记的普通错误串
// (uuid.Parse / json 之类)应原样保留,方便 operator 诊断。
// 变异检查:把 redactFailureReason 改成永远返回 [REDACTED] → 红。
func TestB19RedactFailureReasonPreservesBenign(t *testing.T) {
	benign := "dlq: handler not registered: usage_record"
	if got := redactFailureReason(benign); got != benign {
		t.Fatalf("benign reason must pass through unredacted; got %q", got)
	}
}
