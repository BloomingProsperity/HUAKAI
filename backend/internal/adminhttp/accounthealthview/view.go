package accounthealthview

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/authcooldown"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	poolrouter "github.com/BloomingProsperity/HUAKAI/internal/pool/router"
)

type Response struct {
	Verdict             string           `json:"verdict"`
	BlockingReasons     []string         `json:"blocking_reasons"`
	ConditionalReasons  []string         `json:"conditional_reasons"`
	EvaluatedAt         string           `json:"evaluated_at"`
	ProviderAccount     ProviderAxis     `json:"provider_account"`
	Credential          CredentialAxis   `json:"credential"`
	ChannelHealth       ChannelAxis      `json:"channel_health"`
	AuthCooldown        AuthCooldownAxis `json:"auth_cooldown"`
	ModelCooldowns      []ModelCooldown  `json:"model_cooldowns"`
	LegacyAccountTimers LegacyTimers     `json:"legacy_account_timers"`
	UnevaluatedGates    []string         `json:"unevaluated_gates"`
}

type ProviderAxis struct {
	Enabled           bool    `json:"enabled"`
	DisableCooling    bool    `json:"disable_cooling"`
	ChannelEnabled    bool    `json:"channel_enabled"`
	ProviderAvailable bool    `json:"provider_available"`
	HealthState       string  `json:"health_state"`
	HealthStateUntil  *string `json:"health_state_until,omitempty"`
	Eligible          bool    `json:"eligible"`
	Persistence       string  `json:"persistence"`
}

type CredentialAxis struct {
	State                       string `json:"state"`
	Eligible                    bool   `json:"eligible"`
	ServingCredentialCandidates int32  `json:"serving_credential_candidates"`
	Vendor                      string `json:"vendor,omitempty"`
	AuthMode                    string `json:"auth_mode,omitempty"`
	LegacyMirrorState           string `json:"legacy_mirror_state"`
	Persistence                 string `json:"persistence"`
}

type ChannelAxis struct {
	RecordFound              bool    `json:"record_found"`
	State                    string  `json:"state"`
	Eligibility              string  `json:"eligibility"`
	ReasonClass              string  `json:"reason_class,omitempty"`
	CooldownUntil            *string `json:"cooldown_until,omitempty"`
	RampStagePct             int     `json:"ramp_stage_pct,omitempty"`
	BypassedByDisableCooling bool    `json:"bypassed_by_disable_cooling"`
	Source                   string  `json:"source"`
	Persistence              string  `json:"persistence"`
}

type AuthCooldownAxis struct {
	Configured               bool    `json:"configured"`
	RecordFound              bool    `json:"record_found"`
	Eligibility              string  `json:"eligibility"`
	HardDisabled             bool    `json:"hard_disabled"`
	Strike                   int     `json:"strike"`
	AuthUntil                *string `json:"auth_until,omitempty"`
	CredentialVersion        int     `json:"credential_version,omitempty"`
	BypassedByDisableCooling bool    `json:"bypassed_by_disable_cooling"`
	Persistence              string  `json:"persistence"`
}

type ModelCooldown struct {
	ModelKey string `json:"model_key"`
	ResetAt  string `json:"reset_at"`
	Reason   string `json:"reason,omitempty"`
	Active   bool   `json:"active"`
}

type LegacyTimers struct {
	AffectsSelector        bool     `json:"affects_selector"`
	ActiveReasons          []string `json:"active_reasons"`
	RateLimitResetAt       *string  `json:"rate_limit_reset_at,omitempty"`
	OverloadUntil          *string  `json:"overload_until,omitempty"`
	TempUnschedulableUntil *string  `json:"temp_unschedulable_until,omitempty"`
}

func Build(
	row admindb.GetAdminProviderAccountHealthRow,
	channelRecord channelhealth.Record,
	channelRecordFound bool,
	authSnapshot authcooldown.Snapshot,
	authConfigured bool,
	now time.Time,
) (Response, error) {
	now = now.UTC()
	providerHealthEligible := poolrouter.ProviderAccountHealthEligible(
		row.HealthState, pgTime(row.HealthStateUntil), now,
	)
	providerEligible := row.Enabled && row.ChannelEnabled && row.ProviderAvailable && providerHealthEligible
	credential := buildCredentialAxis(row)
	channelAxis := buildChannelAxis(row.DisableCooling, channelRecord, channelRecordFound, now)
	authAxis := buildAuthAxis(row.DisableCooling, authSnapshot, authConfigured)

	modelCooldowns, err := modelCooldowns(row.ModelRateLimits, now)
	if err != nil {
		return Response{}, err
	}
	legacy := legacyTimers(row, now)

	blocking := make([]string, 0, 7)
	if !row.Enabled {
		blocking = append(blocking, "account_disabled")
	}
	if !row.ChannelEnabled {
		blocking = append(blocking, "channel_disabled")
	}
	if !row.ProviderAvailable {
		blocking = append(blocking, "provider_unavailable")
	}
	if row.Enabled && !providerHealthEligible {
		blocking = append(blocking, "provider_health")
	}
	if row.Enabled {
		switch credential.State {
		case "ambiguous":
			blocking = append(blocking, "credential_ambiguous")
		case "unavailable":
			blocking = append(blocking, "credential_unavailable")
		}
	}
	if channelAxis.Eligibility == "blocked" {
		blocking = append(blocking, "channel_health")
	}
	if authAxis.Eligibility == "blocked" {
		blocking = append(blocking, "auth_cooldown")
	}

	conditional := make([]string, 0, 2)
	if channelAxis.Eligibility == "request_dependent" {
		conditional = append(conditional, "channel_ramp_admission")
	}
	if hasActiveModelCooldown(modelCooldowns) {
		conditional = append(conditional, "model_cooldown")
	}
	verdict := "eligible"
	if len(blocking) > 0 {
		verdict = "blocked"
	} else if len(conditional) > 0 {
		verdict = "request_dependent"
	}

	return Response{
		Verdict:            verdict,
		BlockingReasons:    blocking,
		ConditionalReasons: conditional,
		EvaluatedAt:        now.Format(time.RFC3339),
		ProviderAccount: ProviderAxis{
			Enabled:           row.Enabled,
			DisableCooling:    row.DisableCooling,
			ChannelEnabled:    row.ChannelEnabled,
			ProviderAvailable: row.ProviderAvailable,
			HealthState:       row.HealthState,
			HealthStateUntil:  formatPGTime(row.HealthStateUntil),
			Eligible:          providerEligible,
			Persistence:       "postgres",
		},
		Credential:          credential,
		ChannelHealth:       channelAxis,
		AuthCooldown:        authAxis,
		ModelCooldowns:      modelCooldowns,
		LegacyAccountTimers: legacy,
		UnevaluatedGates: []string{
			"protocol_family",
			"model_allow_list",
			"capability_flags",
			"concurrency_and_queue",
			"group_policy",
			"window_cost",
			"session_count",
			"context_window",
			"rpm_tpm_precheck",
		},
	}, nil
}

func buildChannelAxis(disableCooling bool, rec channelhealth.Record, found bool, now time.Time) ChannelAxis {
	axis := ChannelAxis{
		State:       string(channelhealth.StateActive),
		Eligibility: "eligible",
		Source:      "no_record_default_active",
		Persistence: "postgres",
	}
	if !found {
		return axis
	}
	axis.RecordFound = true
	axis.State = string(rec.State)
	axis.ReasonClass = string(rec.ReasonClass)
	axis.CooldownUntil = formatOptionalTime(rec.CooldownUntil)
	axis.RampStagePct = rec.RampStagePct
	axis.Source = "latest_provider_account_record"
	switch rec.State {
	case channelhealth.StateRamping:
		switch {
		case disableCooling:
			axis.Eligibility = "eligible"
			axis.BypassedByDisableCooling = true
		case rec.RampStagePct <= 0:
			axis.Eligibility = "blocked"
		case rec.RampStagePct >= 100:
			axis.Eligibility = "eligible"
		default:
			axis.Eligibility = "request_dependent"
		}
	case channelhealth.StateCoolingDown:
		if disableCooling {
			axis.Eligibility = "eligible"
			axis.BypassedByDisableCooling = true
		} else {
			// 冷却到期只代表后台恢复协调器可以转态；在持租约写成 ramping
			// 之前，请求热路径仍会拒绝，因此诊断也必须继续显示阻断。
			axis.Eligibility = "blocked"
		}
	default:
		if !channelhealth.IsEligible(rec, "", now) {
			axis.Eligibility = "blocked"
		}
	}
	return axis
}

func buildCredentialAxis(row admindb.GetAdminProviderAccountHealthRow) CredentialAxis {
	vendor := credentialstore.Normalize(row.CredentialVendor)
	authMode := credentialstore.Normalize(row.CredentialAuthMode)
	state := credentialSelectionState(row.ServingCredentialCandidates, vendor, authMode)
	if state != "resolved" {
		vendor = ""
		authMode = ""
	}
	return CredentialAxis{
		State:                       state,
		Eligible:                    state == "resolved",
		ServingCredentialCandidates: row.ServingCredentialCandidates,
		Vendor:                      vendor,
		AuthMode:                    authMode,
		LegacyMirrorState:           row.CredentialState,
		Persistence:                 "postgres",
	}
}

func buildAuthAxis(disableCooling bool, snap authcooldown.Snapshot, configured bool) AuthCooldownAxis {
	axis := AuthCooldownAxis{
		Configured:  configured,
		RecordFound: snap.Found,
		Eligibility: "eligible",
		Persistence: "process_local",
	}
	if !configured {
		return axis
	}
	axis.HardDisabled = snap.HardDisabled
	axis.Strike = snap.Strike
	axis.AuthUntil = formatOptionalTime(snap.AuthUntil)
	axis.CredentialVersion = snap.CredentialVersion
	if !snap.Eligible && disableCooling && !snap.HardDisabled {
		axis.BypassedByDisableCooling = true
	} else if !snap.Eligible {
		axis.Eligibility = "blocked"
	}
	return axis
}

func modelCooldowns(raw []byte, now time.Time) ([]ModelCooldown, error) {
	if len(raw) == 0 {
		return []ModelCooldown{}, nil
	}
	var stored map[string]struct {
		RateLimitResetAt string `json:"rate_limit_reset_at"`
		Reason           string `json:"reason"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(stored))
	for key := range stored {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]ModelCooldown, 0, len(keys))
	for _, key := range keys {
		entry := stored[key]
		modelKey := strings.TrimSpace(key)
		resetAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(entry.RateLimitResetAt))
		if modelKey == "" || err != nil {
			continue
		}
		out = append(out, ModelCooldown{
			ModelKey: modelKey,
			ResetAt:  resetAt.UTC().Format(time.RFC3339),
			Reason:   strings.TrimSpace(entry.Reason),
			Active:   resetAt.After(now),
		})
	}
	return out, nil
}

func hasActiveModelCooldown(cooldowns []ModelCooldown) bool {
	for _, cooldown := range cooldowns {
		if cooldown.Active {
			return true
		}
	}
	return false
}

func legacyTimers(row admindb.GetAdminProviderAccountHealthRow, now time.Time) LegacyTimers {
	active := make([]string, 0, 3)
	for _, item := range []struct {
		name string
		at   pgtype.Timestamptz
	}{
		{name: "rate_limited", at: row.RateLimitResetAt},
		{name: "overloaded", at: row.OverloadUntil},
		{name: "temp_unschedulable", at: row.TempUnschedulableUntil},
	} {
		if item.at.Valid && item.at.Time.After(now) {
			active = append(active, item.name)
		}
	}
	return LegacyTimers{
		AffectsSelector:        false,
		ActiveReasons:          active,
		RateLimitResetAt:       formatPGTime(row.RateLimitResetAt),
		OverloadUntil:          formatPGTime(row.OverloadUntil),
		TempUnschedulableUntil: formatPGTime(row.TempUnschedulableUntil),
	}
}

func pgTime(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time.UTC()
}

func formatPGTime(ts pgtype.Timestamptz) *string {
	if !ts.Valid {
		return nil
	}
	value := ts.Time.UTC().Format(time.RFC3339)
	return &value
}

func formatOptionalTime(ts *time.Time) *string {
	if ts == nil {
		return nil
	}
	value := ts.UTC().Format(time.RFC3339)
	return &value
}
