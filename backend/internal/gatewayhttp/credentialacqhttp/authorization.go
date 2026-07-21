package credentialacqhttp

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/tenantcapability"
)

func resolveCredentialAcqAdmin(w http.ResponseWriter, r *http.Request, d AdminCredentialAcquisitionDeps) (admin.AdminIdentity, bool) {
	if d.Auth == nil || d.Sessions == nil || d.Credentials == nil || d.AuditStore == nil || d.Accounts == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "admin credential acquisition dependency unset")
		return admin.AdminIdentity{}, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		if errors.Is(err, admin.ErrAdminBackend) {
			writeJSONError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
		} else {
			writeJSONError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
		}
		return admin.AdminIdentity{}, false
	}
	if ident.Role != admin.RolePlatformAdmin && ident.Role != admin.RoleTenantOperator {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "admin role required")
		return admin.AdminIdentity{}, false
	}
	return ident, true
}

func authorizeCredentialAcqTarget(w http.ResponseWriter, r *http.Request, d AdminCredentialAcquisitionDeps, ident admin.AdminIdentity, tenantID, accountID int64) bool {
	if tenantID <= 0 || accountID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "tenant_id and provider_account_id are required")
		return false
	}
	switch ident.Role {
	case admin.RolePlatformAdmin:
		if d.PlatformTenantID <= 0 {
			writeJSONError(w, http.StatusServiceUnavailable, "platform_tenant_not_configured", "platform tenant scope is not configured")
			return false
		}
		if tenantID != d.PlatformTenantID {
			writeJSONError(w, http.StatusForbidden, "cross_tenant_account_admin_forbidden", "platform_admin can only acquire credentials for the platform tenant")
			return false
		}
	case admin.RoleTenantOperator:
		if ident.ScopeTenantID <= 0 || tenantID != ident.ScopeTenantID {
			writeJSONError(w, http.StatusForbidden, "admin_forbidden", "tenant scope mismatch")
			return false
		}
		if d.Capabilities == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "tenant capability dependency unset")
			return false
		}
		allowed, err := d.Capabilities.Allowed(r.Context(), tenantID, tenantcapability.AdvancedAccountIntake)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "tenant_capability_failed", "tenant capability lookup temporarily unavailable")
			return false
		}
		if !allowed {
			writeJSONError(w, http.StatusForbidden, "tenant_capability_not_granted", "advanced account intake is not granted for this tenant")
			return false
		}
	}
	if _, err := d.Accounts.GetAdminProviderAccount(r.Context(), admindb.GetAdminProviderAccountParams{ID: accountID, TenantID: tenantID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSONError(w, http.StatusNotFound, "provider_account_not_found", "provider account not found")
		} else {
			writeJSONError(w, http.StatusServiceUnavailable, "provider_account_lookup_failed", "provider account lookup temporarily unavailable")
		}
		return false
	}
	return true
}

func writeCredentialAcqAdminAudit(r *http.Request, d AdminCredentialAcquisitionDeps, actorID, actorRole string, session credentialacq.Session, action, reason string) {
	if d.AuditStore == nil {
		return
	}
	payload, _ := json.Marshal(credentialacq.AuditSanitizePayload(map[string]any{
		"tenant_id": session.TenantID, "flow_id": session.ID, "vendor": session.Vendor,
		"auth_mode": session.AuthMode, "flow_kind": string(session.Kind), "status": string(session.Status),
	}))
	tenantID := session.TenantID
	targetID := session.ProviderAccountID
	reqID := middleware.GetReqID(r.Context())
	_, _ = d.AuditStore.InsertAdminAuditEvent(r.Context(), admindb.InsertAdminAuditEventParams{
		TenantID: &tenantID, ActorID: actorID, ActorRole: actorRole,
		Action: action, TargetType: "provider_account", TargetID: &targetID,
		RequestID: &reqID, Reason: chineseReason(reason, reason), Payload: payload,
	})
}

func writeCredentialAcqSealedAdminAudit(r *http.Request, d AdminCredentialAcquisitionDeps, actorID, actorRole string, session credentialacq.Session, entrypoint string) {
	if d.AuditStore == nil {
		return
	}
	payload, _ := json.Marshal(credentialacq.AuditSanitizePayload(map[string]any{
		"tenant_id": session.TenantID, "flow_id": session.ID, "vendor": session.Vendor,
		"auth_mode": session.AuthMode, "flow_kind": string(session.Kind), "status": string(session.Status),
		"entrypoint": entrypoint, "result": "denied", "error_class": "credential_mode_sealed",
		"failure_reason": "credential_acquisition_feature_disabled", "severity": "warning",
	}))
	tenantID := session.TenantID
	targetID := session.ProviderAccountID
	reqID := middleware.GetReqID(r.Context())
	_, _ = d.AuditStore.InsertAdminAuditEvent(r.Context(), admindb.InsertAdminAuditEventParams{
		TenantID: &tenantID, ActorID: actorID, ActorRole: actorRole,
		Action: credentialacq.EventFailed, TargetType: "provider_account", TargetID: &targetID,
		RequestID: &reqID, Reason: chineseReason("拒绝推进封存的账号凭据模式", "拒绝推进封存的账号凭据模式"), Payload: payload,
	})
}

func credentialAcqFlowMatchesPathAccount(w http.ResponseWriter, r *http.Request, session credentialacq.Session) bool {
	accountID, ok := parseAdminPoolID(w, r)
	if !ok {
		return false
	}
	if session.ProviderAccountID != accountID {
		writeJSONError(w, http.StatusForbidden, "credential_acquisition_account_mismatch", "credential acquisition flow does not belong to provider account")
		return false
	}
	return true
}
