package billing

import (
	"context"
	"testing"
	"time"
)

func TestMemoryReplayStore_RecordAndLookup(t *testing.T) {
	s := NewMemoryReplayStore()
	ctx := context.Background()
	if err := s.Record(ctx, 1, 100, 200, "application/json", []byte(`{"ok":true}`), time.Hour); err != nil {
		t.Fatalf("Record: %v", err)
	}
	rec, ok, err := s.Lookup(ctx, 1, 100)
	if err != nil || !ok || rec == nil {
		t.Fatalf("Lookup miss: ok=%v err=%v", ok, err)
	}
	if rec.ResponseStatus != 200 || string(rec.ResponseBody) != `{"ok":true}` {
		t.Fatalf("Lookup wrong: %+v", rec)
	}
}

func TestMemoryReplayStore_DedupOnConflict(t *testing.T) {
	s := NewMemoryReplayStore()
	ctx := context.Background()
	_ = s.Record(ctx, 1, 100, 200, "application/json", []byte("first"), time.Hour)
	// 同 (tenant, claim) 重复写应被忽略 (ON CONFLICT DO NOTHING 语义)。
	_ = s.Record(ctx, 1, 100, 500, "application/json", []byte("second"), time.Hour)
	rec, ok, _ := s.Lookup(ctx, 1, 100)
	if !ok || string(rec.ResponseBody) != "first" {
		t.Fatalf("dedup failed: ok=%v body=%q", ok, rec.ResponseBody)
	}
}

func TestMemoryReplayStore_Expiry(t *testing.T) {
	s := NewMemoryReplayStore()
	ctx := context.Background()
	_ = s.Record(ctx, 1, 100, 200, "application/json", []byte("x"), time.Nanosecond)
	time.Sleep(2 * time.Millisecond)
	if _, ok, _ := s.Lookup(ctx, 1, 100); ok {
		t.Fatal("expired record must not be found")
	}
}

func TestMemoryReplayStore_TenantIsolation(t *testing.T) {
	s := NewMemoryReplayStore()
	ctx := context.Background()
	_ = s.Record(ctx, 1, 100, 200, "application/json", []byte("t1"), time.Hour)
	if _, ok, _ := s.Lookup(ctx, 2, 100); ok {
		t.Fatal("tenant 2 must not see tenant 1 record")
	}
}
