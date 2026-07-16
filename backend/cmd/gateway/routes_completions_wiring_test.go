package main

import (
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/retrybudget"
	"github.com/BloomingProsperity/HUAKAI/internal/upstreamfeedback"
)

func TestNonChatHandlerDepsInjectSharedFeedbackAndRetryBudget(t *testing.T) {
	feedback := upstreamfeedback.NewObserver(upstreamfeedback.Dependencies{})
	budget := retrybudget.New(10, time.Minute)
	d := &deps{
		cfg:              &Config{},
		upstreamFeedback: feedback,
		retryBudget:      budget,
	}

	cases := []struct {
		name     string
		feedback *upstreamfeedback.Observer
		budget   any
	}{
		{name: "completions", feedback: completionsHandlerDeps(d).Feedback, budget: completionsHandlerDeps(d).RetryBudget},
		{name: "embeddings", feedback: embeddingsHandlerDeps(d).Feedback, budget: embeddingsHandlerDeps(d).RetryBudget},
		{name: "rerank", feedback: rerankHandlerDeps(d).Feedback, budget: rerankHandlerDeps(d).RetryBudget},
		{name: "images", feedback: imageHandlerDeps(d).Feedback, budget: imageHandlerDeps(d).RetryBudget},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.feedback != feedback {
				t.Fatal("handler 未收到生产共享上游反馈器")
			}
			if tc.budget != budget {
				t.Fatal("handler 未收到生产租户重试预算")
			}
		})
	}
}
