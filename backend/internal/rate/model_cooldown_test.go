package rate

import (
	"context"
	"testing"
	"time"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

type fakeModelCooldownQueries struct {
	calls int
	arg   dbbilling.SetProviderAccountModelRateLimitParams
}

func (f *fakeModelCooldownQueries) SetProviderAccountModelRateLimit(_ context.Context, arg dbbilling.SetProviderAccountModelRateLimitParams) error {
	f.calls++
	f.arg = arg
	return nil
}

func TestModelCooldownServiceRecordsDefault404Cooldown(t *testing.T) {
	// 守护的回归:上游 404 必须变为一次 model 作用域的冷却写入,而不只是
	// 一个请求局部的客户端错误。变异自检:删掉 service 的查询写入会使
	// fake.calls=0。
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	fake := &fakeModelCooldownQueries{}
	svc := NewModelCooldownService(fake, WithNow(func() time.Time { return now }))

	err := svc.RecordModelRateLimit(context.Background(), ModelCooldownInput{
		TenantID:          7,
		ProviderAccountID: 101,
		ModelKey:          "upstream-gpt-4o",
		StatusCode:        404,
		UpstreamRequestID: "up-req-1",
	})
	if err != nil {
		t.Fatalf("RecordModelRateLimit: %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("writes=%d, want 1", fake.calls)
	}
	if fake.arg.TenantID != 7 || fake.arg.ProviderAccountID != 101 {
		t.Fatalf("scope=(tenant=%d account=%d), want tenant 7 account 101", fake.arg.TenantID, fake.arg.ProviderAccountID)
	}
	if fake.arg.ModelKey != "upstream-gpt-4o" {
		t.Fatalf("model_key=%q, want upstream-gpt-4o", fake.arg.ModelKey)
	}
	if fake.arg.Reason != string(ReasonModelLimitExceeded) {
		t.Fatalf("reason=%q, want %q", fake.arg.Reason, ReasonModelLimitExceeded)
	}
	if fake.arg.UpstreamStatusCode != 404 || fake.arg.UpstreamRequestID != "up-req-1" {
		t.Fatalf("upstream evidence=(%d,%q), want (404, up-req-1)", fake.arg.UpstreamStatusCode, fake.arg.UpstreamRequestID)
	}
	if fake.arg.SourceLayer != "gateway_upstream_error" {
		t.Fatalf("source_layer=%q, want gateway_upstream_error", fake.arg.SourceLayer)
	}
	got := fake.arg.ResetAt.Time
	if got.Before(now.Add(5*time.Minute)) || got.After(now.Add(5*time.Minute+time.Second)) {
		t.Fatalf("reset_at=%s, want about now+5m", got.Format(time.RFC3339Nano))
	}
}
