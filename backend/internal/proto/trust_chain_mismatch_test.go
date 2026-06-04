package proto

import (
	"strings"
	"testing"
)

func TestEmitModelMismatchLoss_NilReturnsNothing(t *testing.T) {
	if got := EmitModelMismatchLoss(nil); got != nil {
		t.Errorf("nil ModelChain must produce no loss, got %+v", got)
	}
}

func TestEmitModelMismatchLoss_AllConsistentNoLoss(t *testing.T) {
	mc := &ModelChain{
		Requested:        "claude-opus-4",
		RouteDecided:     "claude-opus-4",
		UpstreamReported: "claude-opus-4",
	}
	if got := EmitModelMismatchLoss(mc); len(got) != 0 {
		t.Errorf("consistent chain must produce no loss, got %+v", got)
	}
}

func TestEmitModelMismatchLoss_StreamingInFlightNoUpstreamNoLoss(t *testing.T) {
	mc := &ModelChain{
		Requested:    "claude-opus-4",
		RouteDecided: "claude-opus-4",
		// UpstreamReported 留空 = streaming inflight
	}
	if got := EmitModelMismatchLoss(mc); len(got) != 0 {
		t.Errorf("streaming-inflight (no upstream) must produce no loss, got %+v", got)
	}
}

func TestEmitModelMismatchLoss_RouteSubstitution(t *testing.T) {
	// 路由层偷换：请求 opus，路由决策 haiku
	mc := &ModelChain{
		Requested:    "claude-opus-4",
		RouteDecided: "claude-haiku-4",
	}
	losses := EmitModelMismatchLoss(mc)
	if len(losses) != 1 {
		t.Fatalf("expected 1 loss, got %d: %+v", len(losses), losses)
	}
	if losses[0].Code != string(MismatchRouteDecidedDiffersFromRequested) {
		t.Errorf("expected route-substitution Code, got %q", losses[0].Code)
	}
	if !strings.Contains(losses[0].Reason, "claude-opus-4") || !strings.Contains(losses[0].Reason, "claude-haiku-4") {
		t.Errorf("Reason should include both models, got %q", losses[0].Reason)
	}
	if losses[0].Severity != ProtocolLossWarning {
		t.Errorf("expected warning severity, got %q", losses[0].Severity)
	}
}

func TestEmitModelMismatchLoss_UpstreamDeviation(t *testing.T) {
	// 路由 OK，上游 vendor 返回了不同模型（vendor 出错或主动 substitute）
	mc := &ModelChain{
		Requested:        "claude-opus-4",
		RouteDecided:     "claude-opus-4",
		UpstreamReported: "claude-sonnet-4",
	}
	losses := EmitModelMismatchLoss(mc)
	if len(losses) != 1 {
		t.Fatalf("expected 1 loss, got %d: %+v", len(losses), losses)
	}
	if losses[0].Code != string(MismatchUpstreamDiffersFromRequested) {
		t.Errorf("expected upstream-deviation Code, got %q", losses[0].Code)
	}
}

func TestEmitModelMismatchLoss_RouteAndUpstreamBothDiverge(t *testing.T) {
	// 双重不一致：路由偷换 + 上游又跟着报第三个
	mc := &ModelChain{
		Requested:        "claude-opus-4",
		RouteDecided:     "claude-haiku-4",
		UpstreamReported: "claude-sonnet-4",
	}
	losses := EmitModelMismatchLoss(mc)
	if len(losses) != 2 {
		t.Fatalf("expected 2 losses, got %d: %+v", len(losses), losses)
	}
	var sawRoute, sawUpstream bool
	for _, l := range losses {
		switch l.Code {
		case string(MismatchRouteDecidedDiffersFromRequested):
			sawRoute = true
		case string(MismatchUpstreamDiffersFromRequested):
			sawUpstream = true
		}
	}
	if !sawRoute || !sawUpstream {
		t.Errorf("expected both dimensions, sawRoute=%v sawUpstream=%v losses=%+v", sawRoute, sawUpstream, losses)
	}
}

func TestEmitModelMismatchLoss_RequestedEmpty(t *testing.T) {
	mc := &ModelChain{RouteDecided: "claude-opus-4"}
	losses := EmitModelMismatchLoss(mc)
	if len(losses) != 1 {
		t.Fatalf("expected 1 loss, got %d: %+v", len(losses), losses)
	}
	if losses[0].Code != string(MismatchRequestedEmpty) {
		t.Errorf("expected requested_empty Code, got %q", losses[0].Code)
	}
}

func TestEmitModelMismatchLoss_RouteEmpty(t *testing.T) {
	mc := &ModelChain{Requested: "claude-opus-4"}
	losses := EmitModelMismatchLoss(mc)
	if len(losses) != 1 {
		t.Fatalf("expected 1 loss, got %d: %+v", len(losses), losses)
	}
	if losses[0].Code != string(MismatchRouteEmpty) {
		t.Errorf("expected route_empty Code, got %q", losses[0].Code)
	}
}

func TestEmitModelMismatchLoss_AllLossesAreNonSilent(t *testing.T) {
	// 防御：T0 反 silent drop 守门 — 任何 loss 都必须 Severity + Reason 非空。
	mc := &ModelChain{
		Requested:        "x",
		RouteDecided:     "y",
		UpstreamReported: "z",
	}
	for _, l := range EmitModelMismatchLoss(mc) {
		if l.Severity == "" {
			t.Errorf("loss must have Severity (INV-7 反 silent drop): %+v", l)
		}
		if l.Reason == "" && l.Code == "" {
			t.Errorf("loss must have Reason or Code: %+v", l)
		}
	}
}
