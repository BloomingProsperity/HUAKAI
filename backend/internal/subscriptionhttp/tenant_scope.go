package subscriptionhttp

import (
	"errors"
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

func authorizeSubscriptionTenant(w http.ResponseWriter, ident admin.AdminIdentity, tenantID, platformTenantID int64) bool {
	if err := ident.CanOperateOwnedTenant(tenantID, platformTenantID); err != nil {
		if errors.Is(err, admin.ErrAdminBackend) {
			writeJSONError(w, http.StatusServiceUnavailable, "admin_scope_unavailable", "platform tenant scope is not configured")
		} else {
			writeJSONError(w, http.StatusForbidden, "admin_forbidden", "caller cannot operate this tenant")
		}
		return false
	}
	return true
}

func authorizeSubscriptionReadTenant(w http.ResponseWriter, ident admin.AdminIdentity, tenantID int64) bool {
	if err := ident.CanIssueForTenant(tenantID); err != nil {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "caller cannot read this tenant")
		return false
	}
	return true
}
