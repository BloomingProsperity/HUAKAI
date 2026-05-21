package router_test

import (
	"context"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

func TestDefaultRouter_RetryableEndClassesMatchGatewayConstants(t *testing.T) {
	t.Parallel()

	plan, err := router.NewDefaultRouter().Plan(context.Background(), router.PlanInput{
		Context: router.RequestContext{RequestID: "r-gateway-class-drift", TenantID: 7},
		Model: router.ResolvedModel{
			ProtocolFamily: "openai_chat",
			PoolCandidates: []int64{101, 102},
		},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	got := make(map[string]bool, len(plan.RetryableEndClasses))
	for _, endClass := range plan.RetryableEndClasses {
		got[endClass] = true
	}
	want := []gateway.StreamEndClass{
		gateway.UpstreamError5xx,
		gateway.UpstreamRateLimit,
		gateway.FirstTokenTimeout,
		gateway.InterEventTimeout,
	}
	if len(got) != len(want) {
		t.Fatalf("retryable class count=%d want %d; got %v", len(got), len(want), plan.RetryableEndClasses)
	}
	for _, endClass := range want {
		if !got[string(endClass)] {
			t.Fatalf("RetryableEndClasses missing gateway.%s value %q; got %v", streamEndConstName(endClass), endClass, plan.RetryableEndClasses)
		}
	}
}

func streamEndConstName(endClass gateway.StreamEndClass) string {
	switch endClass {
	case gateway.UpstreamError5xx:
		return "UpstreamError5xx"
	case gateway.UpstreamRateLimit:
		return "UpstreamRateLimit"
	case gateway.FirstTokenTimeout:
		return "FirstTokenTimeout"
	case gateway.InterEventTimeout:
		return "InterEventTimeout"
	default:
		return "unknown"
	}
}
