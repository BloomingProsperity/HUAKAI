package adminhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

type AdminChannelTestTemplateDeps struct {
	Auth  adminCatalogAuth
	Store adminChannelTestTemplateStore
}

type adminChannelTestTemplateStore interface {
	CreateChannelTestTemplate(context.Context, admindb.CreateChannelTestTemplateParams) (admindb.ChannelTestTemplate, error)
	ListChannelTestTemplatesByTenant(context.Context, admindb.ListChannelTestTemplatesByTenantParams) ([]admindb.ChannelTestTemplate, error)
	GetChannelTestTemplate(context.Context, admindb.GetChannelTestTemplateParams) (admindb.ChannelTestTemplate, error)
	UpdateChannelTestTemplate(context.Context, admindb.UpdateChannelTestTemplateParams) (admindb.ChannelTestTemplate, error)
	DeleteChannelTestTemplate(context.Context, admindb.DeleteChannelTestTemplateParams) (admindb.ChannelTestTemplate, error)
}

type channelTestTemplateListResponse struct {
	Object string                    `json:"object"`
	Items  []channelTestTemplateItem `json:"items"`
	Limit  int32                     `json:"limit"`
	Offset int32                     `json:"offset"`
}

type channelTestTemplateItem struct {
	ID           int64           `json:"id"`
	TenantID     int64           `json:"tenant_id"`
	Name         string          `json:"name"`
	Method       string          `json:"method"`
	Path         string          `json:"path"`
	BodyTemplate string          `json:"body_template"`
	Headers      json.RawMessage `json:"headers"`
	CreatedAt    string          `json:"created_at"`
}

type channelTestTemplateRequest struct {
	Name         string          `json:"name"`
	Method       string          `json:"method"`
	Path         string          `json:"path"`
	BodyTemplate string          `json:"body_template"`
	Headers      json.RawMessage `json:"headers"`
}

type channelTestTemplateDeleteResponse struct {
	Object  string `json:"object"`
	ID      int64  `json:"id"`
	Deleted bool   `json:"deleted"`
}

func MountChannelTestTemplateRoutes(r chi.Router, d AdminChannelTestTemplateDeps) {
	r.Get("/", newChannelTestTemplateListHandler(d))
	r.Post("/", newChannelTestTemplateCreateHandler(d))
	r.Get("/{id}", newChannelTestTemplateGetHandler(d))
	r.Put("/{id}", newChannelTestTemplateUpdateHandler(d))
	r.Delete("/{id}", newChannelTestTemplateDeleteHandler(d))
}

func NewChannelTestTemplateListHandler(d AdminChannelTestTemplateDeps) http.HandlerFunc {
	return newChannelTestTemplateListHandler(d)
}

func NewChannelTestTemplateCreateHandler(d AdminChannelTestTemplateDeps) http.HandlerFunc {
	return newChannelTestTemplateCreateHandler(d)
}

func NewChannelTestTemplateGetHandler(d AdminChannelTestTemplateDeps) http.HandlerFunc {
	return newChannelTestTemplateGetHandler(d)
}

func NewChannelTestTemplateUpdateHandler(d AdminChannelTestTemplateDeps) http.HandlerFunc {
	return newChannelTestTemplateUpdateHandler(d)
}

func NewChannelTestTemplateDeleteHandler(d AdminChannelTestTemplateDeps) http.HandlerFunc {
	return newChannelTestTemplateDeleteHandler(d)
}

func newChannelTestTemplateListHandler(d AdminChannelTestTemplateDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveChannelTestTemplateAdmin(w, r, d)
		if !ok {
			return
		}
		page, ok := parseAdminCatalogPage(w, r, ident)
		if !ok {
			return
		}
		rows, err := d.Store.ListChannelTestTemplatesByTenant(r.Context(), admindb.ListChannelTestTemplatesByTenantParams{
			TenantID:   page.TenantID,
			PageLimit:  page.Limit,
			PageOffset: page.Offset,
		})
		if err != nil {
			writeChannelTestTemplateError(w, err, "channel_test_template_list_failed")
			return
		}
		items := make([]channelTestTemplateItem, 0, len(rows))
		for _, row := range rows {
			items = append(items, channelTestTemplateItemFromRow(row))
		}
		writeAdminCatalogJSON(w, http.StatusOK, channelTestTemplateListResponse{
			Object: "admin_channel_test_templates_list",
			Items:  items,
			Limit:  page.Limit,
			Offset: page.Offset,
		})
	}
}

func newChannelTestTemplateCreateHandler(d AdminChannelTestTemplateDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveChannelTestTemplateAdmin(w, r, d)
		if !ok {
			return
		}
		tenantID, ok := parseAdminCatalogTenant(w, r, ident)
		if !ok {
			return
		}
		req, ok := decodeChannelTestTemplateRequest(w, r, true)
		if !ok {
			return
		}
		arg, ok := validateChannelTestTemplateRequest(w, tenantID, req)
		if !ok {
			return
		}
		row, err := d.Store.CreateChannelTestTemplate(r.Context(), admindb.CreateChannelTestTemplateParams{
			TenantID:     arg.TenantID,
			Name:         arg.Name,
			Method:       arg.Method,
			Path:         arg.Path,
			BodyTemplate: arg.BodyTemplate,
			Headers:      arg.Headers,
		})
		if err != nil {
			writeChannelTestTemplateError(w, err, "channel_test_template_create_failed")
			return
		}
		writeAdminCatalogJSON(w, http.StatusCreated, channelTestTemplateItemFromRow(row))
	}
}

func newChannelTestTemplateGetHandler(d AdminChannelTestTemplateDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveChannelTestTemplateAdmin(w, r, d)
		if !ok {
			return
		}
		tenantID, ok := parseAdminCatalogTenant(w, r, ident)
		if !ok {
			return
		}
		id, ok := parseChannelTestTemplateID(w, r)
		if !ok {
			return
		}
		row, err := d.Store.GetChannelTestTemplate(r.Context(), admindb.GetChannelTestTemplateParams{TenantID: tenantID, ID: id})
		if err != nil {
			writeChannelTestTemplateError(w, err, "channel_test_template_get_failed")
			return
		}
		writeAdminCatalogJSON(w, http.StatusOK, channelTestTemplateItemFromRow(row))
	}
}

func newChannelTestTemplateUpdateHandler(d AdminChannelTestTemplateDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveChannelTestTemplateAdmin(w, r, d)
		if !ok {
			return
		}
		tenantID, ok := parseAdminCatalogTenant(w, r, ident)
		if !ok {
			return
		}
		id, ok := parseChannelTestTemplateID(w, r)
		if !ok {
			return
		}
		req, ok := decodeChannelTestTemplateRequest(w, r, true)
		if !ok {
			return
		}
		arg, ok := validateChannelTestTemplateRequest(w, tenantID, req)
		if !ok {
			return
		}
		row, err := d.Store.UpdateChannelTestTemplate(r.Context(), admindb.UpdateChannelTestTemplateParams{
			TenantID:     tenantID,
			ID:           id,
			Name:         arg.Name,
			Method:       arg.Method,
			Path:         arg.Path,
			BodyTemplate: arg.BodyTemplate,
			Headers:      arg.Headers,
		})
		if err != nil {
			writeChannelTestTemplateError(w, err, "channel_test_template_update_failed")
			return
		}
		writeAdminCatalogJSON(w, http.StatusOK, channelTestTemplateItemFromRow(row))
	}
}

func newChannelTestTemplateDeleteHandler(d AdminChannelTestTemplateDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveChannelTestTemplateAdmin(w, r, d)
		if !ok {
			return
		}
		tenantID, ok := parseAdminCatalogTenant(w, r, ident)
		if !ok {
			return
		}
		id, ok := parseChannelTestTemplateID(w, r)
		if !ok {
			return
		}
		row, err := d.Store.DeleteChannelTestTemplate(r.Context(), admindb.DeleteChannelTestTemplateParams{TenantID: tenantID, ID: id})
		if err != nil {
			writeChannelTestTemplateError(w, err, "channel_test_template_delete_failed")
			return
		}
		writeAdminCatalogJSON(w, http.StatusOK, channelTestTemplateDeleteResponse{
			Object:  "admin_channel_test_template_deleted",
			ID:      row.ID,
			Deleted: true,
		})
	}
}

func resolveChannelTestTemplateAdmin(w http.ResponseWriter, r *http.Request, d AdminChannelTestTemplateDeps) (admin.AdminIdentity, bool) {
	if d.Auth == nil || d.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "gateway_not_configured",
			"admin channel test template dependency unset")
		return admin.AdminIdentity{}, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		writeAdminAuthError(w, err)
		return admin.AdminIdentity{}, false
	}
	switch ident.Role {
	case admin.RoleTenantOperator, admin.RolePlatformAdmin:
	default:
		writeError(w, http.StatusForbidden, "admin_forbidden", "admin role required")
		return admin.AdminIdentity{}, false
	}
	return ident, true
}

type channelTestTemplateParams struct {
	TenantID     int64
	Name         string
	Method       string
	Path         string
	BodyTemplate string
	Headers      []byte
}

func decodeChannelTestTemplateRequest(w http.ResponseWriter, r *http.Request, required bool) (channelTestTemplateRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "body_read_error", err.Error())
		return channelTestTemplateRequest{}, false
	}
	if strings.TrimSpace(string(body)) == "" {
		if required {
			writeError(w, http.StatusBadRequest, "invalid_json", "request body is required")
			return channelTestTemplateRequest{}, false
		}
		return channelTestTemplateRequest{}, true
	}
	var req channelTestTemplateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return channelTestTemplateRequest{}, false
	}
	return req, true
}

func validateChannelTestTemplateRequest(w http.ResponseWriter, tenantID int64, req channelTestTemplateRequest) (channelTestTemplateParams, bool) {
	name := strings.TrimSpace(req.Name)
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	path := strings.TrimSpace(req.Path)
	if name == "" || len(name) > 128 {
		writeError(w, http.StatusBadRequest, "invalid_template_name", "name is required and must be at most 128 characters")
		return channelTestTemplateParams{}, false
	}
	if !isAllowedChannelTestTemplateMethod(method) {
		writeError(w, http.StatusBadRequest, "invalid_template_method", "method must be GET, POST, PUT, PATCH, or DELETE")
		return channelTestTemplateParams{}, false
	}
	if path == "" || !strings.HasPrefix(path, "/") || len(path) > 2048 {
		writeError(w, http.StatusBadRequest, "invalid_template_path", "path must start with / and be at most 2048 characters")
		return channelTestTemplateParams{}, false
	}
	headers, ok := normalizeChannelTestTemplateHeaders(w, req.Headers)
	if !ok {
		return channelTestTemplateParams{}, false
	}
	return channelTestTemplateParams{
		TenantID:     tenantID,
		Name:         name,
		Method:       method,
		Path:         path,
		BodyTemplate: req.BodyTemplate,
		Headers:      headers,
	}, true
}

func normalizeChannelTestTemplateHeaders(w http.ResponseWriter, raw json.RawMessage) ([]byte, bool) {
	if len(bytesTrimSpace(raw)) == 0 {
		return []byte(`{}`), true
	}
	var headers map[string]json.RawMessage
	if err := json.Unmarshal(raw, &headers); err != nil || headers == nil {
		writeError(w, http.StatusBadRequest, "invalid_template_headers", "headers must be a JSON object")
		return nil, false
	}
	for name := range headers {
		if isCredentialHeaderName(name) {
			writeError(w, http.StatusBadRequest, "credential_header_not_allowed",
				fmt.Sprintf("header %q must not be stored in channel test templates", name))
			return nil, false
		}
	}
	normalized, err := json.Marshal(headers)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_template_headers", err.Error())
		return nil, false
	}
	return normalized, true
}

func parseChannelTestTemplateID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(r, "id")), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_template_id", "id must be a positive int64")
		return 0, false
	}
	return id, true
}

func channelTestTemplateItemFromRow(row admindb.ChannelTestTemplate) channelTestTemplateItem {
	headers := json.RawMessage(row.Headers)
	if len(bytesTrimSpace(headers)) == 0 {
		headers = json.RawMessage(`{}`)
	}
	return channelTestTemplateItem{
		ID:           row.ID,
		TenantID:     row.TenantID,
		Name:         row.Name,
		Method:       row.Method,
		Path:         row.Path,
		BodyTemplate: row.BodyTemplate,
		Headers:      headers,
		CreatedAt:    formatCatalogTime(row.CreatedAt),
	}
}

func isAllowedChannelTestTemplateMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func isCredentialHeaderName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "proxy-authorization", "cookie", "x-api-key", "api-key", "x-auth-token":
		return true
	default:
		return false
	}
}

func bytesTrimSpace(raw []byte) []byte {
	return []byte(strings.TrimSpace(string(raw)))
}

func writeChannelTestTemplateError(w http.ResponseWriter, err error, fallbackCode string) {
	switch {
	case isChannelTestTemplateUniqueViolation(err):
		writeError(w, http.StatusConflict, "channel_test_template_name_conflict", "channel test template name already exists")
	case errors.Is(err, pgx.ErrNoRows):
		writeError(w, http.StatusNotFound, "channel_test_template_not_found", "channel test template not found")
	default:
		writeError(w, http.StatusServiceUnavailable, fallbackCode, err.Error())
	}
}

func isChannelTestTemplateUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == "23505" &&
		pgErr.ConstraintName == "uq_channel_test_templates_tenant_name"
}
