package gatewayhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountadvanced"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountcreate"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountops"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/poolaccountadmin"
	"github.com/BloomingProsperity/HUAKAI/internal/mixedchannelrisk"
)

var (
	errAdminPoolAccountTxPoolUnset                  = accountcreate.ErrPoolUnset
	errProviderAccountMixedRiskConfirmationRequired = accountcreate.ErrMixedRiskConfirmRequired
	errProviderAccountProtocolIncompatible          = accountcreate.ErrProtocolIncompatible
)

type AdminPoolAccountAuth = poolaccountadmin.Auth
type AdminPoolAccountStore = poolaccountadmin.Store
type AdminPoolAccountRiskStore = poolaccountadmin.RiskStore
type AdminPoolAccountAtomicCreateStore = poolaccountadmin.AtomicCreateStore
type AdminPoolAccountCredentialWriter = poolaccountadmin.CredentialWriter
type AdminPoolAccountChannelHealthInitializer = poolaccountadmin.ChannelHealthInitializer
type AdminPoolAccountDeps = poolaccountadmin.Deps
type adminPoolAccountCreateWithMixedRiskParams = accountcreate.Params
type adminPoolAccountCreateWithMixedRiskResult = accountcreate.Result

func NewAdminPoolAccountStoreAdapter(base AdminPoolAccountStore, pool *pgxpool.Pool) AdminPoolAccountStore {
	return poolaccountadmin.NewStoreAdapter(base, pool)
}

func MountAdminPoolAccountRoutes(r chi.Router, d AdminPoolAccountDeps) {
	r.Get("/", newListProviderAccountsHandler(d))
	r.Post("/", newCreateProviderAccountHandler(d))
	r.Get("/{id}", newGetProviderAccountHandler(d))
	r.Get("/{id}/operations", newGetProviderAccountOperationsHandler(d))
	r.Patch("/{id}", newUpdateProviderAccountHandler(d))
	r.Patch("/{id}/enabled", newUpdateProviderAccountEnabledHandler(d))
	r.Post("/{id}/clear-rate-limit", newClearProviderAccountRateLimitHandler(d))
	r.Delete("/{id}", newDeleteProviderAccountHandler(d))
}

type providerAccountOperationsStateReader interface {
	GetProviderAccountOperationsState(context.Context, admindb.GetProviderAccountOperationsStateParams) (admindb.ProviderAccountOperationsState, error)
}

type providerAccountCredentialInventory interface {
	ListByAccount(context.Context, int64, int64) ([]credentialstore.CredentialMetadata, error)
}

type providerAccountChannelHealthReader interface {
	LatestByProviderAccount(context.Context, int64, int64) (channelhealth.Record, error)
}

type createProviderAccountRequest = poolaccountadmin.CreateRequest
type mutateProviderAccountRequest = poolaccountadmin.MutateRequest
type updateProviderAccountRequest = poolaccountadmin.UpdateRequest

type providerAccountAdvancedCarrier interface {
	SetProviderAccountAdvanced(accountadvanced.Mutation)
}

type providerAccountListResponse = poolaccountadmin.ListResponse
type providerAccountPage = poolaccountadmin.Page
type providerAccountResponse = poolaccountadmin.Response

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
		req.ProbeModel = poolaccountadmin.CleanOptionalString(req.ProbeModel)
		req.Tags = poolaccountadmin.CleanStringList(req.Tags)
		req.ModelAllowList = poolaccountadmin.CleanStringList(req.ModelAllowList)
		req.CapabilityFlags = poolaccountadmin.CleanStringList(req.CapabilityFlags)
		useCredentialStore := req.Vendor != "" || req.AuthMode != ""
		if err := poolaccountadmin.ValidateCreate(req, useCredentialStore && d.Credentials != nil); err != nil {
			writeJSONError(w, http.StatusBadRequest, "admin_bad_request", err.Error())
			return
		}
		if useCredentialStore && d.Credentials == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "credential store dependency unset")
			return
		}
		providerFamily, err := d.Store.GetProviderProtocolForAccountCreate(r.Context(), admindb.GetProviderProtocolForAccountCreateParams{
			TenantID: tenantID, ProviderID: req.ProviderID,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSONError(w, http.StatusBadRequest, "admin_bad_request", "provider does not exist")
				return
			}
			writeJSONError(w, http.StatusServiceUnavailable, "provider_protocol_lookup_failed", err.Error())
			return
		}
		if err := validateProviderAccountProtocolCompatibility(providerFamily, req.AccountType, req.Vendor, req.AuthMode); err != nil {
			writeJSONError(w, http.StatusBadRequest, "admin_bad_request", err.Error())
			return
		}
		confirmed, ok := parseProviderAccountMixedRiskConfirm(w, r, req)
		if !ok {
			return
		}
		actorID := ident.AuditActor()
		dbCredentials := []byte(req.Credentials)
		if useCredentialStore {
			dbCredentials = []byte(`{}`)
		}
		createArg := admindb.InsertProviderAccountParams{
			TenantID: tenantID, ProviderID: req.ProviderID, ChannelID: req.ChannelID,
			Name: req.Name, AccountType: req.AccountType, Enabled: req.Enabled,
			Credentials: dbCredentials, CapConcurrency: req.CapConcurrency, Priority: req.Priority,
			StaticWeight: req.StaticWeight, ProbeModel: req.ProbeModel, Tags: req.Tags,
			Extra:          poolaccountadmin.NormalizedExtra(req.Extra),
			ModelAllowList: req.ModelAllowList, CapabilityFlags: req.CapabilityFlags, ActorID: &actorID,
		}
		accountadvanced.ApplyCreate(req.Advanced, &createArg)
		createResult, err := insertProviderAccountWithMixedRiskCheck(r.Context(), d.Store, createArg, req, providerFamily, confirmed)
		if err != nil {
			if errors.Is(err, errProviderAccountProtocolIncompatible) {
				writeJSONError(w, http.StatusBadRequest, "admin_bad_request", err.Error())
				return
			}
			if errors.Is(err, errProviderAccountMixedRiskConfirmationRequired) {
				writeProviderAccountMixedRiskRequired(w, createResult.RiskReport)
				return
			}
			writeJSONError(w, http.StatusServiceUnavailable, "provider_account_insert_failed", err.Error())
			return
		}
		id := createResult.ID
		riskReport := createResult.RiskReport
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
		if riskReport.HighRisk {
			payload, _ = json.Marshal(map[string]any{
				"tenant_id":                    tenantID,
				"provider_id":                  req.ProviderID,
				"channel_id":                   req.ChannelID,
				"name":                         req.Name,
				"account_type":                 req.AccountType,
				"vendor":                       req.Vendor,
				"auth_mode":                    req.AuthMode,
				"credential_id":                credentialID,
				"credential_version":           credentialVersion,
				"channel_health_initialized":   channelHealthInitialized,
				"credentials_present":          true,
				"mixed_channel_risk_confirmed": true,
				"mixed_channel_risks":          riskReport.Items,
			})
		}
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
		options, requestError := poolaccountadmin.ParseListOptions(r)
		if requestError != nil {
			writeJSONError(w, requestError.Status, requestError.Code, requestError.Message)
			return
		}
		rows, err := d.Store.ListAdminProviderAccounts(r.Context(), admindb.ListAdminProviderAccountsParams{
			TenantID: tenantID, AfterID: options.AfterID, LimitCount: options.Limit + 1,
			PoolGroupID: options.PoolGroupID, StateFilter: options.StateFilter, TagFilter: options.TagFilter,
		})
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "provider_account_list_failed", err.Error())
			return
		}
		hasMore := int32(len(rows)) > options.Limit
		if hasMore {
			rows = rows[:options.Limit]
		}
		items := make([]providerAccountResponse, 0, len(rows))
		for _, row := range rows {
			items = append(items, providerAccountDTO(row))
		}
		var nextCursor *string
		if hasMore && len(rows) > 0 {
			next := poolaccountadmin.EncodeCursor(rows[len(rows)-1].ID)
			nextCursor = &next
		}
		writeAuditJSON(w, http.StatusOK, providerAccountListResponse{
			Items: items,
			Page:  providerAccountPage{Cursor: options.Cursor, NextCursor: nextCursor, HasMore: hasMore},
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

func newGetProviderAccountOperationsHandler(d AdminPoolAccountDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, tenantID, ok := resolveProviderAccountAdmin(w, r, d)
		if !ok {
			return
		}
		id, ok := parseAdminPoolID(w, r)
		if !ok {
			return
		}
		stateReader, ok := d.Store.(providerAccountOperationsStateReader)
		if !ok {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "provider account operations state dependency unset")
			return
		}
		credentialReader, ok := d.Credentials.(providerAccountCredentialInventory)
		if !ok {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "provider account credential inventory dependency unset")
			return
		}
		account, err := d.Store.GetAdminProviderAccount(r.Context(), admindb.GetAdminProviderAccountParams{ID: id, TenantID: tenantID})
		if err != nil {
			writeProviderAccountReadError(w, err, "provider_account_get_failed")
			return
		}
		routingState, err := stateReader.GetProviderAccountOperationsState(r.Context(), admindb.GetProviderAccountOperationsStateParams{
			ID: id, TenantID: tenantID,
		})
		if err != nil {
			writeProviderAccountReadError(w, err, "provider_account_operations_state_failed")
			return
		}
		credentials, err := credentialReader.ListByAccount(r.Context(), tenantID, id)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "provider_account_credentials_failed", "provider account credential inventory is temporarily unavailable")
			return
		}
		var health *channelhealth.Record
		healthAvailable := false
		if reader, ok := d.ChannelHealth.(providerAccountChannelHealthReader); ok {
			healthAvailable = true
			record, err := reader.LatestByProviderAccount(r.Context(), tenantID, id)
			if err != nil && !errors.Is(err, channelhealth.ErrNotFound) {
				writeJSONError(w, http.StatusServiceUnavailable, "provider_account_channel_health_failed", "provider account channel health is temporarily unavailable")
				return
			}
			if err == nil {
				health = &record
			}
		}
		writeAuditJSON(w, http.StatusOK, accountops.Aggregate(accountops.Input{
			Account: account, RoutingState: routingState, Credentials: credentials,
			ChannelHealth: health, ChannelHealthAvailable: healthAvailable,
		}))
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
		if err := poolaccountadmin.ValidateUpdate(req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "admin_bad_request", err.Error())
			return
		}
		actorID := ident.AuditActor()
		arg := admindb.UpdateAdminProviderAccountParams{
			ID: id, TenantID: tenantID, ActorID: &actorID,
			Enabled: req.Enabled, Priority: req.Priority, StaticWeight: req.StaticWeight, CapConcurrency: req.CapConcurrency,
		}
		if req.ProbeModel != nil {
			arg.SetProbeModel = true
			arg.ProbeModel = poolaccountadmin.CleanOptionalString(req.ProbeModel)
		}
		if req.Tags != nil {
			arg.SetTags = true
			arg.Tags = poolaccountadmin.CleanStringList(*req.Tags)
		}
		if req.Extra != nil {
			arg.SetExtra = true
			arg.Extra = poolaccountadmin.NormalizedExtra(*req.Extra)
		}
		if req.ModelAllowList != nil {
			arg.SetModelAllowList = true
			arg.ModelAllowList = poolaccountadmin.CleanStringList(*req.ModelAllowList)
		}
		if req.CapabilityFlags != nil {
			arg.SetCapabilityFlags = true
			arg.CapabilityFlags = poolaccountadmin.CleanStringList(*req.CapabilityFlags)
		}
		accountadvanced.ApplyUpdate(req.Advanced, &arg)
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
		actorID := ident.AuditActor()
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
		actorID := ident.AuditActor()
		account, err := d.Store.ClearProviderAccountRateLimit(r.Context(), admindb.ClearProviderAccountRateLimitParams{
			ID: id, TenantID: tenantID, ActorID: &actorID,
		})
		if err != nil {
			writeProviderAccountReadError(w, err, "provider_account_clear_rate_limit_failed")
			return
		}
		payload, _ := json.Marshal(map[string]any{"tenant_id": tenantID, "cleared": true})
		if err := writeProviderAccountAudit(r.Context(), r, d.Store, ident, tenantID,
			"clear_provider_account_rate_limit", id, chineseReason("", "清除 provider account rate limit"), payload); err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "audit_write_failed", err.Error())
			return
		}
		// 返回重新激活后的 account 行（UPDATE...RETURNING 已经给到清除后的状态），
		// 让运维 UI 看到该账号不再被停用，而不是一个不透明的 204。
		writeAuditJSON(w, http.StatusOK, providerAccountDTO(account))
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
		actorID := ident.AuditActor()
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
	identity, tenantID, requestError := poolaccountadmin.ResolveTenant(r, d)
	if requestError != nil {
		writeJSONError(w, requestError.Status, requestError.Code, requestError.Message)
		return admin.AdminIdentity{}, 0, false
	}
	return identity, tenantID, true
}

func resolvePlatformAdmin(w http.ResponseWriter, r *http.Request, d AdminPoolAccountDeps) (admin.AdminIdentity, bool) {
	identity, requestError := poolaccountadmin.ResolvePlatform(r, d)
	if requestError != nil {
		writeJSONError(w, requestError.Status, requestError.Code, requestError.Message)
		return admin.AdminIdentity{}, false
	}
	return identity, true
}

func decodeAdminPoolJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	if carrier, ok := dst.(providerAccountAdvancedCarrier); ok {
		advanced, err := accountadvanced.Parse(raw)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "admin_bad_request", err.Error())
			return false
		}
		carrier.SetProviderAccountAdvanced(advanced)
	}
	return true
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

func insertProviderAccountWithMixedRiskCheck(ctx context.Context, store AdminPoolAccountStore, createArg admindb.InsertProviderAccountParams, req createProviderAccountRequest, providerFamily string, confirmed bool) (adminPoolAccountCreateWithMixedRiskResult, error) {
	atomicStore, ok := store.(AdminPoolAccountAtomicCreateStore)
	if !ok {
		return adminPoolAccountCreateWithMixedRiskResult{}, errAdminPoolAccountTxPoolUnset
	}
	candidate := mixedchannelrisk.Account{
		ProviderID: req.ProviderID, ChannelID: req.ChannelID,
		AccountType: req.AccountType, Vendor: req.Vendor, AuthMode: req.AuthMode,
	}
	return atomicStore.InsertProviderAccountWithMixedRiskCheck(ctx, adminPoolAccountCreateWithMixedRiskParams{
		Insert: createArg, Candidate: candidate, ProviderFamily: providerFamily, Confirmed: confirmed,
	})
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

func parseAdminPoolID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := poolaccountadmin.ParsePositiveID(chi.URLParam(r, "id"))
	if err != nil {
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

func providerAccountDTO(row admindb.AdminProviderAccountRow) providerAccountResponse {
	return poolaccountadmin.DTO(row)
}

func writeProviderAccountReadError(w http.ResponseWriter, err error, code string) {
	requestError := poolaccountadmin.ReadError(err, code)
	writeJSONError(w, requestError.Status, requestError.Code, requestError.Message)
}

func chineseReason(got, fallback string) *string {
	if reason := strings.TrimSpace(got); reason != "" {
		return &reason
	}
	return &fallback
}

func writeProviderAccountAudit(ctx context.Context, r *http.Request, store AdminPoolAccountStore, ident admin.AdminIdentity, tenantID int64, action string, targetID int64, reason *string, payload []byte) error {
	return poolaccountadmin.WriteAudit(ctx, r, store, ident, tenantID, action, targetID, reason, payload)
}
