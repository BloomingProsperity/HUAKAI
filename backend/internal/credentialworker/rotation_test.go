package credentialworker

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeRotationStore struct {
	due          []RotationCandidate
	dueErr       error
	gotOlderThan time.Time
	gotLimit     int
	flagged      []RotationCandidate
	flagErrOn    int // 1-based index at which FlagNeedsRotation returns an error; 0 = never
}

func (f *fakeRotationStore) DueForRotation(_ context.Context, olderThan time.Time, limit int) ([]RotationCandidate, error) {
	f.gotOlderThan = olderThan
	f.gotLimit = limit
	return f.due, f.dueErr
}

func (f *fakeRotationStore) FlagNeedsRotation(_ context.Context, c RotationCandidate) error {
	if f.flagErrOn != 0 && len(f.flagged)+1 == f.flagErrOn {
		return errors.New("flag failed")
	}
	f.flagged = append(f.flagged, c)
	return nil
}

var rotNow = time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)

// Disabled (maxAge<=0) must not touch the store at all — opt-in, default off.
// Mutation guard: if the maxAge<=0 short-circuit is removed, DueForRotation gets
// called and gotLimit becomes non-zero → red.
func TestScanRotationDue_DisabledByDefault(t *testing.T) {
	f := &fakeRotationStore{due: []RotationCandidate{{ProviderAccountID: 1}}}
	for _, maxAge := range []time.Duration{0, -time.Hour} {
		n, err := ScanRotationDue(context.Background(), f, nil, maxAge, rotNow, 50)
		if err != nil || n != 0 {
			t.Fatalf("maxAge=%v must be a no-op, got n=%d err=%v", maxAge, n, err)
		}
	}
	if f.gotLimit != 0 || len(f.flagged) != 0 {
		t.Fatalf("disabled scan must not query/flag, got limit=%d flagged=%d", f.gotLimit, len(f.flagged))
	}
}

// Each due candidate is flagged AND alerted; the cutoff passed to the store is
// now-maxAge. Mutation guards: if FlagNeedsRotation is not called, flagged is
// empty → red; if olderThan used the wrong sign (now+maxAge), the cutoff check
// → red.
func TestScanRotationDue_FlagsAndAlerts(t *testing.T) {
	due := []RotationCandidate{
		{TenantID: 1, ProviderAccountID: 10, CredentialID: 100},
		{TenantID: 1, ProviderAccountID: 11, CredentialID: 101},
	}
	f := &fakeRotationStore{due: due}
	var alerted []int64
	alert := func(_ context.Context, c RotationCandidate) { alerted = append(alerted, c.ProviderAccountID) }

	n, err := ScanRotationDue(context.Background(), f, alert, 90*24*time.Hour, rotNow, 50)
	if err != nil || n != 2 {
		t.Fatalf("two due credentials must flag 2, got n=%d err=%v", n, err)
	}
	if len(f.flagged) != 2 {
		t.Fatalf("both candidates must be flagged needs_rotation, got %d", len(f.flagged))
	}
	if len(alerted) != 2 || alerted[0] != 10 || alerted[1] != 11 {
		t.Fatalf("each flagged candidate must alert, got %v", alerted)
	}
	if want := rotNow.Add(-90 * 24 * time.Hour); !f.gotOlderThan.Equal(want) {
		t.Fatalf("cutoff must be now-maxAge=%v, got %v", want, f.gotOlderThan)
	}
}

// A flag error stops the scan and surfaces — a transient DB fault is not
// swallowed, and the count reflects only what was actually flagged.
func TestScanRotationDue_StopsOnFlagError(t *testing.T) {
	f := &fakeRotationStore{
		due:       []RotationCandidate{{ProviderAccountID: 1}, {ProviderAccountID: 2}, {ProviderAccountID: 3}},
		flagErrOn: 2,
	}
	n, err := ScanRotationDue(context.Background(), f, nil, time.Hour, rotNow, 50)
	if err == nil {
		t.Fatal("flag error must surface, got nil")
	}
	if n != 1 {
		t.Fatalf("only the first candidate flagged before the error, want n=1 got %d", n)
	}
}

// A DueForRotation error surfaces without flagging anything.
func TestScanRotationDue_QueryErrorSurfaces(t *testing.T) {
	f := &fakeRotationStore{dueErr: errors.New("db down")}
	n, err := ScanRotationDue(context.Background(), f, nil, time.Hour, rotNow, 50)
	if err == nil || n != 0 || len(f.flagged) != 0 {
		t.Fatalf("query error must surface with nothing flagged, got n=%d err=%v", n, err)
	}
}

// nil store is a safe no-op (never panics).
func TestScanRotationDue_NilStore(t *testing.T) {
	if n, err := ScanRotationDue(context.Background(), nil, nil, time.Hour, rotNow, 50); err != nil || n != 0 {
		t.Fatalf("nil store must be a no-op, got n=%d err=%v", n, err)
	}
}

// A non-positive limit is clamped to a safe default rather than fetching 0 rows.
func TestScanRotationDue_DefaultLimit(t *testing.T) {
	f := &fakeRotationStore{}
	ScanRotationDue(context.Background(), f, nil, time.Hour, rotNow, 0)
	if f.gotLimit <= 0 {
		t.Fatalf("non-positive limit must be clamped to a positive default, got %d", f.gotLimit)
	}
}
