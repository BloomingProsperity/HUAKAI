package controlhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauth"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/notify"
)

type NotifySettingsService interface {
	GetSettings(context.Context, int64, int64) (notify.Settings, error)
	UpsertSettings(context.Context, notify.Settings) (notify.Settings, error)
}

type NotifyAdminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type NotifyUserDeps struct {
	Service NotifySettingsService
}

type NotifyAdminDeps struct {
	Auth             NotifyAdminAuth
	Service          NotifyAdminSettingsService
	PlatformTenantID int64
}

type NotifyAdminSettingsService interface {
	GetSettings(context.Context, int64, int64) (notify.Settings, error)
	UpsertSettingsWithAdminLog(context.Context, notify.Settings, notify.AdminMutation) (notify.Settings, error)
}

type notifySettingsRequest struct {
	NotifyType        string           `json:"notify_type"`
	WebhookURL        string           `json:"webhook_url,omitempty"`
	WebhookSecret     string           `json:"webhook_secret,omitempty"`
	NotificationEmail string           `json:"notification_email,omitempty"`
	BarkURL           string           `json:"bark_url,omitempty"`
	GotifyURL         string           `json:"gotify_url,omitempty"`
	GotifyToken       string           `json:"gotify_token,omitempty"`
	GotifyPriority    *int             `json:"gotify_priority,omitempty"`
	BalanceThreshold  *decimal.Decimal `json:"balance_threshold,omitempty"`
	// ExtraEmails 额外抄送邮箱: 低余额/告警通知除主收件人外, 再逐个独立投递到这些地址(notifier.sendExtraEmailCopies)。
	// 校验交后端 ValidateSettings(写路径 UpsertSettings 已调): ≤10 条 + 每条 rejectHeaderInjection + mail.ParseAddress。
	ExtraEmails []string `json:"extra_emails,omitempty"`
}

type notifySettingsResponse struct {
	TenantID                int64           `json:"tenant_id"`
	UserID                  int64           `json:"user_id"`
	NotifyType              string          `json:"notify_type"`
	WebhookURL              string          `json:"webhook_url,omitempty"`
	WebhookSecretConfigured bool            `json:"webhook_secret_configured,omitempty"`
	NotificationEmail       string          `json:"notification_email,omitempty"`
	BarkURL                 string          `json:"bark_url,omitempty"`
	GotifyURL               string          `json:"gotify_url,omitempty"`
	GotifyTokenConfigured   bool            `json:"gotify_token_configured,omitempty"`
	GotifyPriority          int             `json:"gotify_priority,omitempty"`
	BalanceThreshold        decimal.Decimal `json:"balance_threshold"`
	ExtraEmails             []string        `json:"extra_emails,omitempty"`
	UpdatedAt               string          `json:"updated_at,omitempty"`
	UpdatedBy               string          `json:"updated_by,omitempty"`
}

func MountNotifyUserRoutes(r chi.Router, d NotifyUserDeps) {
	h := notifyUserHandler{deps: d}
	r.Get("/v1/users/me/notifications", h.get)
	r.Put("/v1/users/me/notifications", h.put)
}

func MountNotifyAdminRoutes(r chi.Router, d NotifyAdminDeps) {
	h := notifyAdminHandler{deps: d}
	r.Get("/v1/admin/users/{user_id}/notifications", h.get)
	r.With(adminsessionauth.AllowSessionWrite(adminsessionauth.SessionSafe)).
		Put("/v1/admin/users/{user_id}/notifications", h.put)
}

type notifyUserHandler struct {
	deps NotifyUserDeps
}

func (h notifyUserHandler) get(w http.ResponseWriter, r *http.Request) {
	ident, ok := notifySessionIdentity(w, r, h.deps.Service)
	if !ok {
		return
	}
	settings, err := h.deps.Service.GetSettings(r.Context(), ident.TenantID, ident.UserID)
	if err != nil {
		writeNotifyError(w, err, "notification_settings_read_failed")
		return
	}
	notifyWriteJSON(w, http.StatusOK, notifyResponseFromSettings(settings))
}

func (h notifyUserHandler) put(w http.ResponseWriter, r *http.Request) {
	ident, ok := notifySessionIdentity(w, r, h.deps.Service)
	if !ok {
		return
	}
	var req notifySettingsRequest
	if !decodeNotifySettingsRequest(w, r, &req) {
		return
	}
	settings := notifyRequestToSettings(req, ident.TenantID, ident.UserID, fmt.Sprintf("user:%d", ident.UserID))
	saved, err := h.deps.Service.UpsertSettings(r.Context(), settings)
	if err != nil {
		writeNotifyError(w, err, "notification_settings_update_failed")
		return
	}
	notifyWriteJSON(w, http.StatusOK, notifyResponseFromSettings(saved))
}

type notifyAdminHandler struct {
	deps NotifyAdminDeps
}

func (h notifyAdminHandler) get(w http.ResponseWriter, r *http.Request) {
	ident, ok := notifyAdminIdentity(w, r, h.deps)
	if !ok {
		return
	}
	tenantID, userID, ok := notifyAdminTarget(w, r, ident, h.deps.PlatformTenantID)
	if !ok {
		return
	}
	settings, err := h.deps.Service.GetSettings(r.Context(), tenantID, userID)
	if err != nil {
		writeNotifyError(w, err, "notification_settings_read_failed")
		return
	}
	notifyWriteJSON(w, http.StatusOK, notifyResponseFromSettings(settings))
}

func (h notifyAdminHandler) put(w http.ResponseWriter, r *http.Request) {
	ident, ok := notifyAdminIdentity(w, r, h.deps)
	if !ok {
		return
	}
	tenantID, userID, ok := notifyAdminTarget(w, r, ident, h.deps.PlatformTenantID)
	if !ok {
		return
	}
	var req notifySettingsRequest
	if !decodeNotifySettingsRequest(w, r, &req) {
		return
	}
	settings := notifyRequestToSettings(req, tenantID, userID, ident.AuditActor())
	saved, err := h.deps.Service.UpsertSettingsWithAdminLog(r.Context(), settings, notify.AdminMutation{
		Actor:     ident.AuditActor(),
		ActorRole: ident.Role,
		RequestID: middleware.GetReqID(r.Context()),
	})
	if err != nil {
		writeNotifyError(w, err, "notification_settings_update_failed")
		return
	}
	notifyWriteJSON(w, http.StatusOK, notifyResponseFromSettings(saved))
}

func notifySessionIdentity(w http.ResponseWriter, r *http.Request, service NotifySettingsService) (sessionauth.SessionIdentity, bool) {
	if service == nil {
		notifyWriteJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "notification settings dependency unset")
		return sessionauth.SessionIdentity{}, false
	}
	ident, ok := sessionauth.SessionFromContext(r.Context())
	if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
		notifyWriteJSONError(w, http.StatusUnauthorized, "session_required", "user session is required")
		return sessionauth.SessionIdentity{}, false
	}
	return ident, true
}

func notifyAdminIdentity(w http.ResponseWriter, r *http.Request, d NotifyAdminDeps) (admin.AdminIdentity, bool) {
	if d.Auth == nil || d.Service == nil {
		notifyWriteJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "notification settings dependency unset")
		return admin.AdminIdentity{}, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		if errors.Is(err, admin.ErrAdminBackend) {
			notifyWriteJSONError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
		} else {
			notifyWriteJSONError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
		}
		return admin.AdminIdentity{}, false
	}
	if ident.Role != admin.RolePlatformAdmin && ident.Role != admin.RoleTenantOperator {
		notifyWriteJSONError(w, http.StatusForbidden, "admin_forbidden", "tenant scope required")
		return admin.AdminIdentity{}, false
	}
	return ident, true
}

func notifyAdminTarget(
	w http.ResponseWriter,
	r *http.Request,
	ident admin.AdminIdentity,
	platformTenantID int64,
) (int64, int64, bool) {
	rawUserID := strings.TrimSpace(chi.URLParam(r, "user_id"))
	userID, err := strconv.ParseInt(rawUserID, 10, 64)
	if err != nil || userID <= 0 {
		notifyWriteJSONError(w, http.StatusBadRequest, "user_id_required", "user_id path parameter must be positive")
		return 0, 0, false
	}
	rawTenantID := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	if rawTenantID == "" && ident.Role == admin.RoleTenantOperator {
		if ident.ScopeTenantID <= 0 {
			notifyWriteJSONError(w, http.StatusForbidden, "admin_forbidden", "tenant scope required")
			return 0, 0, false
		}
		return ident.ScopeTenantID, userID, true
	}
	tenantID, err := strconv.ParseInt(rawTenantID, 10, 64)
	if err != nil || tenantID <= 0 {
		notifyWriteJSONError(w, http.StatusBadRequest, "tenant_id_required", "tenant_id query parameter must be positive")
		return 0, 0, false
	}
	if err := ident.CanManageFinalUsersForTenant(tenantID, platformTenantID); err != nil {
		if errors.Is(err, admin.ErrAdminBackend) {
			notifyWriteJSONError(w, http.StatusServiceUnavailable, "admin_scope_unavailable", "platform tenant scope is unavailable")
			return 0, 0, false
		}
		notifyWriteJSONError(w, http.StatusForbidden, "admin_forbidden", "caller cannot act on this tenant scope")
		return 0, 0, false
	}
	return tenantID, userID, true
}

func decodeNotifySettingsRequest(w http.ResponseWriter, r *http.Request, dst *notifySettingsRequest) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		notifyWriteJSONError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return false
	}
	return true
}

func notifyRequestToSettings(req notifySettingsRequest, tenantID, userID int64, actor string) notify.Settings {
	out := notify.Settings{
		TenantID:          tenantID,
		UserID:            userID,
		NotifyType:        notify.Type(req.NotifyType),
		WebhookURL:        req.WebhookURL,
		WebhookSecret:     req.WebhookSecret,
		NotificationEmail: req.NotificationEmail,
		BarkURL:           req.BarkURL,
		GotifyURL:         req.GotifyURL,
		GotifyToken:       req.GotifyToken,
		GotifyPriority:    5,
		BalanceThreshold:  notify.DefaultLowBalanceThreshold,
		ExtraEmails:       req.ExtraEmails, // 原值透传; 数量/格式/header 注入校验交 ValidateSettings(UpsertSettings 写路径已调)
		UpdatedBy:         actor,
	}
	if req.GotifyPriority != nil {
		out.GotifyPriority = *req.GotifyPriority
	}
	if req.BalanceThreshold != nil {
		out.BalanceThreshold = *req.BalanceThreshold
	}
	return out
}

func notifyResponseFromSettings(settings notify.Settings) notifySettingsResponse {
	resp := notifySettingsResponse{
		TenantID:                settings.TenantID,
		UserID:                  settings.UserID,
		NotifyType:              string(settings.NotifyType),
		WebhookURL:              settings.WebhookURL,
		WebhookSecretConfigured: strings.TrimSpace(settings.WebhookSecret) != "",
		NotificationEmail:       settings.NotificationEmail,
		BarkURL:                 settings.BarkURL,
		GotifyURL:               settings.GotifyURL,
		GotifyTokenConfigured:   strings.TrimSpace(settings.GotifyToken) != "",
		GotifyPriority:          settings.GotifyPriority,
		BalanceThreshold:        settings.BalanceThreshold,
		ExtraEmails:             settings.ExtraEmails, // GET 读回, 支持 read-modify-write
		UpdatedBy:               settings.UpdatedBy,
	}
	if !settings.UpdatedAt.IsZero() {
		resp.UpdatedAt = settings.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	return resp
}

func writeNotifyError(w http.ResponseWriter, err error, code string) {
	switch {
	case errors.Is(err, notify.ErrInvalidSettings), errors.Is(err, notify.ErrUnsafeEndpoint), errors.Is(err, notify.ErrHeaderInjection):
		notifyWriteJSONError(w, http.StatusBadRequest, code, "notification settings are invalid")
	default:
		notifyWriteJSONError(w, http.StatusServiceUnavailable, code, "notification settings dependency failed")
	}
}

func notifyWriteJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func notifyWriteJSONError(w http.ResponseWriter, status int, code, message string) {
	notifyWriteJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}
