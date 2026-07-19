package accountquota

import (
	"strings"
	"testing"
	"time"
)

func TestSnapshotValidateRejectsAmbiguousFacts(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	value := 25.0
	base := Snapshot{
		TenantID: 7, ProviderAccountID: 101, Vendor: "vendor",
		Source: SourceUpstreamUsage, ObservedAt: now,
	}
	tests := []struct {
		name  string
		facts []Fact
		want  string
	}{
		{name: "未知值携带数字", facts: []Fact{{MetricKey: "five_hour", State: StateUnknown, RemainingPercent: &value}}, want: "unknown 事实不得携带数值"},
		{name: "同维度重复", facts: []Fact{{MetricKey: "model_quota", ModelKey: "m", State: StateAvailable}, {MetricKey: "model_quota", ModelKey: "m", State: StateExhausted}}, want: "重复维度"},
		{name: "错误缺少分类", facts: []Fact{{MetricKey: "probe_status", State: StateError}}, want: "错误事实缺少 error_class"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			candidate.Facts = test.facts
			err := candidate.Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate()=%v，期望包含 %q", err, test.want)
			}
		})
	}
}

func TestParseViewPreservesStateAndComputesFreshness(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	raw := []byte(`[
{"metric_key":"fresh","state":"available","observed_at":"2026-07-19T09:59:00Z","valid_until":"2026-07-19T11:00:00Z","source":"upstream_usage"},
{"metric_key":"expired","state":"exhausted","observed_at":"2026-07-19T08:00:00Z","valid_until":"2026-07-19T09:00:00Z","source":"upstream_usage"},
{"metric_key":"future","state":"available","observed_at":"2026-07-19T10:01:00Z","source":"upstream_usage"}
]`)
	facts, err := ParseView(raw, now)
	if err != nil {
		t.Fatalf("ParseView: %v", err)
	}
	if len(facts) != 3 || !facts[0].Fresh || facts[1].Fresh || facts[2].Fresh {
		t.Fatalf("freshness=%+v", facts)
	}
	if facts[1].State != StateExhausted {
		t.Fatalf("过期只改变 fresh，不得篡改事实状态：%+v", facts[1])
	}
}
