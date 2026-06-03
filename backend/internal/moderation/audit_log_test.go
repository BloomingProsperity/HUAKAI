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
