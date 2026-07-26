package adminpoolhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/accountquota"
	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauth"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountadvanced"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountcreate"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountsubscription"
	"github.com/BloomingProsperity/HUAKAI/internal/mixedchannelrisk"
	"github.com/BloomingProsperity/HUAKAI/internal/quotawindowview"
	"github.com/BloomingProsperity/HUAKAI/internal/tenantcapability"
)

const (
	defaultAdminProviderAccountLimit = int32(50)
	maxAdminProviderAccountLimit     = int32(200)
	providerAccountCursorPrefix      = "provider_account_id:"
)

var (
	errProviderAccountMixedRiskConfirmationRequired = accountcreate.ErrMixedRiskConfirmRequired
	errProviderAccountProtocolIncompatible          = accountcreate.ErrProtocolIncompatible
)

type AdminPoolAccountAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type AdminPoolAccountDataStore interface {
	GetProviderProtocolForAccountCreate(context.Context, admindb.GetProviderProtocolForAccountCreateParams) (string, error)
	InsertProviderAccount(context.Context, admindb.InsertProviderAccountParams) (int64, error)
	ListAdminProviderAccounts(context.Context, admindb.ListAdminProviderAccountsParams) ([]admindb.AdminProviderAccountRow, error)
	GetAdminProviderAccount(context.Context, admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error)
	InsertAdminAuditEvent(context.Context, admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error)
}

type AdminPoolAccountStore interface {
	AdminPoolAccountDataStore
	UpdateAdminProviderAccountWithAudit(
		context.Context,
		admindb.UpdateAdminProviderAccountParams,
		admindb.InsertAdminAuditEventParams,
	) (admindb.AdminProviderAccountRow, error)
	UpdateProviderAccountEnabledWithAudit(
		context.Context,
		admindb.UpdateProviderAccountEnabledParams,
		admindb.InsertAdminAuditEventParams,
	) error
	SoftDeleteProviderAccountWithAudit(
		context.Context,
		admindb.SoftDeleteProviderAccountParams,
		admindb.InsertAdminAuditEventParams,
	) error
}

type AdminPoolAccountRiskStore interface {
	ListProviderAccountRiskPeers(context.Context, admindb.ListProviderAccountRiskPeersParams) ([]admindb.ProviderAccountRiskPeerRow, error)
}

type AdminPoolAccountCredentialWriter interface {
	Create(context.Context, credentialstore.CreateCredentialInput) (credentialstore.CredentialMetadata, error)
	// WithTransaction 让建号把账号插入、凭据写入、管理审计合入单事务，任一失败整体回滚。
	WithTransaction(context.Context, func(*credentialstore.Store, db.DBTX) error) error
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
	Capabilities      interface {
		Allowed(context.Context, int64, string) (bool, error)
	}
	PlatformTenantID int64
}

func MountAdminPoolAccountRoutes(r chi.Router, d AdminPoolAccountDeps) {
	safe := adminsessionauth.AllowSessionWrite(adminsessionauth.SessionSafe)
	r.Get("/", newListProviderAccountsHandler(d))
	r.With(safe).Post("/", newCreateProviderAccountHandler(d))
	r.Get("/{id}", newGetProviderAccountHandler(d))
	r.With(safe).Patch("/{id}", newUpdateProviderAccountHandler(d))
	r.With(safe).Patch("/{id}/enabled", newUpdateProviderAccountEnabledHandler(d))
	r.With(safe).Post("/{id}/clear-rate-limit", newClearProviderAccountRateLimitHandler(d))
	r.With(safe).Post("/{id}/recover", newRecoverProviderAccountStateHandler(d))
	r.With(safe).Delete("/{id}", newDeleteProviderAccountHandler(d))
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

type providerAccountTodayStats struct {
	WindowStart        time.Time `json:"window_start"`
	ObservedAt         time.Time `json:"observed_at"`
	RequestCount       int64     `json:"request_count"`
	SuccessCount       int64     `json:"success_count"`
	FailureCount       int64     `json:"failure_count"`
	FailureRatePercent float64   `json:"failure_rate_percent"`
	TTFTP95MS          *float64  `json:"ttft_p95_ms"`
}

type providerAccountResponse struct {
	ID                       int64                      `json:"id"`
	TenantID                 int64                      `json:"tenant_id"`
	ProviderID               int64                      `json:"provider_id"`
	ChannelID                int64                      `json:"channel_id"`
	Name                     string                     `json:"name"`
	AccountType              string                     `json:"account_type"`
	Enabled                  bool                       `json:"enabled"`
	ExpiresAt                *time.Time                 `json:"expires_at"`
	HealthState              string                     `json:"health_state"`
	CredentialState          string                     `json:"credential_state"`
	CapConcurrency           int32                      `json:"cap_concurrency"`
	InFlightCount            int32                      `json:"in_flight_count"`
	Priority                 int32                      `json:"priority"`
	StaticWeight             int32                      `json:"static_weight"`
	UpstreamCostRatio        *float64                   `json:"upstream_cost_ratio"`
	ProbeModel               *string                    `json:"probe_model"`
	Tags                     []string                   `json:"tags"`
	Subscription             *accountsubscription.View  `json:"subscription,omitempty"`
	SystemLabels             []string                   `json:"system_labels"`
	Extra                    json.RawMessage            `json:"extra"`
	LastDispatchAt           *time.Time                 `json:"last_dispatch_at"`
	LastProbeLatencyMS       *int32                     `json:"last_probe_latency_ms"`
	LastProbeAt              *time.Time                 `json:"last_probe_at"`
	LastRequestObservedAt    *time.Time                 `json:"last_request_observed_at"`
	ObservationSource        string                     `json:"last_request_observation_source"`
	TodayStats               *providerAccountTodayStats `json:"today_stats,omitempty"`
	QuotaWindows             quotawindowview.Matrix     `json:"quota_windows"`
	QuotaFacts               []accountquota.ViewFact    `json:"quota_facts"`
	ModelAllowList           []string                   `json:"model_allow_list"`
	CapabilityFlags          []string                   `json:"capability_flags"`
	RateLimitedAt            *time.Time                 `json:"rate_limited_at"`
	RateLimitResetAt         *time.Time                 `json:"rate_limit_reset_at"`
	RateLimitReason          *string                    `json:"rate_limit_reason"`
	OverloadUntil            *time.Time                 `json:"overload_until"`
	TempUnschedulableUntil   *time.Time                 `json:"temp_unschedulable_until"`
	TokenVersion             int32                      `json:"token_version"`
	LastRefreshAt            *time.Time                 `json:"last_refresh_at"`
	LastRefreshOutcome       *string                    `json:"last_refresh_outcome"`
	OAuthEndpointHealth      string                     `json:"oauth_endpoint_health,omitempty"`
	RPMLimit                 int64                      `json:"rpm_limit"`
	TPMLimit                 int64                      `json:"tpm_limit"`
	WindowCostLimitCents     int64                      `json:"window_cost_limit_cents"`
	MaxSessions              int32                      `json:"max_sessions"`
	DisableCooling           bool                       `json:"disable_cooling"`
	RefreshLeadSeconds       *int32                     `json:"refresh_lead_seconds"`
	TLSFingerprintRotate     bool                       `json:"tls_fingerprint_rotate"`
	CustomErrorCodesEnabled  bool                       `json:"custom_error_codes_enabled"`
	CustomErrorCodes         []int32                    `json:"custom_error_codes"`
	PoolMode                 bool                       `json:"pool_mode"`
	TempUnschedulableEnabled bool                       `json:"temp_unschedulable_enabled"`
	TempUnschedulableRules   json.RawMessage            `json:"temp_unschedulable_rules,omitempty"`
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
		if !authorizeProviderAccountCreate(w, r, d, ident, tenantID) {
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
		candidate := mixedchannelrisk.Account{
			ProviderID: req.ProviderID, ChannelID: req.ChannelID,
			AccountType: req.AccountType, Vendor: req.Vendor, AuthMode: req.AuthMode,
		}
		var (
			id         int64
			created    credentialstore.CredentialMetadata
			riskReport mixedchannelrisk.Report
			failCode   string
		)
		// 账号插入、凭据写入、管理审计合入单事务：任一失败整体回滚，杜绝审计失败后残留
		// 悬空账号占用唯一名，导致同名重试撞 uq_provider_accounts_tenant_name。
		txErr := d.Credentials.WithTransaction(r.Context(), func(txStore *credentialstore.Store, database db.DBTX) error {
			tx, ok := database.(pgx.Tx)
			if !ok {
				return errors.New("provider account create transaction is not pgx.Tx")
			}
			insertResult, err := accountcreate.InsertTx(r.Context(), tx, accountcreate.Params{
				Insert: createArg, Candidate: candidate, ProviderFamily: providerFamily, Confirmed: confirmed,
			})
			// 缺确认失败时也要带出风险报告，供 mixed-risk 确认响应复用。
			riskReport = insertResult.RiskReport
			if err != nil {
				failCode = "provider_account_insert_failed"
				return err
			}
			id = insertResult.ID
			created, err = txStore.Create(r.Context(), credentialstore.CreateCredentialInput{
				TenantID: tenantID, ProviderAccountID: id,
				Vendor: req.Vendor, AuthMode: req.AuthMode,
				Payload: req.Credentials, ActorID: actorID,
			})
			if err != nil {
				failCode = "account_credential_insert_failed"
				return err
			}
			healthService := channelhealth.NewService(
				channelhealth.NewPostgresStore(tx), channelhealth.DefaultPolicy(), nil,
			)
			if _, err := healthService.EnsureDefaultActive(r.Context(), channelhealth.ChannelKey{
				TenantID: tenantID, Vendor: created.Vendor, ProviderAccountID: id,
				AccountCredentialID: created.ID, CredentialVersion: int(created.Version),
			}); err != nil {
				failCode = "channel_health_initialize_failed"
				return err
			}
			if err := writeProviderAccountAuditTx(r.Context(), tx, r, ident, tenantID,
				"create_provider_account", id, chineseReason(req.Reason, "创建 provider account"),
				providerAccountCreateAuditPayload(tenantID, req, created, riskReport)); err != nil {
				failCode = "audit_write_failed"
				return err
			}
			return nil
		})
		if txErr != nil {
			switch {
			case errors.Is(txErr, errProviderAccountProtocolIncompatible):
				writeJSONError(w, http.StatusBadRequest, "admin_bad_request", txErr.Error())
			case errors.Is(txErr, errProviderAccountMixedRiskConfirmationRequired):
				writeProviderAccountMixedRiskRequired(w, riskReport)
			default:
				if failCode == "" {
					failCode = "provider_account_insert_failed"
				}
				writeJSONError(w, http.StatusServiceUnavailable, failCode, txErr.Error())
			}
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
		writeAuditJSON(w, http.StatusCreated, response)
	}
}

func authorizeProviderAccountCreate(w http.ResponseWriter, r *http.Request, d AdminPoolAccountDeps, ident admin.AdminIdentity, tenantID int64) bool {
	switch ident.Role {
	case admin.RolePlatformAdmin:
		if d.PlatformTenantID <= 0 {
			writeJSONError(w, http.StatusServiceUnavailable, "platform_tenant_not_configured", "platform tenant scope is not configured")
			return false
		}
		if tenantID != d.PlatformTenantID {
			writeJSONError(w, http.StatusForbidden, "cross_tenant_account_admin_forbidden", "platform_admin can only create accounts for the platform tenant")
			return false
		}
		return true
	case admin.RoleTenantOperator:
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
		return true
	default:
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "admin role required")
		return false
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
		observedAt := time.Now().UTC()
		statsSince := time.Date(observedAt.Year(), observedAt.Month(), observedAt.Day(), 0, 0, 0, 0, time.UTC)
		rows, err := d.Store.ListAdminProviderAccounts(r.Context(), admindb.ListAdminProviderAccountsParams{
			TenantID: tenantID, AfterID: afterID, LimitCount: limit + 1,
			PoolGroupID: poolGroupID, StateFilter: stateFilter, TagFilter: tagFilter,
			SubscriptionVendorFilter: subscriptionFilters.Vendor,
			SubscriptionPlanFilter:   subscriptionFilters.Plan,
			SubscriptionScopeFilter:  subscriptionFilters.Scope,
			SubscriptionStatusFilter: subscriptionFilters.Status,
			SubscriptionSourceFilter: subscriptionFilters.Source,
			StatsSince:               statsSince,
			StatsUntil:               observedAt,
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
		payload, _ := json.Marshal(map[string]any{"tenant_id": tenantID, "updated": true})
		audit := providerAccountAuditParams(r, ident, tenantID,
			"update_provider_account", id, chineseReason(req.Reason, "更新 provider account"), payload)
		account, err := d.Store.UpdateAdminProviderAccountWithAudit(r.Context(), arg, audit)
		if err != nil {
			writeProviderAccountReadError(w, err, "provider_account_update_failed")
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
