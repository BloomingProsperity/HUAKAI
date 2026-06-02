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
	allowed, rollback := s.reserveCooldown("verify", user.TenantID, user.Email, s.verificationCooldown)
	if !allowed {
		return nil
	}
	err := s.sendForTenant(ctx, user.TenantID, Message{
		TenantID: user.TenantID,
		To:       user.Email,
		Subject:  "HUAKAI email verification",
		HTMLBody: buildVerificationBody(token),
	})
	if err != nil {
		// 硬失败(永久失败 / 无 outbox / enqueue 失败)= 既没发出也没入队重试 → 回滚 cooldown,
		// 否则用户首发失败后,冷却窗口内的合法重发会被静默吞掉、返回 nil 却没发任何邮件(S2-079)。
		// 发送成功或已入 DLQ 重试时 sendForTenant 返回 nil,保留 cooldown,不重复发。
		rollback()
	}
	return err
}

func (s *AuthSender) SendPasswordReset(ctx context.Context, user userauth.User, token string) error {
	if s == nil {
		return ErrEmailBackendUnconfigured
	}
	if strings.TrimSpace(token) == "" {
		return nil
	}
	allowed, rollback := s.reserveCooldown("reset", user.TenantID, user.Email, s.resetCooldown)
	if !allowed {
		return nil
	}
	err := s.sendForTenant(ctx, user.TenantID, Message{
		TenantID: user.TenantID,
		To:       user.Email,
		Subject:  "HUAKAI password reset",
		HTMLBody: buildPasswordResetBody(token),
	})
	if err != nil {
		// 见 SendVerification:硬失败回滚 cooldown,避免冷却窗口吞掉合法重发(S2-079)。
		rollback()
	}
	return err
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

// reserveCooldown 原子地检查并占用冷却窗口。返回 (allowed, rollback):
//   - allowed=false:仍在冷却期,应抑制本次发送(rollback 为 no-op)。
//   - allowed=true:已占用窗口;调用方在发送"硬失败"(既未发出也未入队重试)时调用 rollback
//     释放窗口,使下一次请求能真正发送,避免冷却窗口把首发失败后的合法重发静默吞掉(S2-079)。
//
// cooldown<=0 视为禁用冷却(始终 allowed,rollback 为 no-op)。
func (s *AuthSender) reserveCooldown(kind string, tenantID int64, email string, cooldown time.Duration) (bool, func()) {
	noop := func() {}
	if cooldown <= 0 {
		return true, noop
	}
	key := fmt.Sprintf("%s:%d:%s", kind, tenantID, strings.ToLower(strings.TrimSpace(email)))
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, had := s.lastSent[key]
	if had && now.Sub(prev) < cooldown {
		return false, noop
	}
	s.lastSent[key] = now
	return true, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		// 仅当当前标记仍是本次占用写入的值时才回滚,避免覆盖期间另一次成功发送写入的更新值。
		if cur, ok := s.lastSent[key]; ok && cur.Equal(now) {
			if had {
				s.lastSent[key] = prev
			} else {
				delete(s.lastSent, key)
			}
		}
	}
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
