package accountops

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

func TestAggregateSeparatesGlobalAndModelBlockers(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	modelReset := now.Add(15 * time.Minute)
	rateReset := now.Add(10 * time.Minute)
	out := Aggregate(Input{
		Now: now,
		Account: admindb.AdminProviderAccountRow{
			ID: 101, TenantID: 7, Enabled: true, HealthState: "healthy", CredentialState: "valid",
			RateLimitResetAt: pgtype.Timestamptz{Time: rateReset, Valid: true},
		},
		RoutingState: admindb.ProviderAccountOperationsState{
			ModelRateLimits: []byte(`{"gpt-x":{"rate_limit_reset_at":"2026-07-16T12:15:00Z","reason":"upstream_429"}}`),
		},
		Credentials: []credentialstore.CredentialMetadata{
			{ID: 9001, Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth, State: credentialstore.StateActive},
		},
		ChannelHealthAvailable: true,
	})
	if !out.Summary.Schedulable || out.Summary.Status != "partially_schedulable" {
		t.Fatalf("summary=%+v，模型级冷却不应把整个账号判为不可调度", out.Summary)
	}
	if out.Summary.GlobalBlockerCount != 0 || out.Summary.ModelBlockerCount != 1 {
		t.Fatalf("summary=%+v，期望 0 个全局阻断和 1 个模型阻断", out.Summary)
	}
	blocker := findBlocker(t, out.Blockers, "model_rate_limit")
	if blocker.Model != "gpt-x" || !blocker.SelectorConsumed || blocker.RecoverAt == nil || !blocker.RecoverAt.Equal(modelReset) {
		t.Fatalf("model blocker=%+v", blocker)
	}
	signal := findSignal(t, out.Signals, "account_rate_limit")
	if signal.SelectorConsumed || signal.Until == nil || !signal.Until.Equal(rateReset) {
		t.Fatalf("legacy signal=%+v，账号级旧字段必须明确标成 selector 未消费", signal)
	}
	if !contains(out.Warnings, "account_rate_limit_not_consumed_by_pool_selector") {
		t.Fatalf("warnings=%v，缺少 selector 未接线告警", out.Warnings)
	}
	refresh := findAction(t, out.Actions, "refresh_now")
	if refresh.Allowed || refresh.DisabledReason != "refresh_trigger_not_wired" || !refresh.SendsUpstream {
		t.Fatalf("refresh action=%+v，不能把尚未接线的刷新触发器报告为可执行", refresh)
	}
	testAction := findAction(t, out.Actions, "test_account")
	if !testAction.Allowed || testAction.Method != "POST" || testAction.Endpoint != "./test" {
		t.Fatalf("test action=%+v", testAction)
	}
	clearAction := findAction(t, out.Actions, "clear_account_rate_limits")
	if !clearAction.Allowed || clearAction.Endpoint != "./clear-rate-limit" {
		t.Fatalf("clear action=%+v", clearAction)
	}
}

func TestAggregateGlobalBlockersAndExpiredHealthNormalization(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	expiredHealth := now.Add(-time.Minute)
	out := Aggregate(Input{
		Now: now,
		Account: admindb.AdminProviderAccountRow{
			ID: 102, TenantID: 7, Enabled: false, HealthState: "cooldown", CredentialState: "refresh_failed",
		},
		RoutingState: admindb.ProviderAccountOperationsState{
			HealthStateUntil: pgtype.Timestamptz{Time: expiredHealth, Valid: true},
		},
		ChannelHealthAvailable: true,
	})
	if out.Summary.Schedulable || out.Summary.Status != "blocked" {
		t.Fatalf("summary=%+v", out.Summary)
	}
	if out.Summary.GlobalBlockerCount != 2 {
		t.Fatalf("blockers=%+v，期望 disabled + credential_state 两个全局阻断", out.Blockers)
	}
	if hasBlocker(out.Blockers, "provider_health") {
		t.Fatalf("blockers=%+v，已过期 provider health 应等待 selector 惰性归一而不是继续阻断", out.Blockers)
	}
	if !contains(out.Warnings, "provider_health_pending_lazy_normalization") {
		t.Fatalf("warnings=%v", out.Warnings)
	}
	clearAction := findAction(t, out.Actions, "clear_account_rate_limits")
	if clearAction.Allowed {
		t.Fatalf("clear action=%+v，clear-rate-limit 不能清除 provider health", clearAction)
	}
}

func TestAggregateChannelCoolingHonorsEscapeFlagButNotHardPause(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	until := now.Add(5 * time.Minute)
	base := admindb.AdminProviderAccountRow{
		ID: 103, TenantID: 7, Enabled: true, HealthState: "healthy", CredentialState: "valid",
		DisableCooling: true,
	}
	cooling := channelhealth.Record{State: channelhealth.StateCoolingDown, CooldownUntil: &until}
	out := Aggregate(Input{Now: now, Account: base, ChannelHealth: &cooling, ChannelHealthAvailable: true})
	if hasBlocker(out.Blockers, "channel_health") || !out.Summary.Schedulable {
		t.Fatalf("out=%+v，disable_cooling 应豁免渠道冷却", out)
	}

	paused := channelhealth.Record{State: channelhealth.StateManualPaused}
	out = Aggregate(Input{Now: now, Account: base, ChannelHealth: &paused, ChannelHealthAvailable: true})
	if !hasBlocker(out.Blockers, "channel_health") || out.Summary.Schedulable {
		t.Fatalf("out=%+v，手动暂停不得被 disable_cooling 绕过", out)
	}
	resume := findAction(t, out.Actions, "resume_channel_health")
	if !resume.Allowed || resume.Endpoint != "./channel-health/resume" {
		t.Fatalf("resume action=%+v", resume)
	}
}

func TestAggregateProviderHealthOverrideOnlyBypassesSoftCooling(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	base := admindb.AdminProviderAccountRow{
		ID: 103, TenantID: 7, Enabled: true, CredentialState: "valid", DisableCooling: true,
	}
	base.HealthState = "cooldown"
	out := Aggregate(Input{Now: now, Account: base, ChannelHealthAvailable: true})
	if hasBlocker(out.Blockers, "provider_health") || !out.Summary.Schedulable {
		t.Fatalf("out=%+v，disable_cooling 应豁免 provider 软冷却", out)
	}
	override := findSignal(t, out.Signals, "provider_health_override")
	if override.State != "cooldown" || !override.SelectorConsumed {
		t.Fatalf("override=%+v", override)
	}

	base.HealthState = "revoked"
	out = Aggregate(Input{Now: now, Account: base, ChannelHealthAvailable: true})
	if !hasBlocker(out.Blockers, "provider_health") || out.Summary.Schedulable {
		t.Fatalf("out=%+v，disable_cooling 不得豁免 revoked", out)
	}
	if hasSignal(out.Signals, "provider_health_override") {
		t.Fatalf("signals=%+v，revoked 不得显示为已豁免", out.Signals)
	}
}

func TestAggregateRecoveryTimeRequiresAllGlobalBlockersToRecover(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	until := now.Add(5 * time.Minute)
	out := Aggregate(Input{
		Now: now,
		Account: admindb.AdminProviderAccountRow{
			ID: 104, TenantID: 7, Enabled: false, HealthState: "healthy", CredentialState: "valid",
		},
		ChannelHealth:          &channelhealth.Record{State: channelhealth.StateCoolingDown, CooldownUntil: &until},
		ChannelHealthAvailable: true,
	})
	if out.Summary.NextAutomaticRecoveryAt != nil {
		t.Fatalf("summary=%+v，手动禁用没有自动恢复时间", out.Summary)
	}

	later := now.Add(20 * time.Minute)
	out = Aggregate(Input{
		Now: now,
		Account: admindb.AdminProviderAccountRow{
			ID: 105, TenantID: 7, Enabled: true, HealthState: "cooldown", CredentialState: "valid",
		},
		RoutingState: admindb.ProviderAccountOperationsState{
			HealthStateUntil: pgtype.Timestamptz{Time: later, Valid: true},
		},
		ChannelHealth:          &channelhealth.Record{State: channelhealth.StateCoolingDown, CooldownUntil: &until},
		ChannelHealthAvailable: true,
	})
	if out.Summary.NextAutomaticRecoveryAt == nil || !out.Summary.NextAutomaticRecoveryAt.Equal(later) {
		t.Fatalf("summary=%+v，多个全局时间阻断应取最晚恢复时间", out.Summary)
	}
}

func TestAggregateDoesNotClaimDefaultHealthWhenVisibilityUnavailable(t *testing.T) {
	out := Aggregate(Input{
		Now: time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC),
		Account: admindb.AdminProviderAccountRow{
			ID: 106, TenantID: 7, Enabled: true, HealthState: "healthy", CredentialState: "valid",
		},
	})
	signal := findSignal(t, out.Signals, "channel_health")
	if signal.State != "visibility_unavailable" || !contains(out.Warnings, "channel_health_visibility_unavailable") {
		t.Fatalf("signal=%+v warnings=%v", signal, out.Warnings)
	}
}

func findBlocker(t *testing.T, blockers []Blocker, code string) Blocker {
	t.Helper()
	for _, blocker := range blockers {
		if blocker.Code == code {
			return blocker
		}
	}
	t.Fatalf("未找到 blocker %q：%+v", code, blockers)
	return Blocker{}
}

func hasBlocker(blockers []Blocker, code string) bool {
	for _, blocker := range blockers {
		if blocker.Code == code {
			return true
		}
	}
	return false
}

func findSignal(t *testing.T, signals []Signal, code string) Signal {
	t.Helper()
	for _, signal := range signals {
		if signal.Code == code {
			return signal
		}
	}
	t.Fatalf("未找到 signal %q：%+v", code, signals)
	return Signal{}
}

func findAction(t *testing.T, actions []Action, code string) Action {
	t.Helper()
	for _, action := range actions {
		if action.Code == code {
			return action
		}
	}
	t.Fatalf("未找到 action %q：%+v", code, actions)
	return Action{}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
