package adminuserhttp

import (
	"errors"
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/admintenant"
	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

// tenantFromQueryOrScope 解析操作目标租户:有 ?tenant_id 用它(经 CanIssueForTenant
// 校验越权),无则要求是 tenant_operator 并落回其 scope。镜像
// adminhttp.parseAdminCatalogTenant,放行 platform_admin 而不松动 RBAC。
func tenantFromQueryOrScope(w http.ResponseWriter, r *http.Request, ident admin.AdminIdentity) (int64, bool) {
	tenantID, err := admintenant.FromQuery(r.URL.Query(), ident)
	if err == nil {
		return tenantID, true
	}
	switch {
	case errors.Is(err, admintenant.ErrTenantIDRequired):
		writeError(w, http.StatusBadRequest, "tenant_id_required",
			"tenant_id query param required for platform_admin")
	case errors.Is(err, admintenant.ErrInvalidTenantID):
		writeError(w, http.StatusBadRequest, "invalid_tenant_id",
			"tenant_id must be a positive int64")
	default:
		writeAdminAuthError(w, err)
	}
	return 0, false
}
