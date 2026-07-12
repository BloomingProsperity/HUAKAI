package tlsfphealth

import (
	"context"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
)

type fakeLister struct{ recs []ProfileRecord }

func (f fakeLister) ListActive(context.Context) ([]ProfileRecord, error) { return f.recs, nil }

type fakeMarker struct{ drifted []int64 }

func (f *fakeMarker) MarkDrift(_ context.Context, _, id int64) error {
	f.drifted = append(f.drifted, id)
	return nil
}

// UTLS-06:只有校验失败的 profile 才被标记 drift_detected;有效的
// (含 preset)保持 active。
func TestWorker_Tick_FlagsOnlyInvalid(t *testing.T) {
	recs := []ProfileRecord{
		{ID: 1, TenantID: 9, Fields: mimicry.ProfileFields{Name: "preset:chrome"}},                                                                         // 有效的 preset
		{ID: 2, TenantID: 9, Fields: mimicry.ProfileFields{CipherSuites: []int{0x10000}, SupportedCurves: []int{29}, TLSSupportedVersions: []int{0x0304}}}, // 无效:超出范围
		{ID: 3, TenantID: 9, Fields: mimicry.ProfileFields{Name: "incomplete-custom"}},                                                                     // 无效:不完整
	}
	marker := &fakeMarker{}
	w := NewWorker(fakeLister{recs: recs}, marker, time.Minute, nil)
	w.tick(context.Background())

	// 变异守卫:跳过校验(从不标记)会使 len==0 -> 变红;
	// 误标有效的 preset 会让结果包含 id 1 -> 变红。
	if len(marker.drifted) != 2 {
		t.Fatalf("expected exactly the 2 invalid profiles flagged, got %v", marker.drifted)
	}
	for _, id := range marker.drifted {
		if id == 1 {
			t.Fatal("a valid preset profile must NOT be flagged drift_detected")
		}
	}
}
