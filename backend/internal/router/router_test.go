package router

import (
	"context"
	"errors"
	"testing"
)

// TestRouter_Plan_RejectsMissingRequestID enforces CMB-6: every request
// must carry a request_id by the time Router runs.
func TestRouter_Plan_RejectsMissingRequestID(t *testing.T) {
	r := NewDefaultRouter()
	_, err := r.Plan(context.Background(), PlanInput{
		Context: RequestContext{TenantID: 1},
		Model:   ResolvedModel{ProtocolFamily: "anthropic_messages"},
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
		Model:   ResolvedModel{ProtocolFamily: "anthropic_messages"},
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
		Model:   ResolvedModel{}, // no ProtocolFamily
	})
	var pe *PlanError
	if !errors.As(err, &pe) || pe.Code != "model_unsupported" {
		t.Fatalf("expected model_unsupported PlanError; got %v", err)
	}
}

// TestRouter_PlanWithPoolGroupID_HappyPath verifies the Phase C escape
// hatch produces a 1-attempt plan whose PoolGroupID matches the input
// and whose RequiredCapabilities reflect Features.
func TestRouter_PlanWithPoolGroupID_HappyPath(t *testing.T) {
	r := NewDefaultRouter()
	plan, err := r.PlanWithPoolGroupID(
		context.Background(),
		PlanInput{
			Context:  RequestContext{RequestID: "r2", TenantID: 7, APIKeyID: 11, UserID: 3},
			Model:    ResolvedModel{ProtocolFamily: "anthropic_messages"},
			Features: RequestFeatures{Stream: true, WantsToolUse: true},
		},
		42,
	)
	if err != nil {
		t.Fatalf("PlanWithPoolGroupID: %v", err)
	}
	if len(plan.Attempts) != 1 {
		t.Fatalf("expected 1 attempt; got %d", len(plan.Attempts))
	}
	a := plan.Attempts[0]
	if a.PoolGroupID != 42 {
		t.Fatalf("expected PoolGroupID=42; got %d", a.PoolGroupID)
	}
	if a.Reason != "primary" {
		t.Fatalf("expected Reason=primary; got %q", a.Reason)
	}
	want := map[string]bool{"stream": true, "tools": true}
	if len(a.RequiredCapabilities) != len(want) {
		t.Fatalf("RequiredCapabilities mismatch; got %v want %v", a.RequiredCapabilities, want)
	}
	for _, c := range a.RequiredCapabilities {
		if !want[c] {
			t.Fatalf("unexpected capability %q in plan; want only stream+tools", c)
		}
	}
	if plan.AttemptBudget != 1 {
		t.Fatalf("expected AttemptBudget=1; got %d", plan.AttemptBudget)
	}
	if plan.SnapshotVersion == "" {
		t.Fatal("expected non-empty SnapshotVersion stamp")
	}
}

// TestRouter_Plan_HappyPathThroughInterface verifies the public Plan()
// method works end-to-end when the chat handler threads
// ExplicitPoolGroupID. This is the path real callers will use; the
// PlanWithPoolGroupID escape hatch is a Phase C v0.1 transitional
// helper only.
func TestRouter_Plan_HappyPathThroughInterface(t *testing.T) {
	r := NewDefaultRouter()
	plan, err := r.Plan(context.Background(), PlanInput{
		Context:             RequestContext{RequestID: "r-iface", TenantID: 5, APIKeyID: 6, UserID: 7},
		Model:               ResolvedModel{ProtocolFamily: "anthropic_messages"},
		Features:            RequestFeatures{Stream: true},
		ExplicitPoolGroupID: 99,
	})
	if err != nil {
		t.Fatalf("Plan via interface should succeed when ExplicitPoolGroupID set; got %v", err)
	}
	if len(plan.Attempts) != 1 || plan.Attempts[0].PoolGroupID != 99 {
		t.Fatalf("expected 1 attempt against pool 99; got %+v", plan.Attempts)
	}
}

// TestRouter_PlanWithPoolGroupID_RejectsZeroPool checks the escape hatch
// fail-closes when caller forgets to thread pool_group_id.
func TestRouter_PlanWithPoolGroupID_RejectsZeroPool(t *testing.T) {
	r := NewDefaultRouter()
	_, err := r.PlanWithPoolGroupID(
		context.Background(),
		PlanInput{Context: RequestContext{RequestID: "r3", TenantID: 1}},
		0,
	)
	if !errors.Is(err, errPoolGroupRequired) {
		t.Fatalf("expected errPoolGroupRequired; got %v", err)
	}
}
