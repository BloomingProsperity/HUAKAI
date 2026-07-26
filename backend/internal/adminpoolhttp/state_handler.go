package adminpoolhttp

import (
	"encoding/json"
	"net/http"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

func newUpdateProviderAccountEnabledHandler(d AdminPoolAccountDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveProviderAccountAdmin(w, r, d)
		if !ok {
			return
		}
		id, ok := parseAdminPoolID(w, r)
		if !ok {
			return
		}
		var req mutateProviderAccountRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		if !validateProviderAccountTenant(w, req.TenantID, tenantID) {
			return
		}
		if req.Enabled == nil {
			writeJSONError(w, http.StatusBadRequest, "enabled_required", "enabled is required")
			return
		}
		actorID := ident.AuditActor()
		action, reason := "enable_provider_account", "启用 provider account"
		if !*req.Enabled {
			action, reason = "disable_provider_account", "禁用 provider account"
		}
		payload, _ := json.Marshal(map[string]any{"tenant_id": tenantID, "enabled": *req.Enabled})
		audit := providerAccountAuditParams(r, ident, tenantID,
			action, id, chineseReason(req.Reason, reason), payload)
		if err := d.Store.UpdateProviderAccountEnabledWithAudit(r.Context(), admindb.UpdateProviderAccountEnabledParams{
			Enabled: *req.Enabled, ActorID: &actorID, ID: id, TenantID: tenantID,
		}, audit); err != nil {
			writeProviderAccountReadError(w, err, "provider_account_update_failed")
			return
		}
		writeAuditJSON(w, http.StatusOK, map[string]any{"id": id, "enabled": *req.Enabled})
	}
}

func newDeleteProviderAccountHandler(d AdminPoolAccountDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveProviderAccountAdmin(w, r, d)
		if !ok {
			return
		}
		id, ok := parseAdminPoolID(w, r)
		if !ok {
			return
		}
		var req mutateProviderAccountRequest
		if !decodeOptionalAdminPoolJSON(w, r, &req) {
			return
		}
		if !validateProviderAccountTenant(w, req.TenantID, tenantID) {
			return
		}
		actorID := ident.AuditActor()
		payload, _ := json.Marshal(map[string]any{"tenant_id": tenantID, "deleted": true})
		audit := providerAccountAuditParams(r, ident, tenantID,
			"delete_provider_account", id, chineseReason(req.Reason, "删除 provider account"), payload)
		if err := d.Store.SoftDeleteProviderAccountWithAudit(r.Context(), admindb.SoftDeleteProviderAccountParams{
			ActorID: &actorID, ID: id, TenantID: tenantID,
		}, audit); err != nil {
			writeProviderAccountReadError(w, err, "provider_account_delete_failed")
			return
		}
		writeAuditJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
	}
}
