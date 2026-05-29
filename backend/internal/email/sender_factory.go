package email

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	obsdlq "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

type SMTPDispatch func(context.Context, SMTPSettings, Message) error

type AuthSender struct {
	store                SettingsStore
	keys                 SecretKeyProvider
	dispatch             SMTPDispatch
	outbox               obsdlq.Outbox
	now                  func() time.Time
	verificationCooldown time.Duration
	resetCooldown        time.Duration
	mu                   sync.Mutex
	lastSent             map[string]time.Time
}

type AuthSenderOption func(*AuthSender)

func WithSMTPDispatch(dispatch SMTPDispatch) AuthSenderOption {
	return func(sender *AuthSender) {
		if dispatch != nil {
			sender.dispatch = dispatch
		}
	}
}

func WithClock(now func() time.Time) AuthSenderOption {
	return func(sender *AuthSender) {
		if now != nil {
			sender.now = now
		}
	}
}

func WithOutbox(outbox obsdlq.Outbox) AuthSenderOption {
	return func(sender *AuthSender) {
		sender.outbox = outbox
	}
}

func BuildEmailSender(_ context.Context, store SettingsStore, keys SecretKeyProvider, opts ...AuthSenderOption) (*AuthSender, error) {
	if store == nil || keys == nil {
		return nil, ErrEmailBackendUnconfigured
	}
	sender := &AuthSender{
		store:                store,
		keys:                 keys,
		dispatch:             defaultSMTPDispatch,
		now:                  time.Now,
		verificationCooldown: DefaultVerificationEmailCooldown,
		resetCooldown:        DefaultPasswordResetCooldown,
		lastSent:             make(map[string]time.Time),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(sender)
		}
	}
	return sender, nil
}

func (s *AuthSender) SendVerification(ctx context.Context, user userauth.User, token string) error {
	if s == nil {
		return ErrEmailBackendUnconfigured
	}
	if strings.TrimSpace(token) == "" {
		return nil
	}
	if !s.markAllowed("verify", user.TenantID, user.Email, s.verificationCooldown) {
		return nil
	}
	return s.sendForTenant(ctx, user.TenantID, Message{
		TenantID: user.TenantID,
		To:       user.Email,
		Subject:  "HUAKAI email verification",
		HTMLBody: buildVerificationBody(token),
	})
}

func (s *AuthSender) SendPasswordReset(ctx context.Context, user userauth.User, token string) error {
	if s == nil {
		return ErrEmailBackendUnconfigured
	}
	if strings.TrimSpace(token) == "" {
		return nil
	}
	if !s.markAllowed("reset", user.TenantID, user.Email, s.resetCooldown) {
		return nil
	}
	return s.sendForTenant(ctx, user.TenantID, Message{
		TenantID: user.TenantID,
		To:       user.Email,
		Subject:  "HUAKAI password reset",
		HTMLBody: buildPasswordResetBody(token),
	})
}

func (s *AuthSender) EmailVerificationEnabled(ctx context.Context, tenantID int64) (bool, error) {
	if s == nil || s.store == nil {
		return false, ErrEmailBackendUnconfigured
	}
	raw, err := s.store.Load(ctx, tenantID)
	if err != nil {
		return false, err
	}
	return parseBool(raw[SettingVerifyRequirement]), nil
}

// SendTenantMessage 用租户 SMTP 设置发送一封任意邮件, 复用加载设置/校验/瞬时失败入 DLQ 的链路。
// 供订阅到期提醒等非鉴权用途复用 (鉴权邮件走 SendVerification/SendPasswordReset 带冷却; 本入口不限频,
// 去重由调用方账本负责)。租户未配 SMTP 返回 ErrEmailBackendUnconfigured; 瞬时失败已入 DLQ 时返回 nil。
func (s *AuthSender) SendTenantMessage(ctx context.Context, tenantID int64, msg Message) error {
	if s == nil {
		return ErrEmailBackendUnconfigured
	}
	if msg.TenantID == 0 {
		msg.TenantID = tenantID
	}
	return s.sendForTenant(ctx, tenantID, msg)
}

func (s *AuthSender) sendForTenant(ctx context.Context, tenantID int64, msg Message) error {
	settings, err := LoadSMTPSettings(ctx, s.store, s.keys, tenantID)
	if err != nil {
		return err
	}
	if err := validateMessage(settings, msg); err != nil {
		return err
	}
	err = s.dispatch(ctx, settings, msg)
	if err == nil {
		return nil
	}
	if s.outbox == nil || isPermanentEmailFailure(err) {
		return err
	}
	if enqueueErr := enqueueEmailRetry(ctx, s.outbox, s.keys, settings, msg, err); enqueueErr != nil {
		return err
	}
	return nil
}

func (s *AuthSender) markAllowed(kind string, tenantID int64, email string, cooldown time.Duration) bool {
	if cooldown <= 0 {
		return true
	}
	key := fmt.Sprintf("%s:%d:%s", kind, tenantID, strings.ToLower(strings.TrimSpace(email)))
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	last, ok := s.lastSent[key]
	if ok && now.Sub(last) < cooldown {
		return false
	}
	s.lastSent[key] = now
	return true
}

func LoadSMTPSettings(ctx context.Context, store SettingsStore, keys SecretKeyProvider, tenantID int64) (SMTPSettings, error) {
	if store == nil || keys == nil {
		return SMTPSettings{}, ErrEmailBackendUnconfigured
	}
	raw, err := store.Load(ctx, tenantID)
	if err != nil {
		return SMTPSettings{}, err
	}
	return SettingsFromStored(ctx, raw, keys, tenantID)
}

func ValidateProductionReleaseGate(ctx context.Context, store SettingsStore, keys SecretKeyProvider) error {
	if store == nil || keys == nil {
		return ErrEmailBackendUnconfigured
	}
	tenantIDs, err := store.ListActiveTenantIDs(ctx)
	if err != nil {
		return err
	}
	if len(tenantIDs) == 0 {
		return fmt.Errorf("%w: no active tenant for production email gate", ErrEmailBackendUnconfigured)
	}
	for _, tenantID := range tenantIDs {
		settings, err := LoadSMTPSettings(ctx, store, keys, tenantID)
		if err != nil {
			return fmt.Errorf("tenant %d: %w", tenantID, err)
		}
		if !settings.VerifyEmailEnabled {
			return fmt.Errorf("%w: tenant %d requires %s=true", ErrEmailBackendUnconfigured, tenantID, SettingVerifyRequirement)
		}
	}
	return nil
}

func defaultSMTPDispatch(ctx context.Context, settings SMTPSettings, msg Message) error {
	return NewSMTPSender(settings).Send(ctx, msg)
}

func buildVerificationBody(token string) string {
	token = SanitizeHeaderValue(token)
	return `<html><body><p>Use this one-time HUAKAI verification token:</p><p><code>` + token + `</code></p><p>This token expires soon.</p></body></html>`
}

func buildPasswordResetBody(token string) string {
	token = SanitizeHeaderValue(token)
	return `<html><body><p>Use this one-time HUAKAI password reset token:</p><p><code>` + token + `</code></p><p>If you did not request this, ignore this email.</p></body></html>`
}

type VerificationPolicy struct {
	store SettingsStore
}

func NewVerificationPolicy(store SettingsStore) VerificationPolicy {
	return VerificationPolicy{store: store}
}

func (p VerificationPolicy) EmailVerificationEnabled(ctx context.Context, tenantID int64) (bool, error) {
	if p.store == nil {
		return false, ErrEmailBackendUnconfigured
	}
	raw, err := p.store.Load(ctx, tenantID)
	if err != nil {
		return false, err
	}
	return parseBool(raw[SettingVerifyRequirement]), nil
}
