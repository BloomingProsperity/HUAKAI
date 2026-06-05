package budget

import (
	"context"
	"testing"
	"time"
)

// 守:memory_fallback 下旧分钟桶必须被回收,否则 Redis 长时间不可用时 map 无限增长。
// Mutation: 去掉 evictStale 调用 → minute100 桶仍在 → RPM==1,本断言红。
func TestMemoryStore_EvictsStaleMinuteBuckets(t *testing.T) {
	cur := time.Unix(60*100, 0).UTC()
	s := NewMemoryStore(func() time.Time { return cur })
	scope := Scope{TenantID: 1, Kind: ScopeAPIKey, ID: "k1"}
	req := CounterRequest{Scope: scope, ClaimID: 1, Limits: LimitPair{RPM: 100, TPM: 100}, RPMIncrement: 1, TPMIncrement: 1}
	if _, err := s.CheckAndIncrement(context.Background(), req); err != nil {
		t.Fatalf("inc1: %v", err)
	}
	if got := s.CounterValue(scope, 100, CounterRPM); got != 1 {
		t.Fatalf("minute100 RPM=%d want 1", got)
	}
	cur = time.Unix(60*103, 0).UTC() // 推进 3 分钟
	req2 := CounterRequest{Scope: scope, ClaimID: 2, Limits: LimitPair{RPM: 100, TPM: 100}, RPMIncrement: 1, TPMIncrement: 1}
	if _, err := s.CheckAndIncrement(context.Background(), req2); err != nil {
		t.Fatalf("inc2: %v", err)
	}
	if got := s.CounterValue(scope, 100, CounterRPM); got != 0 {
		t.Fatalf("minute100 RPM=%d after advance, want 0 (evicted)", got)
	}
}
