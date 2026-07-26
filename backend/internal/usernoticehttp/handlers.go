package usernoticehttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/panelauth"
	"github.com/BloomingProsperity/HUAKAI/internal/usernotice"
)

const (
	defaultLimit = 50
	maxLimit     = 100
)

type Service interface {
	Broadcast(context.Context, usernotice.BroadcastInput) (usernotice.BroadcastResult, error)
	ListForUser(context.Context, usernotice.ListInput) ([]usernotice.Notification, error)
	MarkRead(context.Context, usernotice.MarkReadInput) (usernotice.Notification, error)
	UnreadCount(context.Context, int64, int64) (int64, error)
}

type AdminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type UserDeps struct {
	Service Service
}

type AdminDeps struct {
	Auth             AdminAuth
	Service          Service
	PlatformTenantID int64
}

type broadcastRequest struct {
	TenantID int64  `json:"tenant_id,omitempty"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Severity string `json:"severity,omitempty"`
}

type broadcastResponse struct {
	Object   string `json:"object"`
	TenantID int64  `json:"tenant_id"`
	Inserted int64  `json:"inserted"`
}

type notificationListResponse struct {
	Object string                 `json:"object"`
	Items  []notificationResponse `json:"items"`
	Limit  int                    `json:"limit"`
	Offset int                    `json:"offset"`
}

type notificationResponse struct {
	ID             int64   `json:"id"`
	TenantID       int64   `json:"tenant_id"`
	UserID         int64   `json:"user_id"`
	Title          string  `json:"title"`
	Body           string  `json:"body"`
	Severity       string  `json:"severity"`
	ReadAt         *string `json:"read_at,omitempty"`
	CreatedByAdmin *int64  `json:"created_by_admin,omitempty"`
	CreatedAt      string  `json:"created_at"`
}

type unreadCountResponse struct {
	Object string `json:"object"`
	Count  int64  `json:"count"`
}

func MountUserRoutes(r chi.Router, d UserDeps) {
	h := userHandler{deps: d}
	r.Get("/v1/notifications/unread-count", h.unreadCount)
	r.Get("/v1/notifications", h.list)
	r.Post("/v1/notifications/{id}/read", h.markRead)
}

func MountAdminRoutes(r chi.Router, d AdminDeps) {
	h := adminHandler{deps: d}
	r.Post("/v1/admin/notifications/broadcast", h.broadcast)
}

type userHandler struct {
	deps UserDeps
}

func (h userHandler) list(w http.ResponseWriter, r *http.Request) {
	ident, ok := userIdentity(w, r, h.deps.Service)
	if !ok {
		return
	}
	limit, offset, ok := parsePage(w, r)
	if !ok {
		return
	}
	unreadOnly, ok := parseUnreadOnly(w, r)
	if !ok {
		return
	}
	items, err := h.deps.Service.ListForUser(r.Context(), usernotice.ListInput{
		TenantID:   ident.TenantID,
		UserID:     ident.UserID,
		UnreadOnly: unreadOnly,
		Limit:      limit,
		Offset:     offset,
	})
	if err != nil {
		writeNoticeError(w, err, "notifications_list_failed")
		return
	}
	writeJSON(w, http.StatusOK, notificationListResponse{
		Object: "notification_list",
		Items:  notificationResponses(items),
		Limit:  limit,
		Offset: offset,
	})
}

func (h userHandler) markRead(w http.ResponseWriter, r *http.Request) {
	ident, ok := userIdentity(w, r, h.deps.Service)
	if !ok {
		return
	}
	id, ok := parsePathID(w, r)
	if !ok {
		return
	}
	updated, err := h.deps.Service.MarkRead(r.Context(), usernotice.MarkReadInput{
		TenantID: ident.TenantID,
		UserID:   ident.UserID,
		ID:       id,
	})
	if err != nil {
		writeNoticeError(w, err, "notification_mark_read_failed")
		return
	}
	writeJSON(w, http.StatusOK, toNotificationResponse(updated))
}

func (h userHandler) unreadCount(w http.ResponseWriter, r *http.Request) {
	ident, ok := userIdentity(w, r, h.deps.Service)
	if !ok {
		return
	}
	count, err := h.deps.Service.UnreadCount(r.Context(), ident.TenantID, ident.UserID)
	if err != nil {
		writeNoticeError(w, err, "notification_unread_count_failed")
		return
	}
	writeJSON(w, http.StatusOK, unreadCountResponse{
		Object: "notification_unread_count",
		Count:  count,
	})
}

type adminHandler struct {
	deps AdminDeps
}

func (h adminHandler) broadcast(w http.ResponseWriter, r *http.Request) {
	ident, ok := adminIdentity(w, r, h.deps)
	if !ok {
		return
	}
	var req broadcastRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	tenantID, ok := resolveAdminTenantValue(w, ident, req.TenantID, h.deps.PlatformTenantID)
	if !ok {
		return
	}
	var createdBy *int64
	if ident.TokenID > 0 {
		createdBy = &ident.TokenID
	}
	result, err := h.deps.Service.Broadcast(r.Context(), usernotice.BroadcastInput{
		TenantID:       tenantID,
		Title:          req.Title,
		Body:           req.Body,
		Severity:       usernotice.Severity(req.Severity),
		CreatedByAdmin: createdBy,
	})
	if err != nil {
		writeNoticeError(w, err, "notification_broadcast_failed")
		return
	}
	writeJSON(w, http.StatusCreated, broadcastResponse{
		Object:   "notification_broadcast",
		TenantID: result.TenantID,
		Inserted: result.Inserted,
	})
}

func userIdentity(w http.ResponseWriter, r *http.Request, service Service) (sessionauth.SessionIdentity, bool) {
	if service == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "notification inbox dependency unset")
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
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "notification inbox dependency unset")
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
	if panelauth.PanelForAdminToken() != panelauth.PanelAdmin {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "admin panel access required")
		return admin.AdminIdentity{}, false
	}
	if ident.Role != admin.RolePlatformAdmin && ident.Role != admin.RoleTenantOperator {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "platform_admin or tenant_operator role required")
		return admin.AdminIdentity{}, false
	}
	return ident, true
}

func resolveAdminTenantValue(w http.ResponseWriter, ident admin.AdminIdentity, tenantID, platformTenantID int64) (int64, bool) {
	if tenantID == 0 && ident.Role == admin.RoleTenantOperator {
		tenantID = ident.ScopeTenantID
	}
	if tenantID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "tenant_id_required", "tenant_id must be positive")
		return 0, false
	}
	if err := ident.CanManageFinalUsersForTenant(tenantID, platformTenantID); err != nil {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "caller cannot act on this tenant scope")
		return 0, false
	}
	return tenantID, true
}

func parsePathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(r, "id")), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "notification_id_required", "notification id path parameter must be positive")
		return 0, false
	}
	return id, true
}

func parsePage(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	limit := defaultLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || v < 1 || v > maxLimit {
			writeJSONError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 100")
			return 0, 0, false
		}
		limit = int(v)
	}
	offset := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || v < 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid_offset", "offset must be non-negative")
			return 0, 0, false
		}
		offset = int(v)
	}
	return limit, offset, true
}

func parseUnreadOnly(w http.ResponseWriter, r *http.Request) (bool, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("unread_only"))
	if raw == "" {
		return false, true
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_unread_only", "unread_only must be a boolean")
		return false, false
	}
	return value, true
}

func decodeRequest(w http.ResponseWriter, r *http.Request, dst any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return false
	}
	return true
}

func writeNoticeError(w http.ResponseWriter, err error, code string) {
	switch {
	case errors.Is(err, usernotice.ErrInvalidInput):
		writeJSONError(w, http.StatusBadRequest, code, "notification request is invalid")
	case errors.Is(err, usernotice.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, code, "notification not found")
	case errors.Is(err, usernotice.ErrRecipientLimitExceeded):
		writeJSONError(w, http.StatusConflict, "notification_recipient_limit_exceeded", "broadcast recipient limit exceeded")
	case errors.Is(err, usernotice.ErrStoreNotConfigured):
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "notification inbox dependency unset")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, code, "notification inbox backend unavailable")
	}
}

func notificationResponses(items []usernotice.Notification) []notificationResponse {
	out := make([]notificationResponse, 0, len(items))
	for _, item := range items {
		out = append(out, toNotificationResponse(item))
	}
	return out
}

func toNotificationResponse(notice usernotice.Notification) notificationResponse {
	resp := notificationResponse{
		ID:             notice.ID,
		TenantID:       notice.TenantID,
		UserID:         notice.UserID,
		Title:          notice.Title,
		Body:           notice.Body,
		Severity:       string(notice.Severity),
		CreatedByAdmin: notice.CreatedByAdmin,
		CreatedAt:      formatTime(notice.CreatedAt),
	}
	if notice.ReadAt != nil {
		formatted := formatTime(*notice.ReadAt)
		resp.ReadAt = &formatted
	}
	return resp
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
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
