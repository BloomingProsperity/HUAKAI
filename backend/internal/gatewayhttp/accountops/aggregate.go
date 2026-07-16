// Package accountops 聚合账号管理、selector、凭据与渠道健康的只读运营状态。
package accountops

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

type Input struct {
	Now                    time.Time
	Account                admindb.AdminProviderAccountRow
	RoutingState           admindb.ProviderAccountOperationsState
	Credentials            []credentialstore.CredentialMetadata
	ChannelHealth          *channelhealth.Record
	ChannelHealthAvailable bool
}

type Response struct {
	AccountID   int64        `json:"account_id"`
	TenantID    int64        `json:"tenant_id"`
	Summary     Summary      `json:"summary"`
	Blockers    []Blocker    `json:"blockers"`
	Signals     []Signal     `json:"signals"`
	Actions     []Action     `json:"actions"`
	Warnings    []string     `json:"warnings"`
	Credentials []Credential `json:"credentials"`
}

type Summary struct {
	Status                  string     `json:"status"`
	Schedulable             bool       `json:"schedulable"`
	GlobalBlockerCount      int        `json:"global_blocker_count"`
	ModelBlockerCount       int        `json:"model_blocker_count"`
	NextAutomaticRecoveryAt *time.Time `json:"next_automatic_recovery_at,omitempty"`
}

type Blocker struct {
	Code             string     `json:"code"`
	Scope            string     `json:"scope"`
	Source           string     `json:"source"`
	State            string     `json:"state,omitempty"`
	Model            string     `json:"model,omitempty"`
	SelectorConsumed bool       `json:"selector_consumed"`
	RecoverAt        *time.Time `json:"recover_at,omitempty"`
}

type Signal struct {
	Code             string     `json:"code"`
	State            string     `json:"state"`
	Source           string     `json:"source"`
	SelectorConsumed bool       `json:"selector_consumed"`
	Until            *time.Time `json:"until,omitempty"`
	Reason           string     `json:"reason,omitempty"`
}

type Action struct {
	Code           string `json:"code"`
	Allowed        bool   `json:"allowed"`
	DisabledReason string `json:"disabled_reason,omitempty"`
	Method         string `json:"method,omitempty"`
	Endpoint       string `json:"endpoint,omitempty"`
	SendsUpstream  bool   `json:"sends_upstream"`
	AffectsTraffic bool   `json:"affects_traffic"`
}

type Credential struct {
	ID                 int64      `json:"id"`
	Vendor             string     `json:"vendor"`
	AuthMode           string     `json:"auth_mode"`
	State              string     `json:"state"`
	Version            int32      `json:"credential_version"`
	Refreshable        bool       `json:"refreshable"`
	AccessExpiresAt    *time.Time `json:"access_expires_at,omitempty"`
	RefreshBeforeAt    *time.Time `json:"refresh_before_at,omitempty"`
	LastRefreshAt      *time.Time `json:"last_refresh_at,omitempty"`
	LastRefreshOutcome *string    `json:"last_refresh_outcome,omitempty"`
	FailureClass       *string    `json:"failure_class,omitempty"`
	FailureCount       int32      `json:"failure_count"`
}

type modelRateLimitJSON struct {
	RateLimitResetAt string `json:"rate_limit_reset_at"`
	Reason           string `json:"reason"`
}

func Aggregate(in Input) Response {
	now := in.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	out := Response{
		AccountID:   in.Account.ID,
		TenantID:    in.Account.TenantID,
		Blockers:    []Blocker{},
		Signals:     []Signal{},
		Actions:     []Action{},
		Warnings:    []string{},
		Credentials: []Credential{},
	}

	addAccountBlockers(&out, in, now)
	modelBlocked := addModelRateLimits(&out, in.RoutingState.ModelRateLimits, now)
	refreshable := addCredentials(&out, in.Credentials)
	addChannelHealth(&out, in.Account.DisableCooling, in.ChannelHealth, in.ChannelHealthAvailable, now)
	addLegacySignals(&out, in.Account, in.RoutingState, now)
	addActions(&out, in.Account, in.ChannelHealth, refreshable, len(in.Credentials) > 0, modelBlocked)
	out.Warnings = append(out.Warnings, "auth_cooldown_process_local_visibility_unavailable")
	if len(in.Credentials) == 0 {
		out.Warnings = append(out.Warnings, "credential_inventory_empty_or_legacy")
	}
	finalizeSummary(&out)
	return out
}

func addAccountBlockers(out *Response, in Input, now time.Time) {
	if !in.Account.Enabled {
		out.Blockers = append(out.Blockers, Blocker{
			Code: "manual_disabled", Scope: "account", Source: "provider_accounts.enabled",
			State: "disabled", SelectorConsumed: true,
		})
	}
	if in.Account.ExpiresAt.Valid && !in.Account.ExpiresAt.Time.After(now) {
		out.Blockers = append(out.Blockers, Blocker{
			Code: "account_expired", Scope: "account", Source: "provider_accounts.expires_at",
			State: "expired", SelectorConsumed: true,
		})
	}
	health := strings.TrimSpace(in.Account.HealthState)
	if health != "" && health != "healthy" {
		if in.Account.DisableCooling && isSoftProviderHealthState(health) {
			out.Signals = append(out.Signals, Signal{
				Code: "provider_health_override", State: health, Source: "provider_accounts.disable_cooling",
				SelectorConsumed: true,
			})
		} else if in.RoutingState.HealthStateUntil.Valid && !in.RoutingState.HealthStateUntil.Time.After(now) {
			out.Warnings = append(out.Warnings, "provider_health_pending_lazy_normalization")
		} else {
			out.Blockers = append(out.Blockers, Blocker{
				Code: "provider_health", Scope: "account", Source: "provider_accounts.health_state",
				State: health, SelectorConsumed: true, RecoverAt: pgTimePtr(in.RoutingState.HealthStateUntil),
			})
		}
	}
	switch strings.TrimSpace(in.Account.CredentialState) {
	case "valid", credentialstore.StateRefreshingWithGrace:
	default:
		out.Blockers = append(out.Blockers, Blocker{
			Code: "credential_state", Scope: "account", Source: "provider_accounts.credential_state",
			State: in.Account.CredentialState, SelectorConsumed: true,
		})
	}
}

func isSoftProviderHealthState(state string) bool {
	switch strings.TrimSpace(state) {
	case "throttled", "cooldown":
		return true
	default:
		return false
	}
}

func addModelRateLimits(out *Response, raw []byte, now time.Time) bool {
	if len(raw) == 0 {
		return false
	}
	var payload map[string]modelRateLimitJSON
	if err := json.Unmarshal(raw, &payload); err != nil {
		out.Warnings = append(out.Warnings, "model_rate_limits_invalid_json")
		return false
	}
	keys := make([]string, 0, len(payload))
	for key := range payload {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	blocked := false
	for _, key := range keys {
		entry := payload[key]
		resetAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(entry.RateLimitResetAt))
		if err != nil || !resetAt.After(now) {
			continue
		}
		resetAt = resetAt.UTC()
		blocked = true
		out.Blockers = append(out.Blockers, Blocker{
			Code: "model_rate_limit", Scope: "model", Source: "provider_accounts.model_rate_limits",
			State: "cooldown", Model: strings.TrimSpace(key), SelectorConsumed: true, RecoverAt: &resetAt,
		})
	}
	return blocked
}

func addCredentials(out *Response, credentials []credentialstore.CredentialMetadata) bool {
	registry := credentialstore.DefaultHandlerRegistry()
	refreshable := false
	for _, meta := range credentials {
		canRefresh := false
		if handler, err := registry.MustLookup(meta.Vendor, meta.AuthMode); err == nil {
			canRefresh = handler.Refreshable()
		}
		refreshable = refreshable || canRefresh
		out.Credentials = append(out.Credentials, Credential{
			ID: meta.ID, Vendor: meta.Vendor, AuthMode: meta.AuthMode, State: meta.State,
			Version: meta.Version, Refreshable: canRefresh, AccessExpiresAt: utcTimePtr(meta.AccessExpiresAt),
			RefreshBeforeAt: utcTimePtr(meta.RefreshBeforeAt), LastRefreshAt: utcTimePtr(meta.LastRefreshAt),
			LastRefreshOutcome: meta.LastRefreshOutcome, FailureClass: meta.FailureClass, FailureCount: meta.FailureCount,
		})
	}
	return refreshable
}

func addChannelHealth(out *Response, disableCooling bool, record *channelhealth.Record, available bool, now time.Time) {
	if !available {
		out.Signals = append(out.Signals, Signal{
			Code: "channel_health", State: "visibility_unavailable", Source: "channel_health",
			SelectorConsumed: true,
		})
		out.Warnings = append(out.Warnings, "channel_health_visibility_unavailable")
		return
	}
	if record == nil {
		out.Signals = append(out.Signals, Signal{
			Code: "channel_health", State: "active_by_default", Source: "channel_health",
			SelectorConsumed: true,
		})
		return
	}
	out.Signals = append(out.Signals, Signal{
		Code: "channel_health", State: string(record.State), Source: "channel_health",
		SelectorConsumed: true, Until: utcTimePtr(record.CooldownUntil), Reason: string(record.ReasonClass),
	})
	switch record.State {
	case channelhealth.StateDisabled, channelhealth.StateManualPaused:
		out.Blockers = append(out.Blockers, Blocker{
			Code: "channel_health", Scope: "account", Source: "channel_health",
			State: string(record.State), SelectorConsumed: true,
		})
	case channelhealth.StateCoolingDown:
		if disableCooling || (record.CooldownUntil != nil && !record.CooldownUntil.After(now)) {
			return
		}
		out.Blockers = append(out.Blockers, Blocker{
			Code: "channel_health", Scope: "account", Source: "channel_health",
			State: string(record.State), SelectorConsumed: true, RecoverAt: utcTimePtr(record.CooldownUntil),
		})
	case channelhealth.StateRamping:
		if !disableCooling {
			out.Signals = append(out.Signals, Signal{
				Code: "channel_ramp_admission", State: "partial", Source: "channel_health.ramp_stage_pct",
				SelectorConsumed: true,
			})
		}
	}
}

func addLegacySignals(out *Response, account admindb.AdminProviderAccountRow, state admindb.ProviderAccountOperationsState, now time.Time) {
	appendLegacy := func(code, source string, until *time.Time, reason string) {
		if until == nil || !until.After(now) {
			return
		}
		out.Signals = append(out.Signals, Signal{
			Code: code, State: "active", Source: source, SelectorConsumed: false, Until: until, Reason: reason,
		})
		out.Warnings = append(out.Warnings, code+"_not_consumed_by_pool_selector")
	}
	appendLegacy("account_rate_limit", "provider_accounts.rate_limit_reset_at", pgTimePtr(account.RateLimitResetAt), stringValue(account.RateLimitReason))
	appendLegacy("account_overload", "provider_accounts.overload_until", pgTimePtr(account.OverloadUntil), "")
	appendLegacy("account_temp_unschedulable", "provider_accounts.temp_unschedulable_until", pgTimePtr(account.TempUnschedulableUntil), stringValue(state.TempUnschedulableReason))
}

func addActions(out *Response, account admindb.AdminProviderAccountRow, health *channelhealth.Record, refreshable, hasCredentials, modelBlocked bool) {
	refreshDisabledReason := "no_refreshable_credential"
	if refreshable {
		refreshDisabledReason = "refresh_trigger_not_wired"
	}
	out.Actions = append(out.Actions,
		Action{
			Code: "refresh_now", Allowed: false, DisabledReason: refreshDisabledReason,
			SendsUpstream: true,
		},
		Action{
			Code: "start_credential_acquisition", Allowed: true, Method: "POST",
			Endpoint: "./credential-acquisitions",
		},
		Action{
			Code: "test_account", Allowed: hasCredentials, DisabledReason: disabledReason(hasCredentials, "credential_inventory_empty"),
			Method: "POST", Endpoint: "./test", SendsUpstream: true,
		},
	)
	clearable := modelBlocked || account.RateLimitResetAt.Valid || account.OverloadUntil.Valid || account.TempUnschedulableUntil.Valid
	out.Actions = append(out.Actions, Action{
		Code: "clear_account_rate_limits", Allowed: clearable,
		DisabledReason: disabledReason(clearable, "no_clearable_account_rate_limit"),
		Method:         "POST", Endpoint: "./clear-rate-limit", AffectsTraffic: true,
	})
	out.Actions = append(out.Actions, Action{
		Code: "enable_account", Allowed: !account.Enabled, DisabledReason: disabledReason(!account.Enabled, "account_already_enabled"),
		Method: "PATCH", Endpoint: "./enabled", AffectsTraffic: true,
	})
	channelResumable := health != nil && (health.State == channelhealth.StateManualPaused || health.State == channelhealth.StateDisabled)
	out.Actions = append(out.Actions, Action{
		Code: "resume_channel_health", Allowed: channelResumable,
		DisabledReason: disabledReason(channelResumable, "channel_health_not_paused"),
		Method:         "POST", Endpoint: "./channel-health/resume", AffectsTraffic: true,
	})
}

func finalizeSummary(out *Response) {
	var global, model int
	var modelRecovery *time.Time
	var globalRecovery *time.Time
	globalAutomatic := true
	for _, blocker := range out.Blockers {
		if blocker.Scope == "model" {
			model++
			if blocker.RecoverAt != nil && (modelRecovery == nil || blocker.RecoverAt.Before(*modelRecovery)) {
				value := blocker.RecoverAt.UTC()
				modelRecovery = &value
			}
		} else {
			global++
			if blocker.RecoverAt == nil {
				globalAutomatic = false
			} else if globalRecovery == nil || blocker.RecoverAt.After(*globalRecovery) {
				value := blocker.RecoverAt.UTC()
				globalRecovery = &value
			}
		}
	}
	out.Summary.GlobalBlockerCount = global
	out.Summary.ModelBlockerCount = model
	switch {
	case global > 0 && globalAutomatic:
		out.Summary.NextAutomaticRecoveryAt = globalRecovery
	case global == 0:
		out.Summary.NextAutomaticRecoveryAt = modelRecovery
	}
	out.Summary.Schedulable = global == 0
	switch {
	case global > 0:
		out.Summary.Status = "blocked"
	case model > 0 || hasSignal(out.Signals, "channel_ramp_admission"):
		out.Summary.Status = "partially_schedulable"
	default:
		out.Summary.Status = "schedulable"
	}
}

func hasSignal(signals []Signal, code string) bool {
	for _, signal := range signals {
		if signal.Code == code {
			return true
		}
	}
	return false
}

func pgTimePtr(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	got := value.Time.UTC()
	return &got
}

func utcTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	out := value.UTC()
	return &out
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func disabledReason(allowed bool, reason string) string {
	if allowed {
		return ""
	}
	return reason
}
