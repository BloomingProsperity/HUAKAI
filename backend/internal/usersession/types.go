package usersession

import (
	"errors"
	"time"
)

type FamilyStatus string

const (
	FamilyStatusActive     FamilyStatus = "active"
	FamilyStatusRevoked    FamilyStatus = "revoked"
	FamilyStatusExpired    FamilyStatus = "expired"
	FamilyStatusSuspicious FamilyStatus = "suspicious"
	FamilyStatusReplaced   FamilyStatus = "replaced"
)

type RefreshTokenStatus string

const (
	RefreshTokenStatusActive   RefreshTokenStatus = "active"
	RefreshTokenStatusConsumed RefreshTokenStatus = "consumed"
	RefreshTokenStatusRevoked  RefreshTokenStatus = "revoked"
	RefreshTokenStatusExpired  RefreshTokenStatus = "expired"
)

var (
	ErrInvalidInput               = errors.New("usersession: invalid input")
	ErrStoreNotConfigured         = errors.New("usersession: store not configured")
	ErrSigningKeyMissing          = errors.New("usersession: signing key missing")
	ErrFamilyNotFound             = errors.New("usersession: family not found")
	ErrFamilyRevoked              = errors.New("usersession: family revoked")
	ErrTokenNotFound              = errors.New("usersession: token not found")
	ErrTokenExpired               = errors.New("usersession: token expired")
	ErrRefreshReplay              = errors.New("usersession: refresh token replay")
	ErrSessionUserMismatch        = errors.New("usersession: session user mismatch")
	ErrAnomalyRejected            = errors.New("usersession: session anomaly rejected")
	ErrDeviceLimitExceeded        = errors.New("usersession: device limit exceeded")
	ErrDeviceConfirmationRequired = errors.New("usersession: device confirmation required")
)

type SessionFamily struct {
	ID            string         `json:"id"`
	UserID        int64          `json:"user_id"`
	TenantID      int64          `json:"tenant_id"`
	Status        FamilyStatus   `json:"status"`
	Generation    int            `json:"generation"`
	CreatedAt     time.Time      `json:"created_at"`
	LastActiveAt  time.Time      `json:"last_active_at"`
	DeviceInfo    map[string]any `json:"device_info"`
	IPBaseline    string         `json:"ip_baseline"`
	RevokedAt     *time.Time     `json:"revoked_at,omitempty"`
	RevokedReason string         `json:"revoked_reason,omitempty"`
}

type RefreshToken struct {
	ID         string             `json:"id"`
	TenantID   int64              `json:"tenant_id"`
	FamilyID   string             `json:"family_id"`
	TokenHash  []byte             `json:"-"`
	Generation int                `json:"generation"`
	Status     RefreshTokenStatus `json:"status"`
	ExpiresAt  time.Time          `json:"expires_at"`
	CreatedAt  time.Time          `json:"created_at"`
	ConsumedAt *time.Time         `json:"consumed_at,omitempty"`
}

type SessionToken struct {
	ID         string     `json:"id"`
	TenantID   int64      `json:"tenant_id"`
	FamilyID   string     `json:"family_id"`
	TokenHash  []byte     `json:"-"`
	Generation int        `json:"generation"`
	ExpiresAt  time.Time  `json:"expires_at"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

type CreateInput struct {
	TenantID   int64          `json:"tenant_id"`
	UserID     int64          `json:"user_id"`
	DeviceInfo map[string]any `json:"device_info,omitempty"`
	IP         string         `json:"ip,omitempty"`
	UserAgent  string         `json:"user_agent,omitempty"`
	RefreshTTL time.Duration  `json:"-"`
	SessionTTL time.Duration  `json:"-"`
	AuthMethod string         `json:"auth_method,omitempty"`
	Remember   bool           `json:"remember,omitempty"`
}

type RefreshInput struct {
	// TenantID and UserID are optional. When both are set, refresh enforces that the
	// presented refresh token belongs to that caller; when both are zero, the refresh
	// token itself is the trust root.
	TenantID     int64         `json:"tenant_id"`
	UserID       int64         `json:"user_id,omitempty"`
	RefreshToken string        `json:"refresh_token"`
	IP           string        `json:"ip,omitempty"`
	UserAgent    string        `json:"user_agent,omitempty"`
	RefreshTTL   time.Duration `json:"-"`
	SessionTTL   time.Duration `json:"-"`
}

type RevokeInput struct {
	TenantID     int64  `json:"tenant_id"`
	UserID       int64  `json:"user_id,omitempty"`
	FamilyID     string `json:"family_id,omitempty"`
	SessionToken string `json:"session_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

type RevokeOthersInput struct {
	TenantID        int64  `json:"tenant_id"`
	UserID          int64  `json:"user_id"`
	CurrentFamilyID string `json:"current_family_id"`
	Reason          string `json:"reason,omitempty"`
}

type IssuedTokens struct {
	SessionToken  string        `json:"session_token"`
	RefreshToken  string        `json:"refresh_token"`
	SessionExpiry time.Time     `json:"session_expires_at"`
	RefreshExpiry time.Time     `json:"refresh_expires_at"`
	Family        SessionFamily `json:"family"`
	Generation    int           `json:"generation"`
}

type RefreshRecord struct {
	Token  RefreshToken
	Family SessionFamily
}

type SessionRecord struct {
	Token  SessionToken
	Family SessionFamily
}

type ValidatedSession struct {
	TenantID   int64
	UserID     int64
	FamilyID   string
	TokenID    string
	Generation int
	ExpiresAt  time.Time
}
