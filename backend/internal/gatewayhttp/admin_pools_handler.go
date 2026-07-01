package gatewayhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

const (
	defaultAdminPoolsLimit            = int32(50)
	maxAdminPoolsLimit                = int32(200)
	maxAdminPoolNameRunes             = 64
	defaultAdminPoolTopKDefault       = int32(1)
	defaultAdminPoolCapabilityDefault = "exact_capability_only"
	adminPoolCapabilitySafeEquivalent = "safe_equivalent_allowed"
)

type AdminPoolsAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type AdminPoolsDataStore interface {
	InsertPool(context.Context, dbbilling.InsertPoolParams) (dbbilling.PoolGroup, error)
	GetPool(context.Context, dbbilling.GetPoolParams) (dbbilling.PoolGroup, error)
	ListPools(context.Context, dbbilling.ListPoolsParams) ([]dbbilling.PoolGroup, error)
	UpdatePool(context.Context, dbbilling.UpdatePoolParams) (dbbilling.PoolGroup, error)
}

type AdminPoolsAuditStore interface {
	InsertAdminAuditEvent(context.Context, admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error)
}

type AdminPoolsStore interface {
	AdminPoolsDataStore
	AdminPoolsAuditStore

	// CreatePoolWithAudit 在**同事务**内 InsertPool + InsertAdminAuditEvent;
	// audit insert 失败时 pool 行不提交 (W5 D1 / synthesis §6 C3 GW-10)。
	// 老 InsertPool + writeAdminPoolAudit 两步式禁用于 mutation 路径,
	// 仅保留供 list/get read-only 兼容。
	CreatePoolWithAudit(ctx context.Context, pp dbbilling.InsertPoolParams, ap admindb.InsertAdminAuditEventParams) (dbbilling.PoolGroup, error)

	// UpdatePoolWithAudit 同事务 UpdatePool + audit insert,语义同上。
	UpdatePoolWithAudit(ctx context.Context, up dbbilling.UpdatePoolParams, ap admindb.InsertAdminAuditEventParams) (dbbilling.PoolGroup, error)
}

type AdminPoolsDeps struct {
	Auth  AdminPoolsAuth
	Store AdminPoolsStore
}

type adminPoolsStoreAdapter struct {
	data  AdminPoolsDataStore
	audit AdminPoolsAuditStore
	// pool 用于 mutation 路径同事务 (CreatePoolWithAudit / UpdatePoolWithAudit);
	// nil 时这两个方法返 ErrAdminPoolsTxPoolUnset,production 必须装。
	pool *pgxpool.Pool
}

// ErrAdminPoolsTxPoolUnset 表示 adapter 没装 pgxpool,无法走同事务 mutation。
// 仅 testing / 弱构造时出现;production 走 NewAdminPoolsStoreAdapter 必带 pool。
var ErrAdminPoolsTxPoolUnset = errors.New("gatewayhttp: admin pools adapter pgxpool unset")

// NewAdminPoolsStoreAdapter 构造同事务 adapter。pool 用于 mutation 同事务;
// nil pool 时 mutation 方法 fail-closed。read-only 路径 (Get/List) 走 data 直读。
func NewAdminPoolsStoreAdapter(data AdminPoolsDataStore, audit AdminPoolsAuditStore, pool *pgxpool.Pool) AdminPoolsStore {
	return adminPoolsStoreAdapter{data: data, audit: audit, pool: pool}
}

func (s adminPoolsStoreAdapter) InsertPool(ctx context.Context, arg dbbilling.InsertPoolParams) (dbbilling.PoolGroup, error) {
	return s.data.InsertPool(ctx, arg)
}

func (s adminPoolsStoreAdapter) GetPool(ctx context.Context, arg dbbilling.GetPoolParams) (dbbilling.PoolGroup, error) {
	return s.data.GetPool(ctx, arg)
}

func (s adminPoolsStoreAdapter) ListPools(ctx context.Context, arg dbbilling.ListPoolsParams) ([]dbbilling.PoolGroup, error) {
	return s.data.ListPools(ctx, arg)
}

func (s adminPoolsStoreAdapter) UpdatePool(ctx context.Context, arg dbbilling.UpdatePoolParams) (dbbilling.PoolGroup, error) {
	return s.data.UpdatePool(ctx, arg)
}

func (s adminPoolsStoreAdapter) InsertAdminAuditEvent(ctx context.Context, arg admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error) {
	return s.audit.InsertAdminAuditEvent(ctx, arg)
}

// CreatePoolWithAudit 在 pgxpool 上 BeginFunc:dbbilling.New(tx).InsertPool +
// admindb.New(tx).InsertAdminAuditEvent;audit insert 失败时 BeginFunc 自动
// rollback,pool 行不留下 (W5 D1 不允许 "先 mutation 后返 503")。
//
// TargetID 在 ap 里保留 nil — 由本方法 insert 成功后用真实 pool.ID 填,
// 防 caller 把 0 当 target_id 写进 audit 行。
func (s adminPoolsStoreAdapter) CreatePoolWithAudit(ctx context.Context, pp dbbilling.InsertPoolParams, ap admindb.InsertAdminAuditEventParams) (dbbilling.PoolGroup, error) {
	if s.pool == nil {
		return dbbilling.PoolGroup{}, ErrAdminPoolsTxPoolUnset
	}
	var out dbbilling.PoolGroup
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		p, err := dbbilling.New(tx).InsertPool(ctx, pp)
		if err != nil {
			return err
		}
		out = p
		ap.TargetID = &p.ID
		if _, err := admindb.New(tx).InsertAdminAuditEvent(ctx, ap); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return dbbilling.PoolGroup{}, err
	}
	return out, nil
}

// UpdatePoolWithAudit 同事务 UpdatePool + audit insert。
//
// 跟 Create 不同,Update 是按 (tenant_id, id) 找已存在行 → ap.TargetID 应由
// caller 填 id (因为 pool.ID 在 UPDATE 前后不变)。这里仍然在 tx 内 fetch
// 实际 pool.ID 覆盖 ap.TargetID 以防 caller 漏填或填错。
func (s adminPoolsStoreAdapter) UpdatePoolWithAudit(ctx context.Context, up dbbilling.UpdatePoolParams, ap admindb.InsertAdminAuditEventParams) (dbbilling.PoolGroup, error) {
	if s.pool == nil {
		return dbbilling.PoolGroup{}, ErrAdminPoolsTxPoolUnset
	}
	var out dbbilling.PoolGroup
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		p, err := dbbilling.New(tx).UpdatePool(ctx, up)
		if err != nil {
			return err
		}
		out = p
		ap.TargetID = &p.ID
		if _, err := admindb.New(tx).InsertAdminAuditEvent(ctx, ap); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return dbbilling.PoolGroup{}, err
	}
	return out, nil
}

func NewAdminPoolsHandler(d AdminPoolsDeps) http.Handler {
	r := chi.NewRouter()
	r.Get("/", newListPoolsHandler(d))
	r.Post("/", newCreatePoolHandler(d))
	r.Get("/{id}", newGetPoolHandler(d))
	r.Patch("/{id}", newUpdatePoolHandler(d))
	return r
}

type adminPoolCreateRequest struct {
	TenantID          *int64  `json:"tenant_id,omitempty"`
	Name              string  `json:"name"`
	TopKDefault       *int32  `json:"top_k_default,omitempty"`
	CapabilityDefault *string `json:"capability_default,omitempty"`
	AllowLastResort   *bool   `json:"allow_last_resort,omitempty"`
	// 兼容本轮 body contract；当前 pool_groups schema 尚无 description 列。
	Description string `json:"description,omitempty"`
}

type adminPoolUpdateRequest struct {
	TenantID          *int64  `json:"tenant_id,omitempty"`
	Name              *string `json:"name,omitempty"`
	TopKDefault       *int32  `json:"top_k_default,omitempty"`
	CapabilityDefault *string `json:"capability_default,omitempty"`
	AllowLastResort   *bool   `json:"allow_last_resort,omitempty"`
	// 兼容请求字段；本 slice 不改 schema，因此不落库。
	Description *string `json:"description,omitempty"`
	Enabled     *bool   `json:"enabled,omitempty"`
}

func newListPoolsHandler(d AdminPoolsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdminPoolOperator(w, r, d)
		if !ok {
			return
		}
		tenantID, ok := resolveAdminPoolTenant(w, r, ident, nil, false)
		if !ok {
			return
		}
		limit, ok := parseAdminPoolsLimit(w, r)
		if !ok {
			return
		}
		items, err := d.Store.ListPools(r.Context(), dbbilling.ListPoolsParams{TenantID: tenantID, LimitCount: limit})
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "pool_list_failed", err.Error())
			return
		}
		writeAuditJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

func newCreatePoolHandler(d AdminPoolsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdminPoolOperator(w, r, d)
		if !ok {
			return
		}
		var req adminPoolCreateRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		tenantID, ok := resolveAdminPoolTenant(w, r, ident, req.TenantID, true)
		if !ok {
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		if err := validateAdminPoolName(req.Name); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_pool_name", err.Error())
			return
		}
		topK, capability, allowLastResort, ok := normalizeAdminPoolCreateDefaults(w, req)
		if !ok {
			return
		}
		// W5 D1:audit insert 失败必须回滚 pool 行 — 用 CreatePoolWithAudit 同事务。
		// pool.ID 在 audit params 里 nil,adapter 拿 INSERT ... RETURNING 的真 ID 填。
		auditParams, err := buildAdminPoolAuditParams(r, ident, tenantID, "create_pool_group", 0, map[string]any{
			"tenant_id": tenantID,
			"name":      req.Name,
		})
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "audit_payload_failed", err.Error())
			return
		}
		pool, err := d.Store.CreatePoolWithAudit(r.Context(),
			dbbilling.InsertPoolParams{
				TenantID:          tenantID,
				Name:              req.Name,
				TopKDefault:       topK,
				CapabilityDefault: capability,
				AllowLastResort:   allowLastResort,
			},
			auditParams,
		)
		if err != nil {
			writeAdminPoolMutationError(w, err, "pool_create_failed")
			return
		}
		writeAuditJSON(w, http.StatusCreated, pool)
	}
}

func newGetPoolHandler(d AdminPoolsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdminPoolOperator(w, r, d)
		if !ok {
			return
		}
		tenantID, ok := resolveAdminPoolTenant(w, r, ident, nil, false)
		if !ok {
			return
		}
		id, ok := parseAdminPoolGroupID(w, r)
		if !ok {
			return
		}
		pool, err := d.Store.GetPool(r.Context(), dbbilling.GetPoolParams{TenantID: tenantID, ID: id})
		if err != nil {
			writeAdminPoolReadError(w, err, "pool_get_failed")
			return
		}
		writeAuditJSON(w, http.StatusOK, pool)
	}
}

func newUpdatePoolHandler(d AdminPoolsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdminPoolOperator(w, r, d)
		if !ok {
			return
		}
		id, ok := parseAdminPoolGroupID(w, r)
		if !ok {
			return
		}
		var req adminPoolUpdateRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		tenantID, ok := resolveAdminPoolTenant(w, r, ident, req.TenantID, true)
		if !ok {
			return
		}
		if req.Name != nil {
			name := strings.TrimSpace(*req.Name)
			if err := validateAdminPoolName(name); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid_pool_name", err.Error())
				return
			}
			req.Name = &name
		}
		if req.TopKDefault != nil {
			if err := validateAdminPoolTopK(*req.TopKDefault); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid_top_k_default", err.Error())
				return
			}
		}
		if req.CapabilityDefault != nil {
			capability := strings.TrimSpace(*req.CapabilityDefault)
			if err := validateAdminPoolCapabilityDefault(capability); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid_capability_default", err.Error())
				return
			}
			req.CapabilityDefault = &capability
		}
		if req.Name == nil && req.TopKDefault == nil && req.CapabilityDefault == nil && req.AllowLastResort == nil && req.Enabled == nil {
			writeJSONError(w, http.StatusBadRequest, "admin_bad_request", "at least one supported field is required")
			return
		}
		auditParams, err := buildAdminPoolAuditParams(r, ident, tenantID, "update_pool_group", id, map[string]any{
			"tenant_id": tenantID,
			"pool_id":   id,
			"updated":   true,
		})
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "audit_payload_failed", err.Error())
			return
		}
		pool, err := d.Store.UpdatePoolWithAudit(r.Context(),
			dbbilling.UpdatePoolParams{
				Name:              req.Name,
				TopKDefault:       req.TopKDefault,
				CapabilityDefault: req.CapabilityDefault,
				AllowLastResort:   req.AllowLastResort,
				Enabled:           req.Enabled,
				TenantID:          tenantID,
				ID:                id,
			},
			auditParams,
		)
		if err != nil {
			writeAdminPoolMutationError(w, err, "pool_update_failed")
			return
		}
		writeAuditJSON(w, http.StatusOK, pool)
	}
}

func resolveAdminPoolOperator(w http.ResponseWriter, r *http.Request, d AdminPoolsDeps) (admin.AdminIdentity, bool) {
	if d.Auth == nil || d.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "admin pool dependency unset")
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
	switch ident.Role {
	case admin.RoleTenantOperator:
		if ident.ScopeTenantID <= 0 {
			writeJSONError(w, http.StatusForbidden, "admin_forbidden", "tenant_operator scope_tenant_id required")
			return admin.AdminIdentity{}, false
		}
	case admin.RolePlatformAdmin:
	default:
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "admin role required")
		return admin.AdminIdentity{}, false
	}
	return ident, true
}

func resolveAdminPoolTenant(w http.ResponseWriter, r *http.Request, ident admin.AdminIdentity, bodyTenantID *int64, allowBodyTenant bool) (int64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	var tenantID int64
	switch {
	case raw != "":
		parsed, ok := parseRequiredPositiveInt64(w, raw, "tenant_id_invalid", "tenant_id must be positive when provided")
		if !ok {
			return 0, false
		}
		tenantID = parsed
	case allowBodyTenant && bodyTenantID != nil:
		if *bodyTenantID <= 0 {
			writeJSONError(w, http.StatusBadRequest, "tenant_id_invalid", "tenant_id must be positive when provided")
			return 0, false
		}
		tenantID = *bodyTenantID
	case ident.Role == admin.RoleTenantOperator:
		tenantID = ident.ScopeTenantID
	default:
		writeJSONError(w, http.StatusBadRequest, "tenant_id_required", "tenant_id must be provided explicitly")
		return 0, false
	}
	if !adminCanAccessTenant(ident, tenantID) {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "caller cannot act on this tenant scope")
		return 0, false
	}
	if bodyTenantID != nil {
		if *bodyTenantID <= 0 {
			writeJSONError(w, http.StatusBadRequest, "tenant_id_invalid", "tenant_id must be positive when provided")
			return 0, false
		}
		if *bodyTenantID != tenantID {
			writeJSONError(w, http.StatusForbidden, "admin_forbidden", "tenant_id does not match admin scope")
			return 0, false
		}
	}
	return tenantID, true
}

func parseAdminPoolsLimit(w http.ResponseWriter, r *http.Request) (int32, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return defaultAdminPoolsLimit, true
	}
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || n <= 0 || n > int64(maxAdminPoolsLimit) {
		writeJSONError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 200")
		return 0, false
	}
	return int32(n), true
}

func parseAdminPoolGroupID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_pool_id", "id must be a positive int64")
		return 0, false
	}
	return id, true
}

func validateAdminPoolName(name string) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if utf8.RuneCountInString(name) > maxAdminPoolNameRunes {
		return fmt.Errorf("name must be 1-64 characters")
	}
	return nil
}

func normalizeAdminPoolCreateDefaults(w http.ResponseWriter, req adminPoolCreateRequest) (int32, string, bool, bool) {
	topK := defaultAdminPoolTopKDefault
	if req.TopKDefault != nil {
		if err := validateAdminPoolTopK(*req.TopKDefault); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_top_k_default", err.Error())
			return 0, "", false, false
		}
		topK = *req.TopKDefault
	}
	capability := defaultAdminPoolCapabilityDefault
	if req.CapabilityDefault != nil {
		capability = strings.TrimSpace(*req.CapabilityDefault)
		if err := validateAdminPoolCapabilityDefault(capability); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_capability_default", err.Error())
			return 0, "", false, false
		}
	}
	allowLastResort := false
	if req.AllowLastResort != nil {
		allowLastResort = *req.AllowLastResort
	}
	return topK, capability, allowLastResort, true
}

func validateAdminPoolTopK(topK int32) error {
	if topK < 1 || topK > 10 {
		return fmt.Errorf("top_k_default must be between 1 and 10")
	}
	return nil
}

func validateAdminPoolCapabilityDefault(capability string) error {
	switch capability {
	case defaultAdminPoolCapabilityDefault, adminPoolCapabilitySafeEquivalent:
		return nil
	default:
		return fmt.Errorf("capability_default must be exact_capability_only or safe_equivalent_allowed")
	}
}

func writeAdminPoolReadError(w http.ResponseWriter, err error, code string) {
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "pool_not_found", "pool not found")
		return
	}
	writeJSONError(w, http.StatusServiceUnavailable, code, err.Error())
}

func writeAdminPoolMutationError(w http.ResponseWriter, err error, code string) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		writeJSONError(w, http.StatusConflict, "pool_name_conflict", "pool name already exists")
		return
	}
	writeAdminPoolReadError(w, err, code)
}

// buildAdminPoolAuditParams 把 audit row 的参数构造抽出来,供同事务 Create/Update
// 的 ap 参数;不再有 writeAdminPoolAudit 两步式 (W5 D1 同事务不允许 mutation+
// audit 分两条 DB 调用)。poolID 为 0 时 TargetID 留 nil 由 adapter 在 INSERT
// 成功后用真实 ID 覆盖。
func buildAdminPoolAuditParams(r *http.Request, ident admin.AdminIdentity, tenantID int64, action string, poolID int64, payload map[string]any) (admindb.InsertAdminAuditEventParams, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return admindb.InsertAdminAuditEventParams{}, err
	}
	actorID := ident.AuditActor()
	requestID := middleware.GetReqID(r.Context())
	params := admindb.InsertAdminAuditEventParams{
		TenantID: &tenantID, ActorID: actorID, ActorRole: ident.Role,
		Action: action, TargetType: "pool_group",
		RequestID: &requestID, Payload: body,
	}
	if poolID > 0 {
		params.TargetID = &poolID
	}
	return params, nil
}
