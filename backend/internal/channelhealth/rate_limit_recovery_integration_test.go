//go:build integration_pg

package channelhealth

import (
	"context"
	"testing"
	"time"
)

func TestClearRateLimitByProviderAccountPersistsAllowedAuditsAndSelectiveState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openChannelHealthSummaryPool(t, ctx)
	tenantID := seedChannelHealthSummaryTenant(t, ctx, pool, "rate-limit-recovery")
	cleanupChannelHealthSummaryTenants(t, pool, tenantID)
	store := NewPostgresStore(pool)
	seedPostgresChannelHealthRecord(t, ctx, pool, store, tenantID, "openai", StateActive, nil)

	var providerAccountID int64
	if err := pool.QueryRow(ctx, `
SELECT id
FROM provider_accounts
WHERE tenant_id = $1
ORDER BY id DESC
LIMIT 1`, tenantID).Scan(&providerAccountID); err != nil {
		t.Fatalf("read provider account: %v", err)
	}
	current, err := store.LatestByProviderAccount(ctx, tenantID, providerAccountID)
	if err != nil {
		t.Fatalf("LatestByProviderAccount: %v", err)
	}
	now := time.Date(2026, 7, 16, 21, 0, 0, 0, time.UTC)
	upstreamAt := now.Add(-2 * time.Minute)
	current.State = StateCoolingDown
	current.ReasonClass = SignalClass("rate_limit_rpm")
	current.CooldownUntil = timePtr(now.Add(time.Hour))
	current.SampleWindow = summarizeSamples([]SignalSample{
		{At: now.Add(-3 * time.Minute), Class: SignalRateLimit, StatusCode: 429},
		{At: upstreamAt, Class: SignalUpstream5xx, StatusCode: 503},
	})
	current.LastSignalClass = SignalRateLimit
	current.LastSignalAt = timePtr(now.Add(-3 * time.Minute))
	current.UpdatedAt = now.Add(-time.Minute)
	if _, err := store.UpsertRecord(ctx, current); err != nil {
		t.Fatalf("seed cooling record: %v", err)
	}

	got, changed, err := NewService(store, DefaultPolicy(), &fixedClock{now: now}).
		ClearRateLimitByProviderAccount(ctx, tenantID, providerAccountID, "operator:integration")
	if err != nil {
		t.Fatalf("ClearRateLimitByProviderAccount: %v", err)
	}
	if !changed || got.State != StateRamping || got.RampStagePct != 1 ||
		got.SampleWindow.RateLimitHits != 0 || got.SampleWindow.Upstream5xxHits != 1 {
		t.Fatalf("persisted recovery mismatch: changed=%v record=%+v", changed, got)
	}

	rows, err := pool.Query(ctx, `
SELECT event_type, actor_id
FROM channel_health_audit_events
WHERE tenant_id = $1 AND provider_account_id = $2
ORDER BY id`, tenantID, providerAccountID)
	if err != nil {
		t.Fatalf("query recovery audits: %v", err)
	}
	defer rows.Close()
	var eventTypes []string
	for rows.Next() {
		var (
			eventType string
			actorID   *string
		)
		if err := rows.Scan(&eventType, &actorID); err != nil {
			t.Fatalf("scan recovery audit: %v", err)
		}
		if actorID == nil || *actorID != "operator:integration" {
			t.Fatalf("audit actor=%v", actorID)
		}
		eventTypes = append(eventTypes, eventType)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate recovery audits: %v", err)
	}
	if len(eventTypes) != 2 ||
		eventTypes[0] != string(EventManualOverride) ||
		eventTypes[1] != string(EventRampStarted) {
		t.Fatalf("recovery event types=%v", eventTypes)
	}
}
