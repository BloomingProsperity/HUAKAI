package twofa

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

const (
	defaultIssuer        = "HUAKAI"
	secretBytes          = 20
	backupCodeBytes      = 10
	backupCodeHashPrefix = "huakai-twofa-backup-code-v1"
	challengeMACPrefix   = "huakai-twofa-login-challenge-v1."
)

type Service struct {
	store             Store
	keys              credentialstore.KeyProvider
	now               func() time.Time
	maxFailedAttempts int
	lockDuration      time.Duration
	challengeTTL      time.Duration
	issuer            string
}

type Option func(*Service)

func NewService(store Store, keys credentialstore.KeyProvider, opts ...Option) *Service {
	s := &Service{
		store:             store,
		keys:              keys,
		now:               func() time.Time { return time.Now().UTC() },
		maxFailedAttempts: DefaultMaxFailedAttempts,
		lockDuration:      DefaultLockDuration,
		challengeTTL:      DefaultChallengeTTL,
		issuer:            defaultIssuer,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func WithNow(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

func WithMaxFailedAttempts(n int) Option {
	return func(s *Service) {
		if n > 0 {
			s.maxFailedAttempts = n
		}
	}
}

func WithLockDuration(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			s.lockDuration = d
		}
	}
}

func WithChallengeTTL(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			s.challengeTTL = d
		}
	}
}

func (s *Service) Setup(ctx context.Context, in SetupInput) (SetupResult, error) {
	if err := validateUserScope(in.TenantID, in.UserID); err != nil {
		return SetupResult{}, err
	}
	if err := s.ready(); err != nil {
		return SetupResult{}, err
	}
	existing, found, err := s.store.GetSettings(ctx, in.TenantID, in.UserID)
	if err != nil {
		return SetupResult{}, err
	}
	if found && existing.Enabled {
		return SetupResult{}, ErrAlreadyEnabled
	}
	secret := make([]byte, secretBytes)
	if _, err := rand.Read(secret); err != nil {
		return SetupResult{}, fmt.Errorf("twofa: generate secret: %w", err)
	}
	defer privacy.Zeroize(secret)
	secretText := encodeSecret(secret)
	secretEnc, err := s.encryptSecret(ctx, in.TenantID, in.UserID, secret)
	if err != nil {
		return SetupResult{}, err
	}
	codes, hashes, err := s.generateBackupCodes(in.TenantID, in.UserID, DefaultBackupCodeCount)
	if err != nil {
		return SetupResult{}, err
	}
	now := s.now().UTC()
	settings := Settings{
		TenantID: in.TenantID, UserID: in.UserID, SecretEnc: secretEnc,
		Enabled: false, FailedAttempts: 0, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.SaveSetup(ctx, settings, hashes); err != nil {
		return SetupResult{}, err
	}
	return SetupResult{
		Secret:      secretText,
		QRData:      s.qrData(in.AccountName, secretText),
		BackupCodes: codes,
	}, nil
}

func (s *Service) Enable(ctx context.Context, in VerifyInput) (Status, error) {
	if err := validateUserScope(in.TenantID, in.UserID); err != nil {
		return Status{}, err
	}
	if err := s.ready(); err != nil {
		return Status{}, err
	}
	settings, found, err := s.store.GetSettings(ctx, in.TenantID, in.UserID)
	if err != nil {
		return Status{}, err
	}
	if !found {
		return Status{}, ErrNotSetup
	}
	if err := s.ensureUnlocked(ctx, &settings); err != nil {
		return Status{}, err
	}
	secret, err := s.decryptSecret(ctx, settings)
	if err != nil {
		return Status{}, err
	}
	defer privacy.Zeroize(secret)
	// 启用是一次性初始证明,**不消费时间步**:把"已消费时间步"防重放收敛在登录路径
	// (审计点名的攻击面)。这枚启用码在随后首次登录里仍可被消费一次,其后任何重复使用
	// 都会被 VerifyLogin 的防重放守卫拒绝——既堵住登录重放,又不破坏"启用后立即用同一码
	// 登录一次"的合法流程。
	if !VerifyTOTP(secret, in.Code, s.now().UTC(), defaultTOTPConfig()) {
		if err := s.recordFailure(ctx, settings); err != nil {
			return Status{}, err
		}
		return Status{}, ErrInvalidCode
	}
	now := s.now().UTC()
	if err := s.store.SetEnabled(ctx, in.TenantID, in.UserID, true, now); err != nil {
		return Status{}, err
	}
	remaining, err := s.store.CountUnusedBackupCodes(ctx, in.TenantID, in.UserID)
	if err != nil {
		return Status{}, err
	}
	return Status{Enabled: true, BackupCodesRemaining: remaining, LastUsedAt: &now}, nil
}

func (s *Service) Disable(ctx context.Context, tenantID, userID int64) error {
	if err := validateUserScope(tenantID, userID); err != nil {
		return err
	}
	if err := s.ready(); err != nil {
		return err
	}
	return s.store.SetEnabled(ctx, tenantID, userID, false, s.now().UTC())
}

func (s *Service) Status(ctx context.Context, tenantID, userID int64) (Status, error) {
	if err := validateUserScope(tenantID, userID); err != nil {
		return Status{}, err
	}
	if err := s.ready(); err != nil {
		return Status{}, err
	}
	settings, found, err := s.store.GetSettings(ctx, tenantID, userID)
	if err != nil {
		return Status{}, err
	}
	if !found {
		return Status{Enabled: false}, nil
	}
	remaining, err := s.store.CountUnusedBackupCodes(ctx, tenantID, userID)
	if err != nil {
		return Status{}, err
	}
	return Status{
		Enabled:              settings.Enabled,
		BackupCodesRemaining: remaining,
		LockedUntil:          cloneTimePtr(settings.LockedUntil),
		LastUsedAt:           cloneTimePtr(settings.LastUsedAt),
	}, nil
}

func (s *Service) RegenerateBackupCodes(ctx context.Context, in VerifyInput) (BackupCodesResult, error) {
	result, err := s.VerifyLogin(ctx, in)
	if err != nil {
		return BackupCodesResult{}, err
	}
	codes, hashes, err := s.generateBackupCodes(result.TenantID, result.UserID, DefaultBackupCodeCount)
	if err != nil {
		return BackupCodesResult{}, err
	}
	if err := s.store.ReplaceBackupCodes(ctx, result.TenantID, result.UserID, hashes, s.now().UTC()); err != nil {
		return BackupCodesResult{}, err
	}
	return BackupCodesResult{BackupCodes: codes}, nil
}

func (s *Service) LoginRequired(ctx context.Context, tenantID, userID int64) (bool, error) {
	if err := validateUserScope(tenantID, userID); err != nil {
		return false, err
	}
	if err := s.ready(); err != nil {
		return false, err
	}
	settings, found, err := s.store.GetSettings(ctx, tenantID, userID)
	if err != nil {
		return false, err
	}
	return found && settings.Enabled, nil
}

func (s *Service) VerifyLogin(ctx context.Context, in VerifyInput) (VerifyResult, error) {
	if err := validateUserScope(in.TenantID, in.UserID); err != nil {
		return VerifyResult{}, err
	}
	if err := s.ready(); err != nil {
		return VerifyResult{}, err
	}
	settings, found, err := s.store.GetSettings(ctx, in.TenantID, in.UserID)
	if err != nil {
		return VerifyResult{}, err
	}
	if !found {
		return VerifyResult{}, ErrNotSetup
	}
	if !settings.Enabled {
		return VerifyResult{}, ErrDisabled
	}
	if err := s.ensureUnlocked(ctx, &settings); err != nil {
		return VerifyResult{}, err
	}
	now := s.now().UTC()
	secret, err := s.decryptSecret(ctx, settings)
	if err != nil {
		return VerifyResult{}, err
	}
	defer privacy.Zeroize(secret)
	if step, ok := VerifyTOTPStep(secret, in.Code, now, defaultTOTPConfig()); ok {
		// 防重放(RFC 6238 §5.2):先按已加载的 LastUsedStep 快速拒绝重复使用的(或更早的)
		// 时间步。这是有效但已用过的码,**不计失败次数**,以免合法用户的网络重试触发锁定。
		if settings.LastUsedStep != nil && step <= *settings.LastUsedStep {
			return VerifyResult{}, ErrCodeReused
		}
		return s.recordTOTPSuccess(ctx, settings, step, now)
	}
	hash, ok := hashBackupCode(in.TenantID, in.UserID, in.Code)
	if ok {
		consumed, err := s.store.ConsumeBackupCode(ctx, in.TenantID, in.UserID, hash, now)
		if err != nil {
			return VerifyResult{}, err
		}
		if consumed {
			return s.recordSuccess(ctx, settings, MethodBackupCode, now)
		}
	}
	if err := s.recordFailure(ctx, settings); err != nil {
		return VerifyResult{}, err
	}
	return VerifyResult{}, ErrInvalidCode
}

func (s *Service) StartLoginChallenge(ctx context.Context, tenantID, userID int64) (Challenge, error) {
	required, err := s.LoginRequired(ctx, tenantID, userID)
	if err != nil {
		return Challenge{}, err
	}
	if !required {
		return Challenge{}, ErrDisabled
	}
	expiresAt := s.now().UTC().Add(s.challengeTTL)
	challengeID, err := s.signChallenge(ctx, challengePayload{
		TenantID: tenantID, UserID: userID, ExpiresAt: expiresAt.Unix(),
	})
	if err != nil {
		return Challenge{}, err
	}
	return Challenge{ID: challengeID, TenantID: tenantID, UserID: userID, ExpiresAt: expiresAt}, nil
}

func (s *Service) VerifyLoginChallenge(ctx context.Context, in ChallengeVerifyInput) (VerifyResult, error) {
	payload, err := s.verifyChallenge(ctx, in.ChallengeID)
	if err != nil {
		return VerifyResult{}, err
	}
	return s.VerifyLogin(ctx, VerifyInput{
		TenantID: payload.TenantID,
		UserID:   payload.UserID,
		Code:     in.Code,
	})
}

func (s *Service) ready() error {
	if s == nil || s.store == nil {
		return ErrStoreNotConfigured
	}
	if s.keys == nil {
		return ErrKeyUnavailable
	}
	return nil
}

func (s *Service) recordSuccess(ctx context.Context, settings Settings, method string, now time.Time) (VerifyResult, error) {
	if err := s.store.MarkSuccess(ctx, settings.TenantID, settings.UserID, now); err != nil {
		return VerifyResult{}, err
	}
	remaining, err := s.store.CountUnusedBackupCodes(ctx, settings.TenantID, settings.UserID)
	if err != nil {
		return VerifyResult{}, err
	}
	return VerifyResult{
		TenantID: settings.TenantID, UserID: settings.UserID, Method: method,
		BackupCodesRemaining: remaining,
	}, nil
}

// recordTOTPSuccess 落 TOTP 成功并消费时间步。MarkTOTPSuccess 的条件更新若未命中
// (stored=false),说明并发请求已抢先消费同一时间步,按重放拒绝——与快速路径的
// LastUsedStep 比较共同构成"读-比-写"竞态下的双层防重放。
func (s *Service) recordTOTPSuccess(ctx context.Context, settings Settings, step int64, now time.Time) (VerifyResult, error) {
	stored, err := s.store.MarkTOTPSuccess(ctx, settings.TenantID, settings.UserID, step, now)
	if err != nil {
		return VerifyResult{}, err
	}
	if !stored {
		return VerifyResult{}, ErrCodeReused
	}
	remaining, err := s.store.CountUnusedBackupCodes(ctx, settings.TenantID, settings.UserID)
	if err != nil {
		return VerifyResult{}, err
	}
	return VerifyResult{
		TenantID: settings.TenantID, UserID: settings.UserID, Method: MethodTOTP,
		BackupCodesRemaining: remaining,
	}, nil
}

func (s *Service) recordFailure(ctx context.Context, settings Settings) error {
	now := s.now().UTC()
	failed := settings.FailedAttempts + 1
	var lockedUntil *time.Time
	if failed >= s.maxFailedAttempts {
		until := now.Add(s.lockDuration)
		lockedUntil = &until
		if err := s.store.MarkFailure(ctx, settings.TenantID, settings.UserID, failed, lockedUntil, now); err != nil {
			return err
		}
		return ErrLocked
	}
	return s.store.MarkFailure(ctx, settings.TenantID, settings.UserID, failed, lockedUntil, now)
}

func (s *Service) ensureUnlocked(ctx context.Context, settings *Settings) error {
	if settings == nil {
		return ErrInvalidInput
	}
	if settings.LockedUntil == nil {
		return nil
	}
	now := s.now().UTC()
	if now.Before(*settings.LockedUntil) {
		return ErrLocked
	}
	if err := s.store.MarkFailure(ctx, settings.TenantID, settings.UserID, 0, nil, now); err != nil {
		return err
	}
	settings.FailedAttempts = 0
	settings.LockedUntil = nil
	return nil
}

func (s *Service) qrData(accountName, secret string) string {
	label := s.issuer
	accountName = strings.TrimSpace(accountName)
	if accountName != "" {
		label += ":" + accountName
	}
	values := url.Values{}
	values.Set("secret", secret)
	values.Set("issuer", s.issuer)
	values.Set("digits", strconv.Itoa(DefaultTOTPDigits))
	values.Set("period", strconv.Itoa(int(DefaultTOTPStep/time.Second)))
	return "otpauth://totp/" + url.PathEscape(label) + "?" + values.Encode()
}

func defaultTOTPConfig() TOTPConfig {
	return TOTPConfig{Digits: DefaultTOTPDigits, Step: DefaultTOTPStep, Window: DefaultTOTPWindow}
}

func validateUserScope(tenantID, userID int64) error {
	if tenantID <= 0 || userID <= 0 {
		return ErrInvalidInput
	}
	return nil
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copied := value.UTC()
	return &copied
}
