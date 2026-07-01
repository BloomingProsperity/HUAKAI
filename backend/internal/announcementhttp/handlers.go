package announcementhttp

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
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauth"
	"github.com/BloomingProsperity/HUAKAI/internal/announcement"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
	"github.com/BloomingProsperity/HUAKAI/internal/panelauth"
)

const (
	defaultLimit = 50
	maxLimit     = 100
)

type Service interface {
	Create(context.Context, announcement.CreateInput) (announcement.Announcement, error)
	Update(context.Context, announcement.UpdateInput) (announcement.Announcement, error)
	Delete(context.Context, int64, int64) error
	ListActive(context.Context, announcement.ListActiveInput) ([]announcement.Announcement, error)
	ListAllAdmin(context.Context, announcement.ListAdminInput) ([]announcement.Announcement, error)
}

type AdminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type UserDeps struct {
	Service          Service
	Sessions         sessionauth.SessionValidator
	ClientIPResolver *clientip.Resolver
}

type AdminDeps struct {
	Auth    AdminAuth
	Service Service
}

type createAnnouncementRequest struct {
	TenantID    int64        `json:"tenant_id"`
	Title       string       `json:"title"`
	Body        string       `json:"body"`
	Severity    string       `json:"severity,omitempty"`
	Active      *bool        `json:"active,omitempty"`
	PublishedAt *time.Time   `json:"published_at,omitempty"`
	ExpiresAt   optionalTime `json:"expires_at,omitempty"`
}

type updateAnnouncementRequest struct {
	Title       *string      `json:"title,omitempty"`
	Body        *string      `json:"body,omitempty"`
	Severity    *string      `json:"severity,omitempty"`
	Active      *bool        `json:"active,omitempty"`
	PublishedAt *time.Time   `json:"published_at,omitempty"`
	ExpiresAt   optionalTime `json:"expires_at,omitempty"`
}

type optionalTime struct {
	Set   bool
	Value *time.Time
}

func (o *optionalTime) UnmarshalJSON(data []byte) error {
	o.Set = true
	if strings.TrimSpace(string(data)) == "null" {
		o.Value = nil
		return nil
	}
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return err
	}
	t := parsed.UTC()
	o.Value = &t
	return nil
}

type announcementListResponse struct {
	Object string                 `json:"object"`
	Items  []announcementResponse `json:"items"`
	Limit  int                    `json:"limit"`
	Offset int                    `json:"offset"`
}

type announcementResponse struct {
	ID             int64   `json:"id"`
	TenantID       int64   `json:"tenant_id"`
	Title          string  `json:"title"`
	Body           string  `json:"body"`
	Severity       string  `json:"severity"`
	Active         bool    `json:"active"`
	PublishedAt    string  `json:"published_at"`
	ExpiresAt      *string `json:"expires_at,omitempty"`
	CreatedByAdmin *int64  `json:"created_by_admin,omitempty"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
}

type deleteResponse struct {
	ID      int64 `json:"id"`
	Deleted bool  `json:"deleted"`
}

func MountUserRoutes(r chi.Router, d UserDeps) {
	r.Get("/v1/announcements", newUserListHandler(d))
}

func MountAdminRoutes(r chi.Router, d AdminDeps) {
	// SessionSafe:站内公告增改删,低危可逆的运营内容,登录 admin(session)可直接写;删靠前端确认弹窗。
	safe := adminsessionauth.AllowSessionWrite(adminsessionauth.SessionSafe)
	r.Get("/v1/admin/announcements", newAdminListHandler(d))
	r.With(safe).Post("/v1/admin/announcements", newAdminCreateHandler(d))
	r.With(safe).Put("/v1/admin/announcements/{id}", newAdminUpdateHandler(d))
	r.With(safe).Delete("/v1/admin/announcements/{id}", newAdminDeleteHandler(d))
}

func newUserListHandler(d UserDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Service == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "announcement dependency unset")
			return
		}
		tenantID, ok := resolveUserTenant(w, r, d)
		if !ok {
			return
		}
		limit, offset, ok := parsePage(w, r)
		if !ok {
			return
		}
		items, err := d.Service.ListActive(r.Context(), announcement.ListActiveInput{
			TenantID: tenantID,
			Limit:    limit,
			Offset:   offset,
		})
		if err != nil {
			writeAnnouncementError(w, err, "announcements_list_failed")
			return
		}
		writeJSON(w, http.StatusOK, announcementListResponse{
			Object: "announcement_list",
			Items:  announcementResponses(items),
			Limit:  limit,
			Offset: offset,
		})
	}
}

func newAdminListHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(w, r, d)
		if !ok {
			return
		}
		tenantID, ok := resolveAdminTenantFromQuery(w, r, ident)
		if !ok {
			return
		}
		limit, offset, ok := parsePage(w, r)
		if !ok {
			return
		}
		items, err := d.Service.ListAllAdmin(r.Context(), announcement.ListAdminInput{
			TenantID: tenantID,
			Limit:    limit,
			Offset:   offset,
		})
		if err != nil {
			writeAnnouncementError(w, err, "announcements_admin_list_failed")
			return
		}
		writeJSON(w, http.StatusOK, announcementListResponse{
			Object: "announcement_list",
			Items:  announcementResponses(items),
			Limit:  limit,
			Offset: offset,
		})
	}
}

func newAdminCreateHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(w, r, d)
		if !ok {
			return
		}
		var req createAnnouncementRequest
		if !decodeRequest(w, r, &req) {
			return
		}
		tenantID, ok := resolveAdminTenantValue(w, ident, req.TenantID)
		if !ok {
			return
		}
		var createdBy *int64
		if ident.TokenID > 0 {
			createdBy = &ident.TokenID
		}
		created, err := d.Service.Create(r.Context(), announcement.CreateInput{
			TenantID:       tenantID,
			Title:          req.Title,
			Body:           req.Body,
			Severity:       announcement.Severity(req.Severity),
			Active:         req.Active,
			PublishedAt:    req.PublishedAt,
			ExpiresAt:      req.ExpiresAt.Value,
			CreatedByAdmin: createdBy,
		})
		if err != nil {
			writeAnnouncementError(w, err, "announcement_create_failed")
			return
		}
		writeJSON(w, http.StatusCreated, toAnnouncementResponse(created))
	}
}

func newAdminUpdateHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(w, r, d)
		if !ok {
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		tenantID, ok := resolveAdminTenantFromQuery(w, r, ident)
		if !ok {
			return
		}
		var req updateAnnouncementRequest
		if !decodeRequest(w, r, &req) {
			return
		}
		var severity *announcement.Severity
		if req.Severity != nil {
			v := announcement.Severity(*req.Severity)
			severity = &v
		}
		updated, err := d.Service.Update(r.Context(), announcement.UpdateInput{
			TenantID:     tenantID,
			ID:           id,
			Title:        req.Title,
			Body:         req.Body,
			Severity:     severity,
			Active:       req.Active,
			PublishedAt:  req.PublishedAt,
			ExpiresAt:    req.ExpiresAt.Value,
			ExpiresAtSet: req.ExpiresAt.Set,
		})
		if err != nil {
			writeAnnouncementError(w, err, "announcement_update_failed")
			return
		}
		writeJSON(w, http.StatusOK, toAnnouncementResponse(updated))
	}
}

func newAdminDeleteHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(w, r, d)
		if !ok {
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		tenantID, ok := resolveAdminTenantFromQuery(w, r, ident)
		if !ok {
			return
		}
		if err := d.Service.Delete(r.Context(), tenantID, id); err != nil {
			writeAnnouncementError(w, err, "announcement_delete_failed")
			return
		}
		writeJSON(w, http.StatusOK, deleteResponse{ID: id, Deleted: true})
	}
}

func resolveUserTenant(w http.ResponseWriter, r *http.Request, d UserDeps) (int64, bool) {
	if ident, ok := sessionauth.SessionFromContext(r.Context()); ok && ident.TenantID > 0 {
		return ident.TenantID, true
	}
	if rawAuth := strings.TrimSpace(r.Header.Get("Authorization")); rawAuth != "" {
		token, ok := parseBearer(rawAuth)
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "session_token_invalid", "session bearer token is invalid")
			return 0, false
		}
		if d.Sessions != nil {
			validated, err := d.Sessions.Validate(r.Context(), token, requestIP(r, d.ClientIPResolver), r.UserAgent())
			if err != nil || validated.TenantID <= 0 {
				writeJSONError(w, http.StatusUnauthorized, "session_token_invalid", "session bearer token is invalid")
				return 0, false
			}
			return validated.TenantID, true
		}
	}
	raw := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	tenantID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || tenantID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "tenant_id_required", "tenant_id query parameter must be positive when no session is present")
		return 0, false
	}
	return tenantID, true
}

func parseBearer(header string) (string, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	return token, token != ""
}

func requestIP(r *http.Request, resolver *clientip.Resolver) string {
	if resolver != nil {
		return resolver.ClientIP(r)
	}
	return r.RemoteAddr
}

func resolveAdmin(w http.ResponseWriter, r *http.Request, d AdminDeps) (admin.AdminIdentity, bool) {
	if d.Auth == nil || d.Service == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "announcement admin dependency unset")
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

func resolveAdminTenantValue(w http.ResponseWriter, ident admin.AdminIdentity, tenantID int64) (int64, bool) {
	if tenantID == 0 && ident.Role == admin.RoleTenantOperator {
		tenantID = ident.ScopeTenantID
	}
	if tenantID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "tenant_id_required", "tenant_id must be positive")
		return 0, false
	}
	if err := ident.CanIssueForTenant(tenantID); err != nil {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "caller cannot act on this tenant scope")
		return 0, false
	}
	return tenantID, true
}

func resolveAdminTenantFromQuery(w http.ResponseWriter, r *http.Request, ident admin.AdminIdentity) (int64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	if raw == "" && ident.Role == admin.RoleTenantOperator {
		return resolveAdminTenantValue(w, ident, ident.ScopeTenantID)
	}
	tenantID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || tenantID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "tenant_id_required", "tenant_id query parameter must be positive")
		return 0, false
	}
	return resolveAdminTenantValue(w, ident, tenantID)
}

func parsePathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(r, "id")), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "announcement_id_required", "announcement id path parameter must be positive")
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

func decodeRequest(w http.ResponseWriter, r *http.Request, dst any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return false
	}
	return true
}

func writeAnnouncementError(w http.ResponseWriter, err error, code string) {
	switch {
	case errors.Is(err, announcement.ErrInvalidInput):
		writeJSONError(w, http.StatusBadRequest, code, "announcement request is invalid")
	case errors.Is(err, announcement.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, code, "announcement not found")
	case errors.Is(err, announcement.ErrStoreNotConfigured):
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "announcement dependency unset")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, code, "announcement backend unavailable")
	}
}

func announcementResponses(items []announcement.Announcement) []announcementResponse {
	out := make([]announcementResponse, 0, len(items))
	for _, item := range items {
		out = append(out, toAnnouncementResponse(item))
	}
	return out
}

func toAnnouncementResponse(ann announcement.Announcement) announcementResponse {
	resp := announcementResponse{
		ID:             ann.ID,
		TenantID:       ann.TenantID,
		Title:          ann.Title,
		Body:           ann.Body,
		Severity:       string(ann.Severity),
		Active:         ann.Active,
		PublishedAt:    formatTime(ann.PublishedAt),
		CreatedByAdmin: ann.CreatedByAdmin,
		CreatedAt:      formatTime(ann.CreatedAt),
		UpdatedAt:      formatTime(ann.UpdatedAt),
	}
	if ann.ExpiresAt != nil {
		formatted := formatTime(*ann.ExpiresAt)
		resp.ExpiresAt = &formatted
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
