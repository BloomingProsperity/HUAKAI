// Package accountbundle 实现租户账号结构包与短时加密恢复包。
package accountbundle

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/accountsource"
)

const (
	ManifestVersion = "huakai-account-bundle/v1"
	EnvelopeVersion = "huakai-account-recovery/v1"
	ModeStructure   = "structure"
	ModeRecovery    = "recovery"
	MaxBundleBytes  = 8 << 20
	MaxRecoveryTTL  = 15 * time.Minute
)

var (
	ErrInvalidBundle      = errors.New("account bundle invalid")
	ErrPassphraseWeak     = errors.New("account bundle passphrase too weak")
	ErrBundleExpired      = errors.New("account recovery bundle expired")
	ErrSignatureMismatch  = errors.New("account recovery bundle signature mismatch")
	ErrRecoveryIncomplete = errors.New("account recovery bundle requires every account to have an active credential")
)

type Manifest struct {
	Version   string    `json:"version"`
	BundleID  string    `json:"bundle_id"`
	Mode      string    `json:"mode"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	Accounts  []Account `json:"accounts"`
}

type Account struct {
	SourceAccountID      int64                         `json:"source_account_id"`
	Template             accountsource.AccountTemplate `json:"template"`
	Vendor               string                        `json:"vendor,omitempty"`
	AuthMode             string                        `json:"auth_mode,omitempty"`
	Credential           json.RawMessage               `json:"credential,omitempty"`
	ExternalAccountID    string                        `json:"external_account_id,omitempty"`
	ExternalSubjectID    string                        `json:"external_subject_id,omitempty"`
	ExternalAccountEmail string                        `json:"external_account_email,omitempty"`
	IdentitySource       string                        `json:"identity_source,omitempty"`
}

type Envelope struct {
	Version    string    `json:"version"`
	BundleID   string    `json:"bundle_id"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	KDF        string    `json:"kdf"`
	Salt       string    `json:"salt"`
	Nonce      string    `json:"nonce"`
	Ciphertext string    `json:"ciphertext"`
	Signature  string    `json:"signature"`
}

type ExportResult struct {
	Mode            string          `json:"mode"`
	BundleID        string          `json:"bundle_id"`
	AccountCount    int             `json:"account_count"`
	CredentialCount int             `json:"credential_count"`
	Bundle          json.RawMessage `json:"bundle"`
}
