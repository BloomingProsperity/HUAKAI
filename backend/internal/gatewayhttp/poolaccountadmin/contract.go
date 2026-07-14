// Package poolaccountadmin 承载管理端 provider account 的请求、回显与存储适配契约。
package poolaccountadmin

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

	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountadvanced"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountcreate"
	"github.com/BloomingProsperity/HUAKAI/internal/mixedchannelrisk"
)

const (
	defaultLimit = int32(50)
	maxLimit     = int32(200)
	cursorPrefix = "provider_account_id:"
)

// Auth 是账号管理路由需要的最小管理员认证能力。
type Auth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

// Store 是 provider account 管理路由的持久化契约。
type Store interface {
	GetProviderProtocolForAccountCreate(context.Context, admindb.GetProviderProtocolForAccountCreateParams) (string, error)
	InsertProviderAccount(context.Context, admindb.InsertProviderAccountParams) (int64, error)
	ListAdminProviderAccounts(context.Context, admindb.ListAdminProviderAccountsParams) ([]admindb.AdminProviderAccountRow, error)
	GetAdminProviderAccount(context.Context, admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error)
	UpdateAdminProviderAccount(context.Context, admindb.UpdateAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error)
	UpdateProviderAccountEnabled(context.Context, admindb.UpdateProviderAccountEnabledParams) error
	ClearProviderAccountRateLimit(context.Context, admindb.ClearProviderAccountRateLimitParams) (admindb.AdminProviderAccountRow, error)
	SoftDeleteProviderAccount(context.Context, admindb.SoftDeleteProviderAccountParams) error
	InsertAdminAuditEvent(context.Context, admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error)
}

// RiskStore 是渠道混用风险检查需要的最小读能力。
type RiskStore interface {
	ListProviderAccountRiskPeers(context.Context, admindb.ListProviderAccountRiskPeersParams) ([]admindb.ProviderAccountRiskPeerRow, error)
}

// AtomicCreateStore 保证风险检查与建号在同一事务中完成。
type AtomicCreateStore interface {
	InsertProviderAccountWithMixedRiskCheck(context.Context, accountcreate.Params) (accountcreate.Result, error)
}

// CredentialWriter 是新建账号后写入加密凭据的最小能力。
type CredentialWriter interface {
	Create(context.Context, credentialstore.CreateCredentialInput) (credentialstore.CredentialMetadata, error)
}

// ChannelHealthInitializer 负责初始化新凭据的渠道健康状态。
type ChannelHealthInitializer interface {
	EnsureDefaultActive(context.Context, channelhealth.ChannelKey) (channelhealth.Record, error)
}

// Deps 集中 provider account 管理路由依赖。
type Deps struct {
	Auth          Auth
	Store         Store
	Credentials   CredentialWriter
	ChannelHealth ChannelHealthInitializer
}

type storeAdapter struct {
	base Store
	pool *pgxpool.Pool
}

// NewStoreAdapter 为基础 store 补上事务型建号能力。
func NewStoreAdapter(base Store, pool *pgxpool.Pool) Store {
	return storeAdapter{base: base, pool: pool}
}

func (s storeAdapter) InsertProviderAccount(ctx context.Context, arg admindb.InsertProviderAccountParams) (int64, error) {
	return s.base.InsertProviderAccount(ctx, arg)
}

func (s storeAdapter) GetProviderProtocolForAccountCreate(ctx context.Context, arg admindb.GetProviderProtocolForAccountCreateParams) (string, error) {
	return s.base.GetProviderProtocolForAccountCreate(ctx, arg)
}

func (s storeAdapter) ListAdminProviderAccounts(ctx context.Context, arg admindb.ListAdminProviderAccountsParams) ([]admindb.AdminProviderAccountRow, error) {
	return s.base.ListAdminProviderAccounts(ctx, arg)
}

func (s storeAdapter) GetAdminProviderAccount(ctx context.Context, arg admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error) {
	return s.base.GetAdminProviderAccount(ctx, arg)
}

func (s storeAdapter) UpdateAdminProviderAccount(ctx context.Context, arg admindb.UpdateAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error) {
	return s.base.UpdateAdminProviderAccount(ctx, arg)
}

func (s storeAdapter) UpdateProviderAccountEnabled(ctx context.Context, arg admindb.UpdateProviderAccountEnabledParams) error {
	return s.base.UpdateProviderAccountEnabled(ctx, arg)
}

func (s storeAdapter) ClearProviderAccountRateLimit(ctx context.Context, arg admindb.ClearProviderAccountRateLimitParams) (admindb.AdminProviderAccountRow, error) {
	return s.base.ClearProviderAccountRateLimit(ctx, arg)
}

func (s storeAdapter) SoftDeleteProviderAccount(ctx context.Context, arg admindb.SoftDeleteProviderAccountParams) error {
	return s.base.SoftDeleteProviderAccount(ctx, arg)
}

func (s storeAdapter) InsertAdminAuditEvent(ctx context.Context, arg admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error) {
	return s.base.InsertAdminAuditEvent(ctx, arg)
}

func (s storeAdapter) InsertProviderAccountWithMixedRiskCheck(ctx context.Context, arg accountcreate.Params) (accountcreate.Result, error) {
	return accountcreate.Insert(ctx, s.pool, arg)
}

// CreateRequest 是建号端点请求；Advanced 与 UpdateRequest 共用同一契约。
type CreateRequest struct {
	TenantID        *int64                   `json:"tenant_id,omitempty"`
	ProviderID      int64                    `json:"provider_id"`
	ChannelID       int64                    `json:"channel_id"`
	Name            string                   `json:"name"`
	AccountType     string                   `json:"account_type"`
	Vendor          string                   `json:"vendor,omitempty"`
	AuthMode        string                   `json:"auth_mode,omitempty"`
	Confirm         *bool                    `json:"confirm,omitempty"`
	Enabled         *bool                    `json:"enabled,omitempty"`
	CapConcurrency  *int32                   `json:"cap_concurrency,omitempty"`
	Priority        *int32                   `json:"priority,omitempty"`
	StaticWeight    *int32                   `json:"static_weight,omitempty"`
	ProbeModel      *string                  `json:"probe_model,omitempty"`
	Tags            []string                 `json:"tags,omitempty"`
	Extra           json.RawMessage          `json:"extra,omitempty"`
	ModelAllowList  []string                 `json:"model_allow_list,omitempty"`
	CapabilityFlags []string                 `json:"capability_flags,omitempty"`
	Credentials     json.RawMessage          `json:"credentials"`
	Reason          string                   `json:"reason,omitempty"`
	Advanced        accountadvanced.Mutation `json:"-"`
}

// MutateRequest 是启停和删除端点共用的小请求。
type MutateRequest struct {
	TenantID *int64 `json:"tenant_id,omitempty"`
	Enabled  *bool  `json:"enabled,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// UpdateRequest 保留所有字段的缺席与显式空值语义。
type UpdateRequest struct {
	TenantID        *int64                   `json:"tenant_id,omitempty"`
	Enabled         *bool                    `json:"enabled,omitempty"`
	Priority        *int32                   `json:"priority,omitempty"`
	StaticWeight    *int32                   `json:"static_weight,omitempty"`
	CapConcurrency  *int32                   `json:"cap_concurrency,omitempty"`
	ProbeModel      *string                  `json:"probe_model,omitempty"`
	Tags            *[]string                `json:"tags,omitempty"`
	Extra           *json.RawMessage         `json:"extra,omitempty"`
	ModelAllowList  *[]string                `json:"model_allow_list,omitempty"`
	CapabilityFlags *[]string                `json:"capability_flags,omitempty"`
	Reason          string                   `json:"reason,omitempty"`
	Advanced        accountadvanced.Mutation `json:"-"`
}

// SetProviderAccountAdvanced 接收统一解析后的高级字段。
func (r *CreateRequest) SetProviderAccountAdvanced(value accountadvanced.Mutation) {
	r.Advanced = value
}

// SetProviderAccountAdvanced 接收统一解析后的高级字段。
func (r *UpdateRequest) SetProviderAccountAdvanced(value accountadvanced.Mutation) {
	r.Advanced = value
}

// ListResponse 是带游标页信息的账号列表。
type ListResponse struct {
	Items []Response `json:"items"`
	Page  Page       `json:"page"`
}

// Page 描述 provider account 列表的游标状态。
type Page struct {
	Cursor     *string `json:"cursor"`
	NextCursor *string `json:"next_cursor"`
	HasMore    bool    `json:"has_more"`
}

// Response 是 list/get/create/update 共用的完整账号回显。
type Response struct {
	ID                       int64                        `json:"id"`
	TenantID                 int64                        `json:"tenant_id"`
	ProviderID               int64                        `json:"provider_id"`
	ChannelID                int64                        `json:"channel_id"`
	Name                     string                       `json:"name"`
	AccountType              string                       `json:"account_type"`
	Enabled                  bool                         `json:"enabled"`
	ExpiresAt                *time.Time                   `json:"expires_at"`
	RPMLimit                 int64                        `json:"rpm_limit"`
	TPMLimit                 int64                        `json:"tpm_limit"`
	WindowCostLimitCents     int64                        `json:"window_cost_limit_cents"`
	MaxSessions              int32                        `json:"max_sessions"`
	DisableCooling           bool                         `json:"disable_cooling"`
	RefreshLeadSeconds       *int32                       `json:"refresh_lead_seconds"`
	TLSFingerprintRotate     bool                         `json:"tls_fingerprint_rotate"`
	HealthState              string                       `json:"health_state"`
	CredentialState          string                       `json:"credential_state"`
	CapConcurrency           int32                        `json:"cap_concurrency"`
	InFlightCount            int32                        `json:"in_flight_count"`
	Priority                 int32                        `json:"priority"`
	StaticWeight             int32                        `json:"static_weight"`
	ProbeModel               *string                      `json:"probe_model"`
	Tags                     []string                     `json:"tags"`
	Extra                    json.RawMessage              `json:"extra"`
	LastDispatchAt           *time.Time                   `json:"last_dispatch_at"`
	LastProbeLatencyMS       *int32                       `json:"last_probe_latency_ms"`
	LastProbeAt              *time.Time                   `json:"last_probe_at"`
	ModelAllowList           []string                     `json:"model_allow_list"`
	CapabilityFlags          []string                     `json:"capability_flags"`
	RateLimitedAt            *time.Time                   `json:"rate_limited_at"`
	RateLimitResetAt         *time.Time                   `json:"rate_limit_reset_at"`
	RateLimitReason          *string                      `json:"rate_limit_reason"`
	OverloadUntil            *time.Time                   `json:"overload_until"`
	TempUnschedulableUntil   *time.Time                   `json:"temp_unschedulable_until"`
	TokenVersion             int32                        `json:"token_version"`
	LastRefreshAt            *time.Time                   `json:"last_refresh_at"`
	LastRefreshOutcome       *string                      `json:"last_refresh_outcome"`
	OAuthEndpointHealth      string                       `json:"oauth_endpoint_health,omitempty"`
	CustomErrorCodesEnabled  bool                         `json:"custom_error_codes_enabled"`
	CustomErrorCodes         []int32                      `json:"custom_error_codes"`
	PoolMode                 bool                         `json:"pool_mode"`
	TempUnschedulableEnabled bool                         `json:"temp_unschedulable_enabled"`
	TempUnschedulableRules   json.RawMessage              `json:"temp_unschedulable_rules,omitempty"`
	ProxyID                  *int64                       `json:"proxy_id"`
	ProxyGroupID             *string                      `json:"proxy_group_id"`
	ProxyBinding             accountadvanced.ProxyBinding `json:"proxy_binding"`
	CreatedAt                *time.Time                   `json:"created_at"`
	UpdatedAt                *time.Time                   `json:"updated_at"`
}

// ValidateCreate 校验建号的基础字段与凭据契约。
func ValidateCreate(req CreateRequest, requireCredentialV2 bool) error {
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
	if len(req.Extra) > 0 && !JSONRawObject(req.Extra) {
		return fmt.Errorf("extra must be a JSON object")
	}
	var object map[string]json.RawMessage
	if len(req.Credentials) == 0 || json.Unmarshal(req.Credentials, &object) != nil || object == nil {
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

// ValidateUpdate 要求至少有一个可写字段，并校验基础字段。
func ValidateUpdate(req UpdateRequest) error {
	if req.Enabled == nil && req.Priority == nil && req.StaticWeight == nil && req.CapConcurrency == nil &&
		req.ProbeModel == nil && req.Tags == nil && req.Extra == nil && req.ModelAllowList == nil &&
		req.CapabilityFlags == nil && !req.Advanced.Any() {
		return fmt.Errorf("at least one supported field is required")
	}
	if req.CapConcurrency != nil && *req.CapConcurrency <= 0 {
		return fmt.Errorf("cap_concurrency must be positive")
	}
	if req.StaticWeight != nil && *req.StaticWeight <= 0 {
		return fmt.Errorf("static_weight must be positive")
	}
	if req.Extra != nil && !JSONRawObject(*req.Extra) {
		return fmt.Errorf("extra must be a JSON object")
	}
	return nil
}

// RequestError 是子包返回给 HTTP 边界的结构化错误。
type RequestError struct {
	Status  int
	Code    string
	Message string
}

// ResolveTenant 解析账号管理请求的管理员与显式租户作用域。
func ResolveTenant(r *http.Request, deps Deps) (admin.AdminIdentity, int64, *RequestError) {
	if deps.Auth == nil || deps.Store == nil {
		return admin.AdminIdentity{}, 0, &RequestError{http.StatusServiceUnavailable, "gateway_not_configured", "admin pool account dependency unset"}
	}
	identity, err := deps.Auth.Resolve(r.Context(), r)
	if err != nil {
		if errors.Is(err, admin.ErrAdminBackend) {
			return admin.AdminIdentity{}, 0, &RequestError{http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure"}
		}
		return admin.AdminIdentity{}, 0, &RequestError{http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential"}
	}
	switch identity.Role {
	case admin.RoleTenantOperator:
		if identity.ScopeTenantID <= 0 {
			return admin.AdminIdentity{}, 0, &RequestError{http.StatusForbidden, "admin_forbidden", "tenant_operator scope_tenant_id required"}
		}
		return identity, identity.ScopeTenantID, nil
	case admin.RolePlatformAdmin:
		if identity.ScopeTenantID > 0 {
			return identity, identity.ScopeTenantID, nil
		}
		tenantID, err := ParsePositiveID(strings.TrimSpace(r.URL.Query().Get("tenant_id")))
		if err != nil {
			return admin.AdminIdentity{}, 0, &RequestError{http.StatusBadRequest, "tenant_id_required", "tenant_id query parameter must be positive"}
		}
		if err := identity.CanIssueForTenant(tenantID); err != nil {
			return admin.AdminIdentity{}, 0, &RequestError{http.StatusForbidden, "admin_forbidden", "tenant scope not permitted"}
		}
		return identity, tenantID, nil
	default:
		return admin.AdminIdentity{}, 0, &RequestError{http.StatusForbidden, "admin_forbidden", "admin role required"}
	}
}

// ResolvePlatform 解析只允许 platform_admin 的管理请求。
func ResolvePlatform(r *http.Request, deps Deps) (admin.AdminIdentity, *RequestError) {
	if deps.Auth == nil || deps.Store == nil {
		return admin.AdminIdentity{}, &RequestError{http.StatusServiceUnavailable, "gateway_not_configured", "admin pool account dependency unset"}
	}
	identity, err := deps.Auth.Resolve(r.Context(), r)
	if err != nil {
		if errors.Is(err, admin.ErrAdminBackend) {
			return admin.AdminIdentity{}, &RequestError{http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure"}
		}
		return admin.AdminIdentity{}, &RequestError{http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential"}
	}
	if identity.Role != admin.RolePlatformAdmin {
		return admin.AdminIdentity{}, &RequestError{http.StatusForbidden, "admin_forbidden", "platform_admin role required"}
	}
	return identity, nil
}

// ParsePositiveID 解析正整数识别符。
func ParsePositiveID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("id must be a positive int64")
	}
	return id, nil
}

// ParseLimit 解析列表页大小，空值使用默认值。
func ParseLimit(raw string) (int32, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultLimit, nil
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || value <= 0 || value > int64(maxLimit) {
		return 0, fmt.Errorf("limit must be between 1 and 200")
	}
	return int32(value), nil
}

// ParseCursor 解析不透明 provider account 游标。
func ParseCursor(raw string) (int64, *string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || !strings.HasPrefix(string(decoded), cursorPrefix) {
		return 0, nil, fmt.Errorf("cursor must be an opaque base64 cursor")
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(string(decoded), cursorPrefix), 10, 64)
	if err != nil || id < 0 {
		return 0, nil, fmt.Errorf("cursor must be an opaque base64 cursor")
	}
	return id, &raw, nil
}

// EncodeCursor 生成 provider account 列表游标。
func EncodeCursor(id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(cursorPrefix + strconv.FormatInt(id, 10)))
}

// ParseStateFilter 校验运维列表支持的状态过滤值。
func ParseStateFilter(raw string) (string, error) {
	state := strings.TrimSpace(raw)
	switch state {
	case "", "active", "error", "disabled", "rate_limited", "overloaded", "temp_unschedulable":
		return state, nil
	default:
		return "", fmt.Errorf("state_filter is invalid")
	}
}

// DTO 将数据库行规范化为统一回显。
func DTO(row admindb.AdminProviderAccountRow) Response {
	return Response{
		ID: row.ID, TenantID: row.TenantID, ProviderID: row.ProviderID, ChannelID: row.ChannelID,
		Name: row.Name, AccountType: row.AccountType, Enabled: row.Enabled, ExpiresAt: pgTimePtr(row.ExpiresAt),
		RPMLimit: row.RPMLimit, TPMLimit: row.TPMLimit, WindowCostLimitCents: row.WindowCostLimitCents,
		MaxSessions: row.MaxSessions, DisableCooling: row.DisableCooling,
		RefreshLeadSeconds: row.RefreshLeadSeconds, TLSFingerprintRotate: row.TLSFingerprintRotate,
		HealthState: row.HealthState, CredentialState: row.CredentialState,
		CapConcurrency: row.CapConcurrency, InFlightCount: row.InFlightCount, Priority: row.Priority,
		StaticWeight: row.StaticWeight, ProbeModel: row.ProbeModel, Tags: nonNilStringSlice(row.Tags),
		Extra:          jsonObjectOrEmpty(row.Extra),
		LastDispatchAt: pgTimePtr(row.LastDispatchAt), LastProbeLatencyMS: row.LastProbeLatencyMS,
		LastProbeAt: pgTimePtr(row.LastProbeAt), ModelAllowList: nonNilStringSlice(row.ModelAllowList),
		CapabilityFlags: nonNilStringSlice(row.CapabilityFlags), RateLimitedAt: pgTimePtr(row.RateLimitedAt),
		RateLimitResetAt: pgTimePtr(row.RateLimitResetAt), RateLimitReason: row.RateLimitReason,
		OverloadUntil: pgTimePtr(row.OverloadUntil), TempUnschedulableUntil: pgTimePtr(row.TempUnschedulableUntil),
		TokenVersion: row.TokenVersion, LastRefreshAt: pgTimePtr(row.LastRefreshAt),
		LastRefreshOutcome: row.LastRefreshOutcome, OAuthEndpointHealth: row.OAuthEndpointHealth,
		CustomErrorCodesEnabled: row.CustomErrorCodesEnabled, CustomErrorCodes: nonNilInt32Slice(row.CustomErrorCodes),
		PoolMode: row.PoolMode, TempUnschedulableEnabled: row.TempUnschedulableEnabled,
		TempUnschedulableRules: jsonArrayOrEmpty(row.TempUnschedulableRules),
		ProxyID:                row.ProxyID, ProxyGroupID: row.ProxyGroupID,
		ProxyBinding: accountadvanced.BindingFromColumns(row.ProxyID, row.ProxyGroupID),
		CreatedAt:    pgTimePtr(row.CreatedAt), UpdatedAt: pgTimePtr(row.UpdatedAt),
	}
}

// CleanOptionalString 去除可选字符串两端空白。
func CleanOptionalString(in *string) *string {
	if in == nil {
		return nil
	}
	out := strings.TrimSpace(*in)
	return &out
}

// CleanStringList 去除空元素并规范化首尾空白。
func CleanStringList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, item := range in {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// JSONRawObject 判断原始 JSON 是否为对象。
func JSONRawObject(raw json.RawMessage) bool {
	var object map[string]json.RawMessage
	return len(raw) > 0 && json.Unmarshal(raw, &object) == nil && object != nil
}

// NormalizedExtra 保留已校验的 extra JSON，空值交给 SQL 默认。
func NormalizedExtra(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return nil
	}
	return []byte(raw)
}

// WriteAudit 写入 provider account 管理操作审计记录。
func WriteAudit(ctx context.Context, r *http.Request, store Store, identity admin.AdminIdentity, tenantID int64, action string, targetID int64, reason *string, payload []byte) error {
	actorID := identity.AuditActor()
	requestID := middleware.GetReqID(r.Context())
	_, err := store.InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
		TenantID: &tenantID, ActorID: actorID, ActorRole: identity.Role,
		Action: action, TargetType: "provider_account", TargetID: &targetID,
		RequestID: &requestID, Reason: reason, Payload: payload,
	})
	return err
}

// ReadError 将账号读取错误归一为 HTTP 边界可用的错误。
func ReadError(err error, code string) RequestError {
	if errors.Is(err, pgx.ErrNoRows) {
		return RequestError{http.StatusNotFound, "provider_account_not_found", "provider account not found"}
	}
	return RequestError{http.StatusServiceUnavailable, code, err.Error()}
}

func jsonArrayOrEmpty(raw []byte) json.RawMessage {
	var values []json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &values) != nil || values == nil {
		return json.RawMessage(`[]`)
	}
	return json.RawMessage(append([]byte(nil), raw...))
}

func pgTimePtr(timestamp pgtype.Timestamptz) *time.Time {
	if !timestamp.Valid {
		return nil
	}
	out := timestamp.Time.UTC()
	return &out
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

func jsonObjectOrEmpty(raw []byte) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(raw)
}

// BuildMixedRiskParams 组装事务建号的风险检查输入。
func BuildMixedRiskParams(insert admindb.InsertProviderAccountParams, req CreateRequest, providerFamily string, confirmed bool) accountcreate.Params {
	return accountcreate.Params{
		Insert: insert,
		Candidate: mixedchannelrisk.Account{
			ProviderID: req.ProviderID, ChannelID: req.ChannelID,
			AccountType: req.AccountType, Vendor: req.Vendor, AuthMode: req.AuthMode,
		},
		ProviderFamily: providerFamily,
		Confirmed:      confirmed,
	}
}
