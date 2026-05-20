package gatewayhttp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

const (
	defaultAdminProviderAccountTenantID = int64(1)
	defaultAdminProviderAccountLimit    = int32(50)
	maxAdminProviderAccountLimit        = int32(200)
	providerAccountCursorPrefix         = "provider_account_id:"
)

type AdminPoolAccountAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type AdminPoolAccountStore interface {
	InsertProviderAccount(context.Context, admindb.InsertProviderAccountParams) (int64, error)
	ListAdminProviderAccounts(context.Context, admindb.ListAdminProviderAccountsParams) ([]admindb.AdminProviderAccountRow, error)
	GetAdminProviderAccount(context.Context, admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error)
	UpdateAdminProviderAccount(context.Context, admindb.UpdateAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error)
	UpdateProviderAccountEnabled(context.Context, admindb.UpdateProviderAccountEnabledParams) error
	ClearProviderAccountRateLimit(context.Context, admindb.ClearProviderAccountRateLimitParams) (admindb.AdminProviderAccountRow, error)
	SoftDeleteProviderAccount(context.Context, admindb.SoftDeleteProviderAccountParams) error
	InsertAdminAuditEvent(context.Context, admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error)
}

type AdminPoolAccountCredentialWriter interface {
	Create(context.Context, credentialstore.CreateCredentialInput) (credentialstore.CredentialMetadata, error)
}

type AdminPoolAccountChannelHealthInitializer interface {
	EnsureDefaultActive(context.Context, channelhealth.ChannelKey) (channelhealth.Record, error)
}

type AdminPoolAccountDeps struct {
	Auth          AdminPoolAccountAuth
	Store         AdminPoolAccountStore
	Credentials   AdminPoolAccountCredentialWriter
	ChannelHealth AdminPoolAccountChannelHealthInitializer
}

func MountAdminPoolAccountRoutes(r chi.Router, d AdminPoolAccountDeps) {
	r.Get("/", newListProviderAccountsHandler(d))
	r.Post("/", newCreateProviderAccountHandler(d))
	r.Get("/{id}", newGetProviderAccountHandler(d))
	r.Patch("/{id}", newUpdateProviderAccountHandler(d))
	r.Patch("/{id}/enabled", newUpdateProviderAccountEnabledHandler(d))
	r.Post("/{id}/clear-rate-limit", newClearProviderAccountRateLimitHandler(d))
	r.Delete("/{id}", newDeleteProviderAccountHandler(d))
}

type createProviderAccountRequest struct {
	TenantID        *int64          `json:"tenant_id,omitempty"`
	ProviderID      int64           `json:"provider_id"`
	ChannelID       int64           `json:"channel_id"`
	Name            string          `json:"name"`
	AccountType     string          `json:"account_type"`
	Vendor          string          `json:"vendor,omitempty"`
	AuthMode        string          `json:"auth_mode,omitempty"`
	Enabled         *bool           `json:"enabled,omitempty"`
	CapConcurrency  *int32          `json:"cap_concurrency,omitempty"`
	Priority        *int32          `json:"priority,omitempty"`
	ModelAllowList  []string        `json:"model_allow_list,omitempty"`
	CapabilityFlags []string        `json:"capability_flags,omitempty"`
	Credentials     json.RawMessage `json:"credentials"`
	Reason          string          `json:"reason,omitempty"`
}

type mutateProviderAccountRequest struct {
	TenantID *int64 `json:"tenant_id,omitempty"`
	Enabled  *bool  `json:"enabled,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

type updateProviderAccountRequest struct {
	TenantID                   *int64           `json:"tenant_id,omitempty"`
	Enabled                    *bool            `json:"enabled,omitempty"`
	Priority                   *int32           `json:"priority,omitempty"`
	CapConcurrency             *int32           `json:"cap_concurrency,omitempty"`
	ModelAllowList             *[]string        `json:"model_allow_list,omitempty"`
	CapabilityFlags            *[]string        `json:"capability_flags,omitempty"`
	CustomErrorCodesEnabled    *bool            `json:"custom_error_codes_enabled,omitempty"`
	CustomErrorCodes           *[]int32         `json:"custom_error_codes,omitempty"`
	PoolMode                   *bool            `json:"pool_mode,omitempty"`
	TempUnschedulableEnabled   *bool            `json:"temp_unschedulable_enabled,omitempty"`
	TempUnschedulableRulesJSON *json.RawMessage `json:"temp_unschedulable_rules,omitempty"`
	Reason                     string           `json:"reason,omitempty"`
}

type providerAccountListResponse struct {
	Items []providerAccountResponse `json:"items"`
	Page  providerAccountPage       `json:"page"`
}

type providerAccountPage struct {
	Cursor     *string `json:"cursor"`
	NextCursor *string `json:"next_cursor"`
	HasMore    bool    `json:"has_more"`
}

type providerAccountResponse struct {
	ID                       int64      `json:"id"`
	TenantID                 int64      `json:"tenant_id"`
	ProviderID               int64      `json:"provider_id"`
	ChannelID                int64      `json:"channel_id"`
	Name                     string     `json:"name"`
	AccountType              string     `json:"account_type"`
	Enabled                  bool       `json:"enabled"`
	ExpiresAt                *time.Time `json:"expires_at"`
	HealthState              string     `json:"health_state"`
	CredentialState          string     `json:"credential_state"`
	CapConcurrency           int32      `json:"cap_concurrency"`
	InFlightCount            int32      `json:"in_flight_count"`
	Priority                 int32      `json:"priority"`
	LastDispatchAt           *time.Time `json:"last_dispatch_at"`
	ModelAllowList           []string   `json:"model_allow_list"`
	CapabilityFlags          []string   `json:"capability_flags"`
	RateLimitedAt            *time.Time `json:"rate_limited_at"`
	RateLimitResetAt         *time.Time `json:"rate_limit_reset_at"`
	RateLimitReason          *string    `json:"rate_limit_reason"`
	OverloadUntil            *time.Time `json:"overload_until"`
	TempUnschedulableUntil   *time.Time `json:"temp_unschedulable_until"`
	TokenVersion             int32      `json:"token_version"`
	LastRefreshAt            *time.Time `json:"last_refresh_at"`
	LastRefreshOutcome       *string    `json:"last_refresh_outcome"`
	OAuthEndpointHealth      string     `json:"oauth_endpoint_health,omitempty"`
	CustomErrorCodesEnabled  bool       `json:"custom_error_codes_enabled"`
	CustomErrorCodes         []int32    `json:"custom_error_codes"`
	PoolMode                 bool       `json:"pool_mode"`
	TempUnschedulableEnabled bool       `json:"temp_unschedulable_enabled"`
	CreatedAt                *time.Time `json:"created_at"`
	UpdatedAt                *time.Time `json:"updated_at"`
}

func newCreateProviderAccountHandler(d AdminPoolAccountDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveProviderAccountAdmin(w, r, d)
		if !ok {
			return
		}
		var req createProviderAccountRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		if !validateProviderAccountTenant(w, req.TenantID, tenantID) {
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		req.AccountType = strings.TrimSpace(req.AccountType)
		req.Vendor = credentialstore.Normalize(req.Vendor)
		req.AuthMode = credentialstore.Normalize(req.AuthMode)
		req.ModelAllowList = cleanStringList(req.ModelAllowList)
		req.CapabilityFlags = cleanStringList(req.CapabilityFlags)
		useCredentialStore := req.Vendor != "" || req.AuthMode != ""
		if err := validateCreateProviderAccount(req, useCredentialStore && d.Credentials != nil); err != nil {
			writeJSONError(w, http.StatusBadRequest, "admin_bad_request", err.Error())
			return
		}
		if useCredentialStore && d.Credentials == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "credential store dependency unset")
			return
		}
		actorID := fmt.Sprintf("%d", ident.TokenID)
		dbCredentials := []byte(req.Credentials)
		if useCredentialStore {
			dbCredentials = []byte(`{}`)
		}
		id, err := d.Store.InsertProviderAccount(r.Context(), admindb.InsertProviderAccountParams{
			TenantID: tenantID, ProviderID: req.ProviderID, ChannelID: req.ChannelID,
			Name: req.Name, AccountType: req.AccountType, Enabled: req.Enabled,
			Credentials: dbCredentials, CapConcurrency: req.CapConcurrency, Priority: req.Priority,
			ModelAllowList: req.ModelAllowList, CapabilityFlags: req.CapabilityFlags, ActorID: &actorID,
		})
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "provider_account_insert_failed", err.Error())
			return
		}
		var credentialID int64
		var credentialVersion int
		channelHealthInitialized := false
		if useCredentialStore {
			created, err := d.Credentials.Create(r.Context(), credentialstore.CreateCredentialInput{
				TenantID: tenantID, ProviderAccountID: id,
				Vendor: req.Vendor, AuthMode: req.AuthMode,
				Payload: req.Credentials, ActorID: actorID,
			})
			if err != nil {
				// Credentials.Create 失败时, 上面 InsertProviderAccount
				// 已经写了一个 enabled=req.Enabled + credentials='{}' 的 account 行, 一旦 credential
				// 创建失败 (加密 / DB IO 等), account 留在池里没可用凭据, 后续 selector 选到它会
				// 401 / 退回; cleanup: 软删该 account 防 orphan 进 pool。
				_ = d.Store.SoftDeleteProviderAccount(r.Context(), admindb.SoftDeleteProviderAccountParams{
					ActorID: &actorID, ID: id, TenantID: tenantID,
				})
				writeJSONError(w, http.StatusServiceUnavailable, "account_credential_insert_failed", err.Error())
				return
			}
			credentialID = created.ID
			credentialVersion = int(created.Version)
			if d.ChannelHealth != nil {
				key := channelhealth.ChannelKey{
					TenantID: tenantID, Vendor: created.Vendor, ProviderAccountID: id,
					AccountCredentialID: created.ID, CredentialVersion: int(created.Version),
				}
				if _, err := d.ChannelHealth.EnsureDefaultActive(r.Context(), key); err == nil {
					channelHealthInitialized = true
				}
			}
		}
		payload, _ := json.Marshal(map[string]any{
			"tenant_id":                  tenantID,
			"provider_id":                req.ProviderID,
			"channel_id":                 req.ChannelID,
			"name":                       req.Name,
			"account_type":               req.AccountType,
			"vendor":                     req.Vendor,
			"auth_mode":                  req.AuthMode,
			"credential_id":              credentialID,
			"credential_version":         credentialVersion,
			"channel_health_initialized": channelHealthInitialized,
			"credentials_present":        true,
		})
		if err := writeProviderAccountAudit(r.Context(), r, d.Store, ident, tenantID,
			"create_provider_account", id, chineseReason(req.Reason, "创建 provider account"), payload); err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "audit_write_failed", err.Error())
			return
		}
		account, err := d.Store.GetAdminProviderAccount(r.Context(), admindb.GetAdminProviderAccountParams{ID: id, TenantID: tenantID})
		if err != nil {
			writeProviderAccountReadError(w, err, "provider_account_get_failed")
			return
		}
		_ = credentialID
		_ = credentialVersion
		writeAuditJSON(w, http.StatusCreated, providerAccountDTO(account))
	}
}

func newListProviderAccountsHandler(d AdminPoolAccountDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, tenantID, ok := resolveProviderAccountAdmin(w, r, d)
		if !ok {
			return
		}
		limit, ok := parseProviderAccountLimit(w, r)
		if !ok {
			return
		}
		afterID, cursor, ok := parseProviderAccountCursor(w, r)
		if !ok {
			return
		}
		poolGroupID, ok := parseProviderAccountPoolGroupID(w, r)
		if !ok {
			return
		}
		stateFilter, ok := parseProviderAccountStateFilter(w, r)
		if !ok {
			return
		}
		rows, err := d.Store.ListAdminProviderAccounts(r.Context(), admindb.ListAdminProviderAccountsParams{
			TenantID: tenantID, AfterID: afterID, LimitCount: limit + 1,
			PoolGroupID: poolGroupID, StateFilter: stateFilter,
		})
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "provider_account_list_failed", err.Error())
			return
		}
		hasMore := int32(len(rows)) > limit
		if hasMore {
			rows = rows[:limit]
		}
		items := make([]providerAccountResponse, 0, len(rows))
		for _, row := range rows {
			items = append(items, providerAccountDTO(row))
		}
		var nextCursor *string
		if hasMore && len(rows) > 0 {
			next := encodeProviderAccountCursor(rows[len(rows)-1].ID)
			nextCursor = &next
		}
		writeAuditJSON(w, http.StatusOK, providerAccountListResponse{
			Items: items,
			Page:  providerAccountPage{Cursor: cursor, NextCursor: nextCursor, HasMore: hasMore},
		})
	}
}

func newGetProviderAccountHandler(d AdminPoolAccountDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, tenantID, ok := resolveProviderAccountAdmin(w, r, d)
		if !ok {
			return
		}
		id, ok := parseAdminPoolID(w, r)
		if !ok {
			return
		}
		account, err := d.Store.GetAdminProviderAccount(r.Context(), admindb.GetAdminProviderAccountParams{ID: id, TenantID: tenantID})
		if err != nil {
			writeProviderAccountReadError(w, err, "provider_account_get_failed")
			return
		}
		writeAuditJSON(w, http.StatusOK, providerAccountDTO(account))
	}
}

func newUpdateProviderAccountHandler(d AdminPoolAccountDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveProviderAccountAdmin(w, r, d)
		if !ok {
			return
		}
		id, ok := parseAdminPoolID(w, r)
		if !ok {
			return
		}
		var req updateProviderAccountRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		if !validateProviderAccountTenant(w, req.TenantID, tenantID) {
			return
		}
		if err := validateUpdateProviderAccount(req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "admin_bad_request", err.Error())
			return
		}
		actorID := fmt.Sprintf("%d", ident.TokenID)
		arg := admindb.UpdateAdminProviderAccountParams{
			ID: id, TenantID: tenantID, ActorID: &actorID,
			Enabled: req.Enabled, Priority: req.Priority, CapConcurrency: req.CapConcurrency,
			CustomErrorCodesEnabled: req.CustomErrorCodesEnabled,
			PoolMode:                req.PoolMode, TempUnschedulableEnabled: req.TempUnschedulableEnabled,
		}
		if req.ModelAllowList != nil {
			arg.SetModelAllowList = true
			arg.ModelAllowList = cleanStringList(*req.ModelAllowList)
		}
		if req.CapabilityFlags != nil {
			arg.SetCapabilityFlags = true
			arg.CapabilityFlags = cleanStringList(*req.CapabilityFlags)
		}
		if req.CustomErrorCodes != nil {
			arg.SetCustomErrorCodes = true
			arg.CustomErrorCodes = *req.CustomErrorCodes
		}
		if req.TempUnschedulableRulesJSON != nil {
			arg.SetTempUnschedulableRules = true
			arg.TempUnschedulableRulesJSON = []byte(*req.TempUnschedulableRulesJSON)
		}
		account, err := d.Store.UpdateAdminProviderAccount(r.Context(), arg)
		if err != nil {
			writeProviderAccountReadError(w, err, "provider_account_update_failed")
			return
		}
		payload, _ := json.Marshal(map[string]any{"tenant_id": tenantID, "updated": true})
		if err := writeProviderAccountAudit(r.Context(), r, d.Store, ident, tenantID,
			"update_provider_account", id, chineseReason(req.Reason, "更新 provider account"), payload); err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "audit_write_failed", err.Error())
			return
		}
		writeAuditJSON(w, http.StatusOK, providerAccountDTO(account))
	}
}

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
		actorID := fmt.Sprintf("%d", ident.TokenID)
		if err := d.Store.UpdateProviderAccountEnabled(r.Context(), admindb.UpdateProviderAccountEnabledParams{
			Enabled: *req.Enabled, ActorID: &actorID, ID: id, TenantID: tenantID,
		}); err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "provider_account_update_failed", err.Error())
			return
		}
		action, reason := "enable_provider_account", "启用 provider account"
		if !*req.Enabled {
			action, reason = "disable_provider_account", "禁用 provider account"
		}
		payload, _ := json.Marshal(map[string]any{"tenant_id": tenantID, "enabled": *req.Enabled})
		if err := writeProviderAccountAudit(r.Context(), r, d.Store, ident, tenantID,
			action, id, chineseReason(req.Reason, reason), payload); err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "audit_write_failed", err.Error())
			return
		}
		writeAuditJSON(w, http.StatusOK, map[string]any{"id": id, "enabled": *req.Enabled})
	}
}

func newClearProviderAccountRateLimitHandler(d AdminPoolAccountDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveProviderAccountAdmin(w, r, d)
		if !ok {
			return
		}
		id, ok := parseAdminPoolID(w, r)
		if !ok {
			return
		}
		actorID := fmt.Sprintf("%d", ident.TokenID)
		if _, err := d.Store.ClearProviderAccountRateLimit(r.Context(), admindb.ClearProviderAccountRateLimitParams{
			ID: id, TenantID: tenantID, ActorID: &actorID,
		}); err != nil {
			writeProviderAccountReadError(w, err, "provider_account_clear_rate_limit_failed")
			return
		}
		payload, _ := json.Marshal(map[string]any{"tenant_id": tenantID, "cleared": true})
		if err := writeProviderAccountAudit(r.Context(), r, d.Store, ident, tenantID,
			"clear_provider_account_rate_limit", id, chineseReason("", "清除 provider account rate limit"), payload); err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "audit_write_failed", err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
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
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		if !validateProviderAccountTenant(w, req.TenantID, tenantID) {
			return
		}
		actorID := fmt.Sprintf("%d", ident.TokenID)
		if err := d.Store.SoftDeleteProviderAccount(r.Context(), admindb.SoftDeleteProviderAccountParams{
			ActorID: &actorID, ID: id, TenantID: tenantID,
		}); err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "provider_account_delete_failed", err.Error())
			return
		}
		payload, _ := json.Marshal(map[string]any{"tenant_id": tenantID, "deleted": true})
		if err := writeProviderAccountAudit(r.Context(), r, d.Store, ident, tenantID,
			"delete_provider_account", id, chineseReason(req.Reason, "删除 provider account"), payload); err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "audit_write_failed", err.Error())
			return
		}
		writeAuditJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
	}
}

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
		tenantID := ident.ScopeTenantID
		if tenantID <= 0 {
			tenantID = defaultAdminProviderAccountTenantID
		}
		return ident, tenantID, true
	default:
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "admin role required")
		return admin.AdminIdentity{}, 0, false
	}
}

func resolvePlatformAdmin(w http.ResponseWriter, r *http.Request, d AdminPoolAccountDeps) (admin.AdminIdentity, bool) {
	if d.Auth == nil || d.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "admin pool account dependency unset")
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
	if ident.Role != admin.RolePlatformAdmin {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "platform_admin role required")
		return admin.AdminIdentity{}, false
	}
	return ident, true
}

func decodeAdminPoolJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	return true
}

func validateCreateProviderAccount(req createProviderAccountRequest, requireCredentialV2 bool) error {
	if req.ProviderID <= 0 || req.ChannelID <= 0 || req.Name == "" {
		return fmt.Errorf("provider_id, channel_id, and name are required")
	}
	switch req.AccountType {
	case "oauth", "api_key", "service_account", "upstream_static", "session":
	default:
		return fmt.Errorf("account_type is invalid")
	}
	if req.CapConcurrency != nil && *req.CapConcurrency <= 0 {
		return fmt.Errorf("cap_concurrency must be positive")
	}
	var obj map[string]json.RawMessage
	if len(req.Credentials) == 0 || json.Unmarshal(req.Credentials, &obj) != nil || obj == nil {
		return fmt.Errorf("credentials must be a JSON object")
	}
	if requireCredentialV2 {
		if req.Vendor == "" || req.AuthMode == "" {
			return fmt.Errorf("vendor and auth_mode are required for account_credentials")
		}
		handler, err := credentialstore.DefaultHandlerRegistry().MustLookup(req.Vendor, req.AuthMode)
		if err != nil {
			return err
		}
		if err := handler.ValidatePayload(req.Credentials); err != nil {
			return err
		}
	}
	return nil
}

func validateUpdateProviderAccount(req updateProviderAccountRequest) error {
	if req.Enabled == nil && req.Priority == nil && req.CapConcurrency == nil && req.ModelAllowList == nil &&
		req.CapabilityFlags == nil && req.CustomErrorCodesEnabled == nil && req.CustomErrorCodes == nil &&
		req.PoolMode == nil && req.TempUnschedulableEnabled == nil && req.TempUnschedulableRulesJSON == nil {
		return fmt.Errorf("at least one supported field is required")
	}
	if req.CapConcurrency != nil && *req.CapConcurrency <= 0 {
		return fmt.Errorf("cap_concurrency must be positive")
	}
	if req.TempUnschedulableRulesJSON != nil {
		var rules []map[string]any
		if len(*req.TempUnschedulableRulesJSON) == 0 || json.Unmarshal(*req.TempUnschedulableRulesJSON, &rules) != nil {
			return fmt.Errorf("temp_unschedulable_rules must be a JSON array")
		}
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

func providerAccountDTO(row admindb.AdminProviderAccountRow) providerAccountResponse {
	return providerAccountResponse{
		ID: row.ID, TenantID: row.TenantID, ProviderID: row.ProviderID, ChannelID: row.ChannelID,
		Name: row.Name, AccountType: row.AccountType, Enabled: row.Enabled, ExpiresAt: pgTimePtr(row.ExpiresAt),
		HealthState: row.HealthState, CredentialState: row.CredentialState,
		CapConcurrency: row.CapConcurrency, InFlightCount: row.InFlightCount, Priority: row.Priority,
		LastDispatchAt: pgTimePtr(row.LastDispatchAt), ModelAllowList: nonNilStringSlice(row.ModelAllowList),
		CapabilityFlags: nonNilStringSlice(row.CapabilityFlags), RateLimitedAt: pgTimePtr(row.RateLimitedAt),
		RateLimitResetAt: pgTimePtr(row.RateLimitResetAt), RateLimitReason: row.RateLimitReason,
		OverloadUntil: pgTimePtr(row.OverloadUntil), TempUnschedulableUntil: pgTimePtr(row.TempUnschedulableUntil),
		TokenVersion: row.TokenVersion, LastRefreshAt: pgTimePtr(row.LastRefreshAt),
		LastRefreshOutcome: row.LastRefreshOutcome, OAuthEndpointHealth: row.OAuthEndpointHealth,
		CustomErrorCodesEnabled: row.CustomErrorCodesEnabled, CustomErrorCodes: nonNilInt32Slice(row.CustomErrorCodes),
		PoolMode: row.PoolMode, TempUnschedulableEnabled: row.TempUnschedulableEnabled,
		CreatedAt: pgTimePtr(row.CreatedAt), UpdatedAt: pgTimePtr(row.UpdatedAt),
	}
}

func pgTimePtr(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time.UTC()
	return &t
}

func nonNilStringSlice(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func nonNilInt32Slice(in []int32) []int32 {
	if in == nil {
		return []int32{}
	}
	return in
}

func cleanStringList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, item := range in {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func writeProviderAccountReadError(w http.ResponseWriter, err error, code string) {
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "provider_account_not_found", "provider account not found")
		return
	}
	writeJSONError(w, http.StatusServiceUnavailable, code, err.Error())
}

func chineseReason(got, fallback string) *string {
	if reason := strings.TrimSpace(got); reason != "" {
		return &reason
	}
	return &fallback
}

func writeProviderAccountAudit(ctx context.Context, r *http.Request, store AdminPoolAccountStore, ident admin.AdminIdentity, tenantID int64, action string, targetID int64, reason *string, payload []byte) error {
	actorID := fmt.Sprintf("%d", ident.TokenID)
	reqID := middleware.GetReqID(r.Context())
	_, err := store.InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
		TenantID: &tenantID, ActorID: actorID, ActorRole: ident.Role,
		Action: action, TargetType: "provider_account", TargetID: &targetID,
		RequestID: &reqID, Reason: reason, Payload: payload,
	})
	return err
}
