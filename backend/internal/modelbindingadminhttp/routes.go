// Package modelbindingadminhttp 暴露 model_pool_bindings 的租户域 admin HTTP 面 ——
// 即 模型→pool 路由绑定:其列和 resolver 早已存在,却没有 admin 写入路径(典型的
// "能力建了却够不着" inert gap)。
//
// 顶层资源 /admin/v1/model-pool-bindings(D1:对齐成熟参照把 channel/model 绑定做成
// 顶层 admin CRUD 的惯例,并复用双角色门让 tenant_operator 能管自己租户的绑定 ——
// model-admin 那个 platform_admin-only 门会把它挡在外面)。门照 proxyadminhttp:
// tenant_operator 自 scope,或 platform_admin 经 ?tenant_id + CanIssueForTenant。
package modelbindingadminhttp

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

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

// adminAuth 把 admin 凭据解析成身份(形态同 adminuserhttp/proxyadminhttp;本地声明以
// 让各包解耦)。
type adminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

// bindingService 是本面所需的、对 registry.PostgresRegistry 的窄接口。本地声明便于
// stub 测试 + 明确契约。
type bindingService interface {
	ListPoolBindingsAdmin(ctx context.Context, tenantID int64, modelID, poolGroupID *int64) ([]registry.AdminBinding, error)
	GetPoolBindingByID(ctx context.Context, id, tenantID int64) (registry.AdminBinding, error)
	CreatePoolBinding(ctx context.Context, in registry.CreateBindingInput) (registry.AdminBinding, error)
	UpdatePoolBinding(ctx context.Context, in registry.UpdateBindingInput) (registry.AdminBinding, error)
	DeletePoolBinding(ctx context.Context, id, tenantID int64, actor, reason string) error
}

// Deps 接线本面。Auth 是共享 admin 解析器;Service 是 registry 的绑定 admin 层。
type Deps struct {
	Auth    adminAuth
	Service bindingService
}

// MountRoutes 在 r 上注册端点。调用方挂在 /admin/v1/model-pool-bindings 下。
func MountRoutes(r chi.Router, d Deps) {
	r.Get("/", newListHandler(d))
	r.Post("/", newCreateHandler(d))
	r.Get("/{id}", newGetHandler(d))
	r.Patch("/{id}", newUpdateHandler(d))
	r.Delete("/{id}", newDeleteHandler(d))
}

// NewRouter 返回一个根部挂好端点的独立 router。
func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	MountRoutes(r, d)
	return r
}

var validSelectionModes = map[string]bool{"strict_priority": true, "priority_weighted": true}
var validFallbackClasses = map[string]bool{
	"normal": true, "context_window": true, "safety": true, "quota": true, "manual": true,
}

// ---- DTO ----

type bindingResponse struct {
	ID                      int64   `json:"id"`
	ModelID                 int64   `json:"model_id"`
	PoolGroupID             int64   `json:"pool_group_id"`
	Priority                int32   `json:"priority"`
	Weight                  int32   `json:"weight"`
	SelectionMode           string  `json:"selection_mode"`
	ProviderModelIDOverride *string `json:"provider_model_id_override"`
	RPMLimit                *int32  `json:"rpm_limit"`
	TPMLimit                *int32  `json:"tpm_limit"`
	MaxParallelRequests     *int32  `json:"max_parallel_requests"`
	FallbackClass           string  `json:"fallback_class"`
	Enabled                 bool    `json:"enabled"`
	DisabledReason          *string `json:"disabled_reason"`
	EffectiveFrom           *string `json:"effective_from"`
	EffectiveUntil          *string `json:"effective_until"`
	Reason                  string  `json:"reason"`
	CreatedAt               string  `json:"created_at"`
	UpdatedAt               string  `json:"updated_at"`
}

func toResponse(b registry.AdminBinding) bindingResponse {
	return bindingResponse{
		ID: b.ID, ModelID: b.ModelID, PoolGroupID: b.PoolGroupID,
		Priority: b.Priority, Weight: b.Weight, SelectionMode: b.SelectionMode,
		ProviderModelIDOverride: b.ProviderModelIDOverride,
		RPMLimit:                b.RPMLimit, TPMLimit: b.TPMLimit, MaxParallelRequests: b.MaxParallelRequests,
		FallbackClass: b.FallbackClass, Enabled: b.Enabled, DisabledReason: b.DisabledReason,
		EffectiveFrom: tsPtr(b.EffectiveFrom), EffectiveUntil: tsPtr(b.EffectiveUntil),
		Reason: b.Reason, CreatedAt: ts(b.CreatedAt), UpdatedAt: ts(b.UpdatedAt),
	}
}

type createBindingRequest struct {
	ModelID                 int64   `json:"model_id"`
	PoolGroupID             int64   `json:"pool_group_id"`
	Priority                *int32  `json:"priority,omitempty"`
	Weight                  *int32  `json:"weight,omitempty"`
	SelectionMode           string  `json:"selection_mode,omitempty"`
	ProviderModelIDOverride *string `json:"provider_model_id_override,omitempty"`
	RPMLimit                *int32  `json:"rpm_limit,omitempty"`
	TPMLimit                *int32  `json:"tpm_limit,omitempty"`
	MaxParallelRequests     *int32  `json:"max_parallel_requests,omitempty"`
	FallbackClass           string  `json:"fallback_class,omitempty"`
	Enabled                 *bool   `json:"enabled,omitempty"`
	DisabledReason          *string `json:"disabled_reason,omitempty"`
	EffectiveFrom           *string `json:"effective_from,omitempty"`
	EffectiveUntil          *string `json:"effective_until,omitempty"`
	Reason                  string  `json:"reason,omitempty"`
}

type updateBindingRequest struct {
	Priority                *int32  `json:"priority,omitempty"`
	Weight                  *int32  `json:"weight,omitempty"`
	SelectionMode           string  `json:"selection_mode,omitempty"`
	ProviderModelIDOverride *string `json:"provider_model_id_override,omitempty"`
	RPMLimit                *int32  `json:"rpm_limit,omitempty"`
	TPMLimit                *int32  `json:"tpm_limit,omitempty"`
	MaxParallelRequests     *int32  `json:"max_parallel_requests,omitempty"`
	FallbackClass           string  `json:"fallback_class,omitempty"`
	Enabled                 *bool   `json:"enabled,omitempty"`
	DisabledReason          *string `json:"disabled_reason,omitempty"`
	EffectiveFrom           *string `json:"effective_from,omitempty"`
	EffectiveUntil          *string `json:"effective_until,omitempty"`
	Reason                  string  `json:"reason,omitempty"`
}

// ---- handler ----

func newListHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, _, ok := resolveTenant(w, r, d)
		if !ok {
			return
		}
		modelID, ok := optID(w, r, "model_id")
		if !ok {
			return
		}
		poolID, ok := optID(w, r, "pool_group_id")
		if !ok {
			return
		}
		items, err := d.Service.ListPoolBindingsAdmin(r.Context(), tenantID, modelID, poolID)
		if err != nil {
			writeServiceError(w, err, "list bindings failed")
			return
		}
		out := make([]bindingResponse, 0, len(items))
		for _, b := range items {
			out = append(out, toResponse(b))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": out})
	}
}

func newGetHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, _, ok := resolveTenant(w, r, d)
		if !ok {
			return
		}
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		b, err := d.Service.GetPoolBindingByID(r.Context(), id, tenantID)
		if err != nil {
			writeServiceError(w, err, "get binding failed")
			return
		}
		writeJSON(w, http.StatusOK, toResponse(b))
	}
}

func newCreateHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, actor, ok := resolveTenant(w, r, d)
		if !ok {
			return
		}
		var req createBindingRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.ModelID <= 0 || req.PoolGroupID <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_binding", "model_id and pool_group_id are required positive ids")
			return
		}
		ef, eu, ok := validateCommon(w, req.SelectionMode, req.FallbackClass, req.Priority, req.Weight, req.MaxParallelRequests, req.EffectiveFrom, req.EffectiveUntil)
		if !ok {
			return
		}
		in := registry.CreateBindingInput{
			TenantID: tenantID, ModelID: req.ModelID, PoolGroupID: req.PoolGroupID,
			Priority: deref32(req.Priority, 100), Weight: deref32(req.Weight, 1),
			SelectionMode:           orDefault(req.SelectionMode, "strict_priority"),
			ProviderModelIDOverride: req.ProviderModelIDOverride,
			RPMLimit:                req.RPMLimit, TPMLimit: req.TPMLimit, MaxParallelRequests: req.MaxParallelRequests,
			FallbackClass: orDefault(req.FallbackClass, "normal"),
			Enabled:       derefBool(req.Enabled, true), DisabledReason: req.DisabledReason,
			EffectiveFrom: ef, EffectiveUntil: eu, Reason: req.Reason, Actor: actor,
		}
		b, err := d.Service.CreatePoolBinding(r.Context(), in)
		if err != nil {
			writeServiceError(w, err, "create binding failed")
			return
		}
		writeJSON(w, http.StatusCreated, toResponse(b))
	}
}

func newUpdateHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, actor, ok := resolveTenant(w, r, d)
		if !ok {
			return
		}
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		var req updateBindingRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		ef, eu, ok := validateCommon(w, req.SelectionMode, req.FallbackClass, req.Priority, req.Weight, req.MaxParallelRequests, req.EffectiveFrom, req.EffectiveUntil)
		if !ok {
			return
		}
		in := registry.UpdateBindingInput{
			ID: id, TenantID: tenantID,
			Priority: deref32(req.Priority, 100), Weight: deref32(req.Weight, 1),
			SelectionMode:           orDefault(req.SelectionMode, "strict_priority"),
			ProviderModelIDOverride: req.ProviderModelIDOverride,
			RPMLimit:                req.RPMLimit, TPMLimit: req.TPMLimit, MaxParallelRequests: req.MaxParallelRequests,
			FallbackClass: orDefault(req.FallbackClass, "normal"),
			Enabled:       derefBool(req.Enabled, true), DisabledReason: req.DisabledReason,
			EffectiveFrom: ef, EffectiveUntil: eu, Reason: req.Reason, Actor: actor,
		}
		b, err := d.Service.UpdatePoolBinding(r.Context(), in)
		if err != nil {
			writeServiceError(w, err, "update binding failed")
			return
		}
		writeJSON(w, http.StatusOK, toResponse(b))
	}
}

func newDeleteHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, actor, ok := resolveTenant(w, r, d)
		if !ok {
			return
		}
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		if err := d.Service.DeletePoolBinding(r.Context(), id, tenantID, actor, ""); err != nil {
			writeServiceError(w, err, "delete binding failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// validateCommon 校验共享可变字段并解析生效窗。成功时返回解析出的 *time.Time 对。
func validateCommon(w http.ResponseWriter, selMode, fbClass string, priority, weight, maxParallelRequests *int32, efRaw, euRaw *string) (*time.Time, *time.Time, bool) {
	if selMode != "" && !validSelectionModes[selMode] {
		writeError(w, http.StatusBadRequest, "invalid_selection_mode", "selection_mode must be strict_priority or priority_weighted")
		return nil, nil, false
	}
	if fbClass != "" && !validFallbackClasses[fbClass] {
		writeError(w, http.StatusBadRequest, "invalid_fallback_class", "fallback_class must be one of normal, context_window, safety, quota, manual")
		return nil, nil, false
	}
	if priority != nil && *priority < 0 {
		writeError(w, http.StatusBadRequest, "invalid_priority", "priority must be >= 0")
		return nil, nil, false
	}
	if weight != nil && *weight <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_weight", "weight must be > 0")
		return nil, nil, false
	}
	if maxParallelRequests != nil && *maxParallelRequests < 0 {
		writeError(w, http.StatusBadRequest, "invalid_max_parallel_requests", "max_parallel_requests must be >= 0")
		return nil, nil, false
	}
	ef, ok := parseTimePtr(w, efRaw, "effective_from")
	if !ok {
		return nil, nil, false
	}
	eu, ok := parseTimePtr(w, euRaw, "effective_until")
	if !ok {
		return nil, nil, false
	}
	if ef != nil && eu != nil && !ef.Before(*eu) {
		writeError(w, http.StatusBadRequest, "invalid_effective_window", "effective_from must be before effective_until")
		return nil, nil, false
	}
	return ef, eu, true
}

// ---- admin 门(照 proxyadminhttp;额外返回 actor 供快照审计) ----

func resolveTenant(w http.ResponseWriter, r *http.Request, d Deps) (int64, string, bool) {
	if d.Auth == nil || d.Service == nil {
		writeError(w, http.StatusServiceUnavailable, "admin_bindings_not_configured", "admin bindings dependency unset")
		return 0, "", false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		writeAdminAuthError(w, err)
		return 0, "", false
	}
	actor := ident.AuditActor()
	switch ident.Role {
	case admin.RoleTenantOperator:
		if ident.ScopeTenantID <= 0 {
			writeError(w, http.StatusForbidden, "admin_tenant_scope_required", "tenant_operator scope_tenant_id required")
			return 0, "", false
		}
		tid, ok := tenantFromQueryOrScope(w, r, ident)
		return tid, actor, ok
	case admin.RolePlatformAdmin:
		tid, ok := tenantFromQueryOrScope(w, r, ident)
		return tid, actor, ok
	default:
		writeError(w, http.StatusForbidden, "admin_forbidden_scope", "admin role required")
		return 0, "", false
	}
}

func tenantFromQueryOrScope(w http.ResponseWriter, r *http.Request, ident admin.AdminIdentity) (int64, bool) {
	tenantParam := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	var tenantID int64
	if tenantParam == "" {
		if ident.Role != admin.RoleTenantOperator {
			writeError(w, http.StatusBadRequest, "tenant_id_required", "tenant_id query param required for platform_admin")
			return 0, false
		}
		tenantID = ident.ScopeTenantID
	} else {
		v, err := strconv.ParseInt(tenantParam, 10, 64)
		if err != nil || v <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_tenant_id", "tenant_id must be a positive int64")
			return 0, false
		}
		tenantID = v
	}
	if tenantID <= 0 {
		writeAdminAuthError(w, admin.ErrAdminForbidden)
		return 0, false
	}
	if err := ident.CanIssueForTenant(tenantID); err != nil {
		writeAdminAuthError(w, err)
		return 0, false
	}
	return tenantID, true
}

// ---- 辅助 ----

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := strings.TrimSpace(chi.URLParam(r, "id"))
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_binding_id", "binding id must be a positive int64")
		return 0, false
	}
	return id, true
}

func optID(w http.ResponseWriter, r *http.Request, key string) (*int64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return nil, true
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_filter", key+" must be a positive int64")
		return nil, false
	}
	return &v, true
}

func parseTimePtr(w http.ResponseWriter, raw *string, field string) (*time.Time, bool) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil, true
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(*raw))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_timestamp", field+" must be RFC3339")
		return nil, false
	}
	return &t, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return false
	}
	return true
}

// writeServiceError 把 registry 哨兵错误映射成 HTTP 状态。
func writeServiceError(w http.ResponseWriter, err error, ctx string) {
	switch {
	case errors.Is(err, registry.ErrBindingNotFound):
		writeError(w, http.StatusNotFound, "binding_not_found", "model pool binding not found")
	case errors.Is(err, registry.ErrBindingConflict):
		writeError(w, http.StatusConflict, "binding_already_exists", "a binding for this (model, pool) already exists")
	case errors.Is(err, registry.ErrModelNotBindable):
		writeError(w, http.StatusUnprocessableEntity, "model_not_bindable", "model not found or not bindable by this tenant")
	case errors.Is(err, registry.ErrPoolGroupNotFound):
		writeError(w, http.StatusUnprocessableEntity, "pool_group_not_found", "pool group not found for this tenant")
	default:
		writeError(w, http.StatusServiceUnavailable, "admin_bindings_backend_error", fmt.Sprintf("%s: %v", ctx, err))
	}
}

func writeAdminAuthError(w http.ResponseWriter, err error) {
	if errors.Is(err, admin.ErrAdminBackend) {
		writeError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
		return
	}
	if errors.Is(err, admin.ErrAdminForbidden) {
		writeError(w, http.StatusForbidden, "admin_forbidden_scope", "admin credential is not allowed for this tenant")
		return
	}
	writeError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]map[string]string{"error": {"code": code, "message": message}})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func tsPtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

func deref32(p *int32, def int32) int32 {
	if p == nil {
		return def
	}
	return *p
}

func derefBool(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}
