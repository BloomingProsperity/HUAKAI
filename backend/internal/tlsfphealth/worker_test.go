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

// UTLS-06: only profiles that fail validation get flagged drift_detected; valid
// ones (incl. presets) are left active.
func TestWorker_Tick_FlagsOnlyInvalid(t *testing.T) {
	recs := []ProfileRecord{
		{ID: 1, TenantID: 9, Fields: mimicry.ProfileFields{Name: "preset:chrome"}},                                                                         // valid preset
		{ID: 2, TenantID: 9, Fields: mimicry.ProfileFields{CipherSuites: []int{0x10000}, SupportedCurves: []int{29}, TLSSupportedVersions: []int{0x0304}}}, // invalid: out of range
		{ID: 3, TenantID: 9, Fields: mimicry.ProfileFields{Name: "incomplete-custom"}},                                                                     // invalid: incomplete
	}
	marker := &fakeMarker{}
	w := NewWorker(fakeLister{recs: recs}, marker, time.Minute, nil)
	w.tick(context.Background())

	// MUTATION GUARD: skipping validation (never flagging) makes len==0 -> red;
	// flagging the valid preset would include id 1 -> red.
	if len(marker.drifted) != 2 {
		t.Fatalf("expected exactly the 2 invalid profiles flagged, got %v", marker.drifted)
	}
	for _, id := range marker.drifted {
		if id == 1 {
			t.Fatal("a valid preset profile must NOT be flagged drift_detected")
		}
	}
}
