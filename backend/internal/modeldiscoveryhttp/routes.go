// Package modeldiscoveryhttp 暴露全局模型发现箱的运维接口。
package modeldiscoveryhttp

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
	"github.com/BloomingProsperity/HUAKAI/internal/modelsync"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

type adminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type discoveryStore interface {
	ListModelDiscoveries(context.Context, registry.ModelDiscoveryListParams) (registry.ModelDiscoveryPage, error)
	PromoteModelDiscovery(context.Context, registry.ModelDiscoveryDecision) (registry.ModelDiscovery, error)
	IgnoreModelDiscovery(context.Context, registry.ModelDiscoveryDecision) (registry.ModelDiscovery, error)
}

type Deps struct {
	Auth  adminAuth
	Store discoveryStore
}

type decisionRequest struct {
	Reason string `json:"reason"`
}

type pageResponse struct {
	Object       string                    `json:"object"`
	Items        []registry.ModelDiscovery `json:"items"`
	NextBeforeID *int64                    `json:"next_before_id"`
}

type itemResponse struct {
	Object    string                  `json:"object"`
	Discovery registry.ModelDiscovery `json:"discovery"`
}

func MountRoutes(router chi.Router, deps Deps) {
	router.Get("/", newListHandler(deps))
	router.With(adminsessionauth.AllowSessionWrite(adminsessionauth.SessionSafe)).Post("/{id}/promote", newDecisionHandler(deps, true))
	router.With(adminsessionauth.AllowSessionWrite(adminsessionauth.SessionSafe)).Post("/{id}/ignore", newDecisionHandler(deps, false))
}

func newListHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := resolvePlatformAdmin(w, r, deps)
		if !ok {
			return
		}
		params, ok := parseListParams(w, r, identity)
		if !ok {
			return
		}
		page, err := deps.Store.ListModelDiscoveries(r.Context(), params)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		if page.Items == nil {
			page.Items = []registry.ModelDiscovery{}
		}
		writeJSON(w, http.StatusOK, pageResponse{
			Object: "model_discovery_page", Items: page.Items, NextBeforeID: page.NextBeforeID,
		})
	}
}

func newDecisionHandler(deps Deps, promote bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := resolvePlatformAdmin(w, r, deps)
		if !ok {
			return
		}
		id, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(r, "id")), 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_model_discovery_id", "model discovery id must be a positive integer")
			return
		}
		var body decisionRequest
		if !decodeJSON(w, r, &body) {
			return
		}
		decision := registry.ModelDiscoveryDecision{
			Access: registry.ModelDiscoveryAccess{
				Role: identity.Role, Actor: identity.AuditActor(), RequestID: middleware.GetReqID(r.Context()),
			},
			ID: id, Reason: body.Reason,
		}
		var item registry.ModelDiscovery
		if promote {
			item, err = deps.Store.PromoteModelDiscovery(r.Context(), decision)
		} else {
			item, err = deps.Store.IgnoreModelDiscovery(r.Context(), decision)
		}
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, itemResponse{Object: "model_discovery", Discovery: item})
	}
}

func resolvePlatformAdmin(w http.ResponseWriter, r *http.Request, deps Deps) (admin.AdminIdentity, bool) {
	if deps.Auth == nil || deps.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "model discovery dependency unset")
		return admin.AdminIdentity{}, false
	}
	identity, err := deps.Auth.Resolve(r.Context(), r)
	if err != nil {
		writeAuthError(w, err)
		return admin.AdminIdentity{}, false
	}
	if identity.Role != admin.RolePlatformAdmin {
		writeError(w, http.StatusForbidden, "admin_forbidden", "platform_admin role required")
		return admin.AdminIdentity{}, false
	}
	return identity, true
}

func parseListParams(w http.ResponseWriter, r *http.Request, identity admin.AdminIdentity) (registry.ModelDiscoveryListParams, bool) {
	params := registry.ModelDiscoveryListParams{
		Access: registry.ModelDiscoveryAccess{Role: identity.Role, Actor: identity.AuditActor()},
		Vendor: modelsync.Vendor(strings.TrimSpace(r.URL.Query().Get("vendor"))),
		Status: strings.TrimSpace(r.URL.Query().Get("status")),
		Search: strings.TrimSpace(r.URL.Query().Get("search")),
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("before_id")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_before_id", "before_id must be a positive integer")
			return registry.ModelDiscoveryListParams{}, false
		}
		params.BeforeID = value
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be a positive integer")
			return registry.ModelDiscoveryListParams{}, false
		}
		params.Limit = value
	}
	return params, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst *decisionRequest) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<14)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid_json", "request body is required")
		} else {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		}
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must contain one JSON object")
		return false
	}
	return true
}

func writeAuthError(w http.ResponseWriter, err error) {
	if errors.Is(err, admin.ErrAdminBackend) {
		writeError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
		return
	}
	writeError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
}

func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, registry.ErrModelDiscoveryInvalid):
		writeError(w, http.StatusBadRequest, "invalid_model_discovery", "model discovery input is invalid")
	case errors.Is(err, registry.ErrModelDiscoveryForbidden):
		writeError(w, http.StatusForbidden, "admin_forbidden", "platform_admin role required")
	case errors.Is(err, registry.ErrModelDiscoveryNotFound):
		writeError(w, http.StatusNotFound, "model_discovery_not_found", "model discovery was not found")
	case errors.Is(err, registry.ErrModelDiscoveryConflict):
		writeError(w, http.StatusConflict, "model_discovery_conflict", "model discovery state or registry identity conflicts")
	case errors.Is(err, registry.ErrRegistryBackend):
		writeError(w, http.StatusServiceUnavailable, "model_discovery_backend_error", "model discovery backend unavailable")
	default:
		writeError(w, http.StatusInternalServerError, "model_discovery_internal_error", "model discovery operation failed")
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]map[string]string{"error": {"code": code, "message": message}})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
