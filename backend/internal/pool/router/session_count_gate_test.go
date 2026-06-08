package router

import (
	"context"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/sessioncap"
)

func TestSessionCountGate_NewSessionOverCap(t *testing.T) {
	reg := sessioncap.NewRegistry(0)
	reg.Register(10, "sess-a")
	reg.Register(10, "sess-b")
	reg.Register(10, "sess-c")

	gate := SessionCountGate{Registry: reg}
	account := &AccountSnapshot{ID: 10, MaxSessions: 3}
	req := SelectionRequest{SessionHash: "sess-d"}

	ok, reason, err := gate.Allow(context.Background(), account, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected gate to exclude new session over cap")
	}
	if reason != GateFailureSessionCount {
		t.Fatalf("expected reason=%q got %q", GateFailureSessionCount, reason)
	}
}

func TestSessionCountGate_ExistingSessionAllowed(t *testing.T) {
	reg := sessioncap.NewRegistry(0)
	reg.Register(10, "sess-a")
	reg.Register(10, "sess-b")
	reg.Register(10, "sess-c")

	gate := SessionCountGate{Registry: reg}
	account := &AccountSnapshot{ID: 10, MaxSessions: 3}
	req := SelectionRequest{SessionHash: "sess-c"}

	ok, _, err := gate.Allow(context.Background(), account, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected gate to allow existing session (re-bind must never be rejected)")
	}
}

func TestSessionCountGate_MaxZeroDefaultSafety(t *testing.T) {
	reg := sessioncap.NewRegistry(0)
	for i := 0; i < 99; i++ {
		reg.Register(10, string(rune('a'+i%26))+string(rune('A'+i/26%26)))
	}

	gate := SessionCountGate{Registry: reg}
	account := &AccountSnapshot{ID: 10, MaxSessions: 0}
	req := SelectionRequest{SessionHash: "new-sess"}

	ok, _, err := gate.Allow(context.Background(), account, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected gate to allow when MaxSessions=0 (default safety)")
	}
}

func TestSessionCountGate_NilRegistryFailOpen(t *testing.T) {
	gate := SessionCountGate{Registry: nil}
	account := &AccountSnapshot{ID: 10, MaxSessions: 1}
	req := SelectionRequest{SessionHash: "any-sess"}

	ok, _, err := gate.Allow(context.Background(), account, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected gate to allow when registry is nil (fail-open)")
	}
}

func TestSessionCountGate_UnderCap(t *testing.T) {
	reg := sessioncap.NewRegistry(0)
	reg.Register(10, "sess-a")
	reg.Register(10, "sess-b")

	gate := SessionCountGate{Registry: reg}
	account := &AccountSnapshot{ID: 10, MaxSessions: 3}
	req := SelectionRequest{SessionHash: "sess-new"}

	ok, _, err := gate.Allow(context.Background(), account, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected gate to allow when under cap")
	}
}
