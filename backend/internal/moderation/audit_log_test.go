package moderation

import (
	"context"
	"strings"
	"testing"
)

func TestAuditLogger_ZeroSampleRateDoesNotWrite(t *testing.T) {
	sink := &auditSinkStub{}
	logger := NewAuditLogger(sink)

	err := logger.Log(context.Background(), ModerationEvent{
		TenantID:    7,
		APIKeyID:    11,
		UserID:      13,
		RequestID:   "req-zero-sample",
		PayloadHash: "hash-only",
		Decision:    DecisionPass,
		ReasonCode:  "clean",
	}, ModerationConfig{Enabled: true, SampleRatePct: 0})
	if err != nil {
		t.Fatalf("Log returned error: %v", err)
	}
	if sink.calls != 0 {
		t.Fatalf("sink calls=%d want 0 when sample_rate_pct=0", sink.calls)
	}
}

func TestAuditLogger_StoresHashMetadataOnly(t *testing.T) {
	sink := &auditSinkStub{}
	logger := NewAuditLogger(sink)

	err := logger.Log(context.Background(), ModerationEvent{
		TenantID:         7,
		APIKeyID:         11,
		UserID:           13,
		RequestID:        "req-hash-only",
		PayloadHash:      "abc123hash",
		Decision:         DecisionBlockKeyword,
		ReasonCode:       "keyword_policy",
		MatchedKeywordID: ptrInt64(17),
	}, ModerationConfig{Enabled: true, SampleRatePct: 100})
	if err != nil {
		t.Fatalf("Log returned error: %v", err)
	}
	if sink.calls != 1 {
		t.Fatalf("sink calls=%d want 1", sink.calls)
	}
	got := sink.events[0]
	if got.PayloadHash != "abc123hash" || got.MatchedKeywordID == nil || *got.MatchedKeywordID != 17 {
		t.Fatalf("stored metadata mismatch: %+v", got)
	}
	if strings.Contains(got.ReasonCode, "forbidden raw body") {
		t.Fatalf("reason code leaked body text: %+v", got)
	}
}

// TestAuditLogger_ViolationWrittenUnconditionallyAtLowSampleRate 锁定合规取证不打折:
// 即便运营把 sample_rate_pct 调到极低甚至 0,每一条违规/拦截审计仍必须落库,不被采样丢弃。
// 变异证:把 audit_log.go 里的 isSampleableDecision 守卫去掉(让所有 decision 都走采样),
// block 事件就会在 SampleRatePct=0 时被丢,sink.calls 变 0,本用例立刻变红。
func TestAuditLogger_ViolationWrittenUnconditionallyAtLowSampleRate(t *testing.T) {
	blockDecisions := []Decision{
		DecisionBlockKeyword,
		DecisionBlockHash,
		DecisionBlockExternal,
		DecisionBlockBackend,
		DecisionFeeCharged,
	}
	for _, decision := range blockDecisions {
		decision := decision
		t.Run(string(decision), func(t *testing.T) {
			// SampleRatePct=0 是最严苛的采样:clean 事件此时全丢。用同一个 request id
			// 在 rate=0 和 rate=1 下都断言落库,证明丢不丢只取决于 decision 而非采样。
			for _, rate := range []int32{0, 1} {
				sink := &auditSinkStub{}
				logger := NewAuditLogger(sink)
				err := logger.Log(context.Background(), ModerationEvent{
					TenantID:    7,
					APIKeyID:    11,
					UserID:      13,
					RequestID:   "req-violation-forensic",
					PayloadHash: "violation-hash",
					Decision:    decision,
					ReasonCode:  "policy_violation",
				}, ModerationConfig{Enabled: true, SampleRatePct: rate})
				if err != nil {
					t.Fatalf("Log returned error: %v", err)
				}
				if sink.calls != 1 {
					t.Fatalf("decision=%s sample_rate=%d: sink calls=%d want 1(违规取证必须无条件落库)", decision, rate, sink.calls)
				}
			}
		})
	}
}

// TestAuditLogger_CleanEventSampledAtZeroRate 锁定 clean(pass)事件仍受采样约束:
// sample_rate_pct=0 时一条 clean 审计应被丢弃,sink 不被调用。这保证修复只豁免违规取证,
// 没有把 clean 噪音也一并无条件写入(否则采样开关失效、审计表被噪音淹没)。
// 变异证:若把 isSampleableDecision 改成永远返回 false(对 clean 也不采样),clean 事件
// 会在 rate=0 时被写入,sink.calls 变 1,本用例变红。
func TestAuditLogger_CleanEventSampledAtZeroRate(t *testing.T) {
	sink := &auditSinkStub{}
	logger := NewAuditLogger(sink)

	err := logger.Log(context.Background(), ModerationEvent{
		TenantID:    7,
		APIKeyID:    11,
		UserID:      13,
		RequestID:   "req-clean-sampled",
		PayloadHash: "clean-hash",
		Decision:    DecisionPass,
		ReasonCode:  "clean",
	}, ModerationConfig{Enabled: true, SampleRatePct: 0})
	if err != nil {
		t.Fatalf("Log returned error: %v", err)
	}
	if sink.calls != 0 {
		t.Fatalf("clean 事件在 sample_rate=0 时 sink calls=%d want 0(clean 必须可被采样丢弃)", sink.calls)
	}
}

func TestDeterministicSamplerStableForRequest(t *testing.T) {
	first := ShouldSample("req-stable", 50)
	for i := 0; i < 10; i++ {
		if got := ShouldSample("req-stable", 50); got != first {
			t.Fatalf("sampler not deterministic: first=%v got=%v", first, got)
		}
	}
	if ShouldSample("anything", 0) {
		t.Fatalf("sample rate 0 unexpectedly sampled")
	}
	if !ShouldSample("anything", 100) {
		t.Fatalf("sample rate 100 did not sample")
	}
}

type auditSinkStub struct {
	calls  int
	events []ModerationEvent
}

func (s *auditSinkStub) InsertModerationLog(_ context.Context, event ModerationEvent) (int64, error) {
	s.calls++
	s.events = append(s.events, event)
	return int64(s.calls), nil
}

func ptrInt64(v int64) *int64 {
	return &v
}
