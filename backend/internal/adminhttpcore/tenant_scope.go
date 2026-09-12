package adminhttpcore

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

// ResolveTenantScopeQuery 是租户作用域只读接口的双角色租户解析唯一实现。
//
// 合同：
//   - tenant_operator 省略 tenant_id 时锁定认证上下文里的本租户；显式给出他人租户 → 403。
//   - platform_admin 必须显式给出正整数 tenant_id；省略、空串、只含空白 → 400 tenant_id_required，
//     非正整数或 all/*/null 之类别名 → 400 invalid_tenant_id。任何情况都不得回落全平台。
//   - 目标租户是否可操作只看认证身份与数据库事实（CanIssueForTenant），不接受请求体自报。
//
// 失败时已写好响应，调用方据 ok=false 直接返回。
func ResolveTenantScopeQuery(w http.ResponseWriter, r *http.Request, ident admin.AdminIdentity) (int64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	if raw == "" && ident.Role == admin.RoleTenantOperator {
		return authorizeTenantScope(w, ident, ident.ScopeTenantID)
	}
	if raw == "" {
		WriteJSONError(w, http.StatusBadRequest, "tenant_id_required", "tenant_id query parameter must be positive")
		return 0, false
	}
	tenantID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || tenantID <= 0 {
		WriteJSONError(w, http.StatusBadRequest, "invalid_tenant_id", "tenant_id must be a positive int64")
		return 0, false
	}
	return authorizeTenantScope(w, ident, tenantID)
}

func authorizeTenantScope(w http.ResponseWriter, ident admin.AdminIdentity, tenantID int64) (int64, bool) {
	if tenantID <= 0 {
		WriteJSONError(w, http.StatusBadRequest, "tenant_id_required", "tenant_id must be positive")
		return 0, false
	}
	if err := ident.CanIssueForTenant(tenantID); err != nil {
		WriteTenantScopeAuthError(w, err)
		return 0, false
	}
	return tenantID, true
}

// WriteTenantScopeAuthError 把管理身份错误映射成稳定的 HTTP 合同：
// 后端瞬时故障 503、越权 403、其余一律 401。
func WriteTenantScopeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, admin.ErrAdminBackend):
		WriteJSONError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
	case errors.Is(err, admin.ErrAdminForbidden):
		WriteJSONError(w, http.StatusForbidden, "admin_forbidden", "caller cannot act on this tenant scope")
	default:
		WriteJSONError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
	}
}
