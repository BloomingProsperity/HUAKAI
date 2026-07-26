package passkey

import (
	"github.com/BloomingProsperity/HUAKAI/internal/textsafe"

	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

const (
	DefaultChallengeTTL = 5 * time.Minute
	PurposeRegister     = "register"
	PurposeLogin        = "login"
)

var (
	ErrInvalidInput              = errors.New("passkey: invalid input")
	ErrStoreNotConfigured        = errors.New("passkey: store not configured")
	ErrUserStoreNotConfigured    = errors.New("passkey: user store not configured")
	ErrConfigNotConfigured       = errors.New("passkey: config not configured")
	ErrFeatureDisabled           = errors.New("passkey: feature disabled")
	ErrRegistrationDisabled      = errors.New("passkey: registration disabled")
	ErrOriginNotAllowed          = errors.New("passkey: origin not allowed")
	ErrCredentialNotFound        = errors.New("passkey: credential not found")
	ErrDuplicateCredential       = errors.New("passkey: duplicate credential")
	ErrCeremonyNotFound          = errors.New("passkey: ceremony not found")
	ErrCeremonyExpired           = errors.New("passkey: ceremony expired")
	ErrCloneDetected             = errors.New("passkey: cloned authenticator detected")
	ErrCredentialOwnerMismatch   = errors.New("passkey: credential owner mismatch")
	ErrCeremonyEngineUnavailable = errors.New("passkey: ceremony engine unavailable")
	ErrSecurityStateChanged      = errors.New("passkey: account security state changed")
)

type Config struct {
	Enabled             bool
	RegistrationEnabled bool
	RPID                string
	RPDisplayName       string
	RPOrigins           []string
	ChallengeTTL        time.Duration
}

type ConfigSource interface {
	Config(context.Context) (Config, error)
}

type StaticConfigSource Config

func (s StaticConfigSource) Config(context.Context) (Config, error) {
	return Config(s), nil
}

type UserReader interface {
	GetUserByID(context.Context, int64, int64) (userauth.User, error)
}

type CredentialRecord struct {
	ID              int64      `json:"id"`
	TenantID        int64      `json:"tenant_id"`
	UserID          int64      `json:"user_id"`
	CredentialID    []byte     `json:"-"`
	PublicKey       []byte     `json:"-"`
	SignCount       uint32     `json:"sign_count"`
	AAGUID          []byte     `json:"-"`
	AttestationType string     `json:"attestation_type,omitempty"`
	Transports      []string   `json:"transports,omitempty"`
	CloneWarning    bool       `json:"clone_warning"`
	Name            string     `json:"name,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	LastUsedAt      *time.Time `json:"last_used_at,omitempty"`
}

type CredentialSummary struct {
	ID              int64      `json:"id"`
	Name            string     `json:"name,omitempty"`
	Transports      []string   `json:"transports,omitempty"`
	AttestationType string     `json:"attestation_type,omitempty"`
	CloneWarning    bool       `json:"clone_warning"`
	SignCount       uint32     `json:"sign_count"`
	CreatedAt       time.Time  `json:"created_at"`
	LastUsedAt      *time.Time `json:"last_used_at,omitempty"`
}

type CeremonySession struct {
	ID          string
	TenantID    int64
	UserID      int64
	Purpose     string
	SessionData []byte
	ExpiresAt   time.Time
	CreatedAt   time.Time
}

type CeremonyOptions json.RawMessage

type BeginResponse struct {
	SessionID string          `json:"session_id"`
	PublicKey json.RawMessage `json:"public_key"`
	ExpiresAt time.Time       `json:"expires_at"`
}

type RegisterBeginInput struct {
	TenantID int64
	User     userauth.User
	Name     string
}

type RegisterFinishInput struct {
	TenantID        int64
	User            userauth.User
	SessionFamilyID string
	AuthVersion     int
	SessionID       string
	CredentialJSON  []byte
	Name            string
}

type LoginBeginInput struct {
	TenantID int64
}

type LoginFinishInput struct {
	TenantID       int64
	SessionID      string
	CredentialJSON []byte
}

type LoginFinishResult struct {
	User       userauth.User
	Credential CredentialRecord
}

type DeleteCredentialInput struct {
	TenantID        int64
	UserID          int64
	SessionFamilyID string
	AuthVersion     int
	ID              int64
}

type WebAuthnUser struct {
	User        userauth.User
	Credentials []CredentialRecord
}

func (u WebAuthnUser) Handle() []byte {
	return WebAuthnUserHandle(u.User.TenantID, u.User.ID)
}

func WebAuthnUserHandle(tenantID, userID int64) []byte {
	var raw [32]byte
	copy(raw[:], []byte("huakai-passkey-user-handle-v1"))
	var ids [16]byte
	binary.BigEndian.PutUint64(ids[:8], uint64(tenantID))
	binary.BigEndian.PutUint64(ids[8:], uint64(userID))
	sum := sha256.Sum256(append(raw[:], ids[:]...))
	return sum[:]
}

func (c Config) loginReady() error {
	if !c.Enabled {
		return ErrFeatureDisabled
	}
	if strings.TrimSpace(c.RPID) == "" || len(c.RPOrigins) == 0 {
		return ErrConfigNotConfigured
	}
	return nil
}

func (c Config) registrationReady() error {
	if err := c.loginReady(); err != nil {
		return err
	}
	if !c.RegistrationEnabled {
		return ErrRegistrationDisabled
	}
	return nil
}

func (c Config) ttl() time.Duration {
	if c.ChallengeTTL > 0 {
		return c.ChallengeTTL
	}
	return DefaultChallengeTTL
}

func (c Config) originAllowed(origin string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return false
	}
	for _, allowed := range c.RPOrigins {
		if strings.EqualFold(strings.TrimSpace(allowed), origin) {
			return true
		}
	}
	return false
}

func summaries(records []CredentialRecord) []CredentialSummary {
	out := make([]CredentialSummary, 0, len(records))
	for _, record := range records {
		out = append(out, CredentialSummary{
			ID: record.ID, Name: record.Name, Transports: append([]string(nil), record.Transports...),
			AttestationType: record.AttestationType, CloneWarning: record.CloneWarning,
			SignCount: record.SignCount, CreatedAt: record.CreatedAt, LastUsedAt: cloneTime(record.LastUsedAt),
		})
	}
	return out
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copied := value.UTC()
	return &copied
}

func cleanName(name string) string {
	// rune 安全:裸 name[:120] 切半多字节字符 → 注册写库 PG 22021 失败。
	return textsafe.TruncateBytes(strings.TrimSpace(name), 120)
}
