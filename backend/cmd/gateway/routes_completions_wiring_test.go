package main

import (
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/retrybudget"
	"github.com/BloomingProsperity/HUAKAI/internal/upstreamfeedback"
)

func TestCompletionsHandlerDepsInjectSharedFeedbackAndRetryBudget(t *testing.T) {
	feedback := upstreamfeedback.NewObserver(upstreamfeedback.Dependencies{})
	budget := retrybudget.New(10, time.Minute)
	d := &deps{
		cfg:              &Config{},
		upstreamFeedback: feedback,
		retryBudget:      budget,
	}

	got := completionsHandlerDeps(d)

	if got.Feedback != feedback {
		t.Fatal("completions handler 未收到生产共享上游反馈器")
	}
	if got.RetryBudget != budget {
		t.Fatal("completions handler 未收到生产租户重试预算")
	}
}
