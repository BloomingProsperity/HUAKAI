// Package proxyadminhttp 暴露出站代理池按租户收敛的管理 HTTP 面
// (list / create / update / delete / set-status)。它是 internal/proxyadmin.Service
// 之上的一层薄传输层:管理门(tenant_operator 限本租户、platform_admin 经
// ?tenant_id+CanIssueForTenant)与 adminuserhttp 一致,且每个响应 DTO 都不含凭据——
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
	"github.com/go-chi/chi/v5/middleware"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauth"
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
	CreateWithAudit(ctx context.Context, in proxyadmin.CreateInput, audit proxyadmin.MutationAudit) (proxyadmin.Proxy, error)
	PatchWithAudit(ctx context.Context, in proxyadmin.PatchInput, audit proxyadmin.MutationAudit) (proxyadmin.Proxy, error)
	Get(ctx context.Context, tenantID, id int64) (proxyadmin.Proxy, error)
	List(ctx context.Context, tenantID int64) ([]proxyadmin.Proxy, error)
	DeleteImpact(ctx context.Context, tenantID, id int64) (proxyadmin.DeleteImpact, error)
	DeleteWithAudit(ctx context.Context, tenantID, id int64, audit proxyadmin.MutationAudit) error
	SetStatusWithAudit(ctx context.Context, tenantID, id int64, status string, audit proxyadmin.MutationAudit) error
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
	safe := adminsessionauth.AllowSessionWrite(adminsessionauth.SessionSafe)
	r.Get("/", newListHandler(d))
	r.With(safe).Post("/", newCreateHandler(d))
	r.Get("/{id}", newGetHandler(d))
	r.Get("/{id}/delete-impact", newDeleteImpactHandler(d))
	r.With(safe).Patch("/{id}", newUpdateHandler(d))
	r.With(safe).Delete("/{id}", newDeleteHandler(d))
	r.With(safe).Put("/{id}/status", newSetStatusHandler(d))
	r.With(safe).Post("/{id}/test", newTestHandler(d))
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

type deleteImpactResponse struct {
	ProxyID                   int64 `json:"proxy_id"`
	DirectAccountCount        int64 `json:"direct_account_count"`
	DefaultTenantCount        int64 `json:"default_tenant_count"`
	GroupAccountCount         int64 `json:"group_account_count"`
	GroupRemainingActiveCount int64 `json:"group_remaining_active_count"`
	CanDelete                 bool  `json:"can_delete"`
}

func toDeleteImpactResponse(impact proxyadmin.DeleteImpact) deleteImpactResponse {
	return deleteImpactResponse{
		ProxyID:                   impact.ProxyID,
		DirectAccountCount:        impact.DirectAccountCount,
		DefaultTenantCount:        impact.DefaultTenantCount,
		GroupAccountCount:         impact.GroupAccountCount,
		GroupRemainingActiveCount: impact.GroupRemainingActiveCount,
		CanDelete:                 impact.CanDelete(),
	}
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
	present      map[string]bool
}

var updateProxyFields = map[string]bool{
	"name": true, "protocol": true, "host": true, "port": true,
	"auth_username": true, "auth_secret": true, "group_id": true,
}

var nonNullableUpdateProxyFields = map[string]bool{
	"name": true, "protocol": true, "host": true, "port": true,
}

func (r *updateProxyRequest) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for key, value := range raw {
		if !updateProxyFields[key] {
			return fmt.Errorf("unknown field %q", key)
		}
		if nonNullableUpdateProxyFields[key] && strings.TrimSpace(string(value)) == "null" {
			return fmt.Errorf("field %q cannot be null", key)
		}
	}
	type plain updateProxyRequest
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*r = updateProxyRequest(decoded)
	r.present = make(map[string]bool, len(raw))
	for key := range raw {
		r.present[key] = true
	}
	return nil
}

func (r updateProxyRequest) has(field string) bool {
	return r.present[field]
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
		ident, tenantID, ok := resolveTenantIdentity(w, r, d)
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
		p, err := d.Service.CreateWithAudit(r.Context(), proxyadmin.CreateInput{
			TenantID:     tenantID,
			Name:         req.Name,
			Protocol:     req.Protocol,
			Host:         req.Host,
			Port:         req.Port,
			AuthUsername: req.AuthUsername,
			AuthSecret:   req.AuthSecret,
			GroupID:      req.GroupID,
			Status:       req.Status,
		}, mutationAudit(r, ident))
		if err != nil {
			writeServiceError(w, err, "create proxy failed")
			return
		}
		writeJSON(w, http.StatusCreated, toProxyResponse(p))
	}
}

func newUpdateHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveTenantIdentity(w, r, d)
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
		if len(req.present) == 0 {
			writeError(w, http.StatusBadRequest, "empty_patch", "at least one mutable field is required")
			return
		}
		p, err := d.Service.PatchWithAudit(r.Context(), proxyadmin.PatchInput{
			TenantID:     tenantID,
			ID:           id,
			Name:         proxyadmin.PatchField[string]{Set: req.has("name"), Value: req.Name},
			Protocol:     proxyadmin.PatchField[string]{Set: req.has("protocol"), Value: req.Protocol},
			Host:         proxyadmin.PatchField[string]{Set: req.has("host"), Value: req.Host},
			Port:         proxyadmin.PatchField[int32]{Set: req.has("port"), Value: req.Port},
			AuthUsername: proxyadmin.PatchField[*string]{Set: req.has("auth_username"), Value: req.AuthUsername},
			AuthSecret:   proxyadmin.PatchField[*string]{Set: req.has("auth_secret"), Value: req.AuthSecret},
			GroupID:      proxyadmin.PatchField[*string]{Set: req.has("group_id"), Value: req.GroupID},
		}, mutationAudit(r, ident))
		if err != nil {
			writeServiceError(w, err, "update proxy failed")
			return
		}
		writeJSON(w, http.StatusOK, toProxyResponse(p))
	}
}

func newDeleteHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveTenantIdentity(w, r, d)
		if !ok {
			return
		}
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		if err := d.Service.DeleteWithAudit(r.Context(), tenantID, id, mutationAudit(r, ident)); err != nil {
			writeServiceError(w, err, "delete proxy failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func newDeleteImpactHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, ok := resolveTenant(w, r, d)
		if !ok {
			return
		}
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		impact, err := d.Service.DeleteImpact(r.Context(), tenantID, id)
		if err != nil {
			writeServiceError(w, err, "get proxy delete impact failed")
			return
		}
		writeJSON(w, http.StatusOK, toDeleteImpactResponse(impact))
	}
}

func newSetStatusHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveTenantIdentity(w, r, d)
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
		if err := d.Service.SetStatusWithAudit(r.Context(), tenantID, id, req.Status, mutationAudit(r, ident)); err != nil {
			writeServiceError(w, err, "set proxy status failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": strings.TrimSpace(req.Status)})
	}
}

// resolveTenant 运行管理门并返回本次操作的目标租户。在咨询 service 之前,
// 它对任何鉴权/作用域失败都会短路(写出响应)。与 adminuserhttp.resolveTenantIdentity 对应。
func resolveTenant(w http.ResponseWriter, r *http.Request, d Deps) (int64, bool) {
	_, tenantID, ok := resolveTenantIdentity(w, r, d)
	return tenantID, ok
}

func resolveTenantIdentity(w http.ResponseWriter, r *http.Request, d Deps) (admin.AdminIdentity, int64, bool) {
	if d.Auth == nil || d.Service == nil {
		writeError(w, http.StatusServiceUnavailable, "admin_proxies_not_configured",
			"admin proxies dependency unset")
		return admin.AdminIdentity{}, 0, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		writeAdminAuthError(w, err)
		return admin.AdminIdentity{}, 0, false
	}
	switch ident.Role {
	case admin.RoleTenantOperator:
		if ident.ScopeTenantID <= 0 {
			writeError(w, http.StatusForbidden, "admin_tenant_scope_required",
				"tenant_operator scope_tenant_id required")
			return admin.AdminIdentity{}, 0, false
		}
		tenantID, ok := tenantFromQueryOrScope(w, r, ident)
		return ident, tenantID, ok
	case admin.RolePlatformAdmin:
		// 开箱单租户:platform_admin 必须指明 ?tenant_id,由 CanIssueForTenant 把关。
		// RBAC 不变——跨租户但显式。
		tenantID, ok := tenantFromQueryOrScope(w, r, ident)
		return ident, tenantID, ok
	default:
		writeError(w, http.StatusForbidden, "admin_forbidden_scope", "admin role required")
		return admin.AdminIdentity{}, 0, false
	}
}

func mutationAudit(r *http.Request, ident admin.AdminIdentity) proxyadmin.MutationAudit {
	return proxyadmin.MutationAudit{
		ActorID:   ident.AuditActor(),
		ActorRole: ident.Role,
		RequestID: middleware.GetReqID(r.Context()),
	}
}

// tenantFromQueryOrScope 从 ?tenant_id(经 CanIssueForTenant 校验)解析目标租户,
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

// writeServiceError 把 proxyadmin 的 sentinel 错误映射为 HTTP 状态码:
// ErrInvalidInput/ErrInvalidStatus/ErrUnsafeHost -> 400、ErrNotFound -> 404、
// ErrInUse -> 409、ErrBackend(及其它一切)-> 503。
func writeServiceError(w http.ResponseWriter, err error, context string) {
	switch {
	case errors.Is(err, proxyadmin.ErrInvalidStatus):
		writeError(w, http.StatusBadRequest, "invalid_status",
			"status must be one of active, disabled, dead")
	case errors.Is(err, proxyadmin.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_proxy", "proxy request is invalid")
	case errors.Is(err, proxyadmin.ErrUnsafeHost):
		writeError(w, http.StatusBadRequest, "unsafe_proxy_host",
			"proxy host is a blocked loopback, link-local, multicast, unspecified, or metadata target")
	case errors.Is(err, proxyadmin.ErrNotFound):
		writeError(w, http.StatusNotFound, "admin_proxy_not_found", "proxy not found")
	case errors.Is(err, proxyadmin.ErrInUse):
		var inUse *proxyadmin.InUseError
		if errors.As(err, &inUse) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": map[string]any{
					"code":    "admin_proxy_in_use",
					"message": "proxy is still referenced; migrate or unbind it before deletion",
					"details": toDeleteImpactResponse(inUse.Impact),
				},
			})
			return
		}
		writeError(w, http.StatusConflict, "admin_proxy_in_use",
			"proxy is still referenced; migrate or unbind it before deletion")
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
