// Package accountintake 实现账号级凭据批量接入的预检与执行服务。
package accountintake

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionprofile"
)

var (
	ErrNotConfigured                      = errors.New("account intake service not configured")
	ErrInvalidInput                       = errors.New("account intake input invalid")
	ErrPlanHashMissing                    = errors.New("account intake plan hash required")
	ErrPlanChanged                        = errors.New("account intake plan changed")
	ErrExecutionStale                     = errors.New("account intake item became stale")
	ErrCodexLaneAbsent                    = errors.New("codex routing lane is not configured")
	ErrCodexLaneMany                      = errors.New("multiple codex routing lanes require explicit selection")
	ErrImportCredentialRefreshUnavailable = errors.New("account intake credential refresher unavailable")
	ErrImportCredentialRefreshFailed      = errors.New("account intake credential refresh failed")
)

const accountIntakeContentLimit = 2 << 20

type AgentTaskRegistrar interface {
	EnsureTask(context.Context, []byte) ([]byte, error)
}

type ProxyMaterial struct {
	Name         string
	Protocol     string
	Host         string
	Port         int32
	AuthUsername string
	AuthSecret   string
	SourceRef    string
}

type ProxyResolveInput struct {
	TenantID  int64
	Material  ProxyMaterial
	ActorID   string
	ActorRole string
	RequestID string
	Reason    string
}

type ProxyResolver interface {
	ResolveTx(context.Context, pgx.Tx, ProxyResolveInput) (int64, error)
}

type AccountDefaults struct {
	ProviderID               int64           `json:"provider_id"`
	ChannelID                int64           `json:"channel_id"`
	NamePrefix               string          `json:"name_prefix"`
	ExactName                string          `json:"exact_name,omitempty"`
	AccountType              string          `json:"account_type"`
	Enabled                  *bool           `json:"enabled,omitempty"`
	ExpiresAt                *time.Time      `json:"expires_at,omitempty"`
	CapConcurrency           *int32          `json:"cap_concurrency,omitempty"`
	CapQueueSticky           *int32          `json:"cap_queue_sticky,omitempty"`
	CapQueueFallback         *int32          `json:"cap_queue_fallback,omitempty"`
	Priority                 *int32          `json:"priority,omitempty"`
	StaticWeight             *int32          `json:"static_weight,omitempty"`
	UpstreamCostRatio        *float64        `json:"upstream_cost_ratio,omitempty"`
	ProbeModel               *string         `json:"probe_model,omitempty"`
	Tags                     []string        `json:"tags,omitempty"`
	Extra                    json.RawMessage `json:"extra,omitempty"`
	ModelAllowList           []string        `json:"model_allow_list,omitempty"`
	CapabilityFlags          []string        `json:"capability_flags,omitempty"`
	RPMLimit                 *int64          `json:"rpm_limit,omitempty"`
	TPMLimit                 *int64          `json:"tpm_limit,omitempty"`
	WindowCostLimitCents     *int64          `json:"window_cost_limit_cents,omitempty"`
	MaxSessions              *int32          `json:"max_sessions,omitempty"`
	DisableCooling           *bool           `json:"disable_cooling,omitempty"`
	RefreshLeadSeconds       *int32          `json:"refresh_lead_seconds,omitempty"`
	TLSFingerprintRotate     *bool           `json:"tls_fingerprint_rotate,omitempty"`
	CustomErrorCodesEnabled  *bool           `json:"custom_error_codes_enabled,omitempty"`
	CustomErrorCodes         []int32         `json:"custom_error_codes,omitempty"`
	PoolMode                 *bool           `json:"pool_mode,omitempty"`
	TempUnschedulableEnabled *bool           `json:"temp_unschedulable_enabled,omitempty"`
	TempUnschedulableRules   json.RawMessage `json:"temp_unschedulable_rules,omitempty"`
	Proxy                    *ProxyMaterial  `json:"-"`
}

type PlanInput struct {
	TenantID        int64
	SourceKind      intake.SourceKind
	DefaultVendor   string
	DefaultAuthMode string
	Content         string
	Account         AccountDefaults
	Now             time.Time
}

type PlanResult struct {
	PlanHash string      `json:"plan_hash"`
	Plan     intake.Plan `json:"plan"`
}

type ExecuteInput struct {
	PlanInput
	PlanHash                 string
	Confirmations            []string
	ReplaceExistingConfig    bool
	ExpectedAccountUpdatedAt *time.Time
	ActorID                  string
	ActorRole                string
	RequestID                string
	Reason                   string
}

type ExecutionStatus string

const (
	StatusCreated  ExecutionStatus = "created"
	StatusUpdated  ExecutionStatus = "updated"
	StatusSkipped  ExecutionStatus = "skipped"
	StatusConflict ExecutionStatus = "conflict"
	StatusFailed   ExecutionStatus = "failed"
)

type ExecutionItem struct {
	Index                    int                              `json:"index"`
	PlannedAction            intake.Action                    `json:"planned_action"`
	Status                   ExecutionStatus                  `json:"status"`
	Code                     string                           `json:"code"`
	Message                  string                           `json:"message"`
	ProviderAccountID        int64                            `json:"provider_account_id,omitempty"`
	AccountCredentialID      int64                            `json:"account_credential_id,omitempty"`
	CredentialVersion        int32                            `json:"credential_version,omitempty"`
	ChannelHealthInitialized bool                             `json:"channel_health_initialized"`
	Subscription             *subscriptionprofile.Observation `json:"subscription,omitempty"`
	SystemLabels             []string                         `json:"system_labels,omitempty"`
	Warnings                 []string                         `json:"warnings,omitempty"`
}

type ExecutionSummary struct {
	Created  int `json:"created"`
	Updated  int `json:"updated"`
	Skipped  int `json:"skipped"`
	Conflict int `json:"conflict"`
	Failed   int `json:"failed"`
}

type ExecutionResult struct {
	PlanHash string           `json:"plan_hash"`
	Items    []ExecutionItem  `json:"items"`
	Summary  ExecutionSummary `json:"summary"`
}
