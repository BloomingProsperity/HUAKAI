package moderation

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestScreener_ConfigErrorPassesWhenTenantNotKnownEnabled(t *testing.T) {
	keywords := &keywordStoreStub{err: errors.New("must not call")}
	beforeMetric := moderationFailureMetricValue("config_backend_error")
	s := NewScreener(ScreenerDeps{
		Config:   configStub{err: errors.New("config db gone")},
		Keywords: keywords,
		Hashes:   hashStoreStub{err: errors.New("must not call")},
		Audit:    &auditSpy{},
	})

	res, err := s.Screen(context.Background(), ScreenRequest{
		TenantID:    7,
		APIKeyID:    11,
		UserID:      13,
		PayloadHash: "hash-config-error",
		Body:        []byte("forbidden"),
	})
	if err != nil {
		t.Fatalf("config error for default-disabled tenant should pass, got %v", err)
	}
	if res.Decision != DecisionPass || res.ReasonCode != "moderation_disabled" {
		t.Fatalf("result=%+v want moderation_disabled pass", res)
	}
	if keywords.calls != 0 {
		t.Fatalf("keyword store was called while config load failed disabled: calls=%d", keywords.calls)
	}
	if got := moderationFailureMetricValue("config_backend_error") - beforeMetric; got != 1 {
		t.Fatalf("config backend metric delta=%d want 1; MUTATION:删除冷缓存 pass 的指标记录会变红", got)
	}
}

func TestScreener_ConfigCacheReusesWithinTTLAndRefreshesAfterExpiry(t *testing.T) {
	now := time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC)
	configs := &countingConfigStore{cfg: ModerationConfig{TenantID: 7, Enabled: false, FailClosed: true}}
	s := NewScreener(ScreenerDeps{
		Config:         configs,
		ConfigCacheTTL: 30 * time.Second,
		Now:            func() time.Time { return now },
	})

	res, err := s.Screen(context.Background(), ScreenRequest{TenantID: 7, RequestID: "req-cache-1"})
	if err != nil || res.ReasonCode != "moderation_disabled" {
		t.Fatalf("first screen result=%+v err=%v", res, err)
	}
	configs.err = errors.New("config db should not be read inside ttl")
	res, err = s.Screen(context.Background(), ScreenRequest{TenantID: 7, RequestID: "req-cache-2"})
	if err != nil || res.ReasonCode != "moderation_disabled" {
		t.Fatalf("cached screen result=%+v err=%v", res, err)
	}
	if configs.calls != 1 {
		t.Fatalf("config calls=%d want 1 inside TTL; MUTATION: removing cache makes this 2", configs.calls)
	}

	now = now.Add(31 * time.Second)
	configs.err = nil
	configs.cfg = ModerationConfig{TenantID: 7, Enabled: true, FailClosed: true}
	res, err = s.Screen(context.Background(), ScreenRequest{TenantID: 7, RequestID: "req-cache-3"})
	if err != nil || res.Decision != DecisionPass || res.ReasonCode != "clean" {
		t.Fatalf("refreshed screen result=%+v err=%v", res, err)
	}
	if configs.calls != 2 {
		t.Fatalf("config calls=%d want 2 after TTL expiry", configs.calls)
	}
}

func TestScreener_ConfigErrorUsesExpiredEnabledCache(t *testing.T) {
	now := time.Date(2026, 7, 5, 9, 0, 0, 0, time.UTC)
	configs := &countingConfigStore{cfg: ModerationConfig{
		TenantID: 7, Enabled: true, FailClosed: true, SampleRatePct: 100,
	}}
	s := NewScreener(ScreenerDeps{
		Config:         configs,
		ConfigCacheTTL: 5 * time.Second,
		Now:            func() time.Time { return now },
	})

	res, err := s.Screen(context.Background(), ScreenRequest{TenantID: 7, RequestID: "req-stale-1"})
	if err != nil || res.Decision != DecisionPass || res.ReasonCode != "clean" {
		t.Fatalf("first screen result=%+v err=%v", res, err)
	}

	now = now.Add(6 * time.Second)
	configs.err = errors.New("config db gone with secret-like text")
	res, err = s.Screen(context.Background(), ScreenRequest{TenantID: 7, RequestID: "req-stale-2"})
	if !errors.Is(err, ErrScreenerBackend) {
		t.Fatalf("err=%v want ErrScreenerBackend from stale enabled config; MUTATION:删除 stale 兜底会变成 pass/nil", err)
	}
	if res.Decision != DecisionBlockBackend || res.ReasonCode != "config_backend_error" {
		t.Fatalf("result=%+v want config backend block from stale enabled config", res)
	}
	if configs.calls != 2 {
		t.Fatalf("config calls=%d want retry after TTL expiry", configs.calls)
	}
}

type countingConfigStore struct {
	cfg   ModerationConfig
	err   error
	calls int
}

func (s *countingConfigStore) GetConfig(context.Context, int64) (ModerationConfig, error) {
	s.calls++
	if s.err != nil {
		return ModerationConfig{}, s.err
	}
	return s.cfg, nil
}
