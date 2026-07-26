// Package tenantadminhttp 提供部署者管理租户生命周期的 HTTP 合同。
package tenantadminhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauth"
	"github.com/BloomingProsperity/HUAKAI/internal/tenantadmin"
)

const bodyLimit = 16 << 10

type AdminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type Service interface {
	List(context.Context) ([]tenantadmin.Tenant, error)
	Get(context.Context, int64) (tenantadmin.Tenant, error)
	Create(context.Context, tenantadmin.CreateInput) (tenantadmin.CreateResult, error)
	SetStatus(context.Context, tenantadmin.StatusInput) (tenantadmin.StatusResult, error)
	InspectDelete(context.Context, int64) (tenantadmin.DeleteImpact, error)
	Delete(context.Context, tenantadmin.DeleteInput) (tenantadmin.DeleteResult, error)
}

type Deps struct {
	Auth    AdminAuth
	Service Service
}

type createRequest struct {
	Name             string `json:"name"`
	AdminEmail       string `json:"admin_email"`
	AdminDisplayName string `json:"admin_display_name,omitempty"`
	AdminPassword    string `json:"admin_password"`
	Reason           string `json:"reason"`
}

type statusRequest struct {
	Status          string `json:"status"`
	ExpectedVersion int64  `json:"expected_version"`
	Reason          string `json:"reason"`
}

type deleteRequest struct {
	ExpectedVersion int64  `json:"expected_version"`
	ImpactHash      string `json:"impact_hash"`
	Reason          string `json:"reason"`
}

func Mount(r chi.Router, deps Deps) {
	safe := adminsessionauth.AllowSessionWrite(adminsessionauth.SessionSafe)
	r.Get("/", listHandler(deps))
	r.With(safe).Post("/", createHandler(deps))
	r.Get("/{id}", getHandler(deps))
	r.With(safe).Patch("/{id}/status", statusHandler(deps))
	r.Get("/{id}/delete-impact", deleteImpactHandler(deps))
	r.With(safe).Delete("/{id}", deleteHandler(deps))
}

func listHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolvePlatformAdmin(w, r, deps); !ok {
			return
		}
		items, err := deps.Service.List(r.Context())
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

func getHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolvePlatformAdmin(w, r, deps); !ok {
			return
		}
		tenantID, ok := pathTenantID(w, r)
		if !ok {
			return
		}
		item, err := deps.Service.Get(r.Context(), tenantID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

func createHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := resolvePlatformAdmin(w, r, deps)
		if !ok {
			return
		}
		var request createRequest
		if !decodeJSON(w, r, &request) {
			return
		}
		result, err := deps.Service.Create(r.Context(), tenantadmin.CreateInput{
			Name: request.Name, AdminEmail: request.AdminEmail,
			AdminDisplayName: request.AdminDisplayName, AdminPassword: request.AdminPassword,
			Audit: auditInput(r, identity, request.Reason),
		})
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

func statusHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := resolvePlatformAdmin(w, r, deps)
		if !ok {
			return
		}
		tenantID, ok := pathTenantID(w, r)
		if !ok {
			return
		}
		var request statusRequest
		if !decodeJSON(w, r, &request) {
			return
		}
		result, err := deps.Service.SetStatus(r.Context(), tenantadmin.StatusInput{
			TenantID: tenantID, Status: strings.TrimSpace(request.Status),
			ExpectedVersion: request.ExpectedVersion,
			Audit:           auditInput(r, identity, request.Reason),
		})
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func deleteImpactHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolvePlatformAdmin(w, r, deps); !ok {
			return
		}
		tenantID, ok := pathTenantID(w, r)
		if !ok {
			return
		}
		impact, err := deps.Service.InspectDelete(r.Context(), tenantID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, impact)
	}
}

func deleteHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := resolvePlatformAdmin(w, r, deps)
		if !ok {
			return
		}
		tenantID, ok := pathTenantID(w, r)
		if !ok {
			return
		}
		var request deleteRequest
		if !decodeJSON(w, r, &request) {
			return
		}
		result, err := deps.Service.Delete(r.Context(), tenantadmin.DeleteInput{
			TenantID: tenantID, ExpectedVersion: request.ExpectedVersion,
			ImpactHash: request.ImpactHash, Audit: auditInput(r, identity, request.Reason),
		})
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func resolvePlatformAdmin(w http.ResponseWriter, r *http.Request, deps Deps) (admin.AdminIdentity, bool) {
	if deps.Auth == nil || deps.Service == nil {
		writeError(w, http.StatusServiceUnavailable, "tenant_admin_not_configured", "租户管理依赖未配置")
		return admin.AdminIdentity{}, false
	}
	identity, err := deps.Auth.Resolve(r.Context(), r)
	if err != nil {
		if errors.Is(err, admin.ErrAdminBackend) {
			writeError(w, http.StatusServiceUnavailable, "admin_backend_error", "管理员认证后端暂时不可用")
		} else {
			writeError(w, http.StatusUnauthorized, "admin_unauthorized", "管理员凭据无效")
		}
		return admin.AdminIdentity{}, false
	}
	if identity.Role != admin.RolePlatformAdmin {
		writeError(w, http.StatusForbidden, "admin_forbidden", "仅部署管理员可以管理租户生命周期")
		return admin.AdminIdentity{}, false
	}
	return identity, true
}

func pathTenantID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(r, "id")), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_tenant_id", "tenant id 必须是正整数")
		return 0, false
	}
	return id, true
}

func auditInput(r *http.Request, identity admin.AdminIdentity, reason string) tenantadmin.AuditInput {
	requestID := middleware.GetReqID(r.Context())
	if requestID == "" {
		requestID = strings.TrimSpace(r.Header.Get("X-Request-ID"))
	}
	return tenantadmin.AuditInput{
		ActorID: identity.AuditActor(), ActorRole: identity.Role,
		RequestID: requestID, Reason: reason,
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
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
	case errors.Is(err, tenantadmin.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "tenant_invalid", "租户请求参数无效")
	case errors.Is(err, tenantadmin.ErrNotFound):
		writeError(w, http.StatusNotFound, "tenant_not_found", "租户不存在")
	case errors.Is(err, tenantadmin.ErrPlatformTenant):
		writeError(w, http.StatusForbidden, "platform_tenant_protected", "平台工作租户不可停用或删除")
	case errors.Is(err, tenantadmin.ErrVersionConflict):
		writeError(w, http.StatusConflict, "tenant_version_conflict", "租户已被其他操作更新，请刷新后重试")
	case errors.Is(err, tenantadmin.ErrImpactChanged):
		writeError(w, http.StatusConflict, "tenant_delete_impact_changed", "租户影响面已变化，请重新预检")
	case errors.Is(err, tenantadmin.ErrDeleteBlocked):
		writeError(w, http.StatusConflict, "tenant_delete_blocked", "租户仍有余额、在途请求或待恢复事项")
	case errors.Is(err, tenantadmin.ErrInvalidTransition):
		writeError(w, http.StatusConflict, "tenant_status_conflict", "租户当前状态不允许该操作")
	case errors.Is(err, tenantadmin.ErrConflict):
		writeError(w, http.StatusConflict, "tenant_conflict", "租户名称或关联资源发生冲突")
	default:
		writeError(w, http.StatusServiceUnavailable, "tenant_admin_failed", "租户管理暂时不可用")
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
