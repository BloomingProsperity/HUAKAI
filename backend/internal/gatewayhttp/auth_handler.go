package gatewayhttp

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	mailinfra "github.com/BloomingProsperity/HUAKAI/internal/email"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

type AuthEmailSender interface {
	SendVerification(context.Context, userauth.User, string) error
	SendPasswordReset(context.Context, userauth.User, string) error
}

type AuthAdminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type AuthEventSink interface {
	RecordAuthEvent(context.Context, AuthEvent)
}

type AuthEvent struct {
	EventType       string `json:"event_type"`
	TenantID        int64  `json:"tenant_id,omitempty"`
	UserID          int64  `json:"user_id,omitempty"`
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

type AuthHandlerDeps struct {
	Auth        *userauth.Service
	Sessions    *usersession.Service
	EmailSender AuthEmailSender
	AdminAuth   AuthAdminAuth
	EventSink   AuthEventSink
}

type authRegisterRequest struct {
	TenantID    int64  `json:"tenant_id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name,omitempty"`
	Password    string `json:"password"`
	InviteCode  string `json:"invite_code,omitempty"`
}

type authLoginRequest struct {
	TenantID   int64          `json:"tenant_id"`
	Email      string         `json:"email"`
	Password   string         `json:"password"`
	DeviceInfo map[string]any `json:"device_info,omitempty"`
}

type authVerifyEmailRequest struct {
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

type authSocialIdentityChangedRequest struct {
	TenantID   int64  `json:"tenant_id"`
	UserID     int64  `json:"user_id"`
	Provider   string `json:"provider"`
	Subject    string `json:"subject,omitempty"`
	ChangeType string `json:"change_type,omitempty"`
}

func MountAuthRoutes(r chi.Router, d AuthHandlerDeps) {
	r.Post("/register", newAuthRegisterHandler(d))
	r.Post("/login", newAuthLoginHandler(d))
	r.Post("/verify-email", newAuthVerifyEmailHandler(d))
	r.Post("/reset-password", newAuthResetPasswordHandler(d))
	r.Post("/oauth-init", newAuthOAuthInitHandler(d))
	r.Post("/oauth-callback", newAuthOAuthCallbackHandler(d))
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
		result, err := d.Auth.Register(r.Context(), userauth.RegisterInput{
			TenantID: req.TenantID, Email: req.Email, DisplayName: req.DisplayName,
			Password: req.Password, InviteCode: req.InviteCode,
		})
		if err != nil {
			recordAuthEvent(r.Context(), d.EventSink, AuthEvent{
				EventType: "user_register_failed", TenantID: req.TenantID, Outcome: "failure", ReasonClass: authReasonClass(err),
			})
			writeAuthError(w, err)
			return
		}
		if result.VerificationToken != "" {
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
		recordAuthEvent(r.Context(), d.EventSink, AuthEvent{
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
		user, err := d.Auth.Authenticate(r.Context(), userauth.LoginInput{TenantID: req.TenantID, Email: req.Email, Password: req.Password})
		if err != nil {
			recordAuthEvent(r.Context(), d.EventSink, AuthEvent{
				EventType: "user_login_failed", TenantID: req.TenantID, Outcome: "failure", ReasonClass: authReasonClass(err), AuthMethod: "password",
			})
			writeAuthError(w, err)
			return
		}
		tokens, err := d.Sessions.Create(r.Context(), usersession.CreateInput{
			TenantID: user.TenantID, UserID: user.ID, DeviceInfo: req.DeviceInfo,
			IP: clientIP(r), UserAgent: r.UserAgent(), AuthMethod: "password",
		})
		if err != nil {
			recordAuthEvent(r.Context(), d.EventSink, AuthEvent{
				EventType: "user_login_session_failed", TenantID: user.TenantID, UserID: user.ID, Outcome: "failure",
				ReasonClass: sessionReasonClass(err), AuthMethod: "password",
			})
			writeSessionError(w, err)
			return
		}
		recordAuthEvent(r.Context(), d.EventSink, AuthEvent{
			EventType: "user_login_succeeded", TenantID: user.TenantID, UserID: user.ID, Outcome: "success", AuthMethod: "password",
		})
		writeAuditJSON(w, http.StatusOK, map[string]any{"user": publicUser(user), "session": tokens})
	}
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
				recordAuthEvent(r.Context(), d.EventSink, AuthEvent{
					EventType: "user_password_reset_failed", TenantID: req.TenantID, Outcome: "failure", ReasonClass: authReasonClass(err),
				})
				writeAuthError(w, err)
				return
			}
			if result.UserID != 0 && result.Token != "" {
				user, err := d.Auth.Store.GetUserByID(r.Context(), req.TenantID, result.UserID)
				if err != nil {
					writeAuthError(w, err)
					return
				}
				if sender := authEmailSender(d); sender != nil {
					if err := sender.SendPasswordReset(r.Context(), user, result.Token); err != nil {
						recordAuthEvent(r.Context(), d.EventSink, AuthEvent{
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
			recordAuthEvent(r.Context(), d.EventSink, AuthEvent{
				EventType: "user_password_reset_requested", TenantID: req.TenantID, UserID: result.UserID, Outcome: "success",
			})
			writeAuditJSON(w, http.StatusAccepted, resp)
			return
		}
		// S1-028: 密码重置必须能吊销既有会话,否则被盗/旧会话在改密后仍存活。在改密前即要求 session
		// 依赖**且后端 store 已配置**:仅判 service 指针非 nil 不够——Store 未注入的服务(NewService(nil))
		// 会让 Revoke 在改密、token 已消费之后才以 ErrStoreNotConfigured 失败,落入"无法吊销"的危险半成态。
		// 故把这类静态可检测的配置缺失在改密前 fail-closed 拦下。
		if d.Sessions == nil || d.Sessions.Store == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "session dependency unset")
			return
		}
		user, err := d.Auth.ResetPassword(r.Context(), userauth.PasswordResetConfirm{
			TenantID: req.TenantID, Token: req.Token, NewPassword: req.NewPassword,
		})
		if err != nil {
			recordAuthEvent(r.Context(), d.EventSink, AuthEvent{
				EventType: "user_password_reset_failed", TenantID: req.TenantID, Outcome: "failure", ReasonClass: authReasonClass(err),
			})
			writeAuthError(w, err)
			return
		}
		// S1-028: 密码已原子改成且 reset token 已被 ConsumePasswordResetToken 一并消费——重置本身已成功
		// 且无法用同一 token 重试。会话吊销是其后的尽力清理:失败时(a)绝不能谎报"已吊销"(原缺陷:
		// revoked,_ 吞错且永远写 SessionPolicy=revoked);(b)也绝不能把"已成功、token 已消费"的重置反报
		// 成 503 失败——否则同一 token 无法重试且误导调用方。故如实返回重置成功 + 在响应/事件里标注会话
		// 吊销真实状态,失败时记 ERROR 审计供告警。旧会话的"保证最终吊销"(跨库原子 / 密码版本失效 /
		// durable pending-revoke)涉及跨库原子性,属架构升级,记入强制 roadmap,不在本 bugfix 内擅自加。
		revoked, revErr := d.Sessions.Revoke(r.Context(), usersession.RevokeInput{
			TenantID: user.TenantID, UserID: user.ID, Reason: "password_reset",
		})
		sessionRevocation := "revoked"
		if revErr != nil {
			sessionRevocation = "failed"
			logInternalError(r.Context(), "", "user_password_reset_session_revoke_failed", revErr)
			recordAuthEvent(r.Context(), d.EventSink, AuthEvent{
				EventType: "user_password_reset_session_revoke_failed", TenantID: user.TenantID, UserID: user.ID,
				Outcome: "failure", ReasonClass: sessionReasonClass(revErr),
			})
		}
		recordAuthEvent(r.Context(), d.EventSink, AuthEvent{
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
			recordAuthEvent(r.Context(), d.EventSink, AuthEvent{
				EventType: "user_social_login_failed", TenantID: req.TenantID, Provider: safeProviderForEvent(req.Provider),
				Outcome: "failure", ReasonClass: authReasonClass(err), AuthMethod: safeProviderForEvent(req.Provider),
			})
			writeAuthError(w, err)
			return
		}
		writeAuditJSON(w, http.StatusCreated, result)
	}
}

func newAuthOAuthCallbackHandler(d AuthHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Sessions == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "auth/session dependency unset")
			return
		}
		var req authOAuthCallbackRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		user, err := d.Auth.CompleteOAuth(r.Context(), userauth.OAuthCallbackInput{
			TenantID: req.TenantID, Provider: req.Provider, State: req.State, Code: req.Code,
		})
		if err != nil {
			writeAuthError(w, err)
			return
		}
		tokens, err := d.Sessions.Create(r.Context(), usersession.CreateInput{
			TenantID: user.TenantID, UserID: user.ID, DeviceInfo: req.DeviceInfo,
			IP: clientIP(r), UserAgent: r.UserAgent(), AuthMethod: req.Provider,
		})
		if err != nil {
			recordAuthEvent(r.Context(), d.EventSink, AuthEvent{
				EventType: "user_social_login_session_failed", TenantID: user.TenantID, UserID: user.ID,
				Provider: safeProviderForEvent(req.Provider), Outcome: "failure", ReasonClass: sessionReasonClass(err),
				AuthMethod: safeProviderForEvent(req.Provider),
			})
			writeSessionError(w, err)
			return
		}
		recordAuthEvent(r.Context(), d.EventSink, AuthEvent{
			EventType: "user_social_login_succeeded", TenantID: user.TenantID, UserID: user.ID,
			Provider: safeProviderForEvent(req.Provider), Outcome: "success", AuthMethod: safeProviderForEvent(req.Provider),
		})
		writeAuditJSON(w, http.StatusOK, map[string]any{"user": publicUser(user), "session": tokens})
	}
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
		recordAuthEvent(r.Context(), d.EventSink, AuthEvent{
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

func recordAuthEvent(ctx context.Context, sink AuthEventSink, event AuthEvent) {
	if sink == nil {
		return
	}
	sink.RecordAuthEvent(ctx, event)
}

func authReasonClass(err error) string {
	switch {
	case errors.Is(err, userauth.ErrInvalidInput):
		return "invalid_auth_request"
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
	// 防御纵深(S1-018):即使误设 HUAKAI_DEV_AUTH_RETURN_TOKEN=true,生产模式下也绝不把明文
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
	case errors.Is(err, userauth.ErrSocialLoginRejected):
		writeJSONError(w, http.StatusForbidden, "social_login_rejected", "social identity claims are not sufficient")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "auth_backend_error", "auth backend transient failure")
	}
}

func clientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
