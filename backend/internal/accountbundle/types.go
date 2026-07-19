// Package accountbundle 实现租户账号的加密迁移包、预检和受控导入。
package accountbundle

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
)

const (
	EnvelopeFormat  = "huakai-account-bundle"
	EnvelopeVersion = 1
	MaxAccounts     = 500
)

var (
	ErrInvalidInput         = errors.New("account bundle input invalid")
	ErrNotConfigured        = errors.New("account bundle service not configured")
	ErrPlanChanged          = errors.New("account bundle plan changed")
	ErrConfirmationRequired = errors.New("account bundle confirmation required")
	ErrIntegrity            = errors.New("account bundle integrity check failed")
	ErrPassword             = errors.New("account bundle password invalid")
	ErrConflict             = errors.New("account bundle has conflicts")
)

type Envelope struct {
	Format           string     `json:"format"`
	Version          int        `json:"version"`
	KDF              KDFSpec    `json:"kdf"`
	Cipher           CipherSpec `json:"cipher"`
	CiphertextSHA256 string     `json:"ciphertext_sha256"`
}

type KDFSpec struct {
	Name      string `json:"name"`
	Salt      []byte `json:"salt"`
	Time      uint32 `json:"time"`
	MemoryKiB uint32 `json:"memory_kib"`
	Threads   uint8  `json:"threads"`
}

type CipherSpec struct {
	Name       string `json:"name"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

type PublicConfig struct {
	Name                     string          `json:"name"`
	AccountType              string          `json:"account_type"`
	Enabled                  bool            `json:"enabled"`
	ExpiresAt                *time.Time      `json:"expires_at,omitempty"`
	CapConcurrency           int32           `json:"cap_concurrency"`
	CapQueueSticky           int32           `json:"cap_queue_sticky"`
	CapQueueFallback         int32           `json:"cap_queue_fallback"`
	Priority                 int32           `json:"priority"`
	StaticWeight             int32           `json:"static_weight"`
	UpstreamCostRatio        *float64        `json:"upstream_cost_ratio,omitempty"`
	ProbeModel               *string         `json:"probe_model,omitempty"`
	Tags                     []string        `json:"tags"`
	Extra                    json.RawMessage `json:"extra"`
	ModelAllowList           []string        `json:"model_allow_list"`
	CapabilityFlags          []string        `json:"capability_flags"`
	RPMLimit                 int64           `json:"rpm_limit"`
	TPMLimit                 int64           `json:"tpm_limit"`
	WindowCostLimitCents     int64           `json:"window_cost_limit_cents"`
	MaxSessions              int32           `json:"max_sessions"`
	DisableCooling           bool            `json:"disable_cooling"`
	RefreshLeadSeconds       *int32          `json:"refresh_lead_seconds,omitempty"`
	TLSFingerprintRotate     bool            `json:"tls_fingerprint_rotate"`
	CustomErrorCodesEnabled  bool            `json:"custom_error_codes_enabled"`
	CustomErrorCodes         []int32         `json:"custom_error_codes"`
	PoolMode                 bool            `json:"pool_mode"`
	TempUnschedulableEnabled bool            `json:"temp_unschedulable_enabled"`
	TempUnschedulableRules   json.RawMessage `json:"temp_unschedulable_rules"`
}

type PortableCredential struct {
	Vendor                 string          `json:"vendor"`
	AuthMode               string          `json:"auth_mode"`
	Payload                json.RawMessage `json:"payload"`
	ExternalAccountID      string          `json:"external_account_id,omitempty"`
	ExternalSubjectID      string          `json:"external_subject_id,omitempty"`
	ExternalAccountEmail   string          `json:"external_account_email,omitempty"`
	ExternalIdentitySource string          `json:"external_identity_source,omitempty"`
}

type PortableProxy struct {
	Ref          string `json:"ref"`
	Name         string `json:"name"`
	Protocol     string `json:"protocol"`
	Host         string `json:"host"`
	Port         int32  `json:"port"`
	AuthUsername string `json:"auth_username,omitempty"`
	AuthSecret   string `json:"auth_secret,omitempty"`
}

type PortableAccount struct {
	Ref              string             `json:"ref"`
	SourceProviderID int64              `json:"source_provider_id"`
	SourceChannelID  int64              `json:"source_channel_id"`
	Config           PublicConfig       `json:"config"`
	Credential       PortableCredential `json:"credential"`
	ProxyRef         string             `json:"proxy_ref,omitempty"`
}

type payloadContent struct {
	CreatedAt time.Time         `json:"created_at"`
	Accounts  []PortableAccount `json:"accounts"`
	Proxies   []PortableProxy   `json:"proxies"`
}

type payload struct {
	Format        string         `json:"format"`
	Version       int            `json:"version"`
	ContentSHA256 string         `json:"content_sha256"`
	Content       payloadContent `json:"content"`
}

type ExportPlanInput struct {
	TenantID   int64
	AccountIDs []int64
	ActorID    string
	ActorRole  string
	RequestID  string
	Reason     string
}

type ExportPlanItem struct {
	AccountID      int64  `json:"account_id"`
	Name           string `json:"name,omitempty"`
	Status         string `json:"status"`
	Code           string `json:"code"`
	Message        string `json:"message"`
	CredentialMode string `json:"credential_mode,omitempty"`
	IncludesProxy  bool   `json:"includes_proxy"`
}

type ExportPlan struct {
	ContractVersion      string           `json:"contract_version"`
	PlanHash             string           `json:"plan_hash"`
	Items                []ExportPlanItem `json:"items"`
	Ready                int              `json:"ready"`
	Conflict             int              `json:"conflict"`
	RequiredConfirmation string           `json:"required_confirmation"`
}

type ExportExecuteInput struct {
	ExportPlanInput
	PlanHash     string
	Password     string
	Confirmation string
}

type ExportResult struct {
	Envelope     Envelope  `json:"envelope"`
	AccountCount int       `json:"account_count"`
	ProxyCount   int       `json:"proxy_count"`
	ExportedAt   time.Time `json:"exported_at"`
}

type Destination struct {
	ProviderID int64 `json:"provider_id"`
	ChannelID  int64 `json:"channel_id"`
}

type ImportPlanInput struct {
	TenantID     int64
	Envelope     Envelope
	Password     string
	Destinations map[string]Destination
	ActorID      string
	ActorRole    string
	RequestID    string
	Reason       string
}

type ImportPlanItem struct {
	Index                    int          `json:"index"`
	AccountRef               string       `json:"account_ref"`
	Name                     string       `json:"name"`
	DestinationKey           string       `json:"destination_key"`
	Status                   string       `json:"status"`
	Code                     string       `json:"code"`
	Message                  string       `json:"message"`
	PlanHash                 string       `json:"plan_hash,omitempty"`
	Plan                     *intake.Plan `json:"plan,omitempty"`
	ExistingAccountUpdatedAt *time.Time   `json:"existing_account_updated_at,omitempty"`
	RequiredConfirmations    []string     `json:"required_confirmations,omitempty"`
}

type ImportPlan struct {
	ContractVersion string           `json:"contract_version"`
	BundleHash      string           `json:"bundle_hash"`
	Items           []ImportPlanItem `json:"items"`
	Ready           int              `json:"ready"`
	Skipped         int              `json:"skipped"`
	Conflict        int              `json:"conflict"`
	Failed          int              `json:"failed"`
}

type ImportExecuteEntry struct {
	AccountRef    string   `json:"account_ref"`
	PlanHash      string   `json:"plan_hash"`
	Confirmations []string `json:"confirmations,omitempty"`
}

type ImportExecuteInput struct {
	ImportPlanInput
	BundleHash string
	Entries    []ImportExecuteEntry
}

type ImportExecutionItem struct {
	AccountRef string                         `json:"account_ref"`
	Status     string                         `json:"status"`
	Code       string                         `json:"code"`
	Message    string                         `json:"message"`
	Result     *accountintake.ExecutionResult `json:"result,omitempty"`
}

type ImportExecutionResult struct {
	BundleHash string                `json:"bundle_hash"`
	Items      []ImportExecutionItem `json:"items"`
	Completed  int                   `json:"completed"`
	Skipped    int                   `json:"skipped"`
	Conflict   int                   `json:"conflict"`
	Failed     int                   `json:"failed"`
}
