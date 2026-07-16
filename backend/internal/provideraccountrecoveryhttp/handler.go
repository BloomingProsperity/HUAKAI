package provideraccountrecoveryhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

type Auth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type AccountStore interface {
	GetAdminProviderAccount(context.Context, admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error)
}

type CredentialStore interface {
	ListByAccount(context.Context, int64, int64) ([]credentialstore.CredentialMetadata, error)
}

type ChannelHealthStore interface {
	GetRecord(context.Context, channelhealth.ChannelKey) (channelhealth.Record, error)
}

type Deps struct {
	Auth          Auth
	Accounts      AccountStore
	Credentials   CredentialStore
	ChannelHealth ChannelHealthStore
	Now           func() time.Time
}

type Response struct {
	AccountID      int64                `json:"account_id"`
	TenantID       int64                `json:"tenant_id"`
	GeneratedAt    time.Time            `json:"generated_at"`
	RequiresAction bool                 `json:"requires_action"`
	Account        AccountSnapshot      `json:"account"`
	Credentials    []CredentialSnapshot `json:"credentials"`
	Channels       []ChannelSnapshot    `json:"channels"`
	Actions        []Action             `json:"actions"`
}

type AccountSnapshot struct {
	Enabled                bool       `json:"enabled"`
	HealthState            string     `json:"health_state"`
	CredentialState        string     `json:"credential_state"`
	RateLimitResetAt       *time.Time `json:"rate_limit_reset_at,omitempty"`
	OverloadUntil          *time.Time `json:"overload_until,omitempty"`
	TempUnschedulableUntil *time.Time `json:"temp_unschedulable_until,omitempty"`
}

type CredentialSnapshot struct {
	ID                 int64      `json:"id"`
	Vendor             string     `json:"vendor"`
	AuthMode           string     `json:"auth_mode"`
	State              string     `json:"state"`
	Version            int32      `json:"credential_version"`
	AccessExpiresAt    *time.Time `json:"access_expires_at,omitempty"`
	LastRefreshOutcome *string    `json:"last_refresh_outcome,omitempty"`
	FailureClass       *string    `json:"failure_class,omitempty"`
	FailureCount       int32      `json:"failure_count"`
}

type ChannelSnapshot struct {
	State             channelhealth.HealthState `json:"state"`
	Vendor            string                    `json:"vendor"`
	CredentialID      int64                     `json:"credential_id"`
	CredentialVersion int                       `json:"credential_version"`
	ReasonClass       channelhealth.SignalClass `json:"reason_class"`
	CooldownUntil     *time.Time                `json:"cooldown_until,omitempty"`
}

type Action struct {
	Action          string   `json:"action"`
	SubjectType     string   `json:"subject_type"`
	SubjectID       int64    `json:"subject_id"`
	StateApplicable bool     `json:"state_applicable"`
	Authorized      bool     `json:"authorized"`
	Available       bool     `json:"available"`
	Recommended     bool     `json:"recommended"`
	ReasonCode      string   `json:"reason_code"`
	RiskLevel       string   `json:"risk_level"`
	Method          string   `json:"method"`
	Path            string   `json:"path"`
	RequiredRole    string   `json:"required_role"`
	RequiredFields  []string `json:"required_fields"`
}

func MountRoutes(r chi.Router, d Deps) {
	r.Get("/{id}/recovery-actions", NewHandler(d))
}

func NewHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveTenant(w, r, d)
		if !ok {
			return
		}
		accountID, ok := parseAccountID(w, r)
		if !ok {
			return
		}
		account, err := d.Accounts.GetAdminProviderAccount(r.Context(), admindb.GetAdminProviderAccountParams{
			ID: accountID, TenantID: tenantID,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusNotFound, "provider_account_not_found", "provider account not found")
				return
			}
			writeError(w, http.StatusServiceUnavailable, "provider_account_recovery_unavailable", "provider account recovery diagnostics are unavailable")
			return
		}
		credentials, err := d.Credentials.ListByAccount(r.Context(), tenantID, accountID)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "provider_account_recovery_unavailable", "provider account recovery diagnostics are unavailable")
			return
		}
		channels := make([]channelhealth.Record, 0, len(credentials))
		for _, credential := range credentials {
			rec, err := d.ChannelHealth.GetRecord(r.Context(), channelhealth.ChannelKey{
				TenantID:            tenantID,
				Vendor:              credentialstore.Normalize(credential.Vendor),
				ProviderAccountID:   accountID,
				AccountCredentialID: credential.ID,
				CredentialVersion:   int(credential.Version),
			})
			switch {
			case err == nil:
				channels = append(channels, rec)
			case errors.Is(err, channelhealth.ErrNotFound):
			default:
				writeError(w, http.StatusServiceUnavailable, "provider_account_recovery_unavailable", "provider account recovery diagnostics are unavailable")
				return
			}
		}

		now := time.Now().UTC()
		if d.Now != nil {
			now = d.Now().UTC()
		}
		writeJSON(w, http.StatusOK, buildResponse(ident, account, credentials, channels, now))
	}
}

func buildResponse(
	ident admin.AdminIdentity,
	account admindb.AdminProviderAccountRow,
	credentials []credentialstore.CredentialMetadata,
	channels []channelhealth.Record,
	now time.Time,
) Response {
	resp := Response{
		AccountID:   account.ID,
		TenantID:    account.TenantID,
		GeneratedAt: now,
		Account:     accountSnapshot(account),
		Credentials: make([]CredentialSnapshot, 0, len(credentials)),
		Channels:    make([]ChannelSnapshot, 0, len(channels)),
		Actions:     make([]Action, 0, len(credentials)+len(channels)*2+2),
	}
	accountPath := fmt.Sprintf("/admin/v1/provider-accounts/%d", account.ID)
	accountAuthorized := ident.Role == admin.RolePlatformAdmin || ident.Role == admin.RoleTenantOperator
	platformAuthorized := ident.Role == admin.RolePlatformAdmin

	if !account.Enabled {
		resp.Actions = append(resp.Actions, newAction(
			"enable_account", "account", account.ID, accountAuthorized, true,
			"account_disabled", "low", http.MethodPatch,
			accountPath+"/enabled?tenant_id="+strconv.FormatInt(account.TenantID, 10),
			"platform_admin_or_tenant_operator", []string{"enabled"},
		))
	}
	if applicable, recommended, reason := accountBackoffState(account, now); applicable {
		resp.Actions = append(resp.Actions, newAction(
			"clear_account_backoff", "account", account.ID, accountAuthorized, recommended,
			reason, "medium", http.MethodPost,
			accountPath+"/clear-rate-limit?tenant_id="+strconv.FormatInt(account.TenantID, 10),
			"platform_admin_or_tenant_operator", []string{},
		))
	}

	for _, credential := range credentials {
		resp.Credentials = append(resp.Credentials, credentialSnapshot(credential))
		if applicable, reason := credentialRotationState(credential, now); applicable {
			resp.Actions = append(resp.Actions, newAction(
				"rotate_credential", "credential", credential.ID, platformAuthorized, true,
				reason, "high", http.MethodPost,
				fmt.Sprintf("%s/credentials/%d/rotate", accountPath, credential.ID),
				admin.RolePlatformAdmin, []string{"tenant_id", "credentials"},
			))
		}
	}

	for _, channel := range channels {
		resp.Channels = append(resp.Channels, channelSnapshot(channel))
		switch channel.State {
		case channelhealth.StateManualPaused:
			resp.Actions = append(resp.Actions, newAction(
				"resume_channel", "credential", channel.Key.AccountCredentialID, platformAuthorized, true,
				"channel_manual_paused", "medium", http.MethodPost,
				accountPath+"/channel-health/resume", admin.RolePlatformAdmin,
				[]string{"tenant_id", "vendor", "account_credential_id", "credential_version", "reason"},
			))
			resp.Actions = append(resp.Actions, newAction(
				"force_channel_active", "credential", channel.Key.AccountCredentialID, platformAuthorized, false,
				"channel_manual_paused", "high", http.MethodPost,
				accountPath+"/channel-health/force-active", admin.RolePlatformAdmin,
				[]string{"tenant_id", "vendor", "account_credential_id", "credential_version", "reason"},
			))
		case channelhealth.StateDisabled, channelhealth.StateCoolingDown:
			resp.Actions = append(resp.Actions, newAction(
				"force_channel_active", "credential", channel.Key.AccountCredentialID, platformAuthorized, false,
				"channel_"+string(channel.State), "high", http.MethodPost,
				accountPath+"/channel-health/force-active", admin.RolePlatformAdmin,
				[]string{"tenant_id", "vendor", "account_credential_id", "credential_version", "reason"},
			))
		}
	}

	for _, action := range resp.Actions {
		if action.Recommended {
			resp.RequiresAction = true
			break
		}
	}
	return resp
}

func newAction(action, subjectType string, subjectID int64, authorized, recommended bool, reasonCode, riskLevel, method, path, requiredRole string, requiredFields []string) Action {
	return Action{
		Action:          action,
		SubjectType:     subjectType,
		SubjectID:       subjectID,
		StateApplicable: true,
		Authorized:      authorized,
		Available:       authorized,
		Recommended:     recommended,
		ReasonCode:      reasonCode,
		RiskLevel:       riskLevel,
		Method:          method,
		Path:            path,
		RequiredRole:    requiredRole,
		RequiredFields:  requiredFields,
	}
}

func accountSnapshot(account admindb.AdminProviderAccountRow) AccountSnapshot {
	return AccountSnapshot{
		Enabled:                account.Enabled,
		HealthState:            account.HealthState,
		CredentialState:        account.CredentialState,
		RateLimitResetAt:       optionalTime(account.RateLimitResetAt),
		OverloadUntil:          optionalTime(account.OverloadUntil),
		TempUnschedulableUntil: optionalTime(account.TempUnschedulableUntil),
	}
}

func credentialSnapshot(credential credentialstore.CredentialMetadata) CredentialSnapshot {
	return CredentialSnapshot{
		ID:                 credential.ID,
		Vendor:             credential.Vendor,
		AuthMode:           credential.AuthMode,
		State:              credential.State,
		Version:            credential.Version,
		AccessExpiresAt:    credential.AccessExpiresAt,
		LastRefreshOutcome: credential.LastRefreshOutcome,
		FailureClass:       credential.FailureClass,
		FailureCount:       credential.FailureCount,
	}
}

func channelSnapshot(rec channelhealth.Record) ChannelSnapshot {
	return ChannelSnapshot{
		State:             rec.State,
		Vendor:            rec.Key.Vendor,
		CredentialID:      rec.Key.AccountCredentialID,
		CredentialVersion: rec.Key.CredentialVersion,
		ReasonClass:       rec.ReasonClass,
		CooldownUntil:     rec.CooldownUntil,
	}
}

func accountBackoffState(account admindb.AdminProviderAccountRow, now time.Time) (bool, bool, string) {
	values := []pgtype.Timestamptz{account.RateLimitResetAt, account.OverloadUntil, account.TempUnschedulableUntil}
	applicable := account.RateLimitReason != nil
	recommended := false
	for _, value := range values {
		if !value.Valid {
			continue
		}
		applicable = true
		if value.Time.After(now) {
			recommended = true
		}
	}
	if !applicable {
		return false, false, ""
	}
	if recommended {
		return true, true, "account_backoff_active"
	}
	return true, false, "account_backoff_markers_stale"
}

func credentialRotationState(credential credentialstore.CredentialMetadata, now time.Time) (bool, string) {
	switch credentialstore.Normalize(credential.State) {
	case credentialstore.StateExpired:
		return true, "credential_expired"
	case credentialstore.StateNeedsRotation:
		return true, "credential_needs_rotation"
	case credentialstore.StateRevoked:
		return true, "credential_revoked"
	case credentialstore.StateOperatorAttention:
		return true, "credential_operator_attention"
	}
	if credential.AccessExpiresAt != nil && !credential.AccessExpiresAt.After(now) {
		handler, ok := credentialstore.DefaultHandlerRegistry().Lookup(credential.Vendor, credential.AuthMode)
		if !ok || !handler.Refreshable() {
			return true, "credential_access_expired"
		}
	}
	if credential.LastRefreshOutcome != nil && credentialstore.Normalize(*credential.LastRefreshOutcome) == "refresh_failed" {
		return true, "credential_refresh_failed"
	}
	return false, ""
}

func optionalTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	t := value.Time.UTC()
	return &t
}

func resolveTenant(w http.ResponseWriter, r *http.Request, d Deps) (admin.AdminIdentity, int64, bool) {
	if d.Auth == nil || d.Accounts == nil || d.Credentials == nil || d.ChannelHealth == nil {
		writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "provider account recovery dependency unset")
		return admin.AdminIdentity{}, 0, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		if errors.Is(err, admin.ErrAdminBackend) {
			writeError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
		} else {
			writeError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
		}
		return admin.AdminIdentity{}, 0, false
	}
	queryTenantID, queryPresent, ok := parseOptionalTenantID(w, r)
	if !ok {
		return admin.AdminIdentity{}, 0, false
	}
	switch ident.Role {
	case admin.RoleTenantOperator:
		if ident.ScopeTenantID <= 0 {
			writeError(w, http.StatusForbidden, "admin_forbidden", "tenant_operator scope_tenant_id required")
			return admin.AdminIdentity{}, 0, false
		}
		if queryPresent && queryTenantID != ident.ScopeTenantID {
			writeError(w, http.StatusForbidden, "admin_forbidden", "tenant_id does not match admin scope")
			return admin.AdminIdentity{}, 0, false
		}
		return ident, ident.ScopeTenantID, true
	case admin.RolePlatformAdmin:
		if ident.ScopeTenantID > 0 {
			if queryPresent && queryTenantID != ident.ScopeTenantID {
				writeError(w, http.StatusForbidden, "admin_forbidden", "tenant_id does not match admin scope")
				return admin.AdminIdentity{}, 0, false
			}
			return ident, ident.ScopeTenantID, true
		}
		if !queryPresent {
			writeError(w, http.StatusBadRequest, "tenant_id_required", "tenant_id query parameter must be positive")
			return admin.AdminIdentity{}, 0, false
		}
		if err := ident.CanIssueForTenant(queryTenantID); err != nil {
			writeError(w, http.StatusForbidden, "admin_forbidden", "tenant scope not permitted")
			return admin.AdminIdentity{}, 0, false
		}
		return ident, queryTenantID, true
	default:
		writeError(w, http.StatusForbidden, "admin_forbidden", "admin role required")
		return admin.AdminIdentity{}, 0, false
	}
}

func parseOptionalTenantID(w http.ResponseWriter, r *http.Request) (int64, bool, bool) {
	values, present := r.URL.Query()["tenant_id"]
	if !present {
		return 0, false, true
	}
	raw := ""
	if len(values) > 0 {
		raw = strings.TrimSpace(values[0])
	}
	tenantID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || tenantID <= 0 {
		writeError(w, http.StatusBadRequest, "tenant_id_invalid", "tenant_id must be positive when provided")
		return 0, false, false
	}
	return tenantID, true, true
}

func parseAccountID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	accountID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || accountID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_provider_account_id", "id must be a positive int64")
		return 0, false
	}
	return accountID, true
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}
