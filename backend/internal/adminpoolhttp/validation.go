package adminpoolhttp

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminhttpcore"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountadvanced"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountcreate"
	"github.com/BloomingProsperity/HUAKAI/internal/mixedchannelrisk"
)

func resolveProviderAccountAdmin(w http.ResponseWriter, r *http.Request, d AdminPoolAccountDeps) (admin.AdminIdentity, int64, bool) {
	if d.Auth == nil || d.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "admin pool account dependency unset")
		return admin.AdminIdentity{}, 0, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		if errors.Is(err, admin.ErrAdminBackend) {
			writeJSONError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
		} else {
			writeJSONError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
		}
		return admin.AdminIdentity{}, 0, false
	}
	switch ident.Role {
	case admin.RoleTenantOperator:
		if ident.ScopeTenantID <= 0 {
			writeJSONError(w, http.StatusForbidden, "admin_forbidden", "tenant_operator scope_tenant_id required")
			return admin.AdminIdentity{}, 0, false
		}
		return ident, ident.ScopeTenantID, true
	case admin.RolePlatformAdmin:
		if ident.ScopeTenantID > 0 {
			return ident, ident.ScopeTenantID, true
		}
		tenantID, okTenant := adminhttpcore.ParseRequiredPositiveQueryInt64(w, r, "tenant_id")
		if !okTenant {
			return admin.AdminIdentity{}, 0, false
		}
		if err := ident.CanIssueForTenant(tenantID); err != nil {
			writeJSONError(w, http.StatusForbidden, "admin_forbidden", "tenant scope not permitted")
			return admin.AdminIdentity{}, 0, false
		}
		return ident, tenantID, true
	default:
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "admin role required")
		return admin.AdminIdentity{}, 0, false
	}
}

func decodeAdminPoolJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	return adminhttpcore.DecodeJSON(w, r, dst)
}

func decodeAdminPoolJSONWithRaw(w http.ResponseWriter, r *http.Request, dst any) ([]byte, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<16))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return nil, false
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return nil, false
	}
	return raw, true
}

func parseProviderAccountAdvanced(w http.ResponseWriter, raw []byte) (accountadvanced.Mutation, bool) {
	adv, err := accountadvanced.Parse(raw)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "admin_bad_request", err.Error())
		return accountadvanced.Mutation{}, false
	}
	return adv, true
}

func validateCreateProviderAccount(req createProviderAccountRequest) error {
	if req.ProviderID <= 0 || req.ChannelID <= 0 || req.Name == "" {
		return fmt.Errorf("provider_id, channel_id, and name are required")
	}
	switch req.AccountType {
	case "oauth", "api_key", "service_account", "upstream_static", "session", "aws_sigv4":
	default:
		return fmt.Errorf("account_type is invalid")
	}
	if req.CapConcurrency != nil && *req.CapConcurrency <= 0 {
		return fmt.Errorf("cap_concurrency must be positive")
	}
	if req.StaticWeight != nil && *req.StaticWeight <= 0 {
		return fmt.Errorf("static_weight must be positive")
	}
	if len(req.Extra) > 0 && !jsonRawObject(req.Extra) {
		return fmt.Errorf("extra must be a JSON object")
	}
	var obj map[string]json.RawMessage
	if len(req.Credentials) == 0 || json.Unmarshal(req.Credentials, &obj) != nil || obj == nil {
		return fmt.Errorf("credentials must be a JSON object")
	}
	if req.Vendor == "" || req.AuthMode == "" {
		return fmt.Errorf("vendor and auth_mode are required for account_credentials")
	}
	if !credentialacq.DirectCredentialInputAllowed(req.Vendor, req.AuthMode) {
		return fmt.Errorf("credential mode requires its dedicated acquisition flow")
	}
	handler, err := credentialstore.DefaultHandlerRegistry().MustLookup(req.Vendor, req.AuthMode)
	if err != nil {
		return err
	}
	return handler.ValidatePayload(req.Credentials)
}

func parseProviderAccountMixedRiskConfirm(w http.ResponseWriter, r *http.Request, req createProviderAccountRequest) (bool, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("confirm"))
	if raw != "" {
		confirmed, err := strconv.ParseBool(raw)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_confirm", "confirm must be true or false")
			return false, false
		}
		return confirmed, true
	}
	if req.Confirm != nil {
		return *req.Confirm, true
	}
	return false, true
}

func validateProviderAccountProtocolCompatibility(family, accountType, vendor, authMode string) error {
	return accountcreate.ValidateProtocolCompatibility(family, accountType, vendor, authMode)
}

func writeProviderAccountMixedRiskRequired(w http.ResponseWriter, report mixedchannelrisk.Report) {
	writeAuditJSON(w, http.StatusBadRequest, map[string]any{
		"error":             "mixed_channel_risk_confirmation_required",
		"message":           "same channel contains accounts from different source/vendor/credential type; resend with confirm=true after operator review",
		"confirm_required":  true,
		"confirm_parameter": "confirm=true",
		"risks":             report.Items,
	})
}

func validateUpdateProviderAccount(req updateProviderAccountRequest, hasAdvanced bool) error {
	if !hasAdvanced && req.Enabled == nil && req.Priority == nil && req.StaticWeight == nil && req.CapConcurrency == nil &&
		req.ProbeModel == nil && req.Tags == nil && req.Extra == nil && req.ModelAllowList == nil &&
		req.CapabilityFlags == nil {
		return fmt.Errorf("at least one supported field is required")
	}
	if req.CapConcurrency != nil && *req.CapConcurrency <= 0 {
		return fmt.Errorf("cap_concurrency must be positive")
	}
	if req.StaticWeight != nil && *req.StaticWeight <= 0 {
		return fmt.Errorf("static_weight must be positive")
	}
	if req.Extra != nil && !jsonRawObject(*req.Extra) {
		return fmt.Errorf("extra must be a JSON object")
	}
	return nil
}

func parseAdminPoolID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_provider_account_id", "id must be a positive int64")
		return 0, false
	}
	return id, true
}

func validateProviderAccountTenant(w http.ResponseWriter, tenantID *int64, scopeTenantID int64) bool {
	if scopeTenantID <= 0 {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "tenant scope required")
		return false
	}
	if tenantID == nil {
		return true
	}
	if *tenantID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "tenant_id_invalid", "tenant_id must be positive when provided")
		return false
	}
	if *tenantID != scopeTenantID {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "tenant_id does not match admin scope")
		return false
	}
	return true
}

func parseProviderAccountLimit(w http.ResponseWriter, r *http.Request) (int32, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return defaultAdminProviderAccountLimit, true
	}
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || n <= 0 || n > int64(maxAdminProviderAccountLimit) {
		writeJSONError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 200")
		return 0, false
	}
	return int32(n), true
}

func parseProviderAccountCursor(w http.ResponseWriter, r *http.Request) (int64, *string, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("cursor"))
	if raw == "" {
		return 0, nil, true
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_cursor", "cursor must be an opaque base64 cursor")
		return 0, nil, false
	}
	text := string(decoded)
	if !strings.HasPrefix(text, providerAccountCursorPrefix) {
		writeJSONError(w, http.StatusBadRequest, "invalid_cursor", "cursor must be an opaque base64 cursor")
		return 0, nil, false
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(text, providerAccountCursorPrefix), 10, 64)
	if err != nil || id < 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_cursor", "cursor must be an opaque base64 cursor")
		return 0, nil, false
	}
	return id, &raw, true
}

func encodeProviderAccountCursor(id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(providerAccountCursorPrefix + strconv.FormatInt(id, 10)))
}

func parseProviderAccountPoolGroupID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("pool_group_id"))
	if raw == "" {
		return 0, true
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_pool_group_id", "pool_group_id must be a positive int64")
		return 0, false
	}
	return n, true
}

func parseProviderAccountStateFilter(w http.ResponseWriter, r *http.Request) (string, bool) {
	state := strings.TrimSpace(r.URL.Query().Get("state_filter"))
	switch state {
	case "", "active", "error", "disabled", "rate_limited", "overloaded", "temp_unschedulable":
		return state, true
	default:
		writeJSONError(w, http.StatusBadRequest, "invalid_state_filter", "state_filter is invalid")
		return "", false
	}
}

func parseProviderAccountTagFilter(r *http.Request) string {
	return strings.TrimSpace(r.URL.Query().Get("tag"))
}
