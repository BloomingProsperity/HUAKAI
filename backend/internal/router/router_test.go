package router

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestRouter_Plan_RejectsMissingRequestID enforces CMB-6: every request
// must carry a request_id by the time Router runs.
func TestRouter_Plan_RejectsMissingRequestID(t *testing.T) {
	r := NewDefaultRouter()
	_, err := r.Plan(context.Background(), PlanInput{
		Context: RequestContext{TenantID: 1},
		Model:   ResolvedModel{ProtocolFamily: "anthropic_messages", PoolCandidates: []int64{42}},
	})
	if err == nil {
		t.Fatal("expected PlanError for missing RequestID")
	}
	var pe *PlanError
	if !errors.As(err, &pe) || pe.Code != "missing_request_id" {
		t.Fatalf("expected missing_request_id PlanError; got %v", err)
	}
}

// TestRouter_Plan_RejectsMissingTenant enforces CMB-1 boundary: Router
// must fail closed when Auth has not run.
func TestRouter_Plan_RejectsMissingTenant(t *testing.T) {
	r := NewDefaultRouter()
	_, err := r.Plan(context.Background(), PlanInput{
		Context: RequestContext{RequestID: "req-x"},
		Model:   ResolvedModel{ProtocolFamily: "anthropic_messages", PoolCandidates: []int64{42}},
	})
	var pe *PlanError
	if !errors.As(err, &pe) || pe.Code != "missing_tenant" {
		t.Fatalf("expected missing_tenant PlanError; got %v", err)
	}
}

// TestRouter_Plan_RejectsUnknownModel ensures Router refuses to plan when
// Registry has not classified the model's protocol family.
func TestRouter_Plan_RejectsUnknownModel(t *testing.T) {
	r := NewDefaultRouter()
	_, err := r.Plan(context.Background(), PlanInput{
		Context: RequestContext{RequestID: "r1", TenantID: 99},
		Model:   ResolvedModel{PoolCandidates: []int64{42}}, // no ProtocolFamily
	})
	var pe *PlanError
	if !errors.As(err, &pe) || pe.Code != "model_unsupported" {
		t.Fatalf("expected model_unsupported PlanError; got %v", err)
	}
}

// TestRouter_Plan_RequiresPoolCandidates verifies the Router fails closed
// when Registry surfaces an empty PoolCandidates list. Registry should
// have already returned ErrTenantNoAccess upstream — this is defense in
// depth (N+5b synthesized plan §"requestPoolGroupID rewrite").
func TestRouter_Plan_RequiresPoolCandidates(t *testing.T) {
	r := NewDefaultRouter()
	_, err := r.Plan(context.Background(), PlanInput{
		Context: RequestContext{RequestID: "rNP", TenantID: 7},
		Model:   ResolvedModel{ProtocolFamily: "anthropic_messages"}, // no PoolCandidates
	})
	var pe *PlanError
	if !errors.As(err, &pe) || pe.Code != "no_eligible_pool" {
		t.Fatalf("expected no_eligible_pool PlanError; got %v", err)
	}
}

// TestRouter_Plan_UsesPrimaryCandidate verifies the Router takes the head
// of PoolCandidates as the single attempt at L0 (AttemptBudget=1).
func TestRouter_Plan_UsesPrimaryCandidate(t *testing.T) {
	r := NewDefaultRouter()
	plan, err := r.Plan(context.Background(), PlanInput{
		Context:  RequestContext{RequestID: "r-pri", TenantID: 5, APIKeyID: 6, UserID: 7},
		Model:    ResolvedModel{ProtocolFamily: "anthropic_messages", PoolCandidates: []int64{99, 100, 101}},
		Features: RequestFeatures{Stream: true, WantsToolUse: true},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Attempts) != 1 {
		t.Fatalf("expected 1 attempt; got %d", len(plan.Attempts))
	}
	if plan.Attempts[0].PoolGroupID != 99 {
		t.Fatalf("expected PoolGroupID=99 (head of candidates); got %d", plan.Attempts[0].PoolGroupID)
	}
	if plan.Attempts[0].Reason != "primary" {
		t.Fatalf("expected Reason=primary; got %q", plan.Attempts[0].Reason)
	}
	want := map[string]bool{"stream": true, "tools": true}
	if len(plan.Attempts[0].RequiredCapabilities) != len(want) {
		t.Fatalf("RequiredCapabilities mismatch; got %v want %v", plan.Attempts[0].RequiredCapabilities, want)
	}
	for _, c := range plan.Attempts[0].RequiredCapabilities {
		if !want[c] {
			t.Fatalf("unexpected capability %q in plan; want only stream+tools", c)
		}
	}
	if plan.AttemptBudget != 1 {
		t.Fatalf("expected AttemptBudget=1; got %d", plan.AttemptBudget)
	}
}

// TestRouter_Plan_StampsConcatenatedSnapshot verifies the registry+router
// snapshot is concatenated onto RoutePlan.SnapshotVersion in the format
// documented in migration 0008.
func TestRouter_Plan_StampsConcatenatedSnapshot(t *testing.T) {
	r := NewDefaultRouter()
	plan, err := r.Plan(context.Background(), PlanInput{
		Context: RequestContext{RequestID: "r-stamp", TenantID: 8},
		Model: ResolvedModel{
			ProtocolFamily:  "anthropic_messages",
			PoolCandidates:  []int64{42},
			SnapshotVersion: "registry:8:5",
		},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	want := "registry:8:5;router:v0.1-phase-c"
	if plan.SnapshotVersion != want {
		t.Fatalf("SnapshotVersion = %q; want %q", plan.SnapshotVersion, want)
	}
}

// TestRouter_Plan_StampsFallbackOnEmptyRegistryStamp covers the defensive
// branch where Resolved.SnapshotVersion is empty (legacy / boot edge).
// The stamp must never start with a bare semicolon.
func TestRouter_Plan_StampsFallbackOnEmptyRegistryStamp(t *testing.T) {
	r := NewDefaultRouter()
	plan, err := r.Plan(context.Background(), PlanInput{
		Context: RequestContext{RequestID: "r-fb", TenantID: 9},
		Model: ResolvedModel{
			ProtocolFamily: "anthropic_messages",
			PoolCandidates: []int64{42},
			// SnapshotVersion intentionally empty
		},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if strings.HasPrefix(plan.SnapshotVersion, ";") {
		t.Fatalf("SnapshotVersion %q must not start with semicolon", plan.SnapshotVersion)
	}
	if !strings.HasPrefix(plan.SnapshotVersion, "registry:unknown;") {
		t.Fatalf("SnapshotVersion %q should fall back to registry:unknown prefix", plan.SnapshotVersion)
	}
}
