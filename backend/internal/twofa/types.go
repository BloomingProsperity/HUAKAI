package twofa

import (
	"context"
	"errors"
	"time"
)

const (
	DefaultTOTPDigits        = 6
	DefaultTOTPWindow        = 1
	DefaultBackupCodeCount   = 10
	DefaultMaxFailedAttempts = 5
	MethodTOTP               = "totp"
	MethodBackupCode         = "backup_code"
)

const DefaultTOTPStep = 30 * time.Second
const DefaultLockDuration = 15 * time.Minute
const DefaultChallengeTTL = 5 * time.Minute

var (
	ErrInvalidInput       = errors.New("twofa: invalid input")
	ErrStoreNotConfigured = errors.New("twofa: store not configured")
	ErrKeyUnavailable     = errors.New("twofa: key unavailable")
	ErrNotSetup           = errors.New("twofa: not setup")
	ErrAlreadyEnabled     = errors.New("twofa: already enabled")
	ErrDisabled           = errors.New("twofa: disabled")
	ErrInvalidCode        = errors.New("twofa: invalid code")
	// ErrCodeReused 表示该 TOTP 码(或更早时间步的码)已被成功消费过,按防重放拒绝
	// (RFC 6238 §5.2)。区别于 ErrInvalidCode:码本身有效,只是已用过,故**不计失败次数**,
	// 避免合法用户在网络重试里重复提交同一码而被锁定。
	ErrCodeReused       = errors.New("twofa: code already used")
	ErrLocked           = errors.New("twofa: locked")
	ErrChallengeInvalid = errors.New("twofa: challenge invalid")
	ErrChallengeExpired = errors.New("twofa: challenge expired")
)

type Settings struct {
	TenantID       int64
	UserID         int64
	SecretEnc      []byte
	Enabled        bool
	FailedAttempts int
	LockedUntil    *time.Time
	LastUsedAt     *time.Time
	// LastUsedStep 是最近一次成功消费的 TOTP 时间步计数器(counter = unix/step)。
	// 防重放:校验只接受匹配时间步严格大于它的码;nil 表示从未消费(对应 DB NULL)。
	LastUsedStep *int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type SetupInput struct {
	TenantID    int64
	UserID      int64
	AccountName string
}

type SetupResult struct {
	Secret      string   `json:"secret"`
	QRData      string   `json:"qr_data"`
	BackupCodes []string `json:"backup_codes"`
}

type VerifyInput struct {
	TenantID int64
	UserID   int64
	Code     string
}

type ChallengeVerifyInput struct {
	ChallengeID string
	Code        string
}

type Status struct {
	Enabled              bool       `json:"enabled"`
	BackupCodesRemaining int        `json:"backup_codes_remaining"`
	LockedUntil          *time.Time `json:"locked_until,omitempty"`
	LastUsedAt           *time.Time `json:"last_used_at,omitempty"`
}

type BackupCodesResult struct {
	BackupCodes []string `json:"backup_codes"`
}

type VerifyResult struct {
	TenantID             int64
	UserID               int64
	Method               string
	BackupCodesRemaining int
	LockedUntil          *time.Time
}

type Challenge struct {
	ID        string    `json:"id"`
	TenantID  int64     `json:"tenant_id"`
	UserID    int64     `json:"user_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

type TOTPConfig struct {
	Digits int
	Step   time.Duration
	Window int
}

type Store interface {
	GetSettings(ctx context.Context, tenantID, userID int64) (Settings, bool, error)
	SaveSetup(ctx context.Context, settings Settings, backupCodeHashes [][]byte) error
	SetEnabled(ctx context.Context, tenantID, userID int64, enabled bool, now time.Time) error
	MarkSuccess(ctx context.Context, tenantID, userID int64, now time.Time) error
	// MarkTOTPSuccess 记录一次成功的 TOTP 校验并落已消费时间步 consumedStep,做防重放的
	// 原子守卫:仅当 consumedStep 严格大于已存 LastUsedStep(或其为 nil/NULL)时才更新,
	// 返回 stored=true;否则不更新、返回 stored=false——表示该时间步已被并发请求消费,
	// 调用方据此按重放拒绝。与 MarkSuccess 一样重置失败计数与锁定。
	MarkTOTPSuccess(ctx context.Context, tenantID, userID int64, consumedStep int64, now time.Time) (stored bool, err error)
	MarkFailure(ctx context.Context, tenantID, userID int64, failedAttempts int, lockedUntil *time.Time, now time.Time) error
	CountUnusedBackupCodes(ctx context.Context, tenantID, userID int64) (int, error)
	ConsumeBackupCode(ctx context.Context, tenantID, userID int64, hash []byte, now time.Time) (bool, error)
	ReplaceBackupCodes(ctx context.Context, tenantID, userID int64, hashes [][]byte, now time.Time) error
}

type atomicFailureStore interface {
	RecordFailure(ctx context.Context, tenantID, userID int64, maxFailedAttempts int, lockUntil, now time.Time) (failedAttempts int, locked bool, err error)
}
