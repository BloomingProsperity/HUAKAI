package adminuserhttp

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

// tenantFromQueryOrScope 解析操作目标租户:有 ?tenant_id 用它(经 CanIssueForTenant
// 校验越权),无则要求是 tenant_operator 并落回其 scope。镜像
// adminhttp.parseAdminCatalogTenant,放行 platform_admin 而不松动 RBAC。
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
