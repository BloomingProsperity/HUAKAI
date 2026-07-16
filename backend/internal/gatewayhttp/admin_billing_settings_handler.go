package gatewayhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

const adminBillingSettingsActionUpdate = "update_billing_settings"

var (
	adminBillingSettingsAllowedValues = []string{
		billing.StreamInputOnlyInterruptedPolicyNoBill.String(),
		billing.StreamInputOnlyInterruptedPolicyNoBillRecord.String(),
	}
	adminBillingSettingsRoadmapValues = []string{"bill_input"}
)

type AdminBillingSettingsAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type AdminBillingSettingsStore interface {
	Get(context.Context, int64, string) (billing.StoredBillingSetting, bool, error)
	UpsertStreamInputOnlyInterruptedPolicy(context.Context, int64, billing.StreamInputOnlyInterruptedPolicy, string) (billing.StoredBillingSetting, error)
}

type AdminBillingSettingsTenantChecker interface {
	AdminCheckTenantExists(context.Context, int64) (bool, error)
}

type AdminBillingSettingsDeps struct {
	Auth          AdminBillingSettingsAuth
	Store         AdminBillingSettingsStore
	TenantChecker AdminBillingSettingsTenantChecker
	AuditUpdater  AdminBillingSettingsAuditUpdater
}

type adminBillingSettingsPutRequest struct {
	TenantID int64  `json:"tenant_id"`
	Policy   string `json:"stream_input_only_interrupted_policy"`
	Reason   string `json:"reason"`
}

type adminBillingSettingsResponse struct {
	TenantID      int64      `json:"tenant_id"`
	Key           string     `json:"key"`
	Value         string     `json:"value"`
	Source        string     `json:"source"`
	AllowedValues []string   `json:"allowed_values"`
	RoadmapValues []string   `json:"roadmap_values"`
	UpdatedAt     *time.Time `json:"updated_at"`
	UpdatedBy     *string    `json:"updated_by"`
}

func MountAdminBillingSettingsRoutes(r chi.Router, d AdminBillingSettingsDeps) {
	r.Get("/settings", newAdminBillingSettingsGetHandler(d))
	r.Put("/settings", newAdminBillingSettingsPutHandler(d))
}

func newAdminBillingSettingsGetHandler(d AdminBillingSettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdminBillingSettings(w, r, d)
		if !ok {
			return
		}
		tenantID, ok := resolveAdminBillingTenantFromQuery(w, r, ident)
		if !ok {
			return
		}
		if !ensureAdminBillingTenantExists(w, r, d, tenantID) {
			return
		}
		row, found, err := d.Store.Get(r.Context(), tenantID, billing.StreamInputOnlyInterruptedPolicyKey)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "billing_settings_read_failed", err.Error())
			return
		}
		resp, ok := adminBillingSettingsResponseFromRow(w, tenantID, row, found)
		if !ok {
			return
		}
		writeAuditJSON(w, http.StatusOK, resp)
	}
}

func newAdminBillingSettingsPutHandler(d AdminBillingSettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdminBillingSettings(w, r, d)
		if !ok {
			return
		}
		var req adminBillingSettingsPutRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		if req.TenantID <= 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid_tenant_id", "tenant_id must be positive")
			return
		}
		if !adminCanAccessTenant(ident, req.TenantID) {
			writeJSONError(w, http.StatusForbidden, "admin_forbidden", "caller cannot act on this tenant scope")
			return
		}
		if !ensureAdminBillingTenantExists(w, r, d, req.TenantID) {
			return
		}
		reason := strings.TrimSpace(req.Reason)
		if reason == "" {
			writeJSONError(w, http.StatusBadRequest, "billing_settings_reason_required", "reason is required")
			return
		}
		policy, ok := parseAdminBillingPolicyValue(w, req.Policy)
		if !ok {
			return
		}
		if d.AuditUpdater == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "admin billing settings audit updater dependency unset")
			return
		}

		actorID := ident.AuditActor()
		result, err := d.AuditUpdater.UpsertStreamInputOnlyInterruptedPolicyWithAudit(r.Context(), AdminBillingSettingsAuditUpdate{
			TenantID:  req.TenantID,
			Policy:    policy,
			UpdatedBy: actorID,
			ActorID:   actorID,
			ActorRole: ident.Role,
			Reason:    reason,
			RequestID: strings.TrimSpace(middleware.GetReqID(r.Context())),
		})
		if err != nil {
			writeAdminBillingSettingsTransactionError(w, r, req.TenantID, actorID, err)
			return
		}
		resp, ok := adminBillingSettingsResponseFromRow(w, req.TenantID, result.Updated, true)
		if !ok {
			return
		}
		writeAuditJSON(w, http.StatusOK, resp)
	}
}

func resolveAdminBillingSettings(w http.ResponseWriter, r *http.Request, d AdminBillingSettingsDeps) (admin.AdminIdentity, bool) {
	if d.Auth == nil || d.Store == nil || d.TenantChecker == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "admin billing settings dependency unset")
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
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "tenant scope required")
		return admin.AdminIdentity{}, false
	}
	return ident, true
}

func ensureAdminBillingTenantExists(w http.ResponseWriter, r *http.Request, d AdminBillingSettingsDeps, tenantID int64) bool {
	exists, err := d.TenantChecker.AdminCheckTenantExists(r.Context(), tenantID)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "tenant_lookup_failed", fmt.Sprintf("tenant existence check failed: %v", err))
		return false
	}
	if !exists {
		writeJSONError(w, http.StatusNotFound, "tenant_not_found", fmt.Sprintf("tenant %d not found", tenantID))
		return false
	}
	return true
}

func resolveAdminBillingTenantFromQuery(w http.ResponseWriter, r *http.Request, ident admin.AdminIdentity) (int64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	if raw == "" && ident.Role == admin.RoleTenantOperator {
		return ident.ScopeTenantID, true
	}
	tenantID, ok := parseRequiredPositiveInt64(w, raw, "tenant_id_required", "tenant_id query parameter must be positive")
	if !ok {
		return 0, false
	}
	if !adminCanAccessTenant(ident, tenantID) {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "caller cannot act on this tenant scope")
		return 0, false
	}
	return tenantID, true
}

func parseRequiredPositiveInt64(w http.ResponseWriter, raw, code, message string) (int64, bool) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, code, message)
		return 0, false
	}
	return id, true
}

func parseAdminBillingPolicyValue(w http.ResponseWriter, raw string) (billing.StreamInputOnlyInterruptedPolicy, bool) {
	policy, err := billing.ParseStreamInputOnlyInterruptedPolicy(raw)
	if err == nil {
		return policy, true
	}
	if errors.Is(err, billing.ErrBillingPolicyRoadmap) {
		writeJSONError(w, http.StatusConflict, "billing_policy_value_roadmap", "bill_input is a roadmap value and cannot be enabled in this phase")
		return "", false
	}
	writeJSONError(w, http.StatusBadRequest, "billing_policy_value_invalid", "stream_input_only_interrupted_policy must be no_bill or no_bill_record")
	return "", false
}

func adminBillingSettingsResponseFromRow(w http.ResponseWriter, tenantID int64, row billing.StoredBillingSetting, found bool) (adminBillingSettingsResponse, bool) {
	resp := adminBillingSettingsResponse{
		TenantID:      tenantID,
		Key:           billing.StreamInputOnlyInterruptedPolicyKey,
		Value:         billing.DefaultStreamInputOnlyInterruptedPolicy.String(),
		Source:        "default",
		AllowedValues: append([]string(nil), adminBillingSettingsAllowedValues...),
		RoadmapValues: append([]string(nil), adminBillingSettingsRoadmapValues...),
	}
	if !found {
		return resp, true
	}
	policy, err := billing.ParseStreamInputOnlyInterruptedPolicy(row.Value)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "billing_settings_invalid_persisted_value", "stored billing policy value is invalid")
		return adminBillingSettingsResponse{}, false
	}
	resp.Value = policy.String()
	resp.Source = "tenant"
	if !row.UpdatedAt.IsZero() {
		updatedAt := row.UpdatedAt.UTC()
		resp.UpdatedAt = &updatedAt
	}
	if updatedBy := strings.TrimSpace(row.UpdatedBy); updatedBy != "" {
		resp.UpdatedBy = &updatedBy
	}
	return resp, true
}

func writeAdminBillingSettingsWriteError(w http.ResponseWriter, err error) {
	if errors.Is(err, billing.ErrBillingPolicyRoadmap) {
		writeJSONError(w, http.StatusConflict, "billing_policy_value_roadmap", "bill_input is a roadmap value and cannot be enabled in this phase")
		return
	}
	if errors.Is(err, billing.ErrBillingSettingInvalid) {
		writeJSONError(w, http.StatusBadRequest, "billing_policy_value_invalid", err.Error())
		return
	}
	writeJSONError(w, http.StatusServiceUnavailable, "billing_settings_update_failed", err.Error())
}

func writeAdminBillingSettingsTransactionError(w http.ResponseWriter, r *http.Request, tenantID int64, actorID string, err error) {
	if errors.Is(err, errAdminBillingSettingsInvalidPersistedValue) {
		writeJSONError(w, http.StatusServiceUnavailable, "billing_settings_invalid_persisted_value", "stored billing policy value is invalid")
		return
	}
	if phase, ok := adminBillingSettingsErrorPhase(err); ok {
		switch phase {
		case adminBillingSettingsTxPhaseRead:
			writeJSONError(w, http.StatusServiceUnavailable, "billing_settings_read_failed", err.Error())
			return
		case adminBillingSettingsTxPhaseAudit:
			_ = privacy.LogSystem(r.Context(), privacy.SystemEvent{
				Severity:   privacy.SeverityError,
				Component:  "gatewayhttp.admin_billing_settings",
				RequestID:  middleware.GetReqID(r.Context()),
				ErrorClass: privacy.ErrorClassFor(r.Context(), err),
				Attrs: map[string]any{
					"event_class": "admin_billing_settings_audit_write_failed",
					"event_type":  adminBillingSettingsActionUpdate,
					"tenant_id":   tenantID,
					"actor_id":    actorID,
				},
			})
			writeJSONError(w, http.StatusServiceUnavailable, "billing_settings_audit_failed", err.Error())
			return
		}
	}
	writeAdminBillingSettingsWriteError(w, err)
}
