// HUAKAI · iKun

// Package controlhttp 暴露订阅分组路由 (routes 表, F-POOL-001 §5.2) 的 admin HTTP 端点。
// handler 由 cmd/gateway/routes.go 挂载。仅 platform_admin 可访问,
// 租户范围来自请求体/查询参数, 审计归属的 adminID 一律取自已认证身份 (绝不取自请求体)。
package controlhttp

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

const routeAdminMaxBodyBytes = 1 << 20 // 1 MiB

// RouteAdminAuth 解析入站 admin 凭据。
type RouteAdminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

// RouteAdminService 是 handler 依赖的 routes 管理能力子集 (由 *routeadmin.RouteAdminService 实现)。
type RouteAdminService interface {
	Create(context.Context, routeadmin.CreateInput) (routeadmin.Route, error)
	List(context.Context, int64) ([]routeadmin.Route, error)
	Get(context.Context, int64, int64) (routeadmin.Route, error)
	Delete(context.Context, int64, int64, int64) (routeadmin.Route, error)
}

// RouteAdminDeps 管理员路由依赖。
type RouteAdminDeps struct {
	Auth    RouteAdminAuth
	Service RouteAdminService
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

func routeAdminToRouteView(r routeadmin.Route) routeView {
	return routeView{
		ID: r.ID, TenantID: r.TenantID, Name: r.Name, UserGroupMatch: r.UserGroupMatch,
		ModelPatternMatch: r.ModelPatternMatch, PoolGroupID: r.PoolGroupID, MatchPriority: r.MatchPriority,
		Enabled: r.Enabled, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func routeAdminToRouteViews(routes []routeadmin.Route) []routeView {
	out := make([]routeView, 0, len(routes))
	for _, r := range routes {
		out = append(out, routeAdminToRouteView(r))
	}
	return out
}

// MountRouteAdminRoutes 挂载管理员分组路由端点 (建 / 列 / 查 / 软删)。
func MountRouteAdminRoutes(r chi.Router, d RouteAdminDeps) {
	r.Post("/", newRouteAdminCreateHandler(d))
	r.Get("/", newRouteAdminListHandler(d))
	r.Get("/{id}", newRouteAdminGetHandler(d))
	r.Delete("/{id}", newRouteAdminDeleteHandler(d))
}

func newRouteAdminCreateHandler(d RouteAdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := routeAdminResolveAdmin(w, r, d)
		if !ok {
			return
		}
		var req createRouteRequest
		if !routeAdminDecodeJSON(w, r, &req) {
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
			routeAdminWriteRouteError(w, err)
			return
		}
		controlWriteJSON(w, http.StatusCreated, map[string]any{"route": routeAdminToRouteView(route)})
	}
}

func newRouteAdminListHandler(d RouteAdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := routeAdminResolveAdmin(w, r, d); !ok {
			return
		}
		tenantID, ok := routeAdminParsePositiveQuery(w, r, "tenant_id")
		if !ok {
			return
		}
		routes, err := d.Service.List(r.Context(), tenantID)
		if err != nil {
			routeAdminWriteRouteError(w, err)
			return
		}
		controlWriteJSON(w, http.StatusOK, map[string]any{"routes": routeAdminToRouteViews(routes)})
	}
}

func newRouteAdminGetHandler(d RouteAdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := routeAdminResolveAdmin(w, r, d); !ok {
			return
		}
		tenantID, ok := routeAdminParsePositiveQuery(w, r, "tenant_id")
		if !ok {
			return
		}
		id, ok := routeAdminParsePathID(w, r)
		if !ok {
			return
		}
		route, err := d.Service.Get(r.Context(), tenantID, id)
		if err != nil {
			routeAdminWriteRouteError(w, err)
			return
		}
		controlWriteJSON(w, http.StatusOK, map[string]any{"route": routeAdminToRouteView(route)})
	}
}

func newRouteAdminDeleteHandler(d RouteAdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := routeAdminResolveAdmin(w, r, d)
		if !ok {
			return
		}
		tenantID, ok := routeAdminParsePositiveQuery(w, r, "tenant_id")
		if !ok {
			return
		}
		id, ok := routeAdminParsePathID(w, r)
		if !ok {
			return
		}
		route, err := d.Service.Delete(r.Context(), tenantID, id, ident.TokenID)
		if err != nil {
			routeAdminWriteRouteError(w, err)
			return
		}
		controlWriteJSON(w, http.StatusOK, map[string]any{"route": routeAdminToRouteView(route)})
	}
}

func routeAdminResolveAdmin(w http.ResponseWriter, r *http.Request, d RouteAdminDeps) (admin.AdminIdentity, bool) {
	if d.Auth == nil || d.Service == nil {
		controlWriteJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "route admin dependency unset")
		return admin.AdminIdentity{}, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		if errors.Is(err, admin.ErrAdminBackend) {
			controlWriteJSONError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
		} else {
			controlWriteJSONError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
		}
		return admin.AdminIdentity{}, false
	}
	if ident.Role != admin.RolePlatformAdmin {
		controlWriteJSONError(w, http.StatusForbidden, "admin_forbidden", "platform_admin role required")
		return admin.AdminIdentity{}, false
	}
	return ident, true
}

func routeAdminParsePathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		controlWriteJSONError(w, http.StatusBadRequest, "invalid_route_id", "route id must be a positive int64")
		return 0, false
	}
	return id, true
}

func routeAdminParsePositiveQuery(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		controlWriteJSONError(w, http.StatusBadRequest, name+"_required", name+" query parameter must be positive")
		return 0, false
	}
	return n, true
}

func routeAdminDecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, routeAdminMaxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		controlWriteJSONError(w, http.StatusBadRequest, "invalid_route_request", "request body is not valid JSON")
		return false
	}
	return true
}

// routeAdminWriteRouteError 把 routeadmin 服务层错误哨兵映射为 HTTP 状态码。
func routeAdminWriteRouteError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, routeadmin.ErrInvalidModelPattern):
		controlWriteJSONError(w, http.StatusBadRequest, "invalid_model_pattern", "model_pattern_match wildcard '*' only allowed as whole pattern or trailing suffix")
	case errors.Is(err, routeadmin.ErrInvalidInput):
		controlWriteJSONError(w, http.StatusBadRequest, "invalid_route_request", "route request is invalid")
	case errors.Is(err, routeadmin.ErrRouteNotFound):
		controlWriteJSONError(w, http.StatusNotFound, "route_not_found", "route not found for tenant")
	case errors.Is(err, routeadmin.ErrPoolGroupNotFound):
		controlWriteJSONError(w, http.StatusNotFound, "pool_group_not_found", "target pool_group not found for tenant")
	case errors.Is(err, routeadmin.ErrDuplicateName):
		controlWriteJSONError(w, http.StatusConflict, "route_name_conflict", "route name already exists for tenant")
	default:
		controlWriteJSONError(w, http.StatusServiceUnavailable, "route_admin_backend_error", "route admin service unavailable")
	}
}
