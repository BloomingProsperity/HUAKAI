package moduleregistry

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestRegisterGetListByCategory — exercises the core CRUD-ish surface.
// Regression it catches: if List/ListByCategory stopped filtering or sorting
// (e.g. ListByCategory returned every module regardless of cat), the category
// count assertion goes RED.
func TestRegisterGetListByCategory(t *testing.T) {
	r := New()
	mustRegister(t, r, ModuleDescriptor{ID: "billing.service", Category: "money-path", Title: "Billing"})
	mustRegister(t, r, ModuleDescriptor{ID: "routing.selector", Category: "routing", Title: "Selector"})
	mustRegister(t, r, ModuleDescriptor{ID: "credentials.worker", Category: "credentials", Title: "Cred worker"})

	if got, ok := r.Get("billing.service"); !ok || got.Title != "Billing" {
		t.Fatalf("Get(billing.service) = %+v ok=%v; want Billing", got, ok)
	}
	if _, ok := r.Get("does.not.exist"); ok {
		t.Fatalf("Get(missing) returned ok=true")
	}
	if all := r.List(); len(all) != 3 {
		t.Fatalf("List len=%d want 3", len(all))
	}
	// List must be sorted by ID — billing < credentials < routing.
	all := r.List()
	if all[0].ID != "billing.service" || all[2].ID != "routing.selector" {
		t.Fatalf("List not sorted by ID: %v", idsOf(all))
	}
	money := r.ListByCategory("money-path")
	if len(money) != 1 || money[0].ID != "billing.service" {
		t.Fatalf("ListByCategory(money-path)=%v want [billing.service]", idsOf(money))
	}
	if got := r.ListByCategory("nope"); len(got) != 0 {
		t.Fatalf("ListByCategory(nope)=%v want empty", idsOf(got))
	}
}

// TestRegisterEmptyIDRejected — an empty ID is a programming error.
// Regression: if Register stopped guarding ID=="" the registry would hold an
// unaddressable module and this returns nil instead of ErrEmptyID -> RED.
func TestRegisterEmptyIDRejected(t *testing.T) {
	r := New()
	if err := r.Register(ModuleDescriptor{ID: "", Title: "ghost"}); err != ErrEmptyID {
		t.Fatalf("Register(empty ID) err=%v want ErrEmptyID", err)
	}
	if len(r.List()) != 0 {
		t.Fatalf("empty-ID descriptor was stored")
	}
}

// TestRegisterDupIDLastWins — documented dup policy is last-wins/idempotent.
// Regression: if Register were changed to first-wins (ignore re-register) the
// Title would still read the OLD value and this assertion goes RED; if it were
// changed to error-on-dup the err!=nil branch fires. Either deviation is caught.
func TestRegisterDupIDLastWins(t *testing.T) {
	r := New()
	mustRegister(t, r, ModuleDescriptor{ID: "billing.service", Title: "old"})
	if err := r.Register(ModuleDescriptor{ID: "billing.service", Title: "new"}); err != nil {
		t.Fatalf("re-register same ID err=%v want nil (idempotent last-wins)", err)
	}
	if len(r.List()) != 1 {
		t.Fatalf("dup ID created a second entry: %d", len(r.List()))
	}
	got, _ := r.Get("billing.service")
	if got.Title != "new" {
		t.Fatalf("dup ID Title=%q want %q (last-wins)", got.Title, "new")
	}
}

// TestSnapshotNoProbeIsUnknown — a module without a probe is "unknown", not
// "error" or "ok". Regression: if runProbe/Snapshot defaulted a no-probe module
// to StatusOK, the operator would see a false-healthy and this goes RED.
func TestSnapshotNoProbeIsUnknown(t *testing.T) {
	r := New()
	mustRegister(t, r, ModuleDescriptor{ID: "x", Title: "x"}) // no probe
	snaps := r.Snapshot(context.Background())
	if len(snaps) != 1 {
		t.Fatalf("snap len=%d want 1", len(snaps))
	}
	if snaps[0].Probe.Status != StatusUnknown {
		t.Fatalf("no-probe status=%q want unknown", snaps[0].Probe.Status)
	}
}

// TestSnapshotSlowProbeTimesOutToUnknown — THE core timeout guarantee. A probe
// that blocks far longer than the per-probe timeout must resolve to "unknown"
// WITHOUT hanging the snapshot.
// Regression: if runProbe waited on the probe instead of racing it against the
// timeout (delete the `case <-ctx.Done()` branch), Snapshot would block ~2s and
// this test's own 1s deadline-style assertion + status assertion go RED.
func TestSnapshotSlowProbeTimesOutToUnknown(t *testing.T) {
	r := NewWithProbeTimeout(50 * time.Millisecond)
	slowStarted := make(chan struct{})
	mustRegister(t, r, ModuleDescriptor{
		ID:    "slow",
		Title: "slow",
		HealthProbe: func(ctx context.Context) ProbeResult {
			close(slowStarted)
			// Block far past the 50ms probe timeout; honor ctx so the goroutine
			// is not truly leaked.
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
			}
			return ProbeResult{Status: StatusOK} // would be a LIE if returned
		},
	})

	start := time.Now()
	snaps := r.Snapshot(context.Background())
	elapsed := time.Since(start)

	<-slowStarted // probe really ran
	if elapsed > 500*time.Millisecond {
		t.Fatalf("Snapshot blocked %v on slow probe; want ~timeout (50ms), not a hang", elapsed)
	}
	if snaps[0].Probe.Status != StatusUnknown {
		t.Fatalf("slow-probe status=%q want unknown (timed out), got=%+v", snaps[0].Probe.Status, snaps[0].Probe)
	}
}

// TestSnapshotRunsProbesConcurrently — N probes each sleeping D must finish in
// well under N*D wall-time, proving they run in parallel not serially.
// Regression: if Snapshot ran probes serially (remove the goroutine / wg), 4
// probes * 60ms would take >240ms and exceed the 200ms ceiling -> RED.
func TestSnapshotRunsProbesConcurrently(t *testing.T) {
	const n = 4
	const each = 60 * time.Millisecond
	r := NewWithProbeTimeout(2 * time.Second) // generous: not testing timeout here
	var ran int32
	for i := 0; i < n; i++ {
		mustRegister(t, r, ModuleDescriptor{
			ID:    string(rune('a' + i)),
			Title: "p",
			HealthProbe: func(ctx context.Context) ProbeResult {
				atomic.AddInt32(&ran, 1)
				time.Sleep(each)
				return ProbeResult{Status: StatusOK}
			},
		})
	}
	start := time.Now()
	snaps := r.Snapshot(context.Background())
	elapsed := time.Since(start)

	if atomic.LoadInt32(&ran) != n {
		t.Fatalf("ran=%d probes want %d", ran, n)
	}
	if elapsed >= n*each {
		t.Fatalf("Snapshot took %v >= serial bound %v; probes did not run concurrently", elapsed, n*each)
	}
	for _, s := range snaps {
		if s.Probe.Status != StatusOK {
			t.Fatalf("probe %s status=%q want ok", s.Descriptor.ID, s.Probe.Status)
		}
	}
}

// TestSnapshotProbePanicBecomesError — a panicking probe must not crash the
// operator path; it degrades to StatusError.
// Regression: remove the recover() in runProbe and this test panics the test
// binary instead of asserting StatusError -> RED (crash).
func TestSnapshotProbePanicBecomesError(t *testing.T) {
	r := New()
	mustRegister(t, r, ModuleDescriptor{
		ID:          "boom",
		Title:       "boom",
		HealthProbe: func(ctx context.Context) ProbeResult { panic("kaboom") },
	})
	snaps := r.Snapshot(context.Background())
	if snaps[0].Probe.Status != StatusError {
		t.Fatalf("panicking probe status=%q want error", snaps[0].Probe.Status)
	}
}

func mustRegister(t *testing.T, r *Registry, d ModuleDescriptor) {
	t.Helper()
	if err := r.Register(d); err != nil {
		t.Fatalf("Register(%s): %v", d.ID, err)
	}
}

func idsOf(ds []ModuleDescriptor) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.ID
	}
	return out
}
