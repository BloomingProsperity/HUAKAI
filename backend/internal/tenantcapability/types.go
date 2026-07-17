// Package tenantcapability 管理部署者授予单层租户的高敏账号能力。
package tenantcapability

import (
	"errors"
	"strings"
	"time"
)

type Capability string

const (
	ClaudeCookie        Capability = "account_intake.claude_cookie"
	ClaudeSetupToken    Capability = "account_intake.claude_setup_token"
	CodexAgentIdentity  Capability = "account_intake.codex_agent_identity"
	CRSAccountSync      Capability = "account_sync.crs"
	AccountBundle       Capability = "account_bundle.structure"
	AccountBundleSecret Capability = "account_bundle.secrets"
)

var (
	ErrInvalid = errors.New("tenant capability input invalid")
	ErrDenied  = errors.New("tenant capability not granted")
)

var known = map[Capability]struct{}{
	ClaudeCookie: {}, ClaudeSetupToken: {}, CodexAgentIdentity: {},
	CRSAccountSync: {}, AccountBundle: {}, AccountBundleSecret: {},
}

type Grant struct {
	TenantID       int64      `json:"tenant_id"`
	Capability     Capability `json:"capability"`
	Status         string     `json:"status"`
	Effective      bool       `json:"effective"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	Revision       int64      `json:"revision"`
	GrantedByActor *string    `json:"granted_by_actor,omitempty"`
	RevokedByActor *string    `json:"revoked_by_actor,omitempty"`
	Reason         string     `json:"reason"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type Mutation struct {
	TenantID   int64
	Capability Capability
	Enabled    bool
	ExpiresAt  *time.Time
	ActorID    string
	Reason     string
	Now        time.Time
}

func Parse(value string) (Capability, error) {
	capability := Capability(strings.TrimSpace(value))
	if _, ok := known[capability]; !ok {
		return "", ErrInvalid
	}
	return capability, nil
}

func All() []Capability {
	return []Capability{
		ClaudeCookie, ClaudeSetupToken, CodexAgentIdentity,
		CRSAccountSync, AccountBundle, AccountBundleSecret,
	}
}
