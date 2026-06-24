package credentialworker

import (
	"context"
	"testing"
	"time"
)

// When enabled via WithRotationScan, RunOnce runs the rotation scan after the
// refresh pass: it queries with the configured limit and a now-maxAge cutoff and
// routes a due refreshable candidate into refresh recovery. Mutation guard: drop
// the ScanRotationDue call in RunOnce and the store is never touched → red.
func TestRunOnce_RunsRotationScanWhenEnabled(t *testing.T) {
	store := &fakeRotationStore{due: []RotationCandidate{oauthCand(3)}}
	s := newTestScheduler(nil, &stormSpy{}, &refresherSpy{}, WithRotationScan(store, 90*24*time.Hour, 10, nil))
	s.now = func() time.Time { return rotNow }

	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if store.gotLimit != 10 || len(store.recovered) != 1 {
		t.Fatalf("enabled rotation scan must query(limit=10)+recover(1), got limit=%d recovered=%d", store.gotLimit, len(store.recovered))
	}
	if want := rotNow.Add(-90 * 24 * time.Hour); !store.gotOlderThan.Equal(want) {
		t.Fatalf("cutoff must be now-maxAge=%v, got %v", want, store.gotOlderThan)
	}
}

// With no WithRotationScan (rotationMaxAge stays 0) the scan is skipped entirely
// even if a store is present — strictly opt-in, zero behavior change by default.
func TestRunOnce_SkipsRotationScanByDefault(t *testing.T) {
	store := &fakeRotationStore{due: []RotationCandidate{oauthCand(1)}}
	s := newTestScheduler(nil, &stormSpy{}, &refresherSpy{})
	s.rotationStore = store // store set, but maxAge left 0 → disabled

	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if store.gotLimit != 0 || len(store.recovered) != 0 || len(store.flagged) != 0 {
		t.Fatalf("maxAge=0 must skip the rotation scan, got limit=%d recovered=%d flagged=%d",
			store.gotLimit, len(store.recovered), len(store.flagged))
	}
}
