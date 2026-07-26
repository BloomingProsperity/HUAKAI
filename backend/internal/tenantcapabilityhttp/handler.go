// Package tenantcapabilityhttp 提供部署者管理租户能力授权的 HTTP 合同。
package tenantcapabilityhttp

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
	"github.com/BloomingProsperity/HUAKAI/internal/tenantcapability"
)

const bodyLimit = 16 << 10

type AdminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type Store interface {
	List(context.Context, int64) ([]tenantcapability.Grant, error)
	Set(context.Context, tenantcapability.SetInput) (tenantcapability.SetResult, error)
}

type Deps struct {
	Auth  AdminAuth
	Store Store
}

type setRequest struct {
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason"`
}

func Mount(r chi.Router, d Deps) {
	r.Get("/", listHandler(d))
	r.With(adminsessionauth.AllowSessionWrite(adminsessionauth.SessionSafe)).
		Put("/{tenantID}/{capability}", setHandler(d))
}

func listHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, ok := resolvePlatformAdmin(w, r, d)
		if !ok {
			return
		}
		tenantID, ok := parseTenantID(w, r.URL.Query().Get("tenant_id"))
		if !ok {
			return
		}
		grants, err := d.Store.List(r.Context(), tenantID)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "tenant_capability_failed", "租户能力查询暂时不可用")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": grants})
	}
}

func setHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := resolvePlatformAdmin(w, r, d)
		if !ok {
			return
		}
		tenantID, ok := parseTenantID(w, chi.URLParam(r, "tenantID"))
		if !ok {
			return
		}
		var req setRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := d.Store.Set(r.Context(), tenantcapability.SetInput{
			TenantID: tenantID, Capability: chi.URLParam(r, "capability"), Enabled: req.Enabled,
			Actor: identity.AuditActor(), ActorRole: identity.Role, Reason: req.Reason,
			RequestID: middleware.GetReqID(r.Context()),
		})
		if err != nil {
			switch {
			case errors.Is(err, tenantcapability.ErrInvalidInput), errors.Is(err, tenantcapability.ErrCapabilityUnknown):
				writeError(w, http.StatusBadRequest, "tenant_capability_invalid", err.Error())
			case errors.Is(err, tenantcapability.ErrTenantNotFound):
				writeError(w, http.StatusNotFound, "tenant_not_found", "租户不存在")
			default:
				writeError(w, http.StatusServiceUnavailable, "tenant_capability_failed", "租户能力更新暂时不可用")
			}
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func resolvePlatformAdmin(w http.ResponseWriter, r *http.Request, d Deps) (admin.AdminIdentity, bool) {
	if d.Auth == nil || d.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "租户能力依赖未配置")
		return admin.AdminIdentity{}, false
	}
	identity, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		if errors.Is(err, admin.ErrAdminBackend) {
			writeError(w, http.StatusServiceUnavailable, "admin_backend_error", "管理员认证后端暂时不可用")
		} else {
			writeError(w, http.StatusUnauthorized, "admin_unauthorized", "管理员凭据无效")
		}
		return admin.AdminIdentity{}, false
	}
	if identity.Role != admin.RolePlatformAdmin {
		writeError(w, http.StatusForbidden, "admin_forbidden", "仅部署管理员可以管理租户能力")
		return admin.AdminIdentity{}, false
	}
	return identity, true
}

func parseTenantID(w http.ResponseWriter, raw string) (int64, bool) {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || value <= 0 {
		writeError(w, http.StatusBadRequest, "tenant_capability_invalid", "tenant_id 必须是正整数")
		return 0, false
	}
	return value, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求体必须只包含一个 JSON 对象")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
