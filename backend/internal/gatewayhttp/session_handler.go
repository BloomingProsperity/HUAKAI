package gatewayhttp

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
	"github.com/BloomingProsperity/HUAKAI/internal/oauthpendinghttp"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

type SessionHandlerDeps struct {
	Sessions         *usersession.Service
	EventSink        AuthEventSink
	ClientIPResolver *clientip.Resolver
}

type sessionRefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type sessionRevokeRequest struct {
	FamilyID     string `json:"family_id,omitempty"`
	SessionToken string `json:"session_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

type sessionListRequest struct{}

func MountSessionRoutes(r chi.Router, d SessionHandlerDeps) {
	MountSessionRefreshRoute(r, d)
	MountSessionProtectedRoutes(r, d)
}

func MountSessionRefreshRoute(r chi.Router, d SessionHandlerDeps) {
	r.Post("/refresh", newSessionRefreshHandler(d))
}

func MountSessionProtectedRoutes(r chi.Router, d SessionHandlerDeps) {
	r.Post("/revoke", newSessionRevokeHandler(d))
	r.Post("/list", newSessionListHandler(d))
}

func newSessionRefreshHandler(d SessionHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Sessions == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "session dependency unset")
			return
		}
		var req sessionRefreshRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		ident, hasValidBearer := optionalSessionIdentity(r, d)
		input := usersession.RefreshInput{
			RefreshToken: req.RefreshToken,
			IP:           d.ClientIPResolver.ClientIP(r), UserAgent: r.UserAgent(),
		}
		if hasValidBearer {
			input.TenantID = ident.TenantID
			input.UserID = ident.UserID
		}
		result, err := d.Sessions.Refresh(r.Context(), input)
		if err != nil {
			tenantID, userID := int64(0), int64(0)
			if hasValidBearer {
				tenantID, userID = ident.TenantID, ident.UserID
			}
			recordAuthEvent(r.Context(), d.EventSink, AuthEvent{
				EventType: "session_refresh_failed", TenantID: tenantID, UserID: userID,
				Outcome: "failure", ReasonClass: sessionReasonClass(err),
			})
			writeSessionError(w, err)
			return
		}
		recordAuthEvent(r.Context(), d.EventSink, AuthEvent{
			EventType: "session_refreshed", TenantID: result.Family.TenantID, UserID: result.Family.UserID, Outcome: "success",
		})
		writeAuditJSON(w, http.StatusOK, map[string]any{"session": result})
	}
}

func optionalSessionIdentity(r *http.Request, d SessionHandlerDeps) (sessionauth.SessionIdentity, bool) {
	if r == nil || d.Sessions == nil {
		return sessionauth.SessionIdentity{}, false
	}
	token, ok := sessionBearerFromHeader(r.Header.Get("Authorization"))
	if !ok {
		return sessionauth.SessionIdentity{}, false
	}
	validated, err := d.Sessions.Validate(r.Context(), token, d.ClientIPResolver.ClientIP(r), r.UserAgent())
	if err != nil {
		return sessionauth.SessionIdentity{}, false
	}
	return sessionauth.SessionIdentity{
		TenantID:   validated.TenantID,
		UserID:     validated.UserID,
		FamilyID:   validated.FamilyID,
		TokenID:    validated.TokenID,
		Generation: validated.Generation,
	}, true
}

func sessionBearerFromHeader(header string) (string, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	return token, token != ""
}

func newSessionRevokeHandler(d SessionHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Sessions == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "session dependency unset")
			return
		}
		var req sessionRevokeRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		ident, ok := sessionauth.SessionFromContext(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "session_token_required", "session bearer token is required")
			return
		}
		if strings.TrimSpace(req.FamilyID) != "" {
			owned, err := sessionFamilyBelongsToCurrentUser(r, d.Sessions, ident, req.FamilyID)
			if err != nil {
				writeSessionError(w, err)
				return
			}
			if !owned {
				writeJSONError(w, http.StatusForbidden, "session_family_forbidden", "session family does not belong to current user")
				return
			}
		}
		count, err := d.Sessions.Revoke(r.Context(), usersession.RevokeInput{
			TenantID: ident.TenantID, UserID: ident.UserID, FamilyID: req.FamilyID,
			SessionToken: req.SessionToken, RefreshToken: req.RefreshToken, Reason: req.Reason,
		})
		if err != nil {
			writeSessionError(w, err)
			return
		}
		writeAuditJSON(w, http.StatusOK, map[string]any{"revoked": count})
	}
}

func sessionFamilyBelongsToCurrentUser(r *http.Request, sessions *usersession.Service, ident sessionauth.SessionIdentity, familyID string) (bool, error) {
	return sessions.FamilyBelongsToUser(r.Context(), ident.TenantID, ident.UserID, familyID)
}

func newSessionListHandler(d SessionHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Sessions == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "session dependency unset")
			return
		}
		var req sessionListRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		ident, ok := sessionauth.SessionFromContext(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "session_token_required", "session bearer token is required")
			return
		}
		families, err := d.Sessions.List(r.Context(), ident.TenantID, ident.UserID)
		if err != nil {
			writeSessionError(w, err)
			return
		}
		writeAuditJSON(w, http.StatusOK, map[string]any{"families": families})
	}
}

func writeSessionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, usersession.ErrInvalidInput):
		writeJSONError(w, http.StatusBadRequest, "invalid_session_request", "session request is invalid")
	case errors.Is(err, usersession.ErrTokenNotFound):
		writeJSONError(w, http.StatusUnauthorized, "session_token_not_found", "refresh token is invalid")
	case errors.Is(err, usersession.ErrRefreshReplay):
		writeJSONError(w, http.StatusConflict, "refresh_token_replay", "refresh token was already consumed")
	case errors.Is(err, usersession.ErrSessionUserMismatch):
		writeJSONError(w, http.StatusUnauthorized, "refresh_token_cross_user_attempt", "refresh token is invalid")
	case errors.Is(err, usersession.ErrTokenExpired):
		writeJSONError(w, http.StatusUnauthorized, "refresh_token_expired", "refresh token is expired")
	case errors.Is(err, usersession.ErrFamilyRevoked), errors.Is(err, usersession.ErrFamilyNotFound):
		writeJSONError(w, http.StatusUnauthorized, "session_family_revoked", "session family is revoked or missing")
	case errors.Is(err, usersession.ErrAnomalyRejected):
		writeJSONError(w, http.StatusForbidden, "session_anomaly_rejected", "session context changed too much")
	case errors.Is(err, usersession.ErrDeviceLimitExceeded):
		writeJSONError(w, http.StatusForbidden, "session_device_limit_exceeded", "too many active devices")
	case errors.Is(err, usersession.ErrDeviceConfirmationRequired):
		writeJSONError(w, http.StatusForbidden, "session_device_confirmation_required", "device confirmation is required")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "session_backend_error", "session backend transient failure")
	}
}

func sessionReasonClass(err error) string {
	switch {
	case errors.Is(err, usersession.ErrInvalidInput):
		return "invalid_session_request"
	case errors.Is(err, usersession.ErrTokenNotFound):
		return "session_token_not_found"
	case errors.Is(err, usersession.ErrRefreshReplay):
		return "refresh_token_replay"
	case errors.Is(err, usersession.ErrSessionUserMismatch):
		return "refresh_token_cross_user_attempt"
	case errors.Is(err, usersession.ErrTokenExpired):
		return "refresh_token_expired"
	case errors.Is(err, usersession.ErrFamilyRevoked), errors.Is(err, usersession.ErrFamilyNotFound):
		return "session_family_revoked"
	case errors.Is(err, usersession.ErrAnomalyRejected):
		return "session_anomaly_rejected"
	case errors.Is(err, usersession.ErrDeviceLimitExceeded):
		return "session_device_limit_exceeded"
	case errors.Is(err, usersession.ErrDeviceConfirmationRequired):
		return "session_device_confirmation_required"
	default:
		return "session_backend_error"
	}
}

// 新设备确认 (device confirmation) 流的 HTTP 集成: 登录侧拦截 + 确认端点。default-dormant:
// usersession.Service.MaxActiveFamilies=0 时整条休眠, 这些函数永不被触发。

// handleDeviceConfirmationRequired 检测 Sessions.Create 返回的设备确认错误: 命中则发确认邮件
// (best-effort, 失败只记日志不致命) 并返回 403 device_confirmation_required (响应体绝不含 token),
// 返回 true 表示已处理; 否则返回 false 让调用方按常规 writeSessionError 处理。
// user 是已认证用户 (取其 email 发信); 调用方在各登录路径上已持有它, 无需重查库。
func handleDeviceConfirmationRequired(w http.ResponseWriter, r *http.Request, d AuthHandlerDeps, user userauth.User, err error) bool {
	var confirmErr *usersession.DeviceConfirmationRequiredError
	if !errors.As(err, &confirmErr) {
		return false
	}
	// 发信 best-effort: 即便邮件不可用也要回 403 让用户知道需确认, 不把发信失败升级成 5xx。
	if sender := authEmailSender(d); sender != nil {
		if sendErr := sender.SendDeviceConfirmation(r.Context(), user, confirmErr.RawToken); sendErr != nil {
			slog.Warn("device confirmation email send failed",
				"tenant_id", user.TenantID, "user_id", user.ID)
		}
	}
	// 响应体只给状态码 + 通用文案, 绝不回显 token (token 仅经邮件交付)。
	writeJSONError(w, http.StatusForbidden, "device_confirmation_required",
		"a confirmation link has been sent to your email to authorize this new device")
	return true
}

// handleDeviceConfirmationRequiredByID 与 handleDeviceConfirmationRequired 同语义, 但只持有
// tenantID/userID (2FA 登录路径没有完整 User)。命中确认错误时按 ID 查用户取 email 发信; 查不到
// 用户仍回 403 (不把发信前的查库失败升级成 5xx)。
func handleDeviceConfirmationRequiredByID(w http.ResponseWriter, r *http.Request, d AuthHandlerDeps, tenantID, userID int64, err error) bool {
	var confirmErr *usersession.DeviceConfirmationRequiredError
	if !errors.As(err, &confirmErr) {
		return false
	}
	var user userauth.User
	if d.Auth != nil && d.Auth.Store != nil {
		if u, lookupErr := d.Auth.Store.GetUserByID(r.Context(), tenantID, userID); lookupErr == nil {
			user = u
		}
	}
	return handleDeviceConfirmationRequired(w, r, d, user, err)
}

func newAuthConfirmDeviceHandler(d AuthHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Sessions == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "session dependency unset")
			return
		}
		var req authConfirmDeviceRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		// 与 verify-email 同款: 无需已登录, token 即凭证。tenant_id + token 二者皆必填。
		if req.TenantID <= 0 || strings.TrimSpace(req.Token) == "" {
			writeJSONError(w, http.StatusBadRequest, "invalid_device_confirmation_request", "tenant_id and token are required")
			return
		}
		if err := d.Sessions.ConfirmDevice(r.Context(), req.TenantID, req.Token); err != nil {
			writeDeviceConfirmationError(w, err)
			return
		}
		writeAuditJSON(w, http.StatusOK, map[string]any{"status": "device_confirmed"})
	}
}

// writeDeviceConfirmationError 把 ConfirmDevice 的错误映射到 4xx (不泄露区分性细节给攻击者)。
func writeDeviceConfirmationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, usersession.ErrInvalidInput):
		writeJSONError(w, http.StatusBadRequest, "invalid_device_confirmation_request", "device confirmation request is invalid")
	case errors.Is(err, usersession.ErrDeviceConfirmationNotFound), errors.Is(err, usersession.ErrRefreshReplay):
		// token 不存在 / 跨租户 / 已被消费, 统一按 401 (避免枚举: 不存在与已用同一响应)。
		writeJSONError(w, http.StatusUnauthorized, "device_confirmation_invalid", "device confirmation token is invalid or already used")
	case errors.Is(err, usersession.ErrTokenExpired):
		writeJSONError(w, http.StatusUnauthorized, "device_confirmation_expired", "device confirmation token is expired")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "device_confirmation_backend_error", "device confirmation backend transient failure")
	}
}

// newAuthOAuthCallbackHandler 处理配置化社交 OAuth 回调:换取已校验身份→建会话登录;身份缺已验证
// 邮箱(QQ/无验证邮箱 GitHub 等)则签发 pending_token 让前端走「补邮箱建号」(端点在 oauthpendinghttp)。
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
		if err := requireOAuthStateCookie(w, r, req.State); err != nil {
			writeAuthError(w, err)
			return
		}
		completion, err := d.Auth.CompleteOAuthDetailed(r.Context(), userauth.OAuthCallbackInput{
			TenantID: req.TenantID, Provider: req.Provider, State: req.State, Code: req.Code,
		})
		if err != nil {
			writeAuthError(w, err)
			return
		}
		if completion.PendingEmail {
			pendingToken, tErr := oauthpendinghttp.MintPendingToken(d.OAuthPendingKey, completion.PendingIdentity, req.TenantID, d.Auth.Clock())
			if tErr != nil || pendingToken == "" {
				writeAuthError(w, userauth.ErrOAuthPendingEmailRequired)
				return
			}
			writeAuditJSON(w, http.StatusAccepted, map[string]any{"code": "oauth_pending_email_required", "pending_token": pendingToken})
			return
		}
		user := completion.User
		tokens, err := d.Sessions.Create(r.Context(), usersession.CreateInput{
			TenantID: user.TenantID, UserID: user.ID, DeviceInfo: req.DeviceInfo,
			IP: d.ClientIPResolver.ClientIP(r), UserAgent: r.UserAgent(), AuthMethod: req.Provider,
		})
		if err != nil {
			recordAuthEvent(r.Context(), d.EventSink, AuthEvent{
				EventType: "user_social_login_session_failed", TenantID: user.TenantID, UserID: user.ID,
				Provider: safeProviderForEvent(req.Provider), Outcome: "failure", ReasonClass: sessionReasonClass(err),
				AuthMethod: safeProviderForEvent(req.Provider),
			})
			if handleDeviceConfirmationRequired(w, r, d, user, err) {
				return
			}
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
