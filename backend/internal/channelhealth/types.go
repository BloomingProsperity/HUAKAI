// 包 channelhealth 实现 F-CH-002 渠道健康自动停用。
//
// 该包刻意保持供应商中立。它只存储 tenant 范围内的凭据身份、安全的原因类别、滚动计数
// 以及状态机证据。原始上游响应文本和凭据材料不属于 API 暴露面。
package channelhealth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/authcooldown"
)

type HealthState string

const (
	StateActive       HealthState = "active"
	StateDegraded     HealthState = "degraded"
	StateCoolingDown  HealthState = "cooling_down"
	StateRamping      HealthState = "ramping"
	StateDisabled     HealthState = "disabled"
	StateManualPaused HealthState = "manual_paused"
)

type SignalClass string

const (
	SignalNone             SignalClass = "none"
	SignalSuccess          SignalClass = "success"
	SignalChannelError     SignalClass = "channel_error"
	SignalClientMalformed  SignalClass = "client_malformed"
	SignalLocalGateway5xx  SignalClass = "local_gateway_5xx"
	SignalUpstream5xx      SignalClass = "upstream_5xx"
	SignalTimeout          SignalClass = "timeout"
	SignalRateLimit        SignalClass = "rate_limit"
	SignalForbidden        SignalClass = "forbidden"
	SignalLatencyP99       SignalClass = "latency_p99"
	SignalAccountSuspended SignalClass = "account_suspended"
	// SignalAuthChallenge:auth 失败(401 / Grok 400-auth)。刻意独立于健康 FSM——applySignal 把它
	// 单独路由进 auth 降级车道(authcooldown),完全不改 rec.State/Score、不进 error-rate/ban-ramp 窗口。
	SignalAuthChallenge                   SignalClass = "auth_challenge"
	SignalTokenRevoked                    SignalClass = "token_revoked"
	SignalCredentialRevoked               SignalClass = "credential_revoked"
	SignalAccountDisabled                 SignalClass = "account_disabled"
	SignalSubscriptionOrWorkspaceDisabled SignalClass = "subscription_or_workspace_disabled"
	SignalPolicyAutoDisabled              SignalClass = "policy_auto_disabled"
	SignalManualOverride                  SignalClass = "manual_override"
)

type ConfidenceTier string

const (
	ConfidenceObserved         ConfidenceTier = "observed"
	ConfidenceInferred         ConfidenceTier = "inferred"
	ConfidenceOperatorOverride ConfidenceTier = "operator_override"
)

type AuditEventType string

const (
	EventDegraded       AuditEventType = "channel_health_degraded"
	EventDisabled       AuditEventType = "channel_disabled"
	EventRecovered      AuditEventType = "channel_recovered"
	EventRampStarted    AuditEventType = "channel_ramp_started"
	EventRampRolledBack AuditEventType = "channel_ramp_rolled_back"
	EventManualOverride AuditEventType = "channel_manual_override"
)

type AlertType string

const (
	AlertBanSignal            AlertType = "ban_signal"
	AlertRepeatedRampRollback AlertType = "repeated_ramp_rollback"
	AlertManualForceActive    AlertType = "manual_force_active"
	AlertNoHealthyAlternate   AlertType = "no_healthy_alternate"
)

type ChannelKey struct {
	TenantID            int64  `json:"tenant_id"`
	ChannelID           string `json:"channel_id"`
	Vendor              string `json:"vendor"`
	ProviderAccountID   int64  `json:"provider_account_id,omitempty"`
	AccountCredentialID int64  `json:"account_credential_id"`
	CredentialVersion   int    `json:"credential_version"`
}

func (k ChannelKey) Validate() error {
	if k.TenantID <= 0 {
		return errors.New("tenant_id must be positive")
	}
	if k.Vendor == "" {
		return errors.New("vendor is required")
	}
	if k.AccountCredentialID <= 0 {
		return errors.New("account_credential_id must be positive")
	}
	if k.CredentialVersion <= 0 {
		return errors.New("credential_version must be positive")
	}
	return nil
}

func (k ChannelKey) StableChannelID() string {
	if k.ChannelID != "" {
		return k.ChannelID
	}
	return fmt.Sprintf("%s:%d:v%d", k.Vendor, k.AccountCredentialID, k.CredentialVersion)
}

func (k ChannelKey) mapKey() string {
	return fmt.Sprintf("%d/%s/%d/%d", k.TenantID, k.Vendor, k.AccountCredentialID, k.CredentialVersion)
}

type Policy struct {
	Version                            string
	MinSampleCount                     int
	MinObservation                     time.Duration
	ErrorRateThresholdPct              float64
	ErrorRateWindow                    time.Duration
	ErrorRateCooldown                  time.Duration
	LatencyP99ThresholdMS              int64
	LatencyWindow                      time.Duration
	LatencyCooldown                    time.Duration
	RateLimitHitRateThresholdPct       float64
	RateLimitWindow                    time.Duration
	DefaultRateLimitCooldown           time.Duration
	Upstream5xxRateThresholdPct        float64
	Upstream5xxWindow                  time.Duration
	Upstream5xxCooldown                time.Duration
	BanSignalMinCooldown               time.Duration
	BanSignalMaxCooldown               time.Duration
	RampStageMinDuration               time.Duration
	RampStageMaxDuration               time.Duration
	RampStageMinSamples                int
	RampErrorThresholdPct              float64
	RampBackoffFactor                  float64
	ManualOverrideRequiresReason       bool
	RepeatedRampRollbackAlertThreshold int
	AutomaticPostBanRamp               bool
}

func DefaultPolicy() Policy {
	return Policy{
		Version:                            "channel-health-v1",
		MinSampleCount:                     10,
		MinObservation:                     time.Minute,
		ErrorRateThresholdPct:              50,
		ErrorRateWindow:                    5 * time.Minute,
		ErrorRateCooldown:                  5 * time.Minute,
		LatencyP99ThresholdMS:              30000,
		LatencyWindow:                      5 * time.Minute,
		LatencyCooldown:                    5 * time.Minute,
		RateLimitHitRateThresholdPct:       40,
		RateLimitWindow:                    5 * time.Minute,
		DefaultRateLimitCooldown:           5 * time.Minute,
		Upstream5xxRateThresholdPct:        50,
		Upstream5xxWindow:                  5 * time.Minute,
		Upstream5xxCooldown:                5 * time.Minute,
		BanSignalMinCooldown:               24 * time.Hour,
		BanSignalMaxCooldown:               72 * time.Hour,
		RampStageMinDuration:               time.Minute,
		RampStageMaxDuration:               10 * time.Minute,
		RampStageMinSamples:                3,
		RampErrorThresholdPct:              10,
		RampBackoffFactor:                  2,
		ManualOverrideRequiresReason:       true,
		RepeatedRampRollbackAlertThreshold: 2,
	}
}

func (p Policy) normalized() Policy {
	def := DefaultPolicy()
	if p.Version == "" {
		p.Version = def.Version
	}
	if p.MinSampleCount <= 0 {
		p.MinSampleCount = def.MinSampleCount
	}
	if p.MinObservation <= 0 {
		p.MinObservation = def.MinObservation
	}
	if p.ErrorRateThresholdPct <= 0 {
		p.ErrorRateThresholdPct = def.ErrorRateThresholdPct
	}
	if p.ErrorRateWindow <= 0 {
		p.ErrorRateWindow = def.ErrorRateWindow
	}
	if p.ErrorRateCooldown <= 0 {
		p.ErrorRateCooldown = def.ErrorRateCooldown
	}
	if p.LatencyP99ThresholdMS <= 0 {
		p.LatencyP99ThresholdMS = def.LatencyP99ThresholdMS
	}
	if p.LatencyWindow <= 0 {
		p.LatencyWindow = def.LatencyWindow
	}
	if p.LatencyCooldown <= 0 {
		p.LatencyCooldown = def.LatencyCooldown
	}
	if p.RateLimitHitRateThresholdPct <= 0 {
		p.RateLimitHitRateThresholdPct = def.RateLimitHitRateThresholdPct
	}
	if p.RateLimitWindow <= 0 {
		p.RateLimitWindow = def.RateLimitWindow
	}
	if p.DefaultRateLimitCooldown <= 0 {
		p.DefaultRateLimitCooldown = def.DefaultRateLimitCooldown
	}
	if p.Upstream5xxRateThresholdPct <= 0 {
		p.Upstream5xxRateThresholdPct = def.Upstream5xxRateThresholdPct
	}
	if p.Upstream5xxWindow <= 0 {
		p.Upstream5xxWindow = def.Upstream5xxWindow
	}
	if p.Upstream5xxCooldown <= 0 {
		p.Upstream5xxCooldown = def.Upstream5xxCooldown
	}
	if p.BanSignalMinCooldown <= 0 {
		p.BanSignalMinCooldown = def.BanSignalMinCooldown
	}
	if p.BanSignalMaxCooldown <= 0 || p.BanSignalMaxCooldown < p.BanSignalMinCooldown {
		p.BanSignalMaxCooldown = def.BanSignalMaxCooldown
	}
	if p.RampStageMinDuration <= 0 {
		p.RampStageMinDuration = def.RampStageMinDuration
	}
	if p.RampStageMaxDuration <= p.RampStageMinDuration {
		p.RampStageMaxDuration = def.RampStageMaxDuration
	}
	if p.RampStageMinSamples <= 0 {
		p.RampStageMinSamples = def.RampStageMinSamples
	}
	if p.RampErrorThresholdPct <= 0 {
		p.RampErrorThresholdPct = def.RampErrorThresholdPct
	}
	if p.RampBackoffFactor <= 1 {
		p.RampBackoffFactor = def.RampBackoffFactor
	}
	if p.RepeatedRampRollbackAlertThreshold <= 0 {
		p.RepeatedRampRollbackAlertThreshold = def.RepeatedRampRollbackAlertThreshold
	}
	return p
}

type Signal struct {
	Key              ChannelKey
	Class            SignalClass
	StatusCode       int
	LatencyMS        int64
	At               time.Time
	RequestID        string
	RateLimitResetAt *time.Time
	// RawUpstreamText 仅用于分类测试而接收。Service 逻辑刻意从不存储或回显该值。
	RawUpstreamText string
	// AuthFailureClass 仅当 Class==SignalAuthChallenge 时有意义:区分 iron-clad/ambiguous,
	// 决定 auth 车道是否允许升级 HardDisabled。零值=ambiguous(安全默认:永不永久禁)。
	AuthFailureClass authcooldown.FailureClass
}

type SignalSample struct {
	At         time.Time   `json:"at"`
	Class      SignalClass `json:"class"`
	StatusCode int         `json:"status_code,omitempty"`
	LatencyMS  int64       `json:"latency_ms,omitempty"`
}

type WindowSummary struct {
	Samples             []SignalSample `json:"samples,omitempty"`
	TotalAttempts       int            `json:"total_attempts"`
	FailedAttempts      int            `json:"failed_attempts"`
	RateLimitHits       int            `json:"rate_limit_hits"`
	Upstream5xxHits     int            `json:"upstream_5xx_hits"`
	LocalGateway5xxHits int            `json:"local_gateway_5xx_hits"`
	BanSignals          int            `json:"ban_signals"`
	LatencyP99MS        int64          `json:"latency_p99_ms"`
	WindowStartedAt     time.Time      `json:"window_started_at,omitempty"`
	WindowEndedAt       time.Time      `json:"window_ended_at,omitempty"`
}

type Record struct {
	Key                   ChannelKey
	State                 HealthState
	Score                 float64
	ReasonClass           SignalClass
	Confidence            ConfidenceTier
	CooldownUntil         *time.Time
	RampStagePct          int
	RampStartedAt         *time.Time
	StateEnteredAt        time.Time
	LastTransitionAt      time.Time
	PolicyVersion         string
	SampleWindow          WindowSummary
	LastSignalClass       SignalClass
	LastSignalAt          *time.Time
	ManualPauseReason     string
	ManualOverrideActorID string
	ManualOverrideReason  string
	RampFailureCount      int
	RecoveryBlockedReason string
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type ChannelHealthState = Record

type ChannelHealthSummary struct {
	ByState          map[HealthState]int64
	Total            int64
	OldestCooldownAt *time.Time
}

type AuditEvent struct {
	Type          AuditEventType
	Key           ChannelKey
	PreviousState HealthState
	NewState      HealthState
	ReasonClass   SignalClass
	PolicyVersion string
	RequestID     string
	ActorID       string
	Payload       map[string]any
	OccurredAt    time.Time
}

type Alert struct {
	Type        AlertType
	Key         ChannelKey
	Severity    string
	ReasonClass SignalClass
	Payload     map[string]any
	CreatedAt   time.Time
}

type ForceCooldownController interface {
	ForceCooldown(context.Context, ChannelKey, time.Time, string) (Record, error)
}

type Store interface {
	Get(context.Context, ChannelKey) (Record, error)
	ListChannelHealth(context.Context, int64, int, int) ([]ChannelHealthState, error)
	ListChannelHealthByProviderAccount(context.Context, int64, int64, int, int) ([]ChannelHealthState, error)
	GetChannelHealth(context.Context, int64, string) (ChannelHealthState, []AuditEvent, error)
	SummarizeChannelHealth(context.Context, int64) (ChannelHealthSummary, error)
	UpsertRecord(context.Context, Record) (Record, error)
	LatestByProviderAccount(context.Context, int64, int64) (Record, error)
	AppendAudit(context.Context, AuditEvent) error
	AppendAlert(context.Context, Alert) error
}

var ErrNotFound = errors.New("channelhealth: record not found")

type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

func newChannelHealthSummary() ChannelHealthSummary {
	return ChannelHealthSummary{ByState: map[HealthState]int64{
		StateActive:       0,
		StateDegraded:     0,
		StateCoolingDown:  0,
		StateRamping:      0,
		StateDisabled:     0,
		StateManualPaused: 0,
	}}
}

func normalizeChannelHealthSummary(summary ChannelHealthSummary) ChannelHealthSummary {
	normalized := newChannelHealthSummary()
	for state, count := range summary.ByState {
		normalized.ByState[state] = count
		if summary.Total == 0 {
			normalized.Total += count
		}
	}
	if summary.Total != 0 {
		normalized.Total = summary.Total
	}
	normalized.OldestCooldownAt = summary.OldestCooldownAt
	return normalized
}
