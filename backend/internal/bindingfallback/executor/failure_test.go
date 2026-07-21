package executor

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
)

func TestUpstreamFailureFromDecisionAppliesOnlyClientProjection(t *testing.T) {
	classification, err := gateway.Classify(http.StatusServiceUnavailable, nil, nil, "openai")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	base, _, err := gateway.ClassifyAttemptHTTPError(http.StatusServiceUnavailable, nil, nil, "openai")
	if err != nil {
		t.Fatalf("ClassifyAttemptHTTPError: %v", err)
	}
	base.ClientStatus = 422
	base.ClientCode = "account_busy"
	base.ClientMessage = "账号暂不可用"
	base.ClientRuleID = "busy-503"

	failure := UpstreamFailureFromDecision(http.StatusServiceUnavailable, nil, base, classification)
	if failure.Status != 422 || failure.Code != "account_busy" || failure.Message != "账号暂不可用" {
		t.Fatalf("客户端投影未生效: %+v", failure)
	}
	if !failure.RetryPermitted || failure.AbortReason != "upstream_5xx" {
		t.Fatalf("客户端投影不得改写静态重试事实: %+v", failure)
	}
}

func TestUpstreamFailureFromDecisionPropagatesRetryAfter(t *testing.T) {
	failure := UpstreamFailureFromDecision(http.StatusTooManyRequests, nil, gateway.AttemptRetryDecision{
		ClientStatus:  http.StatusTooManyRequests,
		ClientCode:    "rate_limited",
		ClientMessage: "rate limited",
	}, gateway.Classification{RetryAfterMs: 1501})

	if failure.RetryAfterSeconds != 2 {
		t.Fatalf("RetryAfterSeconds=%d，期望向上取整为 2", failure.RetryAfterSeconds)
	}
	rec := httptest.NewRecorder()
	WriteHTTP(rec, failure)
	if got := rec.Header().Get("Retry-After"); got != "2" {
		t.Fatalf("Retry-After=%q，期望 2", got)
	}
}
