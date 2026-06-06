package router

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

var benchmarkSelectionResult *SelectionResult

func BenchmarkDefaultSelectorSelect(b *testing.B) {
	now := time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC)
	accounts := make([]*AccountSnapshot, 64)
	for i := range accounts {
		accounts[i] = &AccountSnapshot{
			ID:             int64(i + 1),
			TenantID:       7,
			ProtocolFamily: "openai_chat",
			Priority:       10 + (i % 4),
			LoadRate:       float64(i%8) / 100,
			LastUsedAt:     now.Add(-time.Duration(i) * time.Second),
			MaxConcurrency: 32,
		}
	}
	policy := &RoutingPolicy{
		TopKDefault:        16,
		BroadTopK:          true,
		FallbackTimeoutMS:  25,
		FallbackMaxWaiting: 8,
	}
	selector := NewDefaultSelector(
		&stubAccountSource{accounts: accounts},
		WithRoutingPolicySource(&stubPolicy{p: policy}),
		WithGateChain(benchmarkAllowAllGateChain()),
		WithSlotManager(benchmarkSlotManager{}),
	)
	req := SelectionRequest{
		TenantID:       7,
		UserID:         3,
		APIKeyID:       11,
		PoolGroupID:    42,
		RequestedModel: "gpt-4o",
		ProtocolFamily: "openai_chat",
		EndpointFamily: "chat",
		SessionHash:    "bench-session",
	}
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := selector.Select(ctx, req)
		if err != nil {
			b.Fatalf("Select: %v", err)
		}
		benchmarkSelectionResult = got
	}
}

type benchmarkSlotManager struct{}

func (benchmarkSlotManager) Acquire(context.Context, *AccountSnapshot, SelectionRequest) (*AcquireResult, error) {
	return &AcquireResult{AcquisitionToken: uuid.New()}, nil
}

func benchmarkAllowAllGateChain() GateChain {
	g := AllowAllGate{}
	return GateChain{
		Tenant:      g,
		Lifecycle:   g,
		Channel:     g,
		Protocol:    g,
		Model:       g,
		Capability:  g,
		Credential:  g,
		Health:      g,
		GroupPolicy: g,
		Exclusion:   g,
	}
}
