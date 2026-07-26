package moderation

import (
	"context"
	"testing"
)

func TestBanCounter_ThresholdTriggeredDisablesKey(t *testing.T) {
	store := &banStoreStub{count: 3}
	counter := NewBanCounter(store)

	res, err := counter.RecordAndCheck(context.Background(), ModerationEvent{
		TenantID: 7,
		APIKeyID: 11,
		Decision: DecisionBlockKeyword,
	}, ModerationConfig{BanThreshold: 3, BanWindowSeconds: 300, AutoDisableKeyOnBan: true})
	if err != nil {
		t.Fatalf("RecordAndCheck returned error: %v", err)
	}
	if !res.Disabled {
		t.Fatalf("Disabled=false want true")
	}
	if store.disabledTenantID != 7 || store.disabledAPIKeyID != 11 {
		t.Fatalf("disabled key mismatch: tenant=%d api_key=%d", store.disabledTenantID, store.disabledAPIKeyID)
	}
}

func TestBanCounter_WindowExpiryDoesNotBan(t *testing.T) {
	store := &banStoreStub{count: 2}
	counter := NewBanCounter(store)

	res, err := counter.RecordAndCheck(context.Background(), ModerationEvent{
		TenantID: 7,
		APIKeyID: 11,
		Decision: DecisionBlockKeyword,
	}, ModerationConfig{BanThreshold: 3, BanWindowSeconds: 300, AutoDisableKeyOnBan: true})
	if err != nil {
		t.Fatalf("RecordAndCheck returned error: %v", err)
	}
	if res.Disabled {
		t.Fatalf("Disabled=true want false below threshold")
	}
	if store.disableCalls != 0 {
		t.Fatalf("disableCalls=%d want 0 below threshold", store.disableCalls)
	}
}

func TestBanCounter_SampleRateZeroStillCountsDedicatedViolationEvents(t *testing.T) {
	// 根因:auto-ban 以前复用受采样控制的 moderation_log 计数。sample_rate=0 时连续违规
	// 不会进入封禁窗口。Mutation:删掉 RecordModerationViolationEvent 并改回数 audit log 时，
	// recordCalls=0 且第三次也不会 Disabled。
	store := &banStoreStub{countFromRecorded: true}
	counter := NewBanCounter(store)
	cfg := ModerationConfig{SampleRatePct: 0, BanThreshold: 3, BanWindowSeconds: 300, AutoDisableKeyOnBan: true}

	var res BanResult
	for i := 0; i < 3; i++ {
		var err error
		res, err = counter.RecordAndCheck(context.Background(), ModerationEvent{
			TenantID: 7,
			APIKeyID: 11,
			Decision: DecisionBlockKeyword,
		}, cfg)
		if err != nil {
			t.Fatalf("RecordAndCheck attempt %d returned error: %v", i+1, err)
		}
	}
	if store.recordCalls != 3 {
		t.Fatalf("dedicated violation record calls=%d want 3", store.recordCalls)
	}
	if !res.Disabled {
		t.Fatalf("Disabled=false want true after three non-sampled violations")
	}
	if store.disableCalls != 1 {
		t.Fatalf("disableCalls=%d want 1 after threshold", store.disableCalls)
	}
}

func TestBanCounter_PassDecisionDoesNotCount(t *testing.T) {
	store := &banStoreStub{count: 99}
	counter := NewBanCounter(store)

	res, err := counter.RecordAndCheck(context.Background(), ModerationEvent{
		TenantID: 7,
		APIKeyID: 11,
		Decision: DecisionPass,
	}, ModerationConfig{BanThreshold: 3, BanWindowSeconds: 300, AutoDisableKeyOnBan: true})
	if err != nil {
		t.Fatalf("RecordAndCheck returned error: %v", err)
	}
	if res.Disabled || store.countCalls != 0 || store.disableCalls != 0 {
		t.Fatalf("pass decision touched ban store: res=%+v store=%+v", res, store)
	}
}

type banStoreStub struct {
	count             int64
	countFromRecorded bool
	recordCalls       int
	countCalls        int
	disableCalls      int
	disabledTenantID  int64
	disabledAPIKeyID  int64
	// lastEvent 保留最近一次落库的违规事件，供断言字段传递不丢失。
	lastEvent ModerationEvent
}

func (s *banStoreStub) RecordModerationViolationEvent(_ context.Context, event ModerationEvent) error {
	s.recordCalls++
	s.lastEvent = event
	return nil
}

func (s *banStoreStub) CountBlocksInWindow(context.Context, int64, int64, int32) (int64, error) {
	s.countCalls++
	if s.countFromRecorded {
		return int64(s.recordCalls), nil
	}
	return s.count, nil
}

func (s *banStoreStub) DisableAPIKey(_ context.Context, tenantID int64, apiKeyID int64) error {
	s.disableCalls++
	s.disabledTenantID = tenantID
	s.disabledAPIKeyID = apiKeyID
	return nil
}
