package userauth

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type UserStatus string

const (
	UserStatusPendingVerification UserStatus = "pending_verification"
	UserStatusActive              UserStatus = "active"
	UserStatusDisabled            UserStatus = "disabled"
	UserStatusLocked              UserStatus = "locked"
	UserStatusResetRequired       UserStatus = "reset_required"
	UserStatusDeleted             UserStatus = "deleted"
)

var (
	ErrInvalidInput                 = errors.New("userauth: invalid input")
	ErrUserNotFound                 = errors.New("userauth: user not found")
	ErrDuplicateUser                = errors.New("userauth: duplicate user")
	ErrInvalidCredentials           = errors.New("userauth: invalid credentials")
	ErrEmailUnverified              = errors.New("userauth: email is not verified")
	ErrUserDisabled                 = errors.New("userauth: user is disabled")
	ErrUserLocked                   = errors.New("userauth: user is locked")
	ErrPasswordResetRequired        = errors.New("userauth: password reset required")
	ErrRegistrationDisabled         = errors.New("userauth: registration disabled")
	ErrPasswordRegistrationDisabled = errors.New("userauth: password registration disabled")
	ErrPasswordLoginDisabled        = errors.New("userauth: password login disabled")
	ErrInviteRequired               = errors.New("userauth: invite code required")
	ErrInviteInvalid                = errors.New("userauth: invite code invalid")
	ErrTokenInvalid                 = errors.New("userauth: token invalid")
	ErrTokenExpired                 = errors.New("userauth: token expired")
	ErrOAuthFlowNotFound            = errors.New("userauth: oauth flow not found")
	ErrOAuthFlowExpired             = errors.New("userauth: oauth flow expired")
	ErrOAuthProviderMissing         = errors.New("userauth: oauth provider not configured")
	ErrSocialLoginRejected          = errors.New("userauth: social login rejected")
	ErrLastLoginMethod              = errors.New("userauth: cannot remove last login method")
	// ErrSocialIdentityAlreadyBound 表示该社交身份(provider+subject)已被「另一个」用户绑定。
	// 用于「先绑定后登录」的绑定接口:防止把同一个 telegram/QQ 账号绑到多个本地账号(接管/混淆)。
	ErrSocialIdentityAlreadyBound   = errors.New("userauth: social identity already bound to another account")
	ErrOAuthPendingEmailRequired    = errors.New("userauth: oauth pending email verification required")
	ErrStoreNotConfigured           = errors.New("userauth: store not configured")
)

type RegistrationMode string

const (
	RegistrationModeOpen           RegistrationMode = "open"
	RegistrationModeInviteRequired RegistrationMode = "invite_required"
	RegistrationModeDisabled       RegistrationMode = "disabled"
)

func ParseRegistrationMode(raw string) (RegistrationMode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(RegistrationModeOpen):
		return RegistrationModeOpen, nil
	case string(RegistrationModeInviteRequired):
		return RegistrationModeInviteRequired, nil
	case string(RegistrationModeDisabled), "admin_only":
		return RegistrationModeDisabled, nil
	default:
		return "", fmt.Errorf("%w: registration mode %q", ErrInvalidInput, raw)
	}
}

type User struct {
	ID                  int64      `json:"id"`
	TenantID            int64      `json:"tenant_id"`
	Email               string     `json:"email"`
	DisplayName         string     `json:"display_name"`
	PasswordHash        string     `json:"-"`
	EmailVerified       bool       `json:"email_verified"`
	InviteCodeUsed      string     `json:"invite_code_used,omitempty"`
	SocialLoginProvider string     `json:"social_login_provider,omitempty"`
	Status              UserStatus `json:"status"`
	PasswordVersion     int        `json:"password_version"`
	FailedLoginCount    int        `json:"failed_login_count"`
	LockedUntil         *time.Time `json:"locked_until,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

type InviteCode struct {
	Code                  string     `json:"code"`
	TenantID              int64      `json:"tenant_id"`
	CreatedBy             int64      `json:"created_by,omitempty"`
	MaxUses               int        `json:"max_uses"`
	UsedCount             int        `json:"used_count"`
	ValidUntil            *time.Time `json:"valid_until,omitempty"`
	Status                string     `json:"status"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
	CommunityInvitationID int64      `json:"community_invitation_id,omitempty"`
}

type InviteCodeStatus string

const (
	InviteCodeStatusDisabled        InviteCodeStatus = "disabled"
	InviteCodeStatusNotFound        InviteCodeStatus = "not_found"
	InviteCodeStatusUsedOrExhausted InviteCodeStatus = "used_or_exhausted"
	InviteCodeStatusExpired         InviteCodeStatus = "expired"
	InviteCodeStatusValid           InviteCodeStatus = "valid"
)

type InviteBinding struct {
	ID         string    `json:"id"`
	TenantID   int64     `json:"tenant_id"`
	UserID     int64     `json:"user_id"`
	InviteCode string    `json:"invite_code_hash"`
	RedeemedAt time.Time `json:"redeemed_at"`
}

// SocialIdentityLink 是某用户一条已绑定的社交登录身份(只读视图)。Subject 是上游
// 用户标识,默认应脱敏后再出网络,避免把可关联的第三方 subject 原样暴露给前端。
type SocialIdentityLink struct {
	Provider string    `json:"provider"`
	Subject  string    `json:"subject"`
	LinkedAt time.Time `json:"linked_at"`
}

type TokenChallenge struct {
	ID        string    `json:"id"`
	UserID    int64     `json:"user_id"`
	TenantID  int64     `json:"tenant_id"`
	RawToken  string    `json:"-"`
	TokenHash []byte    `json:"-"`
	ExpiresAt time.Time `json:"expires_at"`
}

type OAuthFlowSession struct {
	ID                     string     `json:"id"`
	TenantID               int64      `json:"tenant_id"`
	Provider               string     `json:"provider"`
	StateHash              []byte     `json:"-"`
	NonceHash              []byte     `json:"-"`
	PKCEVerifier           string     `json:"-"`
	PKCEVerifierCiphertext []byte     `json:"-"`
	RedirectURI            string     `json:"redirect_uri"`
	ExpiresAt              time.Time  `json:"expires_at"`
	ConsumedAt             *time.Time `json:"consumed_at,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
}

type OAuthFlowChallenge struct {
	ID            string
	TenantID      int64
	Provider      string
	State         string
	StateHash     []byte
	Nonce         string
	NonceHash     []byte
	PKCEVerifier  string
	PKCEChallenge string
	RedirectURI   string
	ExpiresAt     time.Time
}

type VerifiedIdentity struct {
	Provider      string
	Subject       string
	Email         string
	DisplayName   string
	EmailVerified bool
}

type CreateUserParams struct {
	TenantID            int64
	Email               string
	DisplayName         string
	PasswordHash        string
	EmailVerified       bool
	InviteCodeUsed      string
	SocialLoginProvider string
	Status              UserStatus
}

type RegisterInput struct {
	TenantID    int64  `json:"tenant_id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name,omitempty"`
	Password    string `json:"password"`
	InviteCode  string `json:"invite_code,omitempty"`
}

type RegistrationResult struct {
	User              User   `json:"user"`
	VerificationToken string `json:"verification_token,omitempty"`
}

type LoginInput struct {
	TenantID int64  `json:"tenant_id"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type PasswordResetRequest struct {
	TenantID int64  `json:"tenant_id"`
	Email    string `json:"email"`
}

type PasswordResetResult struct {
	UserID int64  `json:"user_id,omitempty"`
	Token  string `json:"reset_token,omitempty"`
}

type PasswordResetConfirm struct {
	TenantID    int64  `json:"tenant_id"`
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

type OAuthInitInput struct {
	TenantID    int64  `json:"tenant_id"`
	Provider    string `json:"provider"`
	RedirectURI string `json:"redirect_uri,omitempty"`
}

type OAuthInitResult struct {
	Provider  string    `json:"provider"`
	State     string    `json:"state"`
	AuthURL   string    `json:"auth_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

type OAuthCallbackInput struct {
	TenantID int64  `json:"tenant_id"`
	Provider string `json:"provider"`
	State    string `json:"state"`
	Code     string `json:"code"`
}
