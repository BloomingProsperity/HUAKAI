package gatewayhttp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

type AuthEmailSender interface {
	SendVerification(context.Context, userauth.User, string) error
	SendPasswordReset(context.Context, userauth.User, string) error
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

func MountAuthRoutes(r chi.Router, d AuthHandlerDeps) {
	r.Post("/register", newAuthRegisterHandler(d))
	r.Post("/login", newAuthLoginHandler(d))
	r.Post("/verify-email", newAuthVerifyEmailHandler(d))
	r.Post("/reset-password", newAuthResetPasswordHandler(d))
	r.Post("/oauth-init", newAuthOAuthInitHandler(d))
	r.Post("/oauth-callback", newAuthOAuthCallbackHandler(d))
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
			writeAuthError(w, err)
			return
		}
		if sender := authEmailSender(d); sender != nil {
			if err := sender.SendVerification(r.Context(), result.User, result.VerificationToken); err != nil {
				writeJSONError(w, http.StatusServiceUnavailable, "email_delivery_failed", "verification email could not be queued")
				return
			}
		}
		writeAuditJSON(w, http.StatusCreated, map[string]any{
			"user":                  publicUser(result.User),
			"verification_required": true,
		})
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
			writeAuthError(w, err)
			return
		}
		tokens, err := d.Sessions.Create(r.Context(), usersession.CreateInput{
			TenantID: user.TenantID, UserID: user.ID, DeviceInfo: req.DeviceInfo,
			IP: clientIP(r), UserAgent: r.UserAgent(), AuthMethod: "password",
		})
		if err != nil {
			writeSessionError(w, err)
			return
		}
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
						writeJSONError(w, http.StatusServiceUnavailable, "email_delivery_failed", "password reset email could not be queued")
						return
					}
				}
			}
			writeAuditJSON(w, http.StatusAccepted, map[string]any{"reset_requested": true})
			return
		}
		user, err := d.Auth.ResetPassword(r.Context(), userauth.PasswordResetConfirm{
			TenantID: req.TenantID, Token: req.Token, NewPassword: req.NewPassword,
		})
		if err != nil {
			writeAuthError(w, err)
			return
		}
		revoked := int64(0)
		if d.Sessions != nil {
			revoked, _ = d.Sessions.Revoke(r.Context(), usersession.RevokeInput{
				TenantID: user.TenantID, UserID: user.ID, Reason: "password_reset",
			})
		}
		writeAuditJSON(w, http.StatusOK, map[string]any{"user": publicUser(user), "sessions_revoked": revoked})
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
			writeSessionError(w, err)
			return
		}
		writeAuditJSON(w, http.StatusOK, map[string]any{"user": publicUser(user), "session": tokens})
	}
}

func authEmailSender(d AuthHandlerDeps) AuthEmailSender {
	if d.EmailSender != nil {
		return d.EmailSender
	}
	return NoopAuthEmailSender{}
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
	case errors.Is(err, userauth.ErrOAuthProviderMissing):
		writeJSONError(w, http.StatusServiceUnavailable, "oauth_provider_not_configured", "oauth provider is not configured")
	case errors.Is(err, userauth.ErrOAuthFlowNotFound), errors.Is(err, userauth.ErrOAuthFlowExpired):
		writeJSONError(w, http.StatusForbidden, "oauth_flow_invalid", "oauth state is invalid or expired")
	case errors.Is(err, userauth.ErrSocialLoginRejected):
		writeJSONError(w, http.StatusForbidden, "social_login_rejected", "social identity claims are not sufficient")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "auth_backend_error", err.Error())
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
