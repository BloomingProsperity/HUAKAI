package notifyhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/notify"
)

type SettingsService interface {
	GetSettings(context.Context, int64, int64) (notify.Settings, error)
	UpsertSettings(context.Context, notify.Settings) (notify.Settings, error)
}

type AdminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type UserDeps struct {
	Service SettingsService
}

type AdminDeps struct {
	Auth    AdminAuth
	Service SettingsService
}

type settingsRequest struct {
	NotifyType        string           `json:"notify_type"`
	WebhookURL        string           `json:"webhook_url,omitempty"`
	WebhookSecret     string           `json:"webhook_secret,omitempty"`
	NotificationEmail string           `json:"notification_email,omitempty"`
	BarkURL           string           `json:"bark_url,omitempty"`
	GotifyURL         string           `json:"gotify_url,omitempty"`
	GotifyToken       string           `json:"gotify_token,omitempty"`
	GotifyPriority    *int             `json:"gotify_priority,omitempty"`
	BalanceThreshold  *decimal.Decimal `json:"balance_threshold,omitempty"`
}

type settingsResponse struct {
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
	UpdatedAt               string          `json:"updated_at,omitempty"`
	UpdatedBy               string          `json:"updated_by,omitempty"`
}

func MountUserRoutes(r chi.Router, d UserDeps) {
	h := userHandler{deps: d}
	r.Get("/v1/users/me/notifications", h.get)
	r.Put("/v1/users/me/notifications", h.put)
}

func MountAdminRoutes(r chi.Router, d AdminDeps) {
	h := adminHandler{deps: d}
	r.Get("/v1/admin/users/{user_id}/notifications", h.get)
	r.Put("/v1/admin/users/{user_id}/notifications", h.put)
}

type userHandler struct {
	deps UserDeps
}

func (h userHandler) get(w http.ResponseWriter, r *http.Request) {
	ident, ok := sessionIdentity(w, r, h.deps.Service)
	if !ok {
		return
	}
	settings, err := h.deps.Service.GetSettings(r.Context(), ident.TenantID, ident.UserID)
	if err != nil {
		writeNotifyError(w, err, "notification_settings_read_failed")
		return
	}
	writeJSON(w, http.StatusOK, responseFromSettings(settings))
}

func (h userHandler) put(w http.ResponseWriter, r *http.Request) {
	ident, ok := sessionIdentity(w, r, h.deps.Service)
	if !ok {
		return
	}
	var req settingsRequest
	if !decodeSettingsRequest(w, r, &req) {
		return
	}
	settings := requestToSettings(req, ident.TenantID, ident.UserID, fmt.Sprintf("user:%d", ident.UserID))
	saved, err := h.deps.Service.UpsertSettings(r.Context(), settings)
	if err != nil {
		writeNotifyError(w, err, "notification_settings_update_failed")
		return
	}
	writeJSON(w, http.StatusOK, responseFromSettings(saved))
}

type adminHandler struct {
	deps AdminDeps
}

func (h adminHandler) get(w http.ResponseWriter, r *http.Request) {
	ident, ok := adminIdentity(w, r, h.deps)
	if !ok {
		return
	}
	tenantID, userID, ok := adminTarget(w, r, ident)
	if !ok {
		return
	}
	settings, err := h.deps.Service.GetSettings(r.Context(), tenantID, userID)
	if err != nil {
		writeNotifyError(w, err, "notification_settings_read_failed")
		return
	}
	writeJSON(w, http.StatusOK, responseFromSettings(settings))
}

func (h adminHandler) put(w http.ResponseWriter, r *http.Request) {
	ident, ok := adminIdentity(w, r, h.deps)
	if !ok {
		return
	}
	tenantID, userID, ok := adminTarget(w, r, ident)
	if !ok {
		return
	}
	var req settingsRequest
	if !decodeSettingsRequest(w, r, &req) {
		return
	}
	settings := requestToSettings(req, tenantID, userID, fmt.Sprintf("admin:%d", ident.TokenID))
	saved, err := h.deps.Service.UpsertSettings(r.Context(), settings)
	if err != nil {
		writeNotifyError(w, err, "notification_settings_update_failed")
		return
	}
	writeJSON(w, http.StatusOK, responseFromSettings(saved))
}

func sessionIdentity(w http.ResponseWriter, r *http.Request, service SettingsService) (sessionauth.SessionIdentity, bool) {
	if service == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "notification settings dependency unset")
		return sessionauth.SessionIdentity{}, false
	}
	ident, ok := sessionauth.SessionFromContext(r.Context())
	if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
		writeJSONError(w, http.StatusUnauthorized, "session_required", "user session is required")
		return sessionauth.SessionIdentity{}, false
	}
	return ident, true
}

func adminIdentity(w http.ResponseWriter, r *http.Request, d AdminDeps) (admin.AdminIdentity, bool) {
	if d.Auth == nil || d.Service == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "notification settings dependency unset")
		return admin.AdminIdentity{}, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
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

func adminTarget(w http.ResponseWriter, r *http.Request, ident admin.AdminIdentity) (int64, int64, bool) {
	rawUserID := strings.TrimSpace(chi.URLParam(r, "user_id"))
	userID, err := strconv.ParseInt(rawUserID, 10, 64)
	if err != nil || userID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "user_id_required", "user_id path parameter must be positive")
		return 0, 0, false
	}
	rawTenantID := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	if rawTenantID == "" && ident.Role == admin.RoleTenantOperator {
		if ident.ScopeTenantID <= 0 {
			writeJSONError(w, http.StatusForbidden, "admin_forbidden", "tenant scope required")
			return 0, 0, false
		}
		return ident.ScopeTenantID, userID, true
	}
	tenantID, err := strconv.ParseInt(rawTenantID, 10, 64)
	if err != nil || tenantID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "tenant_id_required", "tenant_id query parameter must be positive")
		return 0, 0, false
	}
	if ident.Role == admin.RoleTenantOperator && ident.ScopeTenantID != tenantID {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "caller cannot act on this tenant scope")
		return 0, 0, false
	}
	return tenantID, userID, true
}

func decodeSettingsRequest(w http.ResponseWriter, r *http.Request, dst *settingsRequest) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return false
	}
	return true
}

func requestToSettings(req settingsRequest, tenantID, userID int64, actor string) notify.Settings {
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

func responseFromSettings(settings notify.Settings) settingsResponse {
	resp := settingsResponse{
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
		writeJSONError(w, http.StatusBadRequest, code, "notification settings are invalid")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, code, "notification settings dependency failed")
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}
