// Package adminquotahttp 暴露按租户作用域的、针对 quota policies 的 admin CRUD
// (/admin/v1/quota-policies)。这属于防滥用的运维配置:它绝不触碰 user_balances
// 或计费账本。它与 adminuserhttp / adminhttp 的 channel-catalog 保持一致:
// platform_admin/tenant_operator 守卫、显式的租户作用域,以及每次变更都原子写入
// 一行 admin_audit_events。
package adminquotahttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauth"
	dbquota "github.com/BloomingProsperity/HUAKAI/internal/db/quotaadmin"
)

const (
	defaultPageLimit = int32(50)
	maxPageLimit     = int32(100)
)

// Deps 是 quota-policy admin 接口面所需的依赖集合。Auth 负责解析 admin 身份;
// Store 负责执行读取以及带审计的变更。
type Deps struct {
	Auth  adminAuth
	Store quotaPolicyStore
}

type adminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

// quotaPolicyStore 把 admin 读取与带审计的变更方法组合在一起。读取直接接受
// sqlc 生成的 params;变更接受一个中立的 params 结构外加审计行,以便 adapter
// 能把二者放进同一个事务里执行。
type quotaPolicyStore interface {
	ListQuotaPoliciesForAdmin(context.Context, dbquota.ListQuotaPoliciesForAdminParams) ([]dbquota.QuotaPolicy, error)
	GetQuotaPolicyByID(context.Context, dbquota.GetQuotaPolicyByIDParams) (dbquota.QuotaPolicy, error)
	CreateQuotaPolicyWithAudit(context.Context, quotaPolicyCreateParams, auditInput) (dbquota.QuotaPolicy, error)
	UpdateQuotaPolicyWithAudit(context.Context, quotaPolicyUpdateParams, auditInput) (dbquota.QuotaPolicy, error)
	DeleteQuotaPolicyWithAudit(context.Context, quotaPolicyDeleteParams, auditInput) (int64, error)
}

// MountRoutes 注册按 id 作用域的 quota-policy CRUD 子树(GET/PUT/DELETE
// /{id})。集合级的 GET/POST 由调用方挂载在裸路径上(见 cmd/gateway/routes.go),
// 与 adminuserhttp 的接线方式一致,这样 chi 报告的就是规范的、不带尾斜杠的
// 集合路径。
func MountRoutes(r chi.Router, d Deps) {
	r.Get("/{id}", newGetHandler(d))
	r.Put("/{id}", newUpdateHandler(d))
	r.Delete("/{id}", newDeleteHandler(d))
}

// MountQuotaPolicyRoutes 在 admin 根路由上内联挂载 quota-policy 全部端点(集合级 + /{id}),
// 与原 gateway 内联挂载一致(不建 chi.Route 子树,保证路径遍历器报告规范、不带尾斜杠的路径)。
// role 制单登录:SessionSafe = 创建/更新配额策略(防滥用 RPM/并发,可逆),登录 admin(session)
// 可直接写;删除留 token-only(分级表对抗验证降档,登录 admin 够不到,只认令牌)。危险者靠前端确认弹窗。
func MountQuotaPolicyRoutes(r chi.Router, d Deps) {
	safe := adminsessionauth.AllowSessionWrite(adminsessionauth.SessionSafe)
	r.Get("/admin/v1/quota-policies", newListHandler(d))
	r.With(safe).Post("/admin/v1/quota-policies", newCreateHandler(d))
	r.Get("/admin/v1/quota-policies/{id}", newGetHandler(d))
	r.With(safe).Put("/admin/v1/quota-policies/{id}", newUpdateHandler(d))
	r.Delete("/admin/v1/quota-policies/{id}", newDeleteHandler(d))
}

// NewRouter 为 handler 逻辑测试构建独立 router,挂相对路径的集合级 GET/POST 与 id 子树。
// (生产挂载走 MountQuotaPolicyRoutes 的全路径内联形态 + 写分级注解;本 router 只为路径无关的
// handler 行为测试,不带 SessionSafe 注解——那由 route_write_class_test 单独覆盖。)
func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Get("/", newListHandler(d))
	r.Post("/", newCreateHandler(d))
	MountRoutes(r, d)
	return r
}

// resolveTenantIdentity 对调用方做认证并解析出操作所针对的租户。tenant_operator
// 可省略 ?tenant_id(使用其自身作用域);platform_admin 必须传 ?tenant_id,并经
// CanActOnTenant 校验。它与 adminuserhttp.resolveTenantIdentity 保持一致,以使
// RBAC 语义完全相同。
func resolveTenantIdentity(w http.ResponseWriter, r *http.Request, d Deps) (admin.AdminIdentity, int64, bool) {
	if d.Auth == nil || d.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "admin_quota_not_configured",
			"admin quota policy dependency unset")
		return admin.AdminIdentity{}, 0, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		writeAdminAuthError(w, err)
		return admin.AdminIdentity{}, 0, false
	}
	switch ident.Role {
	case admin.RoleTenantOperator:
		if ident.ScopeTenantID() <= 0 {
			writeError(w, http.StatusForbidden, "admin_tenant_scope_required",
				"tenant_operator scope_tenant_id required")
			return admin.AdminIdentity{}, 0, false
		}
		if tenantID, ok := tenantFromQueryOrScope(w, r, ident); ok {
			return ident, tenantID, true
		}
		return admin.AdminIdentity{}, 0, false
	case admin.RolePlatformAdmin:
		if tenantID, ok := tenantFromQueryOrScope(w, r, ident); ok {
			return ident, tenantID, true
		}
		return admin.AdminIdentity{}, 0, false
	default:
		writeError(w, http.StatusForbidden, "admin_forbidden_scope", "admin role required")
		return admin.AdminIdentity{}, 0, false
	}
}

// tenantFromQueryOrScope 解析目标租户:若带有 ?tenant_id,则通过 CanActOnTenant
// (跨租户守卫)校验;若不带,则回退到 tenant_operator 自身的作用域。
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
	if raw == "" {
		writeError(w, http.StatusBadRequest, "quota_policy_id_required", "quota policy id is required")
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_quota_policy_id",
			"quota policy id must be a positive int64")
		return 0, false
	}
	return id, true
}

func pagination(w http.ResponseWriter, r *http.Request) (int32, int32, bool) {
	limit := defaultPageLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be a positive integer")
			return 0, 0, false
		}
		limit = int32(parsed)
		if limit > maxPageLimit {
			limit = maxPageLimit
		}
	}
	offset := int32(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "invalid_offset", "offset must be a non-negative integer")
			return 0, 0, false
		}
		offset = int32(parsed)
	}
	return limit, offset, true
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

func timestamp(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.UTC().Format(time.RFC3339)
}
