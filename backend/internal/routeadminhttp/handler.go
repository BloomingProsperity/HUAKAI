// HUAKAI · iKun

// Package routeadminhttp 暴露订阅分组路由 (routes 表, F-POOL-001 §5.2) 的 admin HTTP 端点。
// handler 由 cmd/gateway/routes.go 挂载。仅 platform_admin 可访问,
// 租户范围来自请求体/查询参数, 审计归属的 adminID 一律取自已认证身份 (绝不取自请求体)。
package routeadminhttp

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
	"github.com/BloomingProsperity/HUAKAI/internal/routeadmin"
)

const maxBodyBytes = 1 << 20 // 1 MiB

// AdminAuth 解析入站 admin 凭据。
type AdminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

// Service 是 handler 依赖的 routes 管理能力子集 (由 *routeadmin.Service 实现)。
type Service interface {
	Create(context.Context, routeadmin.CreateInput) (routeadmin.Route, error)
	List(context.Context, int64) ([]routeadmin.Route, error)
	Get(context.Context, int64, int64) (routeadmin.Route, error)
	Delete(context.Context, int64, int64, int64) (routeadmin.Route, error)
}

// AdminDeps 管理员路由依赖。
type AdminDeps struct {
	Auth    AdminAuth
	Service Service
}

type createRouteRequest struct {
	TenantID          int64  `json:"tenant_id"`
	Name              string `json:"name"`
	UserGroupMatch    string `json:"user_group_match"`
	ModelPatternMatch string `json:"model_pattern_match,omitempty"`
	PoolGroupID       int64  `json:"pool_group_id"`
	MatchPriority     *int   `json:"match_priority,omitempty"`
}

// routeView 是面向管理员的 route DTO — snake_case, 仅暴露管理 CRUD 关心的核心字段。
type routeView struct {
	ID                int64     `json:"id"`
	TenantID          int64     `json:"tenant_id"`
	Name              string    `json:"name"`
	UserGroupMatch    string    `json:"user_group_match"`
	ModelPatternMatch string    `json:"model_pattern_match"`
	PoolGroupID       int64     `json:"pool_group_id"`
	MatchPriority     int       `json:"match_priority"`
	Enabled           bool      `json:"enabled"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

func toRouteView(r routeadmin.Route) routeView {
	return routeView{
		ID: r.ID, TenantID: r.TenantID, Name: r.Name, UserGroupMatch: r.UserGroupMatch,
		ModelPatternMatch: r.ModelPatternMatch, PoolGroupID: r.PoolGroupID, MatchPriority: r.MatchPriority,
		Enabled: r.Enabled, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func toRouteViews(routes []routeadmin.Route) []routeView {
	out := make([]routeView, 0, len(routes))
	for _, r := range routes {
		out = append(out, toRouteView(r))
	}
	return out
}

// MountRouteAdminRoutes 挂载管理员分组路由端点 (建 / 列 / 查 / 软删)。
func MountRouteAdminRoutes(r chi.Router, d AdminDeps) {
	r.Post("/", newAdminCreateRouteHandler(d))
	r.Get("/", newAdminListRoutesHandler(d))
	r.Get("/{id}", newAdminGetRouteHandler(d))
	r.Delete("/{id}", newAdminDeleteRouteHandler(d))
}

func newAdminCreateRouteHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(w, r, d)
		if !ok {
			return
		}
		var req createRouteRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		route, err := d.Service.Create(r.Context(), routeadmin.CreateInput{
			TenantID:          req.TenantID,
			Name:              req.Name,
			UserGroupMatch:    req.UserGroupMatch,
			ModelPatternMatch: req.ModelPatternMatch,
			PoolGroupID:       req.PoolGroupID,
			MatchPriority:     req.MatchPriority,
			AdminID:           ident.TokenID, // 审计归属取自已认证身份, 非请求体
		})
		if err != nil {
			writeRouteError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"route": toRouteView(route)})
	}
}

func newAdminListRoutesHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAdmin(w, r, d); !ok {
			return
		}
		tenantID, ok := parsePositiveQuery(w, r, "tenant_id")
		if !ok {
			return
		}
		routes, err := d.Service.List(r.Context(), tenantID)
		if err != nil {
			writeRouteError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"routes": toRouteViews(routes)})
	}
}

func newAdminGetRouteHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAdmin(w, r, d); !ok {
			return
		}
		tenantID, ok := parsePositiveQuery(w, r, "tenant_id")
		if !ok {
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		route, err := d.Service.Get(r.Context(), tenantID, id)
		if err != nil {
			writeRouteError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"route": toRouteView(route)})
	}
}

func newAdminDeleteRouteHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(w, r, d)
		if !ok {
			return
		}
		tenantID, ok := parsePositiveQuery(w, r, "tenant_id")
		if !ok {
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		route, err := d.Service.Delete(r.Context(), tenantID, id, ident.TokenID)
		if err != nil {
			writeRouteError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"route": toRouteView(route)})
	}
}

func resolveAdmin(w http.ResponseWriter, r *http.Request, d AdminDeps) (admin.AdminIdentity, bool) {
	if d.Auth == nil || d.Service == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "route admin dependency unset")
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
	if ident.Role != admin.RolePlatformAdmin {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "platform_admin role required")
		return admin.AdminIdentity{}, false
	}
	return ident, true
}

func parsePathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_route_id", "route id must be a positive int64")
		return 0, false
	}
	return id, true
}

func parsePositiveQuery(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		writeJSONError(w, http.StatusBadRequest, name+"_required", name+" query parameter must be positive")
		return 0, false
	}
	return n, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_route_request", "request body is not valid JSON")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

// writeRouteError 把 routeadmin 服务层错误哨兵映射为 HTTP 状态码。
func writeRouteError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, routeadmin.ErrInvalidModelPattern):
		writeJSONError(w, http.StatusBadRequest, "invalid_model_pattern", "model_pattern_match wildcard '*' only allowed as whole pattern or trailing suffix")
	case errors.Is(err, routeadmin.ErrInvalidInput):
		writeJSONError(w, http.StatusBadRequest, "invalid_route_request", "route request is invalid")
	case errors.Is(err, routeadmin.ErrRouteNotFound):
		writeJSONError(w, http.StatusNotFound, "route_not_found", "route not found for tenant")
	case errors.Is(err, routeadmin.ErrPoolGroupNotFound):
		writeJSONError(w, http.StatusNotFound, "pool_group_not_found", "target pool_group not found for tenant")
	case errors.Is(err, routeadmin.ErrDuplicateName):
		writeJSONError(w, http.StatusConflict, "route_name_conflict", "route name already exists for tenant")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "route_admin_backend_error", "route admin service unavailable")
	}
}
