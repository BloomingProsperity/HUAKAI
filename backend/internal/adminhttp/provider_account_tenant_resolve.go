package adminhttp

import (
	"net/http"
	"strconv"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

// resolveProviderAccountQueryOrScope 解析 provider account 控制面的目标 tenant，
// 并统一通过 CanActOnTenant 裁决。有限作用域缺省为自身根，平台全域必须显式传参。
//
// **杜绝静默默认到 tenant 1**:provider-account 的 test / health / upstream-models 三个 handler
// 此前在该分支直接返回硬编码的 tenant 1,导致 (a) 全局 admin 永远够不到 tenant>1 的账号
// (按 tenant_id 过滤的 SQL 必返 404),又 (b) 冒着误操作 tenant 1 账号的风险。三处复制同一
// 缺陷,这里收敛成单一 helper 消除再次漂移。
//
// 缺失 ?tenant_id → 400;非正整数 → 400;越权 → 403。任一失败都已写好响应,调用方据 ok=false
// 直接返回。
func resolveProviderAccountQueryOrScope(w http.ResponseWriter, r *http.Request, ident admin.AdminIdentity) (int64, bool) {
	param := r.URL.Query().Get("tenant_id")
	if param == "" {
		tenantID := ident.ScopeTenantID()
		if tenantID <= 0 {
			writeError(w, http.StatusBadRequest, "tenant_id_required", "platform_admin must specify ?tenant_id=N")
			return 0, false
		}
		if err := ident.CanActOnTenant(tenantID); err != nil {
			writeError(w, http.StatusForbidden, "admin_forbidden", "tenant scope not permitted")
			return 0, false
		}
		return tenantID, true
	}
	tenantID, err := strconv.ParseInt(param, 10, 64)
	if err != nil || tenantID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_tenant_id", "tenant_id must be a positive int64")
		return 0, false
	}
	if err := ident.CanActOnTenant(tenantID); err != nil {
		writeError(w, http.StatusForbidden, "admin_forbidden", "tenant scope not permitted")
		return 0, false
	}
	return tenantID, true
}
