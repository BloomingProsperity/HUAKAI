package proxyhealth

import (
	"context"
	"testing"
	"time"
)

// PROXY-04: hysteresis must require N consecutive fails to mark dead and M
// consecutive successes to recover, so a flapping proxy never oscillates.
func TestDecideStatus_Hysteresis(t *testing.T) {
	c := &counters{}
	// active stays active until deadThreshold consecutive fails
	for i := 1; i < deadThreshold; i++ {
		if got := decideStatus("active", false, c); got != "" {
			t.Fatalf("fail #%d should not flip yet, got %q", i, got)
		}
	}
	if got := decideStatus("active", false, c); got != "dead" {
		t.Fatalf("fail #%d should flip active->dead, got %q", deadThreshold, got)
	}

	// dead recovers after recoverThreshold consecutive successes
	c2 := &counters{}
	for i := 1; i < recoverThreshold; i++ {
		if got := decideStatus("dead", true, c2); got != "" {
			t.Fatalf("success #%d should not recover yet, got %q", i, got)
		}
	}
	if got := decideStatus("dead", true, c2); got != "active" {
		t.Fatalf("success #%d should recover dead->active, got %q", recoverThreshold, got)
	}

	// MUTATION GUARD: a success resets the fail counter (and vice versa), so pure
	// flapping never transitions. Removing the counter reset would flip here.
	c3 := &counters{}
	for i := 0; i < 6; i++ {
		if got := decideStatus("active", false, c3); got != "" {
			t.Fatalf("flap fail should not transition, got %q", got)
		}
		if got := decideStatus("active", true, c3); got != "" {
			t.Fatalf("flap success should not transition, got %q", got)
		}
	}
}

type fakeLister struct{ rows []ProxyTarget }

func (f fakeLister) List(context.Context) ([]ProxyTarget, error) { return f.rows, nil }

type fakeProber struct{ ok bool }

func (f fakeProber) Probe(context.Context, ProxyTarget) bool { return f.ok }

type fakeStore struct {
	touched []int64
	set     []string
}

func (f *fakeStore) Touch(_ context.Context, id int64) error {
	f.touched = append(f.touched, id)
	return nil
}
func (f *fakeStore) SetStatus(_ context.Context, _, _ int64, status string) error {
	f.set = append(f.set, status)
	return nil
}

func TestWorker_Tick_FlipsDeadAfterThreshold(t *testing.T) {
	store := &fakeStore{}
	w := NewWorker(
		fakeLister{rows: []ProxyTarget{{ID: 1, TenantID: 9, Status: "active", Host: "h", Port: 1}}},
		fakeProber{ok: false}, store, time.Minute, nil)
	for i := 0; i < deadThreshold; i++ {
		w.tick(context.Background())
	}
	got := ""
	for _, s := range store.set {
		if s == "dead" {
			got = s
		}
	}
	if got != "dead" {
		t.Fatalf("expected a dead transition after %d failing ticks, set=%v", deadThreshold, store.set)
	}
}

func TestWorker_Tick_RecoversAfterThreshold(t *testing.T) {
	store := &fakeStore{}
	w := NewWorker(
		fakeLister{rows: []ProxyTarget{{ID: 2, TenantID: 9, Status: "dead", Host: "h", Port: 1}}},
		fakeProber{ok: true}, store, time.Minute, nil)
	for i := 0; i < recoverThreshold; i++ {
		w.tick(context.Background())
	}
	got := ""
	for _, s := range store.set {
		if s == "active" {
			got = s
		}
	}
	if got != "active" {
		t.Fatalf("expected an active recovery after %d ok ticks, set=%v", recoverThreshold, store.set)
	}
}

// No transition -> Touch advances last_check_at (so the oldest-checked ordering
// makes progress and the proxy is re-probed).
func TestWorker_Tick_TouchesWhenNoChange(t *testing.T) {
	store := &fakeStore{}
	w := NewWorker(
		fakeLister{rows: []ProxyTarget{{ID: 3, TenantID: 9, Status: "active", Host: "h", Port: 1}}},
		fakeProber{ok: true}, store, time.Minute, nil)
	w.tick(context.Background())
	if len(store.touched) != 1 || store.touched[0] != 3 {
		t.Fatalf("expected Touch(3), got %v", store.touched)
	}
	if len(store.set) != 0 {
		t.Fatalf("expected no status change, got %v", store.set)
	}
}
