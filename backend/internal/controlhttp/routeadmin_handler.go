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
	"github.com/go-chi/chi/v5/middleware"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauth"
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
	Update(context.Context, routeadmin.UpdateInput) (routeadmin.Route, error)
	SetEnabledWithActor(context.Context, int64, int64, bool, routeadmin.MutationLog) (routeadmin.Route, error)
	DeleteWithActor(context.Context, int64, int64, routeadmin.MutationLog) (routeadmin.Route, error)
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

// updateRouteRequest 是 PUT /{id} 的请求体。**不含 tenant_id** —— 租户取自 query 参数、行由
// path id 定位, 故 body 内出现 tenant_id 会被 DisallowUnknownFields 拒(防经更新跨租户搬移/走私)。
type updateRouteRequest struct {
	Name              string `json:"name"`
	UserGroupMatch    string `json:"user_group_match"`
	ModelPatternMatch string `json:"model_pattern_match,omitempty"`
	PoolGroupID       int64  `json:"pool_group_id"`
	MatchPriority     *int   `json:"match_priority,omitempty"`
}

// setRouteEnabledRequest 是 PUT /{id}/enabled 的请求体。**不含 tenant_id** —— 租户取自 query、行由 path id
// 定位, body 内出现 tenant_id 会被 DisallowUnknownFields 拒(防经此面跨租户搬移/走私)。Enabled 用 *bool 强制
// 显式存在: 缺字段(空 body `{}`)→ nil → 400, 防客户端漏传 enabled 时按 Go 零值静默把路由停用。
type setRouteEnabledRequest struct {
	Enabled *bool `json:"enabled"`
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

// MountRouteAdminRoutes 挂载管理员分组路由端点 (建 / 列 / 查 / 改 / 启停 / 软删)。
// {id}/enabled 与 {id} 段深不同, chi 不冲突: PUT /55 命中 {id}(全替换), PUT /55/enabled 命中 {id}/enabled(启停)。
func MountRouteAdminRoutes(r chi.Router, d RouteAdminDeps) {
	// SessionSafe: 分组路由增改停删允许登录管理员会话写入；权限、租户条件和操作日志
	// 全部由后端强制，前端确认弹窗只减少误操作，不承担鉴权职责。
	safe := adminsessionauth.AllowSessionWrite(adminsessionauth.SessionSafe)
	r.With(safe).Post("/", newRouteAdminCreateHandler(d))
	r.Get("/", newRouteAdminListHandler(d))
	r.Get("/{id}", newRouteAdminGetHandler(d))
	r.With(safe).Put("/{id}", newRouteAdminUpdateHandler(d))
	r.With(safe).Put("/{id}/enabled", newRouteAdminSetEnabledHandler(d))
	r.With(safe).Delete("/{id}", newRouteAdminDeleteHandler(d))
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
			AdminID:           ident.TokenID,
			ActorID:           ident.AuditActor(),
			ActorRole:         ident.Role,
			RequestID:         middleware.GetReqID(r.Context()),
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

// newRouteAdminUpdateHandler 处理 PUT /{id}: 全替换一条 route 的可编辑字段。
// PUT 幂等: 同一请求体重放产生同一行状态(updated_at 每次写都 bump, 仅供审计追踪, 不破坏幂等)。
// 全替换语义提醒: 省略 match_priority 即回落 DB 默认 100 —— 后续 priority 真裁决落地后该字段变
// 真选路键, 故前端编辑须 read-modify-write 始终显式带 match_priority, 防 read-omit-write 静默重置。
func newRouteAdminUpdateHandler(d RouteAdminDeps) http.HandlerFunc {
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
		var req updateRouteRequest
		if !routeAdminDecodeJSON(w, r, &req) {
			return
		}
		route, err := d.Service.Update(r.Context(), routeadmin.UpdateInput{
			TenantID:          tenantID, // 取自 query, 不可经 body 改 → 防跨租户搬移
			ID:                id,       // 取自 path
			Name:              req.Name,
			UserGroupMatch:    req.UserGroupMatch,
			ModelPatternMatch: req.ModelPatternMatch,
			PoolGroupID:       req.PoolGroupID,
			MatchPriority:     req.MatchPriority,
			AdminID:           ident.TokenID,
			ActorID:           ident.AuditActor(),
			ActorRole:         ident.Role,
			RequestID:         middleware.GetReqID(r.Context()),
		})
		if err != nil {
			routeAdminWriteRouteError(w, err)
			return
		}
		controlWriteJSON(w, http.StatusOK, map[string]any{"route": routeAdminToRouteView(route)})
	}
}

// newRouteAdminSetEnabledHandler 处理 PUT /{id}/enabled: 独立翻转一条 route 的 enabled 闸(非全替换)。
// 停用即把路由移出分组路由生效集(热路径 gate 过滤 enabled=true)而不软删, 可后续再启用。幂等。
// tenant 取 query、id 取 path、操作者取已认证身份；enabled 必须显式在 body 给(*bool, 防空 body 静默停用)。
func newRouteAdminSetEnabledHandler(d RouteAdminDeps) http.HandlerFunc {
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
		var req setRouteEnabledRequest
		if !routeAdminDecodeJSON(w, r, &req) {
			return
		}
		if req.Enabled == nil {
			controlWriteJSONError(w, http.StatusBadRequest, "invalid_route_request", "enabled field is required")
			return
		}
		route, err := d.Service.SetEnabledWithActor(r.Context(), tenantID, id, *req.Enabled, routeadmin.MutationLog{
			ActorID:       ident.AuditActor(),
			ActorRole:     ident.Role,
			RequestID:     middleware.GetReqID(r.Context()),
			LegacyAdminID: ident.TokenID,
		})
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
		route, err := d.Service.DeleteWithActor(r.Context(), tenantID, id, routeadmin.MutationLog{
			ActorID:       ident.AuditActor(),
			ActorRole:     ident.Role,
			RequestID:     middleware.GetReqID(r.Context()),
			LegacyAdminID: ident.TokenID,
		})
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
