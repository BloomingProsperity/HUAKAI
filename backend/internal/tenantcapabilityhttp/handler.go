// Package tenantcapabilityhttp 提供部署治理主体管理租户能力授权的 HTTP 合同。
package tenantcapabilityhttp

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
	"github.com/BloomingProsperity/HUAKAI/internal/tenantcapability"
)

const bodyLimit = 32 << 10

type AdminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type Service interface {
	List(context.Context, int64) ([]tenantcapability.Grant, error)
	Set(context.Context, tenantcapability.Mutation) (tenantcapability.Grant, error)
}

type Deps struct {
	Auth    AdminAuth
	Service Service
}

type mutationRequest struct {
	TenantID  int64      `json:"tenant_id"`
	Enabled   *bool      `json:"enabled"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Reason    string     `json:"reason"`
}

func Mount(r chi.Router, deps Deps) {
	r.Get("/", listHandler(deps))
	r.Put("/{capability}", mutationHandler(deps))
}

func listHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, ok := resolveDeployer(w, r, deps)
		if !ok {
			return
		}
		tenantID, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("tenant_id")), 10, 64)
		if err != nil || tenantID <= 0 {
			writeError(w, http.StatusBadRequest, "tenant_capability_invalid", "tenant_id must be positive")
			return
		}
		grants, err := deps.Service.List(r.Context(), tenantID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"tenant_id": tenantID, "grants": grants, "known_capabilities": tenantcapability.All(),
		})
	}
}

func mutationHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := resolveDeployer(w, r, deps)
		if !ok {
			return
		}
		capability, err := tenantcapability.Parse(chi.URLParam(r, "capability"))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		var request mutationRequest
		if !decode(w, r, &request) {
			return
		}
		if request.Enabled == nil {
			writeError(w, http.StatusBadRequest, "tenant_capability_invalid", "enabled is required")
			return
		}
		grant, err := deps.Service.Set(r.Context(), tenantcapability.Mutation{
			TenantID: request.TenantID, Capability: capability, Enabled: *request.Enabled,
			ExpiresAt: request.ExpiresAt, ActorID: identity.AuditActor(), Reason: request.Reason,
		})
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, grant)
	}
}

func resolveDeployer(w http.ResponseWriter, r *http.Request, deps Deps) (admin.AdminIdentity, bool) {
	if deps.Auth == nil || deps.Service == nil {
		writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "tenant capability dependency unset")
		return admin.AdminIdentity{}, false
	}
	identity, err := deps.Auth.Resolve(r.Context(), r)
	if err != nil {
		if errors.Is(err, admin.ErrAdminBackend) {
			writeError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
		} else {
			writeError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
		}
		return admin.AdminIdentity{}, false
	}
	if identity.Source == admin.AdminSourceSession || identity.Role != admin.RolePlatformAdmin || identity.ScopeTenantID != 0 {
		writeError(w, http.StatusForbidden, "admin_forbidden", "deployment platform_admin token required")
		return admin.AdminIdentity{}, false
	}
	return identity, true
}

func decode(w http.ResponseWriter, r *http.Request, destination any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be one valid JSON object")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must contain exactly one JSON object")
		return false
	}
	return true
}

func writeServiceError(w http.ResponseWriter, err error) {
	if errors.Is(err, tenantcapability.ErrInvalid) {
		writeError(w, http.StatusBadRequest, "tenant_capability_invalid", "tenant capability request is invalid")
		return
	}
	writeError(w, http.StatusServiceUnavailable, "tenant_capability_failed", "tenant capability service is temporarily unavailable")
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
