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

// TestRouter_Plan_UsesRankedCandidates 验证 Router 保留 registry 排序，
// 并输出有界 multi-attempt plan。
func TestRouter_Plan_UsesRankedCandidates(t *testing.T) {
	r := NewDefaultRouter()
	plan, err := r.Plan(context.Background(), PlanInput{
		Context:  RequestContext{RequestID: "r-pri", TenantID: 5, APIKeyID: 6, UserID: 7},
		Model:    ResolvedModel{ProtocolFamily: "anthropic_messages", PoolCandidates: []int64{99, 100, 101}},
		Features: RequestFeatures{Stream: true, WantsToolUse: true},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Attempts) != 3 {
		t.Fatalf("expected 3 attempts; got %d", len(plan.Attempts))
	}
	assertAttempts(t, plan, []wantAttempt{
		{index: 0, poolGroupID: 99, reason: "primary"},
		{index: 1, poolGroupID: 100, reason: "cross_pool_fallback"},
		{index: 2, poolGroupID: 101, reason: "cross_pool_fallback"},
	})
	if plan.AttemptBudget != 3 {
		t.Fatalf("expected AttemptBudget=3; got %d", plan.AttemptBudget)
	}
	want := map[string]bool{"stream": true, "tools": true}
	for i, attempt := range plan.Attempts {
		if len(attempt.RequiredCapabilities) != len(want) {
			t.Fatalf("attempt %d RequiredCapabilities mismatch; got %v want %v", i, attempt.RequiredCapabilities, want)
		}
		for _, c := range attempt.RequiredCapabilities {
			if !want[c] {
				t.Fatalf("attempt %d unexpected capability %q in plan; want only stream+tools", i, c)
			}
		}
	}
}

func TestRouter_Plan_MetadataAbsentFallsBackToPoolCandidateOrder(t *testing.T) {
	r := NewDefaultRouter()
	plan, err := r.Plan(context.Background(), PlanInput{
		Context: RequestContext{RequestID: "r-meta-missing", TenantID: 5},
		Model: ResolvedModel{
			ProtocolFamily:  "openai_chat",
			ProviderModelID: "default-upstream",
			PoolCandidates:  []int64{201, 202},
		},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	assertAttempts(t, plan, []wantAttempt{
		{index: 0, poolGroupID: 201, reason: "primary", upstreamModelID: "default-upstream"},
		{index: 1, poolGroupID: 202, reason: "cross_pool_fallback", upstreamModelID: "default-upstream"},
		{index: 2, poolGroupID: 201, reason: "same_pool_account_failover", upstreamModelID: "default-upstream"},
	})
	if plan.AttemptBudget != 3 {
		t.Fatalf("AttemptBudget=%d want 3", plan.AttemptBudget)
	}
}

func TestRouter_Plan_SinglePoolAddsSamePoolFailover(t *testing.T) {
	r := NewDefaultRouter()
	plan, err := r.Plan(context.Background(), PlanInput{
		Context: RequestContext{RequestID: "r-single", TenantID: 5},
		Model:   ResolvedModel{ProtocolFamily: "openai_chat", PoolCandidates: []int64{301}},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	assertAttempts(t, plan, []wantAttempt{
		{index: 0, poolGroupID: 301, reason: "primary"},
		{index: 1, poolGroupID: 301, reason: "same_pool_account_failover"},
	})
	if plan.AttemptBudget != 2 {
		t.Fatalf("AttemptBudget=%d want 2", plan.AttemptBudget)
	}
}

func TestRouter_Plan_TruncatesBudgetAtThree(t *testing.T) {
	r := NewDefaultRouter()
	plan, err := r.Plan(context.Background(), PlanInput{
		Context: RequestContext{RequestID: "r-budget", TenantID: 5},
		Model:   ResolvedModel{ProtocolFamily: "openai_chat", PoolCandidates: []int64{401, 402, 403, 404}},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	assertAttempts(t, plan, []wantAttempt{
		{index: 0, poolGroupID: 401, reason: "primary"},
		{index: 1, poolGroupID: 402, reason: "cross_pool_fallback"},
		{index: 2, poolGroupID: 403, reason: "cross_pool_fallback"},
	})
	if plan.AttemptBudget != 3 {
		t.Fatalf("AttemptBudget=%d want 3", plan.AttemptBudget)
	}
}

func TestRouter_Plan_CarriesPerPoolUpstreamModelOverride(t *testing.T) {
	r := NewDefaultRouter()
	plan, err := r.Plan(context.Background(), PlanInput{
		Context: RequestContext{RequestID: "r-model-override", TenantID: 5},
		Model: ResolvedModel{
			ProtocolFamily:  "openai_chat",
			ProviderModelID: "default-model",
			PoolCandidates:  []int64{501, 502},
			PoolMetadata: []PoolCandidateMeta{
				{PoolGroupID: 501, ProviderModelID: "pool-a-model"},
				{PoolGroupID: 502, ProviderModelID: "pool-b-model"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	assertAttempts(t, plan, []wantAttempt{
		{index: 0, poolGroupID: 501, reason: "primary", upstreamModelID: "pool-a-model"},
		{index: 1, poolGroupID: 502, reason: "cross_pool_fallback", upstreamModelID: "pool-b-model"},
		{index: 2, poolGroupID: 501, reason: "same_pool_account_failover", upstreamModelID: "pool-a-model"},
	})
}

func TestRouter_Plan_RetryableEndClassesMatchPreDeliveryFailures(t *testing.T) {
	r := NewDefaultRouter()
	plan, err := r.Plan(context.Background(), PlanInput{
		Context: RequestContext{RequestID: "r-retryable-classes", TenantID: 5},
		Model:   ResolvedModel{ProtocolFamily: "openai_chat", PoolCandidates: []int64{601, 602}},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.RetryableEndClasses == nil {
		t.Fatal("RetryableEndClasses must be non-nil for a multi-attempt plan")
	}

	retryable := make(map[string]bool, len(plan.RetryableEndClasses))
	for _, endClass := range plan.RetryableEndClasses {
		retryable[endClass] = true
	}
	for _, want := range []string{
		"upstream_error_5xx",
		"upstream_rate_limit",
		"first_token_timeout",
		"inter_event_timeout",
	} {
		if !retryable[want] {
			t.Fatalf("RetryableEndClasses missing %q; got %v", want, plan.RetryableEndClasses)
		}
	}
	for _, forbidden := range []string{
		"upstream_auth_failure",
		"upstream_error_4xx",
		"response_event_too_large",
	} {
		if retryable[forbidden] {
			t.Fatalf("RetryableEndClasses must not include %q; got %v", forbidden, plan.RetryableEndClasses)
		}
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

type wantAttempt struct {
	index           int
	poolGroupID     int64
	reason          string
	upstreamModelID string
}

func assertAttempts(t *testing.T, plan RoutePlan, want []wantAttempt) {
	t.Helper()
	if len(plan.Attempts) != len(want) {
		t.Fatalf("attempt len=%d want %d", len(plan.Attempts), len(want))
	}
	for i, w := range want {
		got := plan.Attempts[i]
		if got.Index != w.index || got.PoolGroupID != w.poolGroupID || got.Reason != w.reason {
			t.Fatalf("attempt[%d]=Index:%d PoolGroupID:%d Reason:%q; want Index:%d PoolGroupID:%d Reason:%q",
				i, got.Index, got.PoolGroupID, got.Reason, w.index, w.poolGroupID, w.reason)
		}
		if got.UpstreamModelID != w.upstreamModelID {
			t.Fatalf("attempt[%d].UpstreamModelID=%q want %q", i, got.UpstreamModelID, w.upstreamModelID)
		}
	}
}
