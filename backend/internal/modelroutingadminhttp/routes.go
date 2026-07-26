// Package modelroutingadminhttp 暴露模型在指定池组内的账号强制 pin 管理面。
// 它只管理 Layer-1 候选子集，不改变模型到池组的绑定或消费端选号算法。
package modelroutingadminhttp

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
	"github.com/go-chi/chi/v5/middleware"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauth"
)

type adminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type overrideService interface {
	List(context.Context, int64) ([]Override, error)
	Create(context.Context, CreateInput) (Override, error)
	Update(context.Context, UpdateInput) (Override, error)
	Delete(context.Context, int64, int64, MutationAudit) error
}

type Deps struct {
	Auth    adminAuth
	Service overrideService
}

func MountRoutes(router chi.Router, deps Deps) {
	safe := adminsessionauth.AllowSessionWrite(adminsessionauth.SessionSafe)
	router.Get("/", newListHandler(deps))
	router.With(safe).Post("/", newCreateHandler(deps))
	router.With(safe).Patch("/{id}", newUpdateHandler(deps))
	router.With(safe).Delete("/{id}", newDeleteHandler(deps))
}

func NewRouter(deps Deps) http.Handler {
	router := chi.NewRouter()
	MountRoutes(router, deps)
	return router
}

type overrideResponse struct {
	ID                 int64   `json:"id"`
	PoolGroupID        int64   `json:"pool_group_id"`
	Model              string  `json:"model"`
	ProviderAccountIDs []int64 `json:"provider_account_ids"`
	Enabled            bool    `json:"enabled"`
	CreatedAt          string  `json:"created_at"`
	UpdatedAt          string  `json:"updated_at"`
}

type createRequest struct {
	PoolGroupID        int64   `json:"pool_group_id"`
	Model              string  `json:"model"`
	ProviderAccountIDs []int64 `json:"provider_account_ids"`
	Enabled            *bool   `json:"enabled,omitempty"`
}

type updateRequest struct {
	ProviderAccountIDs *[]int64 `json:"provider_account_ids,omitempty"`
	Enabled            *bool    `json:"enabled,omitempty"`
}

func newListHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, ok := resolveTenant(w, r, deps)
		if !ok {
			return
		}
		items, err := deps.Service.List(r.Context(), tenantID)
		if err != nil {
			writeServiceError(w, err, "列举强制 pin 失败")
			return
		}
		response := make([]overrideResponse, 0, len(items))
		for _, item := range items {
			response = append(response, toResponse(item))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": response})
	}
}

func newCreateHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, tenantID, ok := resolveTenantIdentity(w, r, deps)
		if !ok {
			return
		}
		var request createRequest
		if !decodeJSON(w, r, &request) {
			return
		}
		model := strings.TrimSpace(request.Model)
		accountIDs, err := normalizeProviderAccountIDs(request.ProviderAccountIDs)
		if request.PoolGroupID <= 0 || model == "" || err != nil {
			writeError(w, http.StatusBadRequest, "invalid_model_routing_override", "pool_group_id、model 与非空正整数 provider_account_ids 必填")
			return
		}
		created, err := deps.Service.Create(r.Context(), CreateInput{
			TenantID: tenantID, PoolGroupID: request.PoolGroupID, Model: model,
			ProviderAccountIDs: accountIDs, Enabled: boolOrDefault(request.Enabled, true),
			Audit: routingMutationAudit(r, identity),
		})
		if err != nil {
			writeServiceError(w, err, "创建强制 pin 失败")
			return
		}
		writeJSON(w, http.StatusCreated, toResponse(created))
	}
}

func newUpdateHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, tenantID, ok := resolveTenantIdentity(w, r, deps)
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
		if request.ProviderAccountIDs == nil && request.Enabled == nil {
			writeError(w, http.StatusBadRequest, "empty_update", "至少提供 provider_account_ids 或 enabled")
			return
		}
		if request.ProviderAccountIDs != nil {
			normalized, err := normalizeProviderAccountIDs(*request.ProviderAccountIDs)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid_provider_account_ids", "provider_account_ids 必须是非空正整数数组")
				return
			}
			request.ProviderAccountIDs = &normalized
		}
		updated, err := deps.Service.Update(r.Context(), UpdateInput{
			ID: id, TenantID: tenantID, ProviderAccountIDs: request.ProviderAccountIDs, Enabled: request.Enabled,
			Audit: routingMutationAudit(r, identity),
		})
		if err != nil {
			writeServiceError(w, err, "更新强制 pin 失败")
			return
		}
		writeJSON(w, http.StatusOK, toResponse(updated))
	}
}

func newDeleteHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, tenantID, ok := resolveTenantIdentity(w, r, deps)
		if !ok {
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		if err := deps.Service.Delete(r.Context(), id, tenantID, routingMutationAudit(r, identity)); err != nil {
			writeServiceError(w, err, "删除强制 pin 失败")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// resolveTenant 复用 admin 身份的租户授权语义，tenant_operator 只能管理自身 scope。
func resolveTenant(w http.ResponseWriter, r *http.Request, deps Deps) (int64, bool) {
	_, tenantID, ok := resolveTenantIdentity(w, r, deps)
	return tenantID, ok
}

func resolveTenantIdentity(w http.ResponseWriter, r *http.Request, deps Deps) (admin.AdminIdentity, int64, bool) {
	if deps.Auth == nil || deps.Service == nil {
		writeError(w, http.StatusServiceUnavailable, "model_routing_overrides_not_configured", "强制 pin 管理依赖未配置")
		return admin.AdminIdentity{}, 0, false
	}
	identity, err := deps.Auth.Resolve(r.Context(), r)
	if err != nil {
		writeAdminAuthError(w, err)
		return admin.AdminIdentity{}, 0, false
	}
	if identity.Role != admin.RoleTenantOperator && identity.Role != admin.RolePlatformAdmin {
		writeError(w, http.StatusForbidden, "admin_forbidden_scope", "需要 admin 角色")
		return admin.AdminIdentity{}, 0, false
	}
	if identity.Role == admin.RoleTenantOperator && identity.ScopeTenantID <= 0 {
		writeError(w, http.StatusForbidden, "admin_tenant_scope_required", "tenant_operator 缺少租户 scope")
		return admin.AdminIdentity{}, 0, false
	}

	rawTenantID := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	var tenantID int64
	if rawTenantID == "" {
		if identity.Role != admin.RoleTenantOperator {
			writeError(w, http.StatusBadRequest, "tenant_id_required", "platform_admin 必须提供 tenant_id")
			return admin.AdminIdentity{}, 0, false
		}
		tenantID = identity.ScopeTenantID
	} else {
		tenantID, err = strconv.ParseInt(rawTenantID, 10, 64)
		if err != nil || tenantID <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_tenant_id", "tenant_id 必须是正整数")
			return admin.AdminIdentity{}, 0, false
		}
	}
	if err := identity.CanIssueForTenant(tenantID); err != nil {
		writeAdminAuthError(w, err)
		return admin.AdminIdentity{}, 0, false
	}
	return identity, tenantID, true
}

func routingMutationAudit(r *http.Request, identity admin.AdminIdentity) MutationAudit {
	return MutationAudit{
		ActorID:   identity.AuditActor(),
		ActorRole: identity.Role,
		RequestID: middleware.GetReqID(r.Context()),
	}
}

func parsePathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(r, "id")), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_model_routing_override_id", "资源 id 必须是正整数")
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
	return true
}

func writeServiceError(w http.ResponseWriter, err error, operation string) {
	switch {
	case errors.Is(err, ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid_model_routing_override", "强制 pin 输入无效")
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "model_routing_override_not_found", "强制 pin 不存在")
	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, "model_routing_override_conflict", "同一池组和模型已存在强制 pin")
	case errors.Is(err, ErrPoolNotOwned):
		writeError(w, http.StatusUnprocessableEntity, "pool_group_not_owned", "池组不存在或不属于目标租户")
	case errors.Is(err, ErrAccountsNotOwned):
		writeError(w, http.StatusUnprocessableEntity, "provider_accounts_not_owned", "账号必须全部属于目标租户和池组")
	default:
		writeError(w, http.StatusServiceUnavailable, "model_routing_overrides_backend_error", fmt.Sprintf("%s：%v", operation, err))
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

func toResponse(item Override) overrideResponse {
	return overrideResponse{
		ID: item.ID, PoolGroupID: item.PoolGroupID, Model: item.Model,
		ProviderAccountIDs: append([]int64(nil), item.ProviderAccountIDs...), Enabled: item.Enabled,
		CreatedAt: item.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: item.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func boolOrDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}
