package gatewayhttp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/accountquota"
	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountadvanced"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountcreate"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountsubscription"
	"github.com/BloomingProsperity/HUAKAI/internal/mixedchannelrisk"
	"github.com/BloomingProsperity/HUAKAI/internal/quotawindowview"
)

const (
	defaultAdminProviderAccountLimit = int32(50)
	maxAdminProviderAccountLimit     = int32(200)
	providerAccountCursorPrefix      = "provider_account_id:"
)

var (
	errAdminPoolAccountTxPoolUnset                  = accountcreate.ErrPoolUnset
	errProviderAccountMixedRiskConfirmationRequired = accountcreate.ErrMixedRiskConfirmRequired
	errProviderAccountProtocolIncompatible          = accountcreate.ErrProtocolIncompatible
)

type AdminPoolAccountAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type AdminPoolAccountStore interface {
	GetProviderProtocolForAccountCreate(context.Context, admindb.GetProviderProtocolForAccountCreateParams) (string, error)
	InsertProviderAccount(context.Context, admindb.InsertProviderAccountParams) (int64, error)
	ListAdminProviderAccounts(context.Context, admindb.ListAdminProviderAccountsParams) ([]admindb.AdminProviderAccountRow, error)
	GetAdminProviderAccount(context.Context, admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error)
	UpdateAdminProviderAccount(context.Context, admindb.UpdateAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error)
	UpdateProviderAccountEnabled(context.Context, admindb.UpdateProviderAccountEnabledParams) error
	SoftDeleteProviderAccount(context.Context, admindb.SoftDeleteProviderAccountParams) error
	InsertAdminAuditEvent(context.Context, admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error)
}

type AdminPoolAccountRiskStore interface {
	ListProviderAccountRiskPeers(context.Context, admindb.ListProviderAccountRiskPeersParams) ([]admindb.ProviderAccountRiskPeerRow, error)
}

type AdminPoolAccountAtomicCreateStore interface {
	InsertProviderAccountWithMixedRiskCheck(context.Context, adminPoolAccountCreateWithMixedRiskParams) (adminPoolAccountCreateWithMixedRiskResult, error)
}

type AdminPoolAccountCredentialWriter interface {
	Create(context.Context, credentialstore.CreateCredentialInput) (credentialstore.CredentialMetadata, error)
}

type AdminPoolAccountChannelHealthInitializer interface {
	EnsureDefaultActive(context.Context, channelhealth.ChannelKey) (channelhealth.Record, error)
}

type AdminPoolAccountDeps struct {
	Auth              AdminPoolAccountAuth
	Store             AdminPoolAccountStore
	Credentials       AdminPoolAccountCredentialWriter
	ChannelHealth     AdminPoolAccountChannelHealthInitializer
	RateLimitRecovery AdminPoolAccountRateLimitRecovery
}

type adminPoolAccountStoreAdapter struct {
	base AdminPoolAccountStore
	pool *pgxpool.Pool
}

type adminPoolAccountCreateWithMixedRiskParams = accountcreate.Params
type adminPoolAccountCreateWithMixedRiskResult = accountcreate.Result

func NewAdminPoolAccountStoreAdapter(base AdminPoolAccountStore, pool *pgxpool.Pool) AdminPoolAccountStore {
	return adminPoolAccountStoreAdapter{base: base, pool: pool}
}

func (s adminPoolAccountStoreAdapter) InsertProviderAccount(ctx context.Context, arg admindb.InsertProviderAccountParams) (int64, error) {
	return s.base.InsertProviderAccount(ctx, arg)
}

func (s adminPoolAccountStoreAdapter) GetProviderProtocolForAccountCreate(ctx context.Context, arg admindb.GetProviderProtocolForAccountCreateParams) (string, error) {
	return s.base.GetProviderProtocolForAccountCreate(ctx, arg)
}

func (s adminPoolAccountStoreAdapter) ListAdminProviderAccounts(ctx context.Context, arg admindb.ListAdminProviderAccountsParams) ([]admindb.AdminProviderAccountRow, error) {
	return s.base.ListAdminProviderAccounts(ctx, arg)
}

func (s adminPoolAccountStoreAdapter) GetAdminProviderAccount(ctx context.Context, arg admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error) {
	return s.base.GetAdminProviderAccount(ctx, arg)
}

func (s adminPoolAccountStoreAdapter) UpdateAdminProviderAccount(ctx context.Context, arg admindb.UpdateAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error) {
	return s.base.UpdateAdminProviderAccount(ctx, arg)
}

func (s adminPoolAccountStoreAdapter) UpdateProviderAccountEnabled(ctx context.Context, arg admindb.UpdateProviderAccountEnabledParams) error {
	return s.base.UpdateProviderAccountEnabled(ctx, arg)
}

func (s adminPoolAccountStoreAdapter) SoftDeleteProviderAccount(ctx context.Context, arg admindb.SoftDeleteProviderAccountParams) error {
	return s.base.SoftDeleteProviderAccount(ctx, arg)
}

func (s adminPoolAccountStoreAdapter) InsertAdminAuditEvent(ctx context.Context, arg admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error) {
	return s.base.InsertAdminAuditEvent(ctx, arg)
}

func (s adminPoolAccountStoreAdapter) InsertProviderAccountWithMixedRiskCheck(ctx context.Context, arg adminPoolAccountCreateWithMixedRiskParams) (adminPoolAccountCreateWithMixedRiskResult, error) {
	return accountcreate.Insert(ctx, s.pool, arg)
}

func MountAdminPoolAccountRoutes(r chi.Router, d AdminPoolAccountDeps) {
	r.Get("/", newListProviderAccountsHandler(d))
	r.Post("/", newCreateProviderAccountHandler(d))
	r.Get("/{id}", newGetProviderAccountHandler(d))
	r.Patch("/{id}", newUpdateProviderAccountHandler(d))
	r.Patch("/{id}/enabled", newUpdateProviderAccountEnabledHandler(d))
	r.Post("/{id}/clear-rate-limit", newClearProviderAccountRateLimitHandler(d))
	r.Post("/{id}/recover", newRecoverProviderAccountStateHandler(d))
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
	Confirm         *bool           `json:"confirm,omitempty"`
	Enabled         *bool           `json:"enabled,omitempty"`
	CapConcurrency  *int32          `json:"cap_concurrency,omitempty"`
	Priority        *int32          `json:"priority,omitempty"`
	StaticWeight    *int32          `json:"static_weight,omitempty"`
	ProbeModel      *string         `json:"probe_model,omitempty"`
	Tags            []string        `json:"tags,omitempty"`
	Extra           json.RawMessage `json:"extra,omitempty"`
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
	TenantID        *int64           `json:"tenant_id,omitempty"`
	Enabled         *bool            `json:"enabled,omitempty"`
	Priority        *int32           `json:"priority,omitempty"`
	StaticWeight    *int32           `json:"static_weight,omitempty"`
	CapConcurrency  *int32           `json:"cap_concurrency,omitempty"`
	ProbeModel      *string          `json:"probe_model,omitempty"`
	Tags            *[]string        `json:"tags,omitempty"`
	Extra           *json.RawMessage `json:"extra,omitempty"`
	ModelAllowList  *[]string        `json:"model_allow_list,omitempty"`
	CapabilityFlags *[]string        `json:"capability_flags,omitempty"`
	// 高级字段由 accountadvanced 从原始 body 统一解析校验。
	Reason string `json:"reason,omitempty"`
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
	ID                       int64                     `json:"id"`
	TenantID                 int64                     `json:"tenant_id"`
	ProviderID               int64                     `json:"provider_id"`
	ChannelID                int64                     `json:"channel_id"`
	Name                     string                    `json:"name"`
	AccountType              string                    `json:"account_type"`
	Enabled                  bool                      `json:"enabled"`
	ExpiresAt                *time.Time                `json:"expires_at"`
	HealthState              string                    `json:"health_state"`
	CredentialState          string                    `json:"credential_state"`
	CapConcurrency           int32                     `json:"cap_concurrency"`
	InFlightCount            int32                     `json:"in_flight_count"`
	Priority                 int32                     `json:"priority"`
	StaticWeight             int32                     `json:"static_weight"`
	UpstreamCostRatio        *float64                  `json:"upstream_cost_ratio"`
	ProbeModel               *string                   `json:"probe_model"`
	Tags                     []string                  `json:"tags"`
	Subscription             *accountsubscription.View `json:"subscription,omitempty"`
	SystemLabels             []string                  `json:"system_labels"`
	Extra                    json.RawMessage           `json:"extra"`
	LastDispatchAt           *time.Time                `json:"last_dispatch_at"`
	LastProbeLatencyMS       *int32                    `json:"last_probe_latency_ms"`
	LastProbeAt              *time.Time                `json:"last_probe_at"`
	LastRequestObservedAt    *time.Time                `json:"last_request_observed_at"`
	ObservationSource        string                    `json:"last_request_observation_source"`
	QuotaWindows             quotawindowview.Matrix    `json:"quota_windows"`
	QuotaFacts               []accountquota.ViewFact   `json:"quota_facts"`
	ModelAllowList           []string                  `json:"model_allow_list"`
	CapabilityFlags          []string                  `json:"capability_flags"`
	RateLimitedAt            *time.Time                `json:"rate_limited_at"`
	RateLimitResetAt         *time.Time                `json:"rate_limit_reset_at"`
	RateLimitReason          *string                   `json:"rate_limit_reason"`
	OverloadUntil            *time.Time                `json:"overload_until"`
	TempUnschedulableUntil   *time.Time                `json:"temp_unschedulable_until"`
	TokenVersion             int32                     `json:"token_version"`
	LastRefreshAt            *time.Time                `json:"last_refresh_at"`
	LastRefreshOutcome       *string                   `json:"last_refresh_outcome"`
	OAuthEndpointHealth      string                    `json:"oauth_endpoint_health,omitempty"`
	RPMLimit                 int64                     `json:"rpm_limit"`
	TPMLimit                 int64                     `json:"tpm_limit"`
	WindowCostLimitCents     int64                     `json:"window_cost_limit_cents"`
	MaxSessions              int32                     `json:"max_sessions"`
	DisableCooling           bool                      `json:"disable_cooling"`
	RefreshLeadSeconds       *int32                    `json:"refresh_lead_seconds"`
	TLSFingerprintRotate     bool                      `json:"tls_fingerprint_rotate"`
	CustomErrorCodesEnabled  bool                      `json:"custom_error_codes_enabled"`
	CustomErrorCodes         []int32                   `json:"custom_error_codes"`
	PoolMode                 bool                      `json:"pool_mode"`
	TempUnschedulableEnabled bool                      `json:"temp_unschedulable_enabled"`
	TempUnschedulableRules   json.RawMessage           `json:"temp_unschedulable_rules,omitempty"`
	// ProxyBinding 由两个代理列派生，与 accountadvanced 写入契约同形。
	ProxyBinding      accountadvanced.ProxyBinding              `json:"proxy_binding"`
	CreatedAt         *time.Time                                `json:"created_at"`
	UpdatedAt         *time.Time                                `json:"updated_at"`
	RateLimitRecovery *providerAccountRateLimitRecoveryResponse `json:"rate_limit_recovery,omitempty"`
}

func newCreateProviderAccountHandler(d AdminPoolAccountDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveProviderAccountAdmin(w, r, d)
		if !ok {
			return
		}
		var req createProviderAccountRequest
		rawBody, ok := decodeAdminPoolJSONWithRaw(w, r, &req)
		if !ok {
			return
		}
		advanced, ok := parseProviderAccountAdvanced(w, rawBody)
		if !ok {
			return
		}
		if !validateProviderAccountTenant(w, req.TenantID, tenantID) {
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		req.AccountType = strings.TrimSpace(req.AccountType)
		req.Vendor = credentialstore.Normalize(req.Vendor)
		req.AuthMode = credentialstore.Normalize(req.AuthMode)
		req.ProbeModel = cleanOptionalString(req.ProbeModel)
		req.Tags = cleanStringList(req.Tags)
		req.ModelAllowList = cleanStringList(req.ModelAllowList)
		req.CapabilityFlags = cleanStringList(req.CapabilityFlags)
		if err := validateCreateProviderAccount(req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "admin_bad_request", err.Error())
			return
		}
		if d.Credentials == nil {
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
		createArg := admindb.InsertProviderAccountParams{
			TenantID: tenantID, ProviderID: req.ProviderID, ChannelID: req.ChannelID,
			Name: req.Name, AccountType: req.AccountType, Enabled: req.Enabled,
			Credentials: []byte(`{}`), CapConcurrency: req.CapConcurrency, Priority: req.Priority,
			StaticWeight: req.StaticWeight, ProbeModel: req.ProbeModel, Tags: req.Tags,
			Extra:          normalizedProviderAccountExtra(req.Extra),
			ModelAllowList: req.ModelAllowList, CapabilityFlags: req.CapabilityFlags, ActorID: &actorID,
		}
		accountadvanced.ApplyCreate(advanced, &createArg)
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
		created, err := d.Credentials.Create(r.Context(), credentialstore.CreateCredentialInput{
			TenantID: tenantID, ProviderAccountID: id,
			Vendor: req.Vendor, AuthMode: req.AuthMode,
			Payload: req.Credentials, ActorID: actorID,
		})
		if err != nil {
			// 凭据创建失败时软删新账号，避免无可服务凭据的孤儿进入选号池。
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
		response, err := providerAccountDTO(account)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "provider_account_quota_projection_invalid", "provider account quota projection is invalid")
			return
		}
		writeAuditJSON(w, http.StatusCreated, response)
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
		tagFilter := parseProviderAccountTagFilter(r)
		subscriptionFilters, parseErr := accountsubscription.Parse(r.URL.Query())
		if parseErr != nil {
			writeJSONError(w, http.StatusBadRequest, parseErr.Code, parseErr.Message)
			return
		}
		rows, err := d.Store.ListAdminProviderAccounts(r.Context(), admindb.ListAdminProviderAccountsParams{
			TenantID: tenantID, AfterID: afterID, LimitCount: limit + 1,
			PoolGroupID: poolGroupID, StateFilter: stateFilter, TagFilter: tagFilter,
			SubscriptionVendorFilter: subscriptionFilters.Vendor,
			SubscriptionPlanFilter:   subscriptionFilters.Plan,
			SubscriptionScopeFilter:  subscriptionFilters.Scope,
			SubscriptionStatusFilter: subscriptionFilters.Status,
			SubscriptionSourceFilter: subscriptionFilters.Source,
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
		observedAt := time.Now().UTC()
		for _, row := range rows {
			item, err := providerAccountDTOAt(row, observedAt)
			if err != nil {
				writeJSONError(w, http.StatusServiceUnavailable, "provider_account_quota_projection_invalid", "provider account quota projection is invalid")
				return
			}
			items = append(items, item)
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
		response, err := providerAccountDetailDTO(account)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "provider_account_quota_projection_invalid", "provider account quota projection is invalid")
			return
		}
		writeAuditJSON(w, http.StatusOK, response)
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
		rawBody, ok := decodeAdminPoolJSONWithRaw(w, r, &req)
		if !ok {
			return
		}
		advanced, ok := parseProviderAccountAdvanced(w, rawBody)
		if !ok {
			return
		}
		if !validateProviderAccountTenant(w, req.TenantID, tenantID) {
			return
		}
		if err := validateUpdateProviderAccount(req, advanced.Any()); err != nil {
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
			arg.ProbeModel = cleanOptionalString(req.ProbeModel)
		}
		if req.Tags != nil {
			arg.SetTags = true
			arg.Tags = cleanStringList(*req.Tags)
		}
		if req.Extra != nil {
			arg.SetExtra = true
			arg.Extra = normalizedProviderAccountExtra(*req.Extra)
		}
		if req.ModelAllowList != nil {
			arg.SetModelAllowList = true
			arg.ModelAllowList = cleanStringList(*req.ModelAllowList)
		}
		if req.CapabilityFlags != nil {
			arg.SetCapabilityFlags = true
			arg.CapabilityFlags = cleanStringList(*req.CapabilityFlags)
		}
		// 高级字段与创建路径共用 accountadvanced 写入契约。
		accountadvanced.ApplyUpdate(advanced, &arg)
		_, err := d.Store.UpdateAdminProviderAccount(r.Context(), arg)
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
		account, err := d.Store.GetAdminProviderAccount(r.Context(), admindb.GetAdminProviderAccountParams{ID: id, TenantID: tenantID})
		if err != nil {
			writeProviderAccountReadError(w, err, "provider_account_get_failed")
			return
		}
		response, err := providerAccountDTO(account)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "provider_account_quota_projection_invalid", "provider account quota projection is invalid")
			return
		}
		writeAuditJSON(w, http.StatusOK, response)
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
		// 全局 platform_admin 没有隐式租户作用域，必须用 ?tenant_id=N 显式指定。
		if ident.ScopeTenantID > 0 {
			return ident, ident.ScopeTenantID, true
		}
		tenantID, okTenant := parsePositiveQueryInt(w, r, "tenant_id")
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

// decodeAdminPoolJSONWithRaw 同时返回原始 body，供 accountadvanced 解析高级字段。
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

// parseProviderAccountAdvanced 从原始 body 解析并校验高级字段;校验失败统一回 400。
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
	handler, err := credentialstore.DefaultHandlerRegistry().MustLookup(req.Vendor, req.AuthMode)
	if err != nil {
		return err
	}
	if err := handler.ValidatePayload(req.Credentials); err != nil {
		return err
	}
	return nil
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

// validateUpdateProviderAccount 校验基础字段；高级字段已由 accountadvanced 校验。
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

func providerAccountDTO(row admindb.AdminProviderAccountRow) (providerAccountResponse, error) {
	return providerAccountDTOAt(row, time.Now().UTC())
}

func providerAccountDTOAt(row admindb.AdminProviderAccountRow, now time.Time) (providerAccountResponse, error) {
	subscription, systemLabels := accountsubscription.Build(row)
	quotaFacts, err := accountquota.ParseView(row.QuotaFacts, now)
	if err != nil {
		return providerAccountResponse{}, err
	}
	return providerAccountResponse{
		ID: row.ID, TenantID: row.TenantID, ProviderID: row.ProviderID, ChannelID: row.ChannelID,
		Name: row.Name, AccountType: row.AccountType, Enabled: row.Enabled, ExpiresAt: pgTimePtr(row.ExpiresAt),
		HealthState: row.HealthState, CredentialState: row.CredentialState,
		CapConcurrency: row.CapConcurrency, InFlightCount: row.InFlightCount, Priority: row.Priority,
		StaticWeight: row.StaticWeight, UpstreamCostRatio: row.UpstreamCostRatio,
		ProbeModel: row.ProbeModel, Tags: nonNilStringSlice(row.Tags),
		Subscription: subscription, SystemLabels: systemLabels,
		Extra:          jsonObjectOrEmpty(row.Extra),
		LastDispatchAt: pgTimePtr(row.LastDispatchAt), LastProbeLatencyMS: row.LastProbeLatencyMS,
		LastProbeAt:           pgTimePtr(row.LastProbeAt),
		LastRequestObservedAt: pgTimePtr(row.LastRequestObservedAt),
		ObservationSource:     "request_completion_event",
		QuotaWindows: quotawindowview.FromPostgres(quotawindowview.PostgresSnapshot{
			ObservedAt: row.QuotaSnapshotObservedAt, Source: row.QuotaSnapshotSource,
			Outcome: row.QuotaSnapshotOutcome, ErrorClass: row.QuotaSnapshotErrorClass,
			FiveHourStart: row.SessionWindow5hStart, FiveHourEnd: row.SessionWindow5hEnd,
			FiveHourUtilization: row.SessionWindow5hUtilization,
			SevenDayStart:       row.SessionWindow7dStart, SevenDayEnd: row.SessionWindow7dEnd,
			SevenDayUtilization: row.SessionWindow7dUtilization,
		}, now),
		QuotaFacts:      quotaFacts,
		ModelAllowList:  nonNilStringSlice(row.ModelAllowList),
		CapabilityFlags: nonNilStringSlice(row.CapabilityFlags), RateLimitedAt: pgTimePtr(row.RateLimitedAt),
		RateLimitResetAt: pgTimePtr(row.RateLimitResetAt), RateLimitReason: row.RateLimitReason,
		OverloadUntil: pgTimePtr(row.OverloadUntil), TempUnschedulableUntil: pgTimePtr(row.TempUnschedulableUntil),
		TokenVersion: row.TokenVersion, LastRefreshAt: pgTimePtr(row.LastRefreshAt),
		LastRefreshOutcome: row.LastRefreshOutcome, OAuthEndpointHealth: row.OAuthEndpointHealth,
		RPMLimit: row.RPMLimit, TPMLimit: row.TPMLimit, WindowCostLimitCents: row.WindowCostLimitCents,
		MaxSessions: row.MaxSessions, DisableCooling: row.DisableCooling, RefreshLeadSeconds: row.RefreshLeadSeconds,
		TLSFingerprintRotate:    row.TLSFingerprintRotate,
		CustomErrorCodesEnabled: row.CustomErrorCodesEnabled, CustomErrorCodes: nonNilInt32Slice(row.CustomErrorCodes),
		PoolMode: row.PoolMode, TempUnschedulableEnabled: row.TempUnschedulableEnabled,
		TempUnschedulableRules: detailRulesOrNil(row.TempUnschedulableRules),
		ProxyBinding:           accountadvanced.BindingFromColumns(row.ProxyID, row.ProxyGroupID),
		CreatedAt:              pgTimePtr(row.CreatedAt), UpdatedAt: pgTimePtr(row.UpdatedAt),
	}, nil
}

func providerAccountDetailDTO(row admindb.AdminProviderAccountRow) (providerAccountResponse, error) {
	response, err := providerAccountDTO(row)
	if err != nil {
		return providerAccountResponse{}, err
	}
	response.TempUnschedulableRules = jsonArrayOrEmpty(row.TempUnschedulableRules)
	return response, nil
}

// detailRulesOrNil 把空规则置 nil，让列表摘要省略详情字段。
func detailRulesOrNil(raw []byte) json.RawMessage {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "[]" || s == "null" {
		return nil
	}
	return json.RawMessage(raw)
}

func jsonArrayOrEmpty(raw []byte) json.RawMessage {
	var values []json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &values) != nil || values == nil {
		return json.RawMessage(`[]`)
	}
	return json.RawMessage(append([]byte(nil), raw...))
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

func cleanOptionalString(in *string) *string {
	if in == nil {
		return nil
	}
	out := strings.TrimSpace(*in)
	return &out
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

func jsonRawObject(raw json.RawMessage) bool {
	var obj map[string]json.RawMessage
	return len(raw) > 0 && json.Unmarshal(raw, &obj) == nil && obj != nil
}

func normalizedProviderAccountExtra(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return nil
	}
	return []byte(raw)
}

func jsonObjectOrEmpty(raw []byte) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(raw)
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
	actorID := ident.AuditActor()
	reqID := middleware.GetReqID(r.Context())
	_, err := store.InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
		TenantID: &tenantID, ActorID: actorID, ActorRole: ident.Role,
		Action: action, TargetType: "provider_account", TargetID: &targetID,
		RequestID: &reqID, Reason: reason, Payload: payload,
	})
	return err
}
