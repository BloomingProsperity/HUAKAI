// Package accountsource 归一远程账号源和恢复包产生的账号候选。
package accountsource

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
)

const (
	StatusReady     = "ready"
	StatusExpired   = "expired"
	StatusCancelled = "cancelled"
	DefaultTTL      = 10 * time.Minute
	MaxItems        = 500
)

var (
	ErrInvalidInput    = errors.New("account source input invalid")
	ErrSessionNotFound = errors.New("account source session not found")
	ErrSessionExpired  = errors.New("account source session expired")
	ErrSessionClosed   = errors.New("account source session closed")
	ErrSessionChanged  = errors.New("account source session changed")
	ErrSessionSource   = errors.New("account source session source mismatch")
)

type AccountTemplate struct {
	Name            string          `json:"name"`
	SourceProvider  string          `json:"source_provider,omitempty"`
	AccountType     string          `json:"account_type"`
	Enabled         bool            `json:"enabled"`
	CapConcurrency  *int32          `json:"cap_concurrency,omitempty"`
	Priority        *int32          `json:"priority,omitempty"`
	StaticWeight    *int32          `json:"static_weight,omitempty"`
	ProbeModel      *string         `json:"probe_model,omitempty"`
	Tags            []string        `json:"tags,omitempty"`
	Extra           json.RawMessage `json:"extra,omitempty"`
	ModelAllowList  []string        `json:"model_allow_list,omitempty"`
	CapabilityFlags []string        `json:"capability_flags,omitempty"`
}

type Item struct {
	Template  AccountTemplate                   `json:"template"`
	Candidate credentialacq.CredentialCandidate `json:"candidate"`
}

type Session struct {
	ID               string            `json:"id"`
	TenantID         int64             `json:"tenant_id"`
	SourceKind       intake.SourceKind `json:"source_kind"`
	Status           string            `json:"status"`
	SourceCommitment string            `json:"-"`
	ItemCount        int               `json:"item_count"`
	RedactedContext  map[string]any    `json:"redacted_context"`
	ExpiresAt        time.Time         `json:"expires_at"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

type CreateInput struct {
	TenantID        int64
	SourceKind      intake.SourceKind
	Items           []Item
	RedactedContext map[string]any
	ActorID         string
	ActorRole       string
	RequestID       string
	ExpiresAt       time.Time
}

type Loaded struct {
	Session Session
	Items   []Item
}
