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
	sel := NewDefaultSelector(&stubAccountSource{accounts: accounts}, WithSlotManager(newMemSlotManager()))

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
	// Mutation: deleting the protocol-family gate selects priority-first account 101.
	if res.AccountID != 202 {
		t.Fatalf("selected AccountID=%d, want anthropic family account 202", res.AccountID)
	}
}
