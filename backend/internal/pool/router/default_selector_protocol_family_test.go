package router

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestDefaultSelectorProtocolFamilyGateRejectsPriorityFirstWrongFamily(t *testing.T) {
	accounts := []*AccountSnapshot{
		{
			ID:             101,
			TenantID:       7,
			Priority:       1,
			LoadRate:       0.01,
			MaxConcurrency: 4,
			HealthState:    "healthy",
			ProtocolFamily: "openai_chat",
		},
		{
			ID:             202,
			TenantID:       7,
			Priority:       50,
			LoadRate:       0.01,
			MaxConcurrency: 4,
			HealthState:    "healthy",
			ProtocolFamily: "anthropic_messages",
		},
	}
	slots := &spySlotManager{inner: newMemSlotManager()}
	sel := NewDefaultSelector(&stubAccountSource{accounts: accounts}, WithSlotManager(slots))

	res, err := sel.Select(context.Background(), SelectionRequest{
		TenantID:       7,
		ClaimID:        0,
		RequestedModel: "claude-3-5-sonnet",
		ProtocolFamily: "anthropic_messages",
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if res == nil || res.AcquisitionToken == uuid.Nil {
		t.Fatalf("Select returned no acquisition result: %+v", res)
	}
	// 变异:删掉 protocol-family gate 会按优先级优先选中账号 101。
	if res.AccountID != 202 {
		t.Fatalf("selected AccountID=%d, want anthropic family account 202", res.AccountID)
	}
	if slots.calls() != 0 {
		t.Fatalf("ClaimID=0 不应占用并发槽，Acquire calls=%d want 0", slots.calls())
	}
}

func TestDefaultSelectorZeroClaimWithBindingCapAcquiresAndReleasesSlot(t *testing.T) {
	accounts := []*AccountSnapshot{{
		ID: 202, TenantID: 7, Priority: 1, LoadRate: 0.01,
		MaxConcurrency: 4, HealthState: "healthy", ProtocolFamily: "gemini_messages",
	}}
	inner := newMemSlotManager()
	slots := &spySlotManager{inner: inner}
	claims := &captureClaimGate{}
	sel := NewDefaultSelector(
		&stubAccountSource{accounts: accounts},
		WithSlotManager(slots),
		WithClaimGate(claims),
	)

	res, err := sel.Select(context.Background(), SelectionRequest{
		TenantID: 7, ClaimID: 0, BindingID: 19, MaxParallelRequests: 1,
		RequestedModel: "gemini-pro", ProtocolFamily: "gemini_messages",
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if res == nil || res.AcquisitionToken == uuid.Nil || res.Release == nil {
		t.Fatalf("result=%+v want token 与 Release", res)
	}
	if slots.calls() != 1 {
		t.Fatalf("Acquire calls=%d want 1", slots.calls())
	}
	if len(claims.calls) != 0 {
		t.Fatalf("无 claim 请求不应写 billing claim，calls=%+v", claims.calls)
	}
	if err := res.Release(context.Background()); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if got := inner.releaseCount(res.AcquisitionToken); got != 1 {
		t.Fatalf("release count=%d want 1", got)
	}
}
