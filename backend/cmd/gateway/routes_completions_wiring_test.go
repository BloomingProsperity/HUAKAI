package main

import (
	"context"
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
		{name: "audio", feedback: audioHandlerDeps(d).Feedback, budget: audioHandlerDeps(d).RetryBudget},
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

func TestNonChatHandlerDepsInjectSameAccountTransientRetries(t *testing.T) {
	d := &deps{cfg: &Config{}}
	fns := []struct {
		name string
		fn   func(context.Context) int
	}{
		{name: "completions", fn: completionsHandlerDeps(d).SameAccountTransientRetries},
		{name: "embeddings", fn: embeddingsHandlerDeps(d).SameAccountTransientRetries},
		{name: "rerank", fn: rerankHandlerDeps(d).SameAccountTransientRetries},
		{name: "images", fn: imageHandlerDeps(d).SameAccountTransientRetries},
		{name: "audio", fn: audioHandlerDeps(d).SameAccountTransientRetries},
	}
	for _, tc := range fns {
		t.Run(tc.name, func(t *testing.T) {
			if tc.fn == nil {
				t.Fatal("未注入同号瞬时重试读取函数")
			}
			if got := tc.fn(context.Background()); got != 0 {
				t.Fatalf("平台设置未接时应默认 0, got=%d", got)
			}
		})
	}
}
