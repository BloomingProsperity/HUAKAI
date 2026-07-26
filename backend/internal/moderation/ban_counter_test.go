package moderation

import (
	"context"
	"testing"
)

func TestBanCounter_BlockUsesAtomicStore(t *testing.T) {
	store := &banStoreStub{result: BanResult{
		EventID: 19, Count: 3, ThresholdReached: true, Disabled: true,
	}}
	counter := NewBanCounter(store)
	event := ModerationEvent{
		TenantID: 7, APIKeyID: 11, UserID: 13, RequestID: "req-1",
		Decision: DecisionBlockKeyword, InputExcerpt: "违规原文",
	}
	cfg := ModerationConfig{
		BanThreshold: 3, BanWindowSeconds: 300, AutoDisableKeyOnBan: true,
	}

	result, err := counter.RecordAndCheck(context.Background(), event, cfg)
	if err != nil {
		t.Fatalf("RecordAndCheck returned error: %v", err)
	}
	if result.EventID != 19 || result.Count != 3 || !result.ThresholdReached || !result.Disabled {
		t.Fatalf("result=%+v", result)
	}
	if store.calls != 1 || store.event != event ||
		store.cfg.BanThreshold != cfg.BanThreshold ||
		store.cfg.BanWindowSeconds != cfg.BanWindowSeconds ||
		store.cfg.AutoDisableKeyOnBan != cfg.AutoDisableKeyOnBan {
		t.Fatalf("atomic store call mismatch calls=%d event=%+v cfg=%+v", store.calls, store.event, store.cfg)
	}
}

func TestBanCounter_PassDecisionDoesNotTouchStore(t *testing.T) {
	store := &banStoreStub{}
	counter := NewBanCounter(store)

	result, err := counter.RecordAndCheck(context.Background(), ModerationEvent{
		TenantID: 7, APIKeyID: 11, Decision: DecisionPass,
	}, ModerationConfig{BanThreshold: 3, BanWindowSeconds: 300})
	if err != nil {
		t.Fatalf("RecordAndCheck returned error: %v", err)
	}
	if result != (BanResult{}) || store.calls != 0 {
		t.Fatalf("pass touched violation store: result=%+v calls=%d", result, store.calls)
	}
}

type banStoreStub struct {
	calls  int
	event  ModerationEvent
	cfg    ModerationConfig
	result BanResult
	err    error
}

func (s *banStoreStub) RecordModerationViolation(_ context.Context, event ModerationEvent, cfg ModerationConfig) (BanResult, error) {
	s.calls++
	s.event = event
	s.cfg = cfg
	return s.result, s.err
}
