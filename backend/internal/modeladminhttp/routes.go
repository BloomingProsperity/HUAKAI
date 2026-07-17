// Package modeladminhttp 暴露模型主体的运维 CRUD，不承载公开模型发现逻辑。
package modeladminhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

type adminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type modelService interface {
	ListAdminModels(context.Context, registry.AdminModelAccess, registry.AdminModelTarget) ([]registry.AdminModel, error)
	GetAdminModel(context.Context, registry.AdminModelAccess, registry.AdminModelTarget, int64) (registry.AdminModel, error)
	CreateAdminModel(context.Context, registry.CreateAdminModelInput) (registry.AdminModel, error)
	UpdateAdminModel(context.Context, registry.UpdateAdminModelInput) (registry.AdminModel, error)
	SoftDeleteAdminModel(context.Context, registry.AdminModelAccess, registry.AdminModelTarget, int64) error
}

type Deps struct {
	Auth    adminAuth
	Service modelService
}

func MountRoutes(router chi.Router, deps Deps) {
	router.Get("/", newListHandler(deps))
	router.Post("/", newCreateHandler(deps))
	router.Get("/{id}", newGetHandler(deps))
	router.Patch("/{id}", newUpdateHandler(deps))
	router.Delete("/{id}", newDeleteHandler(deps))
}

func NewRouter(deps Deps) http.Handler {
	router := chi.NewRouter()
	MountRoutes(router, deps)
	return router
}

type modelResponse struct {
	ID                      int64           `json:"id"`
	TenantID                *int64          `json:"tenant_id"`
	Scope                   string          `json:"scope"`
	CanonicalID             string          `json:"canonical_id"`
	ProtocolFamily          string          `json:"protocol_family"`
	DefaultProviderModelID  string          `json:"default_provider_model_id"`
	DefaultContextWindow    int32           `json:"default_context_window"`
	DefaultRequestTimeoutMS int32           `json:"default_request_timeout_ms"`
	PricingClass            string          `json:"pricing_class"`
	ModelOwner              string          `json:"model_owner"`
	ModelCreatedAt          *string         `json:"model_created_at"`
	Capabilities            map[string]bool `json:"capabilities"`
	MaxOutputTokens         *int32          `json:"max_output_tokens"`
	ModelMode               *string         `json:"model_mode"`
	Status                  string          `json:"status"`
	CreatedAt               string          `json:"created_at"`
	UpdatedAt               string          `json:"updated_at"`
}

type createRequest struct {
	CanonicalID             string `json:"canonical_id"`
	ProtocolFamily          string `json:"protocol_family"`
	DefaultProviderModelID  string `json:"default_provider_model_id"`
	DefaultContextWindow    *int32 `json:"default_context_window,omitempty"`
	DefaultRequestTimeoutMS *int32 `json:"default_request_timeout_ms,omitempty"`
	PricingClass            string `json:"pricing_class,omitempty"`
	ModelOwner              string `json:"model_owner,omitempty"`
	Status                  string `json:"status,omitempty"`
}

type updateRequest struct {
	DefaultProviderModelID  *string `json:"default_provider_model_id,omitempty"`
	DefaultContextWindow    *int32  `json:"default_context_window,omitempty"`
	DefaultRequestTimeoutMS *int32  `json:"default_request_timeout_ms,omitempty"`
	PricingClass            *string `json:"pricing_class,omitempty"`
	ProtocolFamily          *string `json:"protocol_family,omitempty"`
	ModelOwner              *string `json:"model_owner,omitempty"`
	Status                  *string `json:"status,omitempty"`
}

func newListHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		access, target, ok := resolveTarget(w, r, deps, false)
		if !ok {
			return
		}
		models, err := deps.Service.ListAdminModels(r.Context(), access, target)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		items := make([]modelResponse, 0, len(models))
		for _, model := range models {
			items = append(items, toResponse(model))
		}
		writeJSON(w, http.StatusOK, map[string]any{"object": "admin_models_list", "items": items})
	}
}

func newGetHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		access, target, ok := resolveTarget(w, r, deps, false)
		if !ok {
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		model, err := deps.Service.GetAdminModel(r.Context(), access, target, id)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toResponse(model))
	}
}

func newCreateHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		access, target, ok := resolveTarget(w, r, deps, true)
		if !ok {
			return
		}
		var request createRequest
		if !decodeJSON(w, r, &request) {
			return
		}
		request.CanonicalID = strings.TrimSpace(request.CanonicalID)
		request.ProtocolFamily = strings.TrimSpace(request.ProtocolFamily)
		request.DefaultProviderModelID = strings.TrimSpace(request.DefaultProviderModelID)
		if request.CanonicalID == "" || request.ProtocolFamily == "" || request.DefaultProviderModelID == "" {
			writeError(w, http.StatusBadRequest, "invalid_admin_model", "canonical_id、protocol_family 与 default_provider_model_id 必填")
			return
		}
		created, err := deps.Service.CreateAdminModel(r.Context(), registry.CreateAdminModelInput{
			Access: access, Target: target,
			CanonicalID: request.CanonicalID, ProtocolFamily: request.ProtocolFamily,
			DefaultProviderModelID:  request.DefaultProviderModelID,
			DefaultContextWindow:    int32OrDefault(request.DefaultContextWindow, 0),
			DefaultRequestTimeoutMS: int32OrDefault(request.DefaultRequestTimeoutMS, 60000),
			PricingClass:            stringOrDefault(request.PricingClass, "standard"),
			ModelOwner:              stringOrDefault(request.ModelOwner, "HUAKAI"),
			Status:                  stringOrDefault(request.Status, "active"),
		})
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toResponse(created))
	}
}

func newUpdateHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		access, target, ok := resolveTarget(w, r, deps, true)
		if !ok {
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		var request updateRequest
		if !decodeJSON(w, r, &request) {
			return
		}
		if request.DefaultProviderModelID == nil && request.DefaultContextWindow == nil &&
			request.DefaultRequestTimeoutMS == nil && request.PricingClass == nil &&
			request.ProtocolFamily == nil && request.ModelOwner == nil && request.Status == nil {
			writeError(w, http.StatusBadRequest, "empty_admin_model_update", "至少提供一个可编辑字段")
			return
		}
		updated, err := deps.Service.UpdateAdminModel(r.Context(), registry.UpdateAdminModelInput{
			Access: access, Target: target, ID: id,
			DefaultProviderModelID:  request.DefaultProviderModelID,
			DefaultContextWindow:    request.DefaultContextWindow,
			DefaultRequestTimeoutMS: request.DefaultRequestTimeoutMS,
			PricingClass:            request.PricingClass,
			ProtocolFamily:          request.ProtocolFamily,
			ModelOwner:              request.ModelOwner,
			Status:                  request.Status,
		})
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toResponse(updated))
	}
}

func newDeleteHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		access, target, ok := resolveTarget(w, r, deps, true)
		if !ok {
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		if err := deps.Service.SoftDeleteAdminModel(r.Context(), access, target, id); err != nil {
			writeServiceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// resolveTarget 完成 HTTP 层第一道权限校验。tenant operator 的缺省 tenant
// 只能来自认证 scope；platform admin 管 tenant 时必须显式给出 tenant_id。
func resolveTarget(w http.ResponseWriter, r *http.Request, deps Deps, write bool) (registry.AdminModelAccess, registry.AdminModelTarget, bool) {
	if deps.Auth == nil || deps.Service == nil {
		writeError(w, http.StatusServiceUnavailable, "admin_models_not_configured", "模型主体管理依赖未配置")
		return registry.AdminModelAccess{}, registry.AdminModelTarget{}, false
	}
	identity, err := deps.Auth.Resolve(r.Context(), r)
	if err != nil {
		writeAdminAuthError(w, err)
		return registry.AdminModelAccess{}, registry.AdminModelTarget{}, false
	}
	if identity.Role != admin.RolePlatformAdmin && identity.Role != admin.RoleTenantOperator {
		writeError(w, http.StatusForbidden, "admin_forbidden_scope", "需要 admin 角色")
		return registry.AdminModelAccess{}, registry.AdminModelTarget{}, false
	}
	access := registry.AdminModelAccess{
		Role: identity.Role, ScopeTenantID: identity.ScopeTenantID, Actor: identity.AuditActor(),
	}
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	if scope == "" {
		scope = registry.ModelScopeTenant
	}
	if scope == registry.ModelScopeGlobal {
		if strings.TrimSpace(r.URL.Query().Get("tenant_id")) != "" {
			writeError(w, http.StatusBadRequest, "global_tenant_id_forbidden", "scope=global 时不能提供 tenant_id")
			return registry.AdminModelAccess{}, registry.AdminModelTarget{}, false
		}
		if identity.Role == admin.RoleTenantOperator {
			if identity.ScopeTenantID <= 0 {
				writeError(w, http.StatusForbidden, "admin_tenant_scope_required", "tenant_operator 缺少租户 scope")
				return registry.AdminModelAccess{}, registry.AdminModelTarget{}, false
			}
			if err := identity.CanIssueForTenant(identity.ScopeTenantID); err != nil {
				writeAdminAuthError(w, err)
				return registry.AdminModelAccess{}, registry.AdminModelTarget{}, false
			}
		}
		if write && identity.Role != admin.RolePlatformAdmin {
			writeError(w, http.StatusForbidden, "admin_global_model_write_forbidden", "tenant_operator 只能读取全局模型")
			return registry.AdminModelAccess{}, registry.AdminModelTarget{}, false
		}
		return access, registry.AdminModelTarget{Scope: registry.ModelScopeGlobal}, true
	}
	if scope != registry.ModelScopeTenant {
		writeError(w, http.StatusBadRequest, "invalid_admin_model_scope", "scope 只能是 tenant 或 global")
		return registry.AdminModelAccess{}, registry.AdminModelTarget{}, false
	}
	if identity.Role == admin.RoleTenantOperator && identity.ScopeTenantID <= 0 {
		writeError(w, http.StatusForbidden, "admin_tenant_scope_required", "tenant_operator 缺少租户 scope")
		return registry.AdminModelAccess{}, registry.AdminModelTarget{}, false
	}
	rawTenantID := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	var tenantID int64
	if rawTenantID == "" {
		if identity.Role == admin.RolePlatformAdmin {
			writeError(w, http.StatusBadRequest, "tenant_id_required", "platform_admin 管理 tenant 模型时必须提供 tenant_id")
			return registry.AdminModelAccess{}, registry.AdminModelTarget{}, false
		}
		tenantID = identity.ScopeTenantID
	} else {
		tenantID, err = strconv.ParseInt(rawTenantID, 10, 64)
		if err != nil || tenantID <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_tenant_id", "tenant_id 必须是正整数")
			return registry.AdminModelAccess{}, registry.AdminModelTarget{}, false
		}
	}
	if err := identity.CanIssueForTenant(tenantID); err != nil {
		writeAdminAuthError(w, err)
		return registry.AdminModelAccess{}, registry.AdminModelTarget{}, false
	}
	return access, registry.AdminModelTarget{Scope: registry.ModelScopeTenant, TenantID: tenantID}, true
}

func parsePathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(r, "id")), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_admin_model_id", "模型 id 必须是正整数")
		return 0, false
	}
	return id, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求体必须是合法 JSON")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求体只能包含一个 JSON 对象")
		return false
	}
	return true
}

func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, registry.ErrModelAdminInvalid):
		writeError(w, http.StatusBadRequest, "invalid_admin_model", "模型主体输入无效")
	case errors.Is(err, registry.ErrModelAdminForbidden):
		writeError(w, http.StatusForbidden, "admin_model_forbidden", "无权操作该模型主体")
	case errors.Is(err, registry.ErrModelAdminNotFound):
		writeError(w, http.StatusNotFound, "admin_model_not_found", "模型主体不存在")
	case errors.Is(err, registry.ErrConflict):
		writeError(w, http.StatusConflict, "admin_model_conflict", "该 scope 内 canonical_id 已存在")
	default:
		writeError(w, http.StatusServiceUnavailable, "admin_models_backend_error", "模型主体管理后端暂不可用")
	}
}

func writeAdminAuthError(w http.ResponseWriter, err error) {
	if errors.Is(err, admin.ErrAdminBackend) {
		writeError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin 鉴权后端暂不可用")
		return
	}
	if errors.Is(err, admin.ErrAdminForbidden) {
		writeError(w, http.StatusForbidden, "admin_forbidden_scope", "admin 凭据无权管理该租户")
		return
	}
	writeError(w, http.StatusUnauthorized, "admin_unauthorized", "admin 凭据缺失或无效")
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]map[string]string{"error": {"code": code, "message": message}})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func toResponse(model registry.AdminModel) modelResponse {
	return modelResponse{
		ID: model.ID, TenantID: model.TenantID, Scope: model.Scope, CanonicalID: model.CanonicalID,
		ProtocolFamily: model.ProtocolFamily, DefaultProviderModelID: model.DefaultProviderModelID,
		DefaultContextWindow: model.DefaultContextWindow, DefaultRequestTimeoutMS: model.DefaultRequestTimeoutMS,
		PricingClass: model.PricingClass, ModelOwner: model.ModelOwner,
		ModelCreatedAt: formatOptionalTime(model.ModelCreatedAt), Capabilities: model.Capabilities,
		MaxOutputTokens: model.MaxOutputTokens, ModelMode: model.ModelMode, Status: model.Status,
		CreatedAt: model.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: model.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func formatOptionalTime(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339Nano)
	return &formatted
}

func int32OrDefault(value *int32, fallback int32) int32 {
	if value == nil {
		return fallback
	}
	return *value
}

func stringOrDefault(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
