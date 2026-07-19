package gatewayhttp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/authaudit"
	"github.com/BloomingProsperity/HUAKAI/internal/captcha"
	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
	mailinfra "github.com/BloomingProsperity/HUAKAI/internal/email"
	"github.com/BloomingProsperity/HUAKAI/internal/loginthrottle"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
	"github.com/BloomingProsperity/HUAKAI/internal/telegramauth"
	"github.com/BloomingProsperity/HUAKAI/internal/twofa"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

type AuthEmailSender interface {
	SendVerification(context.Context, userauth.User, string) error
	SendPasswordReset(context.Context, userauth.User, string) error
	// SendDeviceConfirmation 发新设备确认邮件 (DevicePolicy=confirm 达上限时), 携带一次性原文 token。
	SendDeviceConfirmation(context.Context, userauth.User, string) error
}

type AuthEmailSendLimiter interface {
	Allow(clientIP string) (bool, time.Duration)
}

type AuthAdminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type AuthEventSink interface {
	RecordAuthEvent(context.Context, AuthEvent)
}

type AuthTwoFactor interface {
	LoginRequired(context.Context, int64, int64) (bool, error)
	StartLoginChallenge(context.Context, int64, int64) (twofa.Challenge, error)
	VerifyLoginChallenge(context.Context, twofa.ChallengeVerifyInput) (twofa.VerifyResult, error)
}

type AuthTwoFactorSettings interface {
	Get(context.Context, platformsettings.SettingKey) (platformsettings.StoredSetting, error)
}
type AuthEvent struct {
	EventType       string `json:"event_type"`
	TenantID        int64  `json:"tenant_id,omitempty"`
	UserID          int64  `json:"user_id,omitempty"`
	IP              string `json:"ip,omitempty"`
	UserAgent       string `json:"user_agent,omitempty"`
	Provider        string `json:"provider,omitempty"`
	Outcome         string `json:"outcome"`
	ReasonClass     string `json:"reason_class,omitempty"`
	AuthMethod      string `json:"auth_method,omitempty"`
	SessionPolicy   string `json:"session_policy,omitempty"`
	SessionsRevoked int64  `json:"sessions_revoked,omitempty"`
}
type NoopAuthEmailSender struct{}

func (NoopAuthEmailSender) SendVerification(context.Context, userauth.User, string) error { return nil }
func (NoopAuthEmailSender) SendPasswordReset(context.Context, userauth.User, string) error {
	return nil
}
func (NoopAuthEmailSender) SendDeviceConfirmation(context.Context, userauth.User, string) error {
	return nil
}

type AuthHandlerDeps struct {
	Auth              *userauth.Service
	Sessions          *usersession.Service
	EmailSender       AuthEmailSender
	EmailSendLimiter  AuthEmailSendLimiter
	AdminAuth         AuthAdminAuth
	EventSink         AuthEventSink
	ClientIPResolver  *clientip.Resolver
	Captcha           captcha.CaptchaVerifier
	TwoFactor         AuthTwoFactor
	TwoFactorSettings AuthTwoFactorSettings
	// LoginThrottle 是密码登录的「argon2 前置」IP 限流闸。nil = 不限流(测试/旧装配),
	// 生产装配必须注入,否则未认证攻击者可对任意邮箱触发昂贵 argon2 放大 CPU。
	LoginThrottle *loginthrottle.Limiter
	// TelegramBotToken 是静态 bot token(测试/旧装配直接注入)。生产走 TelegramBotTokenResolver
	// 请求期读后台设置(settings-first,空则回退 env),使运营在管理台改 token 即生效、不重部署。
	TelegramBotToken         string
	TelegramBotTokenResolver func(context.Context) string
	TelegramWidgetMaxAge     time.Duration
	// OAuthPendingKey 是「社交登录无邮箱→补邮箱」流程 pending/challenge token 的 HMAC 密钥
	//(由会话签名密钥域分隔派生,见 DeriveOAuthPendingKey)。空 = 补邮箱流程停用(端点返 503)。
	OAuthPendingKey []byte
}

// resolveTelegramBotToken 请求期解析 bot token:优先 resolver(后台设置 settings-first),否则静态字段。
func (d AuthHandlerDeps) resolveTelegramBotToken(ctx context.Context) string {
	if d.TelegramBotTokenResolver != nil {
		if v := strings.TrimSpace(d.TelegramBotTokenResolver(ctx)); v != "" {
			return v
		}
	}
	return d.TelegramBotToken
}

type authRegisterRequest struct {
	TenantID     int64  `json:"tenant_id"`
	Email        string `json:"email"`
	DisplayName  string `json:"display_name,omitempty"`
	Password     string `json:"password"`
	InviteCode   string `json:"invite_code,omitempty"`
	CaptchaToken string `json:"captcha_token,omitempty"`
}

type authLoginRequest struct {
	TenantID     int64          `json:"tenant_id"`
	Email        string         `json:"email"`
	Password     string         `json:"password"`
	DeviceInfo   map[string]any `json:"device_info,omitempty"`
	CaptchaToken string         `json:"captcha_token,omitempty"`
}

type authTwoFactorLoginRequest struct {
	ChallengeID string         `json:"challenge_id"`
	Code        string         `json:"code"`
	DeviceInfo  map[string]any `json:"device_info,omitempty"`
}

type authVerifyEmailRequest struct {
	TenantID int64  `json:"tenant_id"`
	Token    string `json:"token"`
}

type authConfirmDeviceRequest struct {
	TenantID int64  `json:"tenant_id"`
	Token    string `json:"token"`
}

type authResetPasswordRequest struct {
	TenantID    int64  `json:"tenant_id"`
	Email       string `json:"email,omitempty"`
	Token       string `json:"token,omitempty"`
	NewPassword string `json:"new_password,omitempty"`
}

type authOAuthInitRequest struct {
	TenantID    int64  `json:"tenant_id"`
	Provider    string `json:"provider"`
	RedirectURI string `json:"redirect_uri,omitempty"`
}

type authOAuthCallbackRequest struct {
	TenantID   int64          `json:"tenant_id"`
	Provider   string         `json:"provider"`
	State      string         `json:"state"`
	Code       string         `json:"code"`
	DeviceInfo map[string]any `json:"device_info,omitempty"`
}

type authTelegramLoginRequest struct {
	TenantID   int64             `json:"tenant_id"`
	Params     map[string]string `json:"params"`
	DeviceInfo map[string]any    `json:"device_info,omitempty"`
}

type authSocialIdentityChangedRequest struct {
	TenantID   int64  `json:"tenant_id"`
	UserID     int64  `json:"user_id"`
	Provider   string `json:"provider"`
	Subject    string `json:"subject,omitempty"`
	ChangeType string `json:"change_type,omitempty"`
}

const (
	oauthStateCookieName   = "huakai_oauth_state"
	oauthStateCookieMaxAge = 600
	oauthInitRouteSuffix   = "/oauth-init"
	oauthCallbackRoutePath = "/oauth-callback"
)

func MountAuthRoutes(r chi.Router, d AuthHandlerDeps) {
	r.Post("/register", newAuthRegisterHandler(d))
	r.Post("/login", newAuthLoginHandler(d))
	r.Post("/login/2fa", newAuthTwoFactorLoginHandler(d))
	r.Post("/verify-email", newAuthVerifyEmailHandler(d))
	r.Post("/confirm-device", newAuthConfirmDeviceHandler(d))
	r.Post("/reset-password", newAuthResetPasswordHandler(d))
	r.Post("/oauth-init", newAuthOAuthInitHandler(d))
	r.Post("/oauth-callback", newAuthOAuthCallbackHandler(d))
	// 「社交登录无已验证邮箱 → 补邮箱建号」两步(发码/验码)由 oauthpendinghttp 独立包挂载,
	// 见 cmd/gateway 装配(mountAuthPendingRoutes)。
	r.Post("/telegram-login", newAuthTelegramLoginHandler(d))
	r.Post("/social/identity-changed", newAuthSocialIdentityChangedHandler(d))
}

func newAuthRegisterHandler(d AuthHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "auth dependency unset")
			return
		}
		var req authRegisterRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		if !verifyAuthCaptcha(
			w, r, d,
			req.TenantID,
			req.CaptchaToken,
			"user_register_failed",
			"",
		) {
			return
		}
		result, err := d.Auth.Register(r.Context(), userauth.RegisterInput{
			TenantID: req.TenantID, Email: req.Email, DisplayName: req.DisplayName,
			Password: req.Password, InviteCode: req.InviteCode,
		})
		if err != nil {
			recordAuthEvent(r.Context(), d.EventSink, r, d.ClientIPResolver, AuthEvent{
				EventType: "user_register_failed", TenantID: req.TenantID, Outcome: "failure", ReasonClass: authReasonClass(err),
			})
			writeAuthError(w, err)
			return
		}
		if result.VerificationToken != "" {
			if !allowAuthEmailSend(w, r, d, result.User.TenantID, result.User.ID) {
				return
			}
			sender := authEmailSender(d)
			if err := sender.SendVerification(r.Context(), result.User, result.VerificationToken); err != nil {
				writeAuthEmailError(w, err, "verification email could not be queued")
				return
			}
		}
		resp := map[string]any{
			"user":                  publicUser(result.User),
			"verification_required": result.VerificationToken != "",
		}
		addDevAuthToken(resp, "verification_token", result.VerificationToken)
		recordAuthEvent(r.Context(), d.EventSink, r, d.ClientIPResolver, AuthEvent{
			EventType: "user_registered", TenantID: result.User.TenantID, UserID: result.User.ID, Outcome: "success",
		})
		writeAuditJSON(w, http.StatusCreated, resp)
	}
}

func newAuthLoginHandler(d AuthHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Sessions == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "auth/session dependency unset")
			return
		}
		var req authLoginRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		// 门1: 在调用 Authenticate(会跑 argon2)之前先过 IP 限流闸。命中即 429, 绝不进 KDF —
		// 否则未认证攻击者可对任意邮箱触发昂贵 argon2(等工修复让 miss 也跑 argon2, 放大了这个面)。
		// key 用可信 client IP(body 的 tenant_id 未认证可伪造, 不进 key)。lease 在登录结果回灌:
		// 成功 Success(不计失败), 失败 Failure(累计/可能封禁); defer Cancel 兜底 panic/早退释放在途槽。
		var lease *loginthrottle.Lease
		if d.LoginThrottle != nil {
			ip := d.ClientIPResolver.ClientIP(r)
			var dec loginthrottle.Decision
			var throttleErr error
			lease, dec, throttleErr = d.LoginThrottle.BeginContext(r.Context(), ip)
			defer func() {
				if err := commitLoginThrottle(r.Context(), lease, (*loginthrottle.Lease).CancelContext); err != nil {
					slog.WarnContext(r.Context(), "释放登录共享限流槽失败", "error", err.Error())
				}
			}()
			if throttleErr != nil {
				recordAuthEvent(r.Context(), d.EventSink, r, d.ClientIPResolver, AuthEvent{
					EventType: "user_login_failed", TenantID: req.TenantID, Outcome: "failure", ReasonClass: "login_throttle_unavailable", AuthMethod: "password",
				})
				writeJSONError(w, http.StatusServiceUnavailable, "login_throttle_unavailable", "login admission is temporarily unavailable")
				return
			}
			if !dec.Allowed {
				recordAuthEvent(r.Context(), d.EventSink, r, d.ClientIPResolver, AuthEvent{
					EventType: "user_login_failed", TenantID: req.TenantID, Outcome: "failure", ReasonClass: "login_rate_limited", AuthMethod: "password",
				})
				writeLoginThrottled(w, dec.RetryAfter)
				return
			}
		}
		if !verifyAuthCaptcha(
			w, r, d,
			req.TenantID,
			req.CaptchaToken,
			"user_login_failed",
			"password",
		) {
			if err := commitLoginThrottle(r.Context(), lease, (*loginthrottle.Lease).FailureContext); err != nil {
				slog.WarnContext(r.Context(), "记录登录共享限流失败结果失败", "error", err.Error())
			}
			return
		}
		user, err := d.Auth.Authenticate(r.Context(), userauth.LoginInput{TenantID: req.TenantID, Email: req.Email, Password: req.Password})
		if err != nil {
			if throttleErr := commitLoginThrottle(r.Context(), lease, (*loginthrottle.Lease).FailureContext); throttleErr != nil {
				slog.WarnContext(r.Context(), "记录登录共享限流失败结果失败", "error", throttleErr.Error())
			}
			// 审计记录真实 reason(操作员可见), 但对外统一 generic, 杜绝状态码/消息枚举(门2)。
			recordAuthEvent(r.Context(), d.EventSink, r, d.ClientIPResolver, AuthEvent{
				EventType: "user_login_failed", TenantID: req.TenantID, Outcome: "failure", ReasonClass: authReasonClass(err), AuthMethod: "password",
			})
			writeLoginFailureGeneric(w, err)
			return
		}
		if err := commitLoginThrottle(r.Context(), lease, (*loginthrottle.Lease).SuccessContext); err != nil {
			slog.WarnContext(r.Context(), "释放登录共享限流成功槽失败", "error", err.Error())
		}
		required, err := authTwoFactorRequired(r.Context(), d, user.TenantID, user.ID)
		if err != nil {
			recordAuthEvent(r.Context(), d.EventSink, r, d.ClientIPResolver, AuthEvent{
				EventType: "user_login_2fa_failed", TenantID: user.TenantID, UserID: user.ID, Outcome: "failure",
				ReasonClass: twoFactorReasonClass(err), AuthMethod: "password",
			})
			writeTwoFactorLoginError(w, err)
			return
		}
		if required {
			challenge, err := d.TwoFactor.StartLoginChallenge(r.Context(), user.TenantID, user.ID)
			if err != nil {
				recordAuthEvent(r.Context(), d.EventSink, r, d.ClientIPResolver, AuthEvent{
					EventType: "user_login_2fa_failed", TenantID: user.TenantID, UserID: user.ID, Outcome: "failure",
					ReasonClass: twoFactorReasonClass(err), AuthMethod: "password",
				})
				writeTwoFactorLoginError(w, err)
				return
			}
			recordAuthEvent(r.Context(), d.EventSink, r, d.ClientIPResolver, AuthEvent{
				EventType: "user_login_2fa_required", TenantID: user.TenantID, UserID: user.ID, Outcome: "challenge", AuthMethod: "password",
			})
			writeAuditJSON(w, http.StatusAccepted, map[string]any{
				"user":                 publicUser(user),
				"two_factor_required":  true,
				"challenge_id":         challenge.ID,
				"challenge_expires_at": challenge.ExpiresAt,
			})
			return
		}
		tokens, err := d.Sessions.Create(r.Context(), usersession.CreateInput{
			TenantID: user.TenantID, UserID: user.ID, DeviceInfo: req.DeviceInfo,
			IP: d.ClientIPResolver.ClientIP(r), UserAgent: r.UserAgent(), AuthMethod: "password",
		})
		if err != nil {
			recordAuthEvent(r.Context(), d.EventSink, r, d.ClientIPResolver, AuthEvent{
				EventType: "user_login_session_failed", TenantID: user.TenantID, UserID: user.ID, Outcome: "failure",
				ReasonClass: sessionReasonClass(err), AuthMethod: "password",
			})
			if handleDeviceConfirmationRequired(w, r, d, user, err) {
				return
			}
			writeSessionError(w, err)
			return
		}
		recordAuthEvent(r.Context(), d.EventSink, r, d.ClientIPResolver, AuthEvent{
			EventType: "user_login_succeeded", TenantID: user.TenantID, UserID: user.ID, Outcome: "success", AuthMethod: "password",
		})
		writeAuditJSON(w, http.StatusOK, map[string]any{"user": publicUser(user), "session": tokens})
	}
}

const loginThrottleCommitTimeout = 2 * time.Second

func commitLoginThrottle(
	requestCtx context.Context,
	lease *loginthrottle.Lease,
	commit func(*loginthrottle.Lease, context.Context) error,
) error {
	if lease == nil || commit == nil {
		return nil
	}
	parent := context.Background()
	if requestCtx != nil {
		parent = context.WithoutCancel(requestCtx)
	}
	commitCtx, cancel := context.WithTimeout(parent, loginThrottleCommitTimeout)
	defer cancel()
	return commit(lease, commitCtx)
}

func newAuthTwoFactorLoginHandler(d AuthHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Sessions == nil || d.TwoFactor == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "auth/session dependency unset")
			return
		}
		var req authTwoFactorLoginRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		if strings.TrimSpace(req.ChallengeID) == "" || strings.TrimSpace(req.Code) == "" {
			writeJSONError(w, http.StatusBadRequest, "invalid_two_factor_request", "challenge_id and code are required")
			return
		}
		result, err := d.TwoFactor.VerifyLoginChallenge(r.Context(), twofa.ChallengeVerifyInput{
			ChallengeID: req.ChallengeID,
			Code:        req.Code,
		})
		if err != nil {
			recordAuthEvent(r.Context(), d.EventSink, r, d.ClientIPResolver, AuthEvent{
				EventType: "user_login_2fa_failed", TenantID: result.TenantID, UserID: result.UserID,
				Outcome: "failure", ReasonClass: twoFactorReasonClass(err), AuthMethod: "password+2fa",
			})
			writeTwoFactorLoginError(w, err)
			return
		}
		authMethod := "password+" + result.Method
		// 账号资格门:challenge 签发(密码对)到此刻之间账号可能被封禁/锁定/删除,
		// 签发会话前必须复核(与 passkey/social 登录同一道门), 否则 challenge 窗口
		// 成为停用控制的绕过口。对外统一 403 account_not_active, 不泄露具体状态。
		if user, err := d.Auth.GetProfile(r.Context(), result.TenantID, result.UserID); err != nil || userauth.EnsureLoginEligible(user, time.Now()) != nil {
			if err == nil {
				err = userauth.EnsureLoginEligible(user, time.Now())
			}
			recordAuthEvent(r.Context(), d.EventSink, r, d.ClientIPResolver, AuthEvent{
				EventType: "user_login_2fa_failed", TenantID: result.TenantID, UserID: result.UserID,
				Outcome: "failure", ReasonClass: authReasonClass(err), AuthMethod: authMethod,
			})
			switch {
			case errors.Is(err, userauth.ErrUserNotFound),
				errors.Is(err, userauth.ErrUserDisabled),
				errors.Is(err, userauth.ErrUserLocked),
				errors.Is(err, userauth.ErrPasswordResetRequired):
				writeJSONError(w, http.StatusForbidden, "account_not_active", "account is no longer active")
			default:
				writeJSONError(w, http.StatusServiceUnavailable, "auth_backend_error", "auth backend transient failure")
			}
			return
		}
		tokens, err := d.Sessions.Create(r.Context(), usersession.CreateInput{
			TenantID: result.TenantID, UserID: result.UserID, DeviceInfo: req.DeviceInfo,
			IP: d.ClientIPResolver.ClientIP(r), UserAgent: r.UserAgent(), AuthMethod: authMethod,
		})
		if err != nil {
			recordAuthEvent(r.Context(), d.EventSink, r, d.ClientIPResolver, AuthEvent{
				EventType: "user_login_session_failed", TenantID: result.TenantID, UserID: result.UserID, Outcome: "failure",
				ReasonClass: sessionReasonClass(err), AuthMethod: authMethod,
			})
			if handleDeviceConfirmationRequiredByID(w, r, d, result.TenantID, result.UserID, err) {
				return
			}
			writeSessionError(w, err)
			return
		}
		recordAuthEvent(r.Context(), d.EventSink, r, d.ClientIPResolver, AuthEvent{
			EventType: "user_login_succeeded", TenantID: result.TenantID, UserID: result.UserID, Outcome: "success", AuthMethod: authMethod,
		})
		writeAuditJSON(w, http.StatusOK, map[string]any{"session": tokens})
	}
}

func authTwoFactorRequired(ctx context.Context, d AuthHandlerDeps, tenantID, userID int64) (bool, error) {
	if d.TwoFactorSettings == nil {
		return false, nil
	}
	setting, err := d.TwoFactorSettings.Get(ctx, platformsettings.KeyTwoFactorEnabled)
	if err != nil || strings.TrimSpace(setting.Value) != "true" {
		return false, nil
	}
	if d.TwoFactor == nil {
		return false, twofa.ErrStoreNotConfigured
	}
	return d.TwoFactor.LoginRequired(ctx, tenantID, userID)
}

func writeTwoFactorLoginError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, twofa.ErrInvalidInput):
		writeJSONError(w, http.StatusBadRequest, "invalid_two_factor_request", "two-factor request is invalid")
	case errors.Is(err, twofa.ErrInvalidCode), errors.Is(err, twofa.ErrChallengeInvalid), errors.Is(err, twofa.ErrChallengeExpired):
		writeJSONError(w, http.StatusUnauthorized, "two_factor_invalid", "two-factor challenge or code is invalid")
	case errors.Is(err, twofa.ErrCodeReused):
		// 码有效但已被消费过(防重放):按校验失败返 401,用独立 code 让前端提示"该验证码
		// 已使用过,请用下一个",而不是落到默认 503 backend_error。
		writeJSONError(w, http.StatusUnauthorized, "two_factor_code_reused", "two-factor code has already been used")
	case errors.Is(err, twofa.ErrLocked):
		writeJSONError(w, http.StatusTooManyRequests, "two_factor_locked", "two-factor verification is temporarily locked")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "two_factor_backend_error", "two-factor service unavailable")
	}
}

func twoFactorReasonClass(err error) string {
	switch {
	case errors.Is(err, twofa.ErrInvalidCode):
		return "two_factor_invalid"
	case errors.Is(err, twofa.ErrCodeReused):
		return "two_factor_code_reused"
	case errors.Is(err, twofa.ErrLocked):
		return "two_factor_locked"
	case errors.Is(err, twofa.ErrChallengeInvalid), errors.Is(err, twofa.ErrChallengeExpired):
		return "two_factor_challenge_invalid"
	case errors.Is(err, twofa.ErrDisabled):
		return "two_factor_disabled"
	default:
		return "two_factor_backend_error"
	}
}

func verifyAuthCaptcha(
	w http.ResponseWriter,
	r *http.Request,
	d AuthHandlerDeps,
	tenantID int64,
	token string,
	eventType string,
	authMethod string,
) bool {
	if d.Captcha == nil {
		return true
	}
	if err := d.Captcha.Verify(
		r.Context(),
		token,
		d.ClientIPResolver.ClientIP(r),
	); err == nil {
		return true
	}
	recordAuthEvent(r.Context(), d.EventSink, r, d.ClientIPResolver, AuthEvent{
		EventType: eventType, TenantID: tenantID, Outcome: "failure",
		ReasonClass: "captcha_failed", AuthMethod: authMethod,
	})
	writeJSONError(w, http.StatusForbidden, "captcha_required", "captcha verification failed")
	return false
}

func newAuthVerifyEmailHandler(d AuthHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "auth dependency unset")
			return
		}
		var req authVerifyEmailRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		user, err := d.Auth.VerifyEmail(r.Context(), req.TenantID, req.Token)
		if err != nil {
			writeAuthError(w, err)
			return
		}
		writeAuditJSON(w, http.StatusOK, map[string]any{"user": publicUser(user), "email_verified": true})
	}
}

func newAuthResetPasswordHandler(d AuthHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "auth dependency unset")
			return
		}
		var req authResetPasswordRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		if strings.TrimSpace(req.Token) == "" {
			result, err := d.Auth.RequestPasswordReset(r.Context(), userauth.PasswordResetRequest{TenantID: req.TenantID, Email: req.Email})
			if err != nil {
				recordAuthEvent(r.Context(), d.EventSink, r, d.ClientIPResolver, AuthEvent{
					EventType: "user_password_reset_failed", TenantID: req.TenantID, Outcome: "failure", ReasonClass: authReasonClass(err),
				})
				writeAuthError(w, err)
				return
			}
			if result.UserID != 0 && result.Token != "" {
				if !allowAuthEmailSend(w, r, d, req.TenantID, result.UserID) {
					return
				}
				user, err := d.Auth.Store.GetUserByID(r.Context(), req.TenantID, result.UserID)
				if err != nil {
					writeAuthError(w, err)
					return
				}
				if sender := authEmailSender(d); sender != nil {
					if err := sender.SendPasswordReset(r.Context(), user, result.Token); err != nil {
						recordAuthEvent(r.Context(), d.EventSink, r, d.ClientIPResolver, AuthEvent{
							EventType: "user_password_reset_failed", TenantID: req.TenantID, UserID: result.UserID,
							Outcome: "failure", ReasonClass: "email_delivery_failed",
						})
						writeAuthEmailError(w, err, "password reset email could not be queued")
						return
					}
				}
			}
			resp := map[string]any{"reset_requested": true}
			addDevAuthToken(resp, "reset_token", result.Token)
			recordAuthEvent(r.Context(), d.EventSink, r, d.ClientIPResolver, AuthEvent{
				EventType: "user_password_reset_requested", TenantID: req.TenantID, UserID: result.UserID, Outcome: "success",
			})
			writeAuditJSON(w, http.StatusAccepted, resp)
			return
		}
		if d.Sessions == nil || d.Sessions.Store == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "session dependency unset")
			return
		}
		confirm := userauth.PasswordResetConfirm{
			TenantID: req.TenantID, Token: req.Token, NewPassword: req.NewPassword,
		}
		subject, err := d.Auth.PreparePasswordReset(r.Context(), confirm)
		if err != nil {
			recordAuthEvent(r.Context(), d.EventSink, r, d.ClientIPResolver, AuthEvent{
				EventType: "user_password_reset_failed", TenantID: req.TenantID, Outcome: "failure", ReasonClass: authReasonClass(err),
			})
			writeAuthError(w, err)
			return
		}
		// PreparePasswordReset 会校验 token，并在吊销之前把该用户挡在一道登录屏障之后，
		// 这样在 revoke 与 commit 之间，旧密码登录无法创建出新的 session。
		revoked, revErr := d.Sessions.Revoke(r.Context(), usersession.RevokeInput{
			TenantID: subject.TenantID, UserID: subject.ID, Reason: "password_reset",
		})
		if revErr != nil {
			logInternalError(r.Context(), "", "user_password_reset_session_revoke_failed", revErr)
			recordAuthEvent(r.Context(), d.EventSink, r, d.ClientIPResolver, AuthEvent{
				EventType: "user_password_reset_session_revoke_failed", TenantID: subject.TenantID, UserID: subject.ID,
				Outcome: "failure", ReasonClass: sessionReasonClass(revErr), SessionPolicy: "failed", SessionsRevoked: revoked,
			})
			writeSessionError(w, revErr)
			return
		}
		user, err := d.Auth.ResetPassword(r.Context(), confirm)
		if err != nil {
			recordAuthEvent(r.Context(), d.EventSink, r, d.ClientIPResolver, AuthEvent{
				EventType: "user_password_reset_failed", TenantID: req.TenantID, Outcome: "failure", ReasonClass: authReasonClass(err),
			})
			writeAuthError(w, err)
			return
		}
		postRevoked, postErr := d.Sessions.Revoke(r.Context(), usersession.RevokeInput{
			TenantID: user.TenantID, UserID: user.ID, Reason: "password_reset",
		})
		sessionRevocation := "revoked"
		if postErr != nil {
			sessionRevocation = "failed"
			logInternalError(r.Context(), "", "user_password_reset_session_revoke_failed", postErr)
			recordAuthEvent(r.Context(), d.EventSink, r, d.ClientIPResolver, AuthEvent{
				EventType: "user_password_reset_session_revoke_failed", TenantID: user.TenantID, UserID: user.ID,
				Outcome: "failure", ReasonClass: sessionReasonClass(postErr), SessionPolicy: "failed", SessionsRevoked: revoked,
			})
		} else {
			revoked += postRevoked
		}
		recordAuthEvent(r.Context(), d.EventSink, r, d.ClientIPResolver, AuthEvent{
			EventType: "user_password_reset_completed", TenantID: user.TenantID, UserID: user.ID,
			Outcome: "success", SessionPolicy: sessionRevocation, SessionsRevoked: revoked,
		})
		writeAuditJSON(w, http.StatusOK, map[string]any{"user": publicUser(user), "sessions_revoked": revoked, "session_revocation": sessionRevocation})
	}
}

func newAuthOAuthInitHandler(d AuthHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "auth dependency unset")
			return
		}
		var req authOAuthInitRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		result, err := d.Auth.StartOAuth(r.Context(), userauth.OAuthInitInput{
			TenantID: req.TenantID, Provider: req.Provider, RedirectURI: req.RedirectURI,
		})
		if err != nil {
			recordAuthEvent(r.Context(), d.EventSink, r, d.ClientIPResolver, AuthEvent{
				EventType: "user_social_login_failed", TenantID: req.TenantID, Provider: safeProviderForEvent(req.Provider),
				Outcome: "failure", ReasonClass: authReasonClass(err), AuthMethod: safeProviderForEvent(req.Provider),
			})
			writeAuthError(w, err)
			return
		}
		setOAuthStateCookie(w, r, result.State)
		writeAuditJSON(w, http.StatusCreated, result)
	}
}

func newAuthTelegramLoginHandler(d AuthHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Sessions == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "auth/session dependency unset")
			return
		}
		botToken := d.resolveTelegramBotToken(r.Context())
		if strings.TrimSpace(botToken) == "" {
			writeAuthError(w, userauth.ErrOAuthProviderMissing)
			return
		}
		var req authTelegramLoginRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		identity, err := telegramauth.VerifyWidget(req.Params, botToken, d.Auth.Clock(), telegramWidgetMaxAge(d))
		if err != nil {
			recordAuthEvent(r.Context(), d.EventSink, r, d.ClientIPResolver, AuthEvent{
				EventType: "user_social_login_failed", TenantID: req.TenantID, Provider: userauth.SocialProviderTelegram,
				Outcome: "failure", ReasonClass: authReasonClass(err), AuthMethod: userauth.SocialProviderTelegram,
			})
			writeAuthError(w, err)
			return
		}
		user, err := d.Auth.ApplyVerifiedSocialIdentity(r.Context(), req.TenantID, identity)
		if err != nil {
			recordAuthEvent(r.Context(), d.EventSink, r, d.ClientIPResolver, AuthEvent{
				EventType: "user_social_login_failed", TenantID: req.TenantID, Provider: userauth.SocialProviderTelegram,
				Outcome: "failure", ReasonClass: authReasonClass(err), AuthMethod: userauth.SocialProviderTelegram,
			})
			writeAuthError(w, err)
			return
		}
		tokens, err := d.Sessions.Create(r.Context(), usersession.CreateInput{
			TenantID: user.TenantID, UserID: user.ID, DeviceInfo: req.DeviceInfo,
			IP: d.ClientIPResolver.ClientIP(r), UserAgent: r.UserAgent(), AuthMethod: userauth.SocialProviderTelegram,
		})
		if err != nil {
			recordAuthEvent(r.Context(), d.EventSink, r, d.ClientIPResolver, AuthEvent{
				EventType: "user_social_login_session_failed", TenantID: user.TenantID, UserID: user.ID,
				Provider: userauth.SocialProviderTelegram, Outcome: "failure", ReasonClass: sessionReasonClass(err),
				AuthMethod: userauth.SocialProviderTelegram,
			})
			if handleDeviceConfirmationRequired(w, r, d, user, err) {
				return
			}
			writeSessionError(w, err)
			return
		}
		recordAuthEvent(r.Context(), d.EventSink, r, d.ClientIPResolver, AuthEvent{
			EventType: "user_social_login_succeeded", TenantID: user.TenantID, UserID: user.ID,
			Provider: userauth.SocialProviderTelegram, Outcome: "success", AuthMethod: userauth.SocialProviderTelegram,
		})
		writeAuditJSON(w, http.StatusOK, map[string]any{"user": publicUser(user), "session": tokens})
	}
}

func telegramWidgetMaxAge(d AuthHandlerDeps) time.Duration {
	if d.TelegramWidgetMaxAge > 0 {
		return d.TelegramWidgetMaxAge
	}
	return 24 * time.Hour
}

func setOAuthStateCookie(w http.ResponseWriter, r *http.Request, state string) {
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookieName,
		Value:    state,
		Path:     oauthCallbackCookiePath(r),
		MaxAge:   oauthStateCookieMaxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func requireOAuthStateCookie(w http.ResponseWriter, r *http.Request, state string) error {
	cookie, err := r.Cookie(oauthStateCookieName)
	if err != nil || cookie.Value != state {
		return userauth.ErrOAuthFlowNotFound
	}
	clearOAuthStateCookie(w, r)
	return nil
}

func clearOAuthStateCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookieName,
		Value:    "",
		Path:     oauthCallbackCookiePath(r),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func oauthCallbackCookiePath(r *http.Request) string {
	if r != nil && r.URL != nil {
		path := r.URL.Path
		if strings.HasSuffix(path, oauthInitRouteSuffix) {
			base := strings.TrimSuffix(path, oauthInitRouteSuffix)
			if base == "" {
				return oauthCallbackRoutePath
			}
			return base + oauthCallbackRoutePath
		}
		if strings.HasSuffix(path, oauthCallbackRoutePath) {
			return path
		}
	}
	return "/v1/auth" + oauthCallbackRoutePath
}

func newAuthSocialIdentityChangedHandler(d AuthHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Auth.Store == nil || d.Sessions == nil || d.AdminAuth == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "auth/session/admin dependency unset")
			return
		}
		ident, ok := resolveAuthAdmin(w, r, d)
		if !ok {
			return
		}
		var req authSocialIdentityChangedRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		provider, ok := safeSocialProvider(req.Provider)
		if !ok || req.TenantID <= 0 || req.UserID <= 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid_auth_request", "auth request is invalid")
			return
		}
		if !adminCanAccessTenant(ident, req.TenantID) {
			writeJSONError(w, http.StatusForbidden, "admin_forbidden", "caller cannot act on this tenant scope")
			return
		}
		user, err := socialIdentityChangedUser(r.Context(), d.Auth.Store, req, provider)
		if err != nil {
			writeAuthError(w, err)
			return
		}
		reason := socialIdentityChangedReason(req.ChangeType)
		revoked, err := d.Sessions.Revoke(r.Context(), usersession.RevokeInput{
			TenantID: user.TenantID,
			UserID:   user.ID,
			Reason:   reason,
		})
		if err != nil {
			writeSessionError(w, err)
			return
		}
		recordAuthEvent(r.Context(), d.EventSink, r, d.ClientIPResolver, AuthEvent{
			EventType: "user_social_identity_changed", TenantID: user.TenantID, UserID: user.ID, Provider: provider,
			Outcome: "success", ReasonClass: reason, SessionPolicy: "revoked", SessionsRevoked: revoked,
		})
		writeAuditJSON(w, http.StatusOK, map[string]any{
			"tenant_id":        user.TenantID,
			"user_id":          user.ID,
			"provider":         provider,
			"session_policy":   "revoked",
			"reason_class":     reason,
			"sessions_revoked": revoked,
		})
	}
}

func recordAuthEvent(ctx context.Context, sink AuthEventSink, r *http.Request, ipResolver *clientip.Resolver, event AuthEvent) {
	if sink == nil {
		return
	}
	event.IP, event.UserAgent = authaudit.WithRequestSource(r, ipResolver, event.IP, event.UserAgent)
	sink.RecordAuthEvent(ctx, event)
}

func authReasonClass(err error) string {
	switch {
	case errors.Is(err, userauth.ErrInvalidInput):
		return "invalid_auth_request"
	case errors.Is(err, userauth.ErrRegistrationDisabled):
		return "registration_disabled"
	case errors.Is(err, userauth.ErrInviteRequired):
		return "invite_required"
	case errors.Is(err, userauth.ErrInviteInvalid):
		return "invite_invalid"
	case errors.Is(err, userauth.ErrEmailUnverified):
		return "email_unverified"
	case errors.Is(err, userauth.ErrUserDisabled):
		return "user_disabled"
	case errors.Is(err, userauth.ErrUserLocked):
		return "user_locked"
	case errors.Is(err, userauth.ErrPasswordResetRequired):
		return "password_reset_required"
	case errors.Is(err, userauth.ErrInvalidCredentials):
		return "invalid_credentials"
	case errors.Is(err, userauth.ErrTokenInvalid), errors.Is(err, userauth.ErrTokenExpired):
		return "auth_token_invalid"
	case errors.Is(err, userauth.ErrUserNotFound):
		return "user_not_found"
	case errors.Is(err, userauth.ErrOAuthProviderMissing):
		return "oauth_provider_not_configured"
	case errors.Is(err, userauth.ErrOAuthFlowNotFound), errors.Is(err, userauth.ErrOAuthFlowExpired):
		return "oauth_flow_invalid"
	case errors.Is(err, userauth.ErrOAuthPendingEmailRequired):
		return "oauth_pending_email_required"
	case errors.Is(err, userauth.ErrSocialLoginRejected):
		return "social_login_rejected"
	default:
		return "auth_backend_error"
	}
}

func safeProviderForEvent(provider string) string {
	out, ok := safeSocialProvider(provider)
	if !ok {
		return ""
	}
	return out
}

func resolveAuthAdmin(w http.ResponseWriter, r *http.Request, d AuthHandlerDeps) (admin.AdminIdentity, bool) {
	ident, err := d.AdminAuth.Resolve(r.Context(), r)
	if err != nil {
		if errors.Is(err, admin.ErrAdminBackend) {
			writeJSONError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
		} else {
			writeJSONError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
		}
		return admin.AdminIdentity{}, false
	}
	if ident.Role != admin.RolePlatformAdmin && ident.Role != admin.RoleTenantOperator {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "tenant scope required")
		return admin.AdminIdentity{}, false
	}
	return ident, true
}

func socialIdentityChangedUser(ctx context.Context, store userauth.Store, req authSocialIdentityChangedRequest, provider string) (userauth.User, error) {
	if subject := strings.TrimSpace(req.Subject); subject != "" {
		user, err := store.GetUserBySocialIdentity(ctx, req.TenantID, provider, subject)
		if err != nil {
			return userauth.User{}, err
		}
		if user.ID != req.UserID {
			return userauth.User{}, userauth.ErrUserNotFound
		}
		return user, nil
	}
	user, err := store.GetUserByID(ctx, req.TenantID, req.UserID)
	if err != nil {
		return userauth.User{}, err
	}
	if user.SocialLoginProvider != provider {
		return userauth.User{}, userauth.ErrUserNotFound
	}
	return user, nil
}

func safeSocialProvider(provider string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case userauth.SocialProviderGoogle:
		return userauth.SocialProviderGoogle, true
	case userauth.SocialProviderGitHub:
		return userauth.SocialProviderGitHub, true
	case userauth.SocialProviderQQ:
		return userauth.SocialProviderQQ, true
	case userauth.SocialProviderWeChat:
		return userauth.SocialProviderWeChat, true
	case userauth.SocialProviderDingTalk:
		return userauth.SocialProviderDingTalk, true
	case userauth.SocialProviderNodeSeek:
		return userauth.SocialProviderNodeSeek, true
	case userauth.SocialProviderLinuxDo:
		return userauth.SocialProviderLinuxDo, true
	case userauth.SocialProviderOIDC:
		return userauth.SocialProviderOIDC, true
	case userauth.SocialProviderDiscord:
		return userauth.SocialProviderDiscord, true
	case userauth.SocialProviderTelegram:
		return userauth.SocialProviderTelegram, true
	default:
		return "", false
	}
}

func socialIdentityChangedReason(changeType string) string {
	switch strings.ToLower(strings.TrimSpace(changeType)) {
	case "provider_password_changed":
		return "social_identity_provider_password_changed"
	case "provider_disabled":
		return "social_identity_provider_disabled"
	case "identity_unlinked":
		return "social_identity_unlinked"
	default:
		return "social_identity_changed"
	}
}

func authEmailSender(d AuthHandlerDeps) AuthEmailSender {
	if d.EmailSender != nil {
		return d.EmailSender
	}
	return NoopAuthEmailSender{}
}

func allowAuthEmailSend(w http.ResponseWriter, r *http.Request, d AuthHandlerDeps, tenantID, userID int64) bool {
	if d.EmailSendLimiter == nil {
		return true
	}
	allowed, retryAfter := d.EmailSendLimiter.Allow(d.ClientIPResolver.ClientIP(r))
	if allowed {
		return true
	}
	recordAuthEvent(r.Context(), d.EventSink, r, d.ClientIPResolver, AuthEvent{
		EventType: "auth_email_send_rate_limited", TenantID: tenantID, UserID: userID,
		Outcome: "failure", ReasonClass: "email_send_rate_limited",
	})
	writeEmailSendThrottled(w, retryAfter)
	return false
}

func writeAuthEmailError(w http.ResponseWriter, err error, message string) {
	if errors.Is(err, mailinfra.ErrEmailBackendUnconfigured) {
		writeJSONError(w, http.StatusServiceUnavailable, "EMAIL_BACKEND_UNCONFIGURED", "email backend is not configured")
		return
	}
	writeJSONError(w, http.StatusServiceUnavailable, "email_delivery_failed", message)
}

func addDevAuthToken(resp map[string]any, key string, token string) {
	if !devAuthReturnTokenEnabled() || strings.TrimSpace(token) == "" {
		return
	}
	slog.Warn("dev mode, do not enable in production", "env", "HUAKAI_DEV_AUTH_RETURN_TOKEN")
	resp[key] = token
}

func devAuthReturnTokenEnabled() bool {
	// 防御纵深:即使误设 HUAKAI_DEV_AUTH_RETURN_TOKEN=true,生产模式下也绝不把明文
	// 验证/重置令牌回写进公开响应体。启动门控 validateDevAuthTokenFlag 是权威拦截(进程直接拒启),
	// 这里在响应写出层再兜一道,确保即便运行期被改环境绕过启动检查也不泄露明文 secret。
	if strings.EqualFold(strings.TrimSpace(os.Getenv("HUAKAI_RELEASE_MODE")), "production") {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(os.Getenv("HUAKAI_DEV_AUTH_RETURN_TOKEN")), "true")
}

func publicUser(user userauth.User) map[string]any {
	return map[string]any{
		"id":                    user.ID,
		"tenant_id":             user.TenantID,
		"email":                 user.Email,
		"display_name":          user.DisplayName,
		"email_verified":        user.EmailVerified,
		"social_login_provider": user.SocialLoginProvider,
		"status":                user.Status,
		"created_at":            user.CreatedAt,
		"updated_at":            user.UpdatedAt,
	}
}

func writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, userauth.ErrInvalidInput):
		writeJSONError(w, http.StatusBadRequest, "invalid_auth_request", "auth request is invalid")
	case errors.Is(err, userauth.ErrRegistrationDisabled):
		writeJSONError(w, http.StatusForbidden, "registration_disabled", "public registration is disabled")
	case errors.Is(err, userauth.ErrInviteRequired):
		writeJSONError(w, http.StatusForbidden, "invite_required", "invite code is required")
	case errors.Is(err, userauth.ErrInviteInvalid):
		writeJSONError(w, http.StatusForbidden, "invite_invalid", "invite code is invalid or exhausted")
	case errors.Is(err, userauth.ErrEmailUnverified):
		writeJSONError(w, http.StatusForbidden, "email_unverified", "email verification is required")
	case errors.Is(err, userauth.ErrUserDisabled):
		writeJSONError(w, http.StatusForbidden, "user_disabled", "user is disabled or locked")
	case errors.Is(err, userauth.ErrUserLocked):
		writeJSONError(w, http.StatusForbidden, "user_locked", "user is locked")
	case errors.Is(err, userauth.ErrPasswordResetRequired):
		writeJSONError(w, http.StatusForbidden, "password_reset_required", "password reset is required")
	case errors.Is(err, userauth.ErrInvalidCredentials):
		writeJSONError(w, http.StatusUnauthorized, "invalid_credentials", "email or password is invalid")
	case errors.Is(err, userauth.ErrTokenInvalid), errors.Is(err, userauth.ErrTokenExpired):
		writeJSONError(w, http.StatusBadRequest, "auth_token_invalid", "token is invalid, expired, or already used")
	case errors.Is(err, userauth.ErrUserNotFound):
		writeJSONError(w, http.StatusNotFound, "user_not_found", "user was not found")
	case errors.Is(err, userauth.ErrOAuthProviderMissing):
		writeJSONError(w, http.StatusServiceUnavailable, "oauth_provider_not_configured", "oauth provider is not configured")
	case errors.Is(err, userauth.ErrOAuthFlowNotFound), errors.Is(err, userauth.ErrOAuthFlowExpired):
		writeJSONError(w, http.StatusForbidden, "oauth_flow_invalid", "oauth state is invalid or expired")
	case errors.Is(err, userauth.ErrOAuthPendingEmailRequired):
		writeJSONError(w, http.StatusAccepted, "oauth_pending_email_required", "oauth email verification is required")
	case errors.Is(err, userauth.ErrSocialLoginRejected):
		writeJSONError(w, http.StatusForbidden, "social_login_rejected", "social identity claims are not sufficient")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "auth_backend_error", "auth backend transient failure")
	}
}

// writeLoginFailureGeneric 是密码登录专用的失败响应(门2)。它把所有「与账号存在性/状态
// 相关」的认证失败 —— 口令错、账号停用、锁定、待邮箱验证、需重置 —— 对外统一成同一个 generic
// 401 invalid_credentials, 杜绝攻击者借状态码/消息差异枚举用户是否存在及其状态。真实 reason 已在
// 调用前写入审计事件(操作员可见, 用户不可见)。非枚举类错误(请求格式错/后端故障)保持区分, 不
// 影响正常排障。注意: 仅登录路径用本函数; 注册/验证/重置/OAuth 仍用 writeAuthError(那些场景的
// invite_required 等提示是正常 UX, 不构成枚举泄露)。
func writeLoginFailureGeneric(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, userauth.ErrInvalidInput):
		writeJSONError(w, http.StatusBadRequest, "invalid_auth_request", "auth request is invalid")
	case errors.Is(err, userauth.ErrInvalidCredentials),
		errors.Is(err, userauth.ErrUserDisabled),
		errors.Is(err, userauth.ErrUserLocked),
		errors.Is(err, userauth.ErrPasswordResetRequired),
		errors.Is(err, userauth.ErrEmailUnverified):
		writeJSONError(w, http.StatusUnauthorized, "invalid_credentials", "email or password is invalid")
	default:
		// ErrStoreNotConfigured 等后端异常不是枚举泄露 —— 返回后端错误便于排障。
		writeJSONError(w, http.StatusServiceUnavailable, "auth_backend_error", "auth backend transient failure")
	}
}

// writeLoginThrottled 是限流命中(门1)的响应: 429 + 粗粒度 Retry-After(秒, 已在 limiter
// 侧对齐), 不携带剩余次数/账号状态, 避免侧信道。
func writeLoginThrottled(w http.ResponseWriter, retryAfter time.Duration) {
	if retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.FormatInt(int64(retryAfter/time.Second), 10))
	}
	writeJSONError(w, http.StatusTooManyRequests, "too_many_attempts", "too many login attempts; please retry later")
}

// writeEmailSendThrottled 是鉴权邮件发送限流器的响应：429 + 粗粒度的
// Retry-After，不暴露 per-email 状态或剩余配额。
func writeEmailSendThrottled(w http.ResponseWriter, retryAfter time.Duration) {
	if retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.FormatInt(int64(retryAfter/time.Second), 10))
	}
	writeJSONError(w, http.StatusTooManyRequests, "too_many_email_sends", "too many email send attempts; please retry later")
}
