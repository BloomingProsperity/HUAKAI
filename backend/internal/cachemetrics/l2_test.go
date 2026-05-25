package cachemetrics

import "testing"

func TestL2MetricsSnapshot(t *testing.T) {
	resetForTesting()
	defer resetForTesting()

	ObserveL2Hit("openai", "gpt-4o")
	ObserveL2Miss("openai", "gpt-4o")
	ObserveL2Miss("openai", "gpt-4o")
	SyncL2SizeBytes([]L2SizeSample{{Vendor: "openai", Model: "gpt-4o", SizeBytes: 123}})

	got := SnapshotL2()["vendor=openai,model=gpt-4o"]
	if got.HitTotal != 1 || got.MissTotal != 2 || got.SizeBytes != 123 {
		t.Fatalf("snapshot=%+v want hit=1 miss=2 size=123", got)
	}

	SyncL2SizeBytes(nil)
	got = SnapshotL2()["vendor=openai,model=gpt-4o"]
	if got.SizeBytes != 0 {
		t.Fatalf("size should zero after full sync, got %+v", got)
	}
}
