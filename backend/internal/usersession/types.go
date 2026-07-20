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
	ErrInvalidInput        = errors.New("usersession: invalid input")
	ErrStoreNotConfigured  = errors.New("usersession: store not configured")
	ErrSigningKeyMissing   = errors.New("usersession: signing key missing")
	ErrFamilyNotFound      = errors.New("usersession: family not found")
	ErrFamilyRevoked       = errors.New("usersession: family revoked")
	ErrTokenNotFound       = errors.New("usersession: token not found")
	ErrTokenExpired        = errors.New("usersession: token expired")
	ErrRefreshReplay       = errors.New("usersession: refresh token replay")
	ErrSessionUserMismatch = errors.New("usersession: session user mismatch")
	ErrAnomalyRejected     = errors.New("usersession: session anomaly rejected")
	// ErrUserIneligible: 会话主体账号已封禁/删除, 会话使用期复核拒绝 (UserGate)。
	ErrUserIneligible             = errors.New("usersession: user ineligible for session")
	ErrDeviceLimitExceeded        = errors.New("usersession: device limit exceeded")
	ErrDeviceConfirmationRequired = errors.New("usersession: device confirmation required")
	// ErrDeviceConfirmationNotFound: 确认 token 无对应 pending 记录 (不存在 / 已消费 / 跨租户)。
	// 与 ErrTokenNotFound 同语义但独立 sentinel, 便于 handler 区分确认流与刷新流。
	ErrDeviceConfirmationNotFound = errors.New("usersession: device confirmation not found")
)

// DeviceConfirmationStatus 是 device_confirmations.status 的合法取值。
type DeviceConfirmationStatus string

const (
	DeviceConfirmationStatusPending   DeviceConfirmationStatus = "pending"
	DeviceConfirmationStatusConfirmed DeviceConfirmationStatus = "confirmed"
	DeviceConfirmationStatusExpired   DeviceConfirmationStatus = "expired"
)

// DeviceConfirmation 是一条新设备确认 pending 记录。只持有 TokenHash (sha256), 永不存原文 token。
type DeviceConfirmation struct {
	ID          int64                    `json:"id"`
	TenantID    int64                    `json:"tenant_id"`
	UserID      int64                    `json:"user_id"`
	TokenHash   []byte                   `json:"-"`
	DeviceInfo  map[string]any           `json:"device_info"`
	IP          string                   `json:"ip"`
	UserAgent   string                   `json:"user_agent"`
	Status      DeviceConfirmationStatus `json:"status"`
	CreatedAt   time.Time                `json:"created_at"`
	ExpiresAt   time.Time                `json:"expires_at"`
	ConfirmedAt *time.Time               `json:"confirmed_at,omitempty"`
}

// DeviceConfirmationRequiredError 在 DevicePolicy=confirm 达上限时由 Create 返回。它携带原文 token
// (RawToken) 让 handler 据此发确认邮件, 但 Error() 绝不打印 RawToken (secret-mask)。Unwrap 返回
// ErrDeviceConfirmationRequired, 使既有的 errors.Is(err, ErrDeviceConfirmationRequired) 分支继续生效。
type DeviceConfirmationRequiredError struct {
	// RawToken 是一次性原文确认 token, 仅供 handler 经邮件交付给用户。
	RawToken string
	// UserID 便于 handler 取用户信息发信 (不含敏感数据)。
	UserID int64
}

func (e *DeviceConfirmationRequiredError) Error() string {
	// 注意: 不得拼接 RawToken, 否则原文 token 会泄进日志/响应。
	return ErrDeviceConfirmationRequired.Error()
}

func (e *DeviceConfirmationRequiredError) Unwrap() error {
	return ErrDeviceConfirmationRequired
}

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
	// TenantID 和 UserID 是可选的。两者都设置时, refresh 会强制
	// 所出示的 refresh token 属于该调用方; 两者都为零时, refresh
	// token 自身即为信任根。
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

type SessionBundle struct {
	Family       SessionFamily
	RefreshToken RefreshToken
	SessionToken SessionToken
}

type SessionCreatePolicy struct {
	MaxActiveFamilies int
	Mode              string
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
