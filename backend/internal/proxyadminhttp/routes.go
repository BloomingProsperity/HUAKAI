// Package proxyadminhttp 暴露出站代理池按租户收敛的管理 HTTP 面
// (list / create / update / delete / set-status)。它是 internal/proxyadmin.Service
// 之上的一层薄传输层:管理门(tenant_operator 限本租户、platform_admin 经
// ?tenant_id+CanActOnTenant)与 adminuserhttp 一致,且每个响应 DTO 都不含凭据——
// 加密的 auth_secret 只写,绝不投影到任何读取路径。
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

// adminAuth 把一个管理凭据解析为身份(与 adminuserhttp.adminAuth 同形;
// 本地定义以保持各包解耦)。
type adminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

// proxyService 是本面所需的、对 proxyadmin.Service 的窄接口。
// 在此声明(而非依赖具体的 *Service)既让 handler 可用桩测试,
// 又把实际消费的方法清楚记录下来。
type proxyService interface {
	Create(ctx context.Context, in proxyadmin.CreateInput) (proxyadmin.Proxy, error)
	Update(ctx context.Context, in proxyadmin.UpdateInput) (proxyadmin.Proxy, error)
	Get(ctx context.Context, tenantID, id int64) (proxyadmin.Proxy, error)
	List(ctx context.Context, tenantID int64) ([]proxyadmin.Proxy, error)
	Delete(ctx context.Context, tenantID, id int64) error
	SetStatus(ctx context.Context, tenantID, id int64, status string) error
}

// Deps 接线管理代理面。Auth 是共享的管理解析器;Service 是 proxyadmin 业务层;
// Prober(可选)执行主动 probe-through 质检。
type Deps struct {
	Auth           adminAuth
	Service        proxyService
	Prober         Prober
	TenantDefaults tenantDefaultProxyService
}

// Prober 抽象"经该代理建隧道到固定 canary 的主动质检"。实现在 cmd/gateway 组合
// proxyadmin.DialTarget(解密拨号 URL)+ proxyhealth.ProbeThrough(SSRF 守卫 + 隧道 + TLS 握手)。
// 凭据全程留在实现内,本接口只回不含凭据的结果。导出以便接线层返回接口类型(规避 typed-nil 陷阱)。
type Prober interface {
	Probe(ctx context.Context, tenantID, id int64) (ProbeOutcome, error)
}

// ProbeOutcome 是一次主动质检的不含凭据结果(error_class 为粗粒度枚举,绝不含原始错误/代理细节)。
type ProbeOutcome struct {
	OK         bool
	LatencyMS  int64
	ErrorClass string
}

// MountRoutes 把代理管理端点注册到 r 上。调用方将其挂在 /admin/v1/proxies 下
// (与挂在 /admin/v1/users 下的 adminuserhttp.MountRoutes 对应)。
func MountRoutes(r chi.Router, d Deps) {
	r.Get("/", newListHandler(d))
	r.Post("/", newCreateHandler(d))
	r.Get("/{id}", newGetHandler(d))
	r.Patch("/{id}", newUpdateHandler(d))
	r.Delete("/{id}", newDeleteHandler(d))
	r.Put("/{id}/status", newSetStatusHandler(d))
	r.Post("/{id}/test", newTestHandler(d))
}

// NewRouter 返回一个独立路由器,代理管理端点挂在其根路径上。
func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	MountRoutes(r, d)
	return r
}

// proxyResponse 是不含凭据的读取 DTO。它刻意没有 auth_secret 字段:
// 加密的凭据只写,绝不能离开 service。
type proxyResponse struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	Protocol     string  `json:"protocol"`
	Host         string  `json:"host"`
	Port         int32   `json:"port"`
	AuthUsername *string `json:"auth_username"`
	GroupID      *string `json:"group_id"`
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
		GroupID:      p.GroupID,
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
	GroupID      *string `json:"group_id"`
	Status       string  `json:"status,omitempty"`
}

type updateProxyRequest struct {
	Name         string  `json:"name"`
	Protocol     string  `json:"protocol"`
	Host         string  `json:"host"`
	Port         int32   `json:"port"`
	AuthUsername *string `json:"auth_username,omitempty"`
	AuthSecret   *string `json:"auth_secret,omitempty"`
	GroupID      *string `json:"group_id"`
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
			GroupID:      req.GroupID,
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
			GroupID:      req.GroupID,
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

// resolveTenant 运行管理门并返回本次操作的目标租户。在咨询 service 之前,
// 它对任何鉴权/作用域失败都会短路(写出响应)。与 adminuserhttp.resolveTenantIdentity 对应。
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
		if ident.ScopeTenantID() <= 0 {
			writeError(w, http.StatusForbidden, "admin_tenant_scope_required",
				"tenant_operator scope_tenant_id required")
			return 0, false
		}
		return tenantFromQueryOrScope(w, r, ident)
	case admin.RolePlatformAdmin:
		// 开箱单租户:platform_admin 必须指明 ?tenant_id,由 CanActOnTenant 把关。
		// RBAC 不变——跨租户但显式。
		return tenantFromQueryOrScope(w, r, ident)
	default:
		writeError(w, http.StatusForbidden, "admin_forbidden_scope", "admin role required")
		return 0, false
	}
}

// tenantFromQueryOrScope 从 ?tenant_id(经 CanActOnTenant 校验)解析目标租户,
// 否则回退到 tenant_operator 自身的作用域。这是 adminuserhttp 模式的本地副本。
func tenantFromQueryOrScope(w http.ResponseWriter, r *http.Request, ident admin.AdminIdentity) (int64, bool) {
	tenantParam := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	var tenantID int64
	if tenantParam == "" {
		if ident.Role != admin.RoleTenantOperator {
			writeError(w, http.StatusBadRequest, "tenant_id_required",
				"tenant_id query param required for platform_admin")
			return 0, false
		}
		tenantID = ident.ScopeTenantID()
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
	if err := ident.CanActOnTenant(tenantID); err != nil {
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

// writeServiceError 把 proxyadmin 的 sentinel 错误映射为 HTTP 状态码:
// ErrInvalidInput/ErrInvalidStatus/ErrUnsafeHost -> 400、ErrNotFound -> 404、
// ErrBackend(及其它一切)-> 503。
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
