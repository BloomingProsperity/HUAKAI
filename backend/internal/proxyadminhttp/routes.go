// Package proxyadminhttp exposes the tenant-scoped admin HTTP surface for the
// outbound proxy pool (list / create / update / delete / set-status). It is a thin
// transport layer over internal/proxyadmin.Service: the admin gate (tenant_operator
// own-scope, platform_admin via ?tenant_id+CanIssueForTenant) mirrors adminuserhttp,
// and every response DTO is secret-free — the encrypted auth_secret is write-only and
// never projected onto a read path.
package proxyadminhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/proxyadmin"
)

// adminAuth resolves an admin credential to an identity (same shape as
// adminuserhttp.adminAuth; defined locally so the packages stay decoupled).
type adminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

// proxyService is the narrow interface over proxyadmin.Service this surface needs.
// Declaring it here (rather than depending on the concrete *Service) keeps the
// handlers testable with a stub and documents the exact methods consumed.
type proxyService interface {
	Create(ctx context.Context, in proxyadmin.CreateInput) (proxyadmin.Proxy, error)
	Update(ctx context.Context, in proxyadmin.UpdateInput) (proxyadmin.Proxy, error)
	Get(ctx context.Context, tenantID, id int64) (proxyadmin.Proxy, error)
	List(ctx context.Context, tenantID int64) ([]proxyadmin.Proxy, error)
	Delete(ctx context.Context, tenantID, id int64) error
	SetStatus(ctx context.Context, tenantID, id int64, status string) error
}

// Deps wires the admin proxy surface. Auth is the shared admin resolver; Service is
// the proxyadmin business layer.
type Deps struct {
	Auth    adminAuth
	Service proxyService
}

// MountRoutes registers the proxy admin endpoints on r. Callers mount it under
// /admin/v1/proxies (mirroring adminuserhttp.MountRoutes under /admin/v1/users).
func MountRoutes(r chi.Router, d Deps) {
	r.Get("/", newListHandler(d))
	r.Post("/", newCreateHandler(d))
	r.Get("/{id}", newGetHandler(d))
	r.Patch("/{id}", newUpdateHandler(d))
	r.Delete("/{id}", newDeleteHandler(d))
	r.Put("/{id}/status", newSetStatusHandler(d))
}

// NewRouter returns a standalone router with the proxy admin endpoints mounted at
// its root.
func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	MountRoutes(r, d)
	return r
}

// proxyResponse is the secret-free read DTO. It deliberately has no auth_secret
// field: the encrypted credential is write-only and must never leave the service.
type proxyResponse struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	Protocol     string  `json:"protocol"`
	Host         string  `json:"host"`
	Port         int32   `json:"port"`
	AuthUsername *string `json:"auth_username"`
	Status       string  `json:"status"`
	LastCheckAt  *string `json:"last_check_at"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
}

func toProxyResponse(p proxyadmin.Proxy) proxyResponse {
	return proxyResponse{
		ID:           p.ID,
		Name:         p.Name,
		Protocol:     p.Protocol,
		Host:         p.Host,
		Port:         p.Port,
		AuthUsername: p.AuthUsername,
		Status:       p.Status,
		LastCheckAt:  timestampPtr(p.LastCheckAt),
		CreatedAt:    timestamp(p.CreatedAt),
		UpdatedAt:    timestamp(p.UpdatedAt),
	}
}

type createProxyRequest struct {
	Name         string  `json:"name"`
	Protocol     string  `json:"protocol"`
	Host         string  `json:"host"`
	Port         int32   `json:"port"`
	AuthUsername *string `json:"auth_username,omitempty"`
	AuthSecret   *string `json:"auth_secret,omitempty"`
	Status       string  `json:"status,omitempty"`
}

type updateProxyRequest struct {
	Name         string  `json:"name"`
	Protocol     string  `json:"protocol"`
	Host         string  `json:"host"`
	Port         int32   `json:"port"`
	AuthUsername *string `json:"auth_username,omitempty"`
	AuthSecret   *string `json:"auth_secret,omitempty"`
}

type setStatusRequest struct {
	Status string `json:"status"`
}

func newListHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, ok := resolveTenant(w, r, d)
		if !ok {
			return
		}
		proxies, err := d.Service.List(r.Context(), tenantID)
		if err != nil {
			writeServiceError(w, err, "list proxies failed")
			return
		}
		items := make([]proxyResponse, 0, len(proxies))
		for _, p := range proxies {
			items = append(items, toProxyResponse(p))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

func newGetHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, ok := resolveTenant(w, r, d)
		if !ok {
			return
		}
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		p, err := d.Service.Get(r.Context(), tenantID, id)
		if err != nil {
			writeServiceError(w, err, "get proxy failed")
			return
		}
		writeJSON(w, http.StatusOK, toProxyResponse(p))
	}
}

func newCreateHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, ok := resolveTenant(w, r, d)
		if !ok {
			return
		}
		var req createProxyRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Protocol) == "" ||
			strings.TrimSpace(req.Host) == "" || req.Port <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_proxy",
				"name, protocol, host are required and port must be a positive integer")
			return
		}
		p, err := d.Service.Create(r.Context(), proxyadmin.CreateInput{
			TenantID:     tenantID,
			Name:         req.Name,
			Protocol:     req.Protocol,
			Host:         req.Host,
			Port:         req.Port,
			AuthUsername: req.AuthUsername,
			AuthSecret:   req.AuthSecret,
			Status:       req.Status,
		})
		if err != nil {
			writeServiceError(w, err, "create proxy failed")
			return
		}
		writeJSON(w, http.StatusCreated, toProxyResponse(p))
	}
}

func newUpdateHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, ok := resolveTenant(w, r, d)
		if !ok {
			return
		}
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		var req updateProxyRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Protocol) == "" ||
			strings.TrimSpace(req.Host) == "" || req.Port <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_proxy",
				"name, protocol, host are required and port must be a positive integer")
			return
		}
		p, err := d.Service.Update(r.Context(), proxyadmin.UpdateInput{
			TenantID:     tenantID,
			ID:           id,
			Name:         req.Name,
			Protocol:     req.Protocol,
			Host:         req.Host,
			Port:         req.Port,
			AuthUsername: req.AuthUsername,
			AuthSecret:   req.AuthSecret,
		})
		if err != nil {
			writeServiceError(w, err, "update proxy failed")
			return
		}
		writeJSON(w, http.StatusOK, toProxyResponse(p))
	}
}

func newDeleteHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, ok := resolveTenant(w, r, d)
		if !ok {
			return
		}
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		if err := d.Service.Delete(r.Context(), tenantID, id); err != nil {
			writeServiceError(w, err, "delete proxy failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func newSetStatusHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, ok := resolveTenant(w, r, d)
		if !ok {
			return
		}
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		var req setStatusRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if err := d.Service.SetStatus(r.Context(), tenantID, id, req.Status); err != nil {
			writeServiceError(w, err, "set proxy status failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": strings.TrimSpace(req.Status)})
	}
}

// resolveTenant runs the admin gate and returns the operation's target tenant. It
// short-circuits (writing the response) on any auth/scope failure BEFORE the service
// is consulted. Mirrors adminuserhttp.resolveTenantIdentity.
func resolveTenant(w http.ResponseWriter, r *http.Request, d Deps) (int64, bool) {
	if d.Auth == nil || d.Service == nil {
		writeError(w, http.StatusServiceUnavailable, "admin_proxies_not_configured",
			"admin proxies dependency unset")
		return 0, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		writeAdminAuthError(w, err)
		return 0, false
	}
	switch ident.Role {
	case admin.RoleTenantOperator:
		if ident.ScopeTenantID <= 0 {
			writeError(w, http.StatusForbidden, "admin_tenant_scope_required",
				"tenant_operator scope_tenant_id required")
			return 0, false
		}
		return tenantFromQueryOrScope(w, r, ident)
	case admin.RolePlatformAdmin:
		// Single-tenant out-of-box: platform_admin must name ?tenant_id, gated by
		// CanIssueForTenant. RBAC unchanged — cross-tenant but explicit.
		return tenantFromQueryOrScope(w, r, ident)
	default:
		writeError(w, http.StatusForbidden, "admin_forbidden_scope", "admin role required")
		return 0, false
	}
}

// tenantFromQueryOrScope resolves the target tenant from ?tenant_id (validated via
// CanIssueForTenant) or falls back to a tenant_operator's own scope. Local copy of
// the adminuserhttp pattern.
func tenantFromQueryOrScope(w http.ResponseWriter, r *http.Request, ident admin.AdminIdentity) (int64, bool) {
	tenantParam := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	var tenantID int64
	if tenantParam == "" {
		if ident.Role != admin.RoleTenantOperator {
			writeError(w, http.StatusBadRequest, "tenant_id_required",
				"tenant_id query param required for platform_admin")
			return 0, false
		}
		tenantID = ident.ScopeTenantID
	} else {
		v, err := strconv.ParseInt(tenantParam, 10, 64)
		if err != nil || v <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_tenant_id",
				"tenant_id must be a positive int64")
			return 0, false
		}
		tenantID = v
	}
	if tenantID <= 0 {
		writeAdminAuthError(w, admin.ErrAdminForbidden)
		return 0, false
	}
	if err := ident.CanIssueForTenant(tenantID); err != nil {
		writeAdminAuthError(w, err)
		return 0, false
	}
	return tenantID, true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := strings.TrimSpace(chi.URLParam(r, "id"))
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_proxy_id", "proxy id must be a positive int64")
		return 0, false
	}
	return id, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return false
	}
	return true
}

// writeServiceError maps proxyadmin sentinels onto HTTP status codes:
// ErrInvalidInput/ErrInvalidStatus/ErrUnsafeHost -> 400, ErrNotFound -> 404,
// ErrBackend (and anything else) -> 503.
func writeServiceError(w http.ResponseWriter, err error, context string) {
	switch {
	case errors.Is(err, proxyadmin.ErrInvalidStatus):
		writeError(w, http.StatusBadRequest, "invalid_status",
			"status must be one of active, disabled, dead")
	case errors.Is(err, proxyadmin.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_proxy", "proxy request is invalid")
	case errors.Is(err, proxyadmin.ErrUnsafeHost):
		writeError(w, http.StatusBadRequest, "unsafe_proxy_host",
			"proxy host resolves to a blocked (loopback/private/metadata) target")
	case errors.Is(err, proxyadmin.ErrNotFound):
		writeError(w, http.StatusNotFound, "admin_proxy_not_found", "proxy not found")
	default:
		writeError(w, http.StatusServiceUnavailable, "admin_proxies_backend_error",
			fmt.Sprintf("%s: %v", context, err))
	}
}

func writeAdminAuthError(w http.ResponseWriter, err error) {
	if errors.Is(err, admin.ErrAdminBackend) {
		writeError(w, http.StatusServiceUnavailable, "admin_backend_error",
			"admin auth backend transient failure")
		return
	}
	if errors.Is(err, admin.ErrAdminForbidden) {
		writeError(w, http.StatusForbidden, "admin_forbidden_scope",
			"admin credential is not allowed for this tenant")
		return
	}
	writeError(w, http.StatusUnauthorized, "admin_unauthorized",
		"missing or invalid admin credential")
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]map[string]string{
		"error": {"code": code, "message": message},
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func timestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func timestampPtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}
