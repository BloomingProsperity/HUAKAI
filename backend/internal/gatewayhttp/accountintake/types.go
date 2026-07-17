// Package accountintake 实现账号级凭据批量接入的预检与执行服务。
package accountintake

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

var (
	ErrNotConfigured   = errors.New("account intake service not configured")
	ErrInvalidInput    = errors.New("account intake input invalid")
	ErrPlanHashMissing = errors.New("account intake plan hash required")
	ErrPlanChanged     = errors.New("account intake plan changed")
	ErrExecutionStale  = errors.New("account intake item became stale")
)

const accountIntakeContentLimit = 2 << 20

type ChannelHealthInitializer interface {
	EnsureDefaultActive(context.Context, channelhealth.ChannelKey) (channelhealth.Record, error)
}

type AccountDefaults struct {
	ProviderID      int64           `json:"provider_id"`
	ChannelID       int64           `json:"channel_id"`
	Name            string          `json:"name,omitempty"`
	NamePrefix      string          `json:"name_prefix"`
	AccountType     string          `json:"account_type"`
	Enabled         *bool           `json:"enabled,omitempty"`
	CapConcurrency  *int32          `json:"cap_concurrency,omitempty"`
	Priority        *int32          `json:"priority,omitempty"`
	StaticWeight    *int32          `json:"static_weight,omitempty"`
	ProbeModel      *string         `json:"probe_model,omitempty"`
	Tags            []string        `json:"tags,omitempty"`
	Extra           json.RawMessage `json:"extra,omitempty"`
	ModelAllowList  []string        `json:"model_allow_list,omitempty"`
	CapabilityFlags []string        `json:"capability_flags,omitempty"`
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

type CandidatePlanInput struct {
	TenantID         int64
	SourceKind       intake.SourceKind
	Candidate        credentialacq.CredentialCandidate
	SourceCommitment string
	Account          AccountDefaults
	Now              time.Time
}

type CandidateFinalizer func(context.Context, db.DBTX, ExecutionItem) error

type CandidateExecuteInput struct {
	CandidatePlanInput
	PlanHash      string
	Confirmations []string
	ActorID       string
	ActorRole     string
	RequestID     string
	Reason        string
	Finalize      CandidateFinalizer
}

type ExecuteInput struct {
	PlanInput
	PlanHash      string
	Confirmations []string
	ActorID       string
	ActorRole     string
	RequestID     string
	Reason        string
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
	Index                    int             `json:"index"`
	PlannedAction            intake.Action   `json:"planned_action"`
	Status                   ExecutionStatus `json:"status"`
	Code                     string          `json:"code"`
	Message                  string          `json:"message"`
	ProviderAccountID        int64           `json:"provider_account_id,omitempty"`
	AccountCredentialID      int64           `json:"account_credential_id,omitempty"`
	CredentialVersion        int32           `json:"credential_version,omitempty"`
	ChannelHealthInitialized bool            `json:"channel_health_initialized"`
	Warnings                 []string        `json:"warnings,omitempty"`
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
