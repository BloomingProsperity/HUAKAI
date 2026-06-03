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
	}, ModerationConfig{BanThreshold: 3, BanWindowSeconds: 300})
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
	}, ModerationConfig{BanThreshold: 3, BanWindowSeconds: 300})
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

func TestBanCounter_PassDecisionDoesNotCount(t *testing.T) {
	store := &banStoreStub{count: 99}
	counter := NewBanCounter(store)

	res, err := counter.RecordAndCheck(context.Background(), ModerationEvent{
		TenantID: 7,
		APIKeyID: 11,
		Decision: DecisionPass,
	}, ModerationConfig{BanThreshold: 3, BanWindowSeconds: 300})
	if err != nil {
		t.Fatalf("RecordAndCheck returned error: %v", err)
	}
	if res.Disabled || store.countCalls != 0 || store.disableCalls != 0 {
		t.Fatalf("pass decision touched ban store: res=%+v store=%+v", res, store)
	}
}

type banStoreStub struct {
	count            int64
	countCalls       int
	disableCalls     int
	disabledTenantID int64
	disabledAPIKeyID int64
}

func (s *banStoreStub) CountBlocksInWindow(context.Context, int64, int64, int32) (int64, error) {
	s.countCalls++
	return s.count, nil
}

func (s *banStoreStub) DisableAPIKey(_ context.Context, tenantID int64, apiKeyID int64) error {
	s.disableCalls++
	s.disabledTenantID = tenantID
	s.disabledAPIKeyID = apiKeyID
	return nil
}
