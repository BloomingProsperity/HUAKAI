// Package claudecookiehttp 提供 Claude Cookie 账号接入的租户管理合同。
package claudecookiehttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/claudecookie"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
)

const requestBodyLimit = 256 << 10

type AdminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type Service interface {
	Convert(context.Context, claudecookie.ConvertInput) (claudecookie.Session, error)
	Plan(context.Context, claudecookie.PlanInput) (accountintake.PlanResult, error)
	Execute(context.Context, claudecookie.ExecuteInput) (accountintake.ExecutionResult, error)
}

type Deps struct {
	Auth    AdminAuth
	Service Service
}

type convertRequest struct {
	TenantID       int64  `json:"tenant_id"`
	SessionKey     string `json:"session_key"`
	OrganizationID string `json:"organization_id,omitempty"`
}

type planRequest struct {
	TenantID  int64                         `json:"tenant_id"`
	SessionID string                        `json:"intake_session_id"`
	Account   accountintake.AccountDefaults `json:"account"`
}

type executeRequest struct {
	planRequest
	PlanHash      string   `json:"plan_hash"`
	Confirmations []string `json:"confirmations,omitempty"`
	Reason        string   `json:"reason,omitempty"`
}

func Mount(r chi.Router, deps Deps) {
	r.Post("/claude-cookie/convert", convertHandler(deps))
	r.Post("/claude-cookie/plan", planHandler(deps))
	r.Post("/claude-cookie/execute", executeHandler(deps))
}

func convertHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := resolve(w, r, deps)
		if !ok {
			return
		}
		var request convertRequest
		if !decodeJSON(w, r, &request) || !validateTenant(w, identity, request.TenantID) {
			request.SessionKey = ""
			return
		}
		result, err := deps.Service.Convert(r.Context(), claudecookie.ConvertInput{
			TenantID: request.TenantID, SessionKey: request.SessionKey, OrganizationID: request.OrganizationID,
			ActorID: identity.AuditActor(), ActorRole: identity.Role, RequestID: middleware.GetReqID(r.Context()),
		})
		request.SessionKey = ""
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

func planHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := resolve(w, r, deps)
		if !ok {
			return
		}
		var request planRequest
		if !decodeJSON(w, r, &request) || !validateTenant(w, identity, request.TenantID) {
			return
		}
		result, err := deps.Service.Plan(r.Context(), claudecookie.PlanInput{
			TenantID: request.TenantID, SessionID: request.SessionID,
			Account: request.Account, ActorID: identity.AuditActor(),
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func executeHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := resolve(w, r, deps)
		if !ok {
			return
		}
		var request executeRequest
		if !decodeJSON(w, r, &request) || !validateTenant(w, identity, request.TenantID) {
			return
		}
		result, err := deps.Service.Execute(r.Context(), claudecookie.ExecuteInput{
			PlanInput: claudecookie.PlanInput{
				TenantID: request.TenantID, SessionID: request.SessionID,
				Account: request.Account, ActorID: identity.AuditActor(),
			},
			PlanHash: request.PlanHash, Confirmations: request.Confirmations,
			ActorRole: identity.Role, RequestID: middleware.GetReqID(r.Context()), Reason: request.Reason,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func resolve(w http.ResponseWriter, r *http.Request, deps Deps) (admin.AdminIdentity, bool) {
	if deps.Auth == nil || deps.Service == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "Claude Cookie intake dependency unset")
		return admin.AdminIdentity{}, false
	}
	identity, err := deps.Auth.Resolve(r.Context(), r)
	if err != nil {
		if errors.Is(err, admin.ErrAdminBackend) {
			writeJSONError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
		} else {
			writeJSONError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
		}
		return admin.AdminIdentity{}, false
	}
	if identity.Source == admin.AdminSourceSession || identity.Role != admin.RoleTenantOperator || identity.ScopeTenantID <= 0 {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "scoped tenant_operator token required")
		return admin.AdminIdentity{}, false
	}
	return identity, true
}

func validateTenant(w http.ResponseWriter, identity admin.AdminIdentity, tenantID int64) bool {
	if tenantID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "claude_cookie_invalid", "tenant_id must be positive")
		return false
	}
	if tenantID != identity.ScopeTenantID {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "tenant scope mismatch")
		return false
	}
	return true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, requestBodyLimit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds 256 KiB")
		} else {
			writeJSONError(w, http.StatusBadRequest, "invalid_json", "request body must be one valid JSON object")
		}
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", "request body must contain exactly one JSON object")
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, err error) {
	var selection *claudecookie.OrganizationSelectionError
	switch {
	case errors.As(err, &selection):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": map[string]string{
				"code": "organization_selection_required", "message": "该会话可访问多个组织，请明确选择后重新提交 Cookie",
			},
			"organizations": selection.Organizations,
		})
	case errors.Is(err, claudecookie.ErrInvalidInput), errors.Is(err, accountintake.ErrInvalidInput):
		writeJSONError(w, http.StatusBadRequest, "claude_cookie_invalid", "Claude Cookie 接入请求无效")
	case errors.Is(err, claudecookie.ErrUpstreamUnauthorized):
		writeJSONError(w, http.StatusUnprocessableEntity, "claude_cookie_rejected", "Cookie 已失效、无权限或无法完成授权")
	case errors.Is(err, claudecookie.ErrSessionNotFound):
		writeJSONError(w, http.StatusNotFound, "intake_session_not_found", "短时接入会话不存在")
	case errors.Is(err, claudecookie.ErrSessionExpired):
		writeJSONError(w, http.StatusGone, "intake_session_expired", "短时接入会话已过期，请重新转换")
	case errors.Is(err, claudecookie.ErrSessionConsumed), errors.Is(err, claudecookie.ErrSessionClosed):
		writeJSONError(w, http.StatusConflict, "intake_session_closed", "短时接入会话已消费或关闭")
	case errors.Is(err, claudecookie.ErrSessionChanged), errors.Is(err, accountintake.ErrPlanChanged):
		writeJSONError(w, http.StatusConflict, "intake_session_changed", "接入会话或账号状态已变化，请重新预检")
	case errors.Is(err, accountintake.ErrPlanHashMissing):
		writeJSONError(w, http.StatusBadRequest, "plan_hash_required", "plan_hash is required")
	case errors.Is(err, pgx.ErrNoRows):
		writeJSONError(w, http.StatusBadRequest, "provider_not_found", "provider does not exist")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "claude_cookie_failed", "Claude Cookie 接入暂时不可用")
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
