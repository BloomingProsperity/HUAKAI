package adminuserhttp

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

// tenantFromQueryOrScope 解析终端用户管理的目标租户：有 ?tenant_id 时使用该值，
// 无则要求 tenant_operator 并落回自身作用域。
//
// 终端用户管理的三身份边界:部署者(platform_admin)只管理平台自有租户
// (tenancy 工作租户)的用户;下级租户的终端用户只有该租户管理员可操作,对下级
// 租户部署者仅保留余额调整口(balanceledger),不得经本包读写其用户。
// PlatformTenantID 未接线时无法证明目标归属，必须 503 fail-closed。
func tenantFromQueryOrScope(w http.ResponseWriter, r *http.Request, d Deps, ident admin.AdminIdentity) (int64, bool) {
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
	if err := ident.CanManageFinalUsersForTenant(tenantID, d.PlatformTenantID); err != nil {
		if ident.Role == admin.RolePlatformAdmin && d.PlatformTenantID <= 0 {
			writeError(w, http.StatusServiceUnavailable, "platform_tenant_not_configured",
				"platform tenant scope is not configured")
			return 0, false
		}
		if ident.Role == admin.RolePlatformAdmin && tenantID != d.PlatformTenantID {
			writeError(w, http.StatusForbidden, "cross_tenant_user_admin_forbidden",
				"platform_admin can only manage users of the platform tenant")
			return 0, false
		}
		writeAdminAuthError(w, err)
		return 0, false
	}
	return tenantID, true
}
