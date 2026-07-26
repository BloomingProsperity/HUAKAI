package adminpoolhttp

import (
	"net/http"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

func newDeletePoolHandler(d AdminPoolsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdminPoolOperator(w, r, d)
		if !ok {
			return
		}
		tenantID, ok := resolveAdminPoolTenant(w, r, ident, nil, false)
		if !ok {
			return
		}
		id, ok := parseAdminPoolGroupID(w, r)
		if !ok {
			return
		}
		auditParams, err := buildAdminPoolAuditParams(r, ident, tenantID, "delete_pool_group", id, map[string]any{
			"tenant_id": tenantID,
			"pool_id":   id,
		})
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "audit_payload_failed", err.Error())
			return
		}
		pool, err := d.Store.DeletePoolWithAudit(r.Context(),
			dbbilling.DeletePoolParams{TenantID: tenantID, ID: id},
			auditParams,
		)
		if err != nil {
			writeAdminPoolMutationError(w, err, "pool_delete_failed")
			return
		}
		writeAuditJSON(w, http.StatusOK, pool)
	}
}
