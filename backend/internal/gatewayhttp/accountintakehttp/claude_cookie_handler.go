package accountintakehttp

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/claudecookie"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
)

type claudeCookiePlanRequest struct {
	TenantID       int64                         `json:"tenant_id"`
	SessionKey     string                        `json:"session_key"`
	OrganizationID string                        `json:"organization_id,omitempty"`
	SetupToken     bool                          `json:"setup_token,omitempty"`
	Account        accountintake.AccountDefaults `json:"account"`
	Reason         string                        `json:"reason,omitempty"`
}

type claudeCookieExecuteRequest struct {
	TenantID      int64    `json:"tenant_id"`
	FlowID        string   `json:"flow_id"`
	PlanHash      string   `json:"plan_hash"`
	Confirmations []string `json:"confirmations,omitempty"`
	Reason        string   `json:"reason,omitempty"`
}

func newClaudeCookiePlanHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdminAccountIntake(w, r, d)
		if !ok {
			return
		}
		if d.CookieService == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "Claude Cookie intake dependency unset")
			return
		}
		var req claudeCookiePlanRequest
		if !decodeAccountIntakeJSON(w, r, &req) {
			return
		}
		defer func() { req.SessionKey = "" }()
		if !validateAccountIntakeTenant(w, ident, req.TenantID) {
			return
		}
		result, err := d.CookieService.Plan(r.Context(), accountintake.CookiePlanInput{
			TenantID: req.TenantID, SessionKey: req.SessionKey, OrganizationID: req.OrganizationID,
			SetupToken: req.SetupToken, Account: req.Account,
			ActorID: ident.AuditActor(), ActorRole: ident.Role,
			RequestID: middleware.GetReqID(r.Context()), Reason: req.Reason,
		})
		if err != nil {
			writeClaudeCookieError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func newClaudeCookieExecuteHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdminAccountIntake(w, r, d)
		if !ok {
			return
		}
		if d.CookieService == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "Claude Cookie intake dependency unset")
			return
		}
		var req claudeCookieExecuteRequest
		if !decodeAccountIntakeJSON(w, r, &req) {
			return
		}
		if !validateAccountIntakeTenant(w, ident, req.TenantID) {
			return
		}
		result, err := d.CookieService.Execute(r.Context(), accountintake.CookieExecuteInput{
			TenantID: req.TenantID, FlowID: req.FlowID, PlanHash: req.PlanHash,
			Confirmations: req.Confirmations, ActorID: ident.AuditActor(), ActorRole: ident.Role,
			RequestID: middleware.GetReqID(r.Context()), Reason: req.Reason,
		})
		if err != nil {
			writeClaudeCookieError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func writeClaudeCookieError(w http.ResponseWriter, err error) {
	var choice *claudecookie.OrganizationChoiceError
	switch {
	case errors.As(err, &choice):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": map[string]string{
				"code":    "claude_organization_choice_required",
				"message": "该会话包含多个组织，请明确选择后重新预检",
			},
			"organizations": choice.Organizations,
		})
	case errors.Is(err, claudecookie.ErrOrganizationNotFound):
		writeJSONError(w, http.StatusBadRequest, "claude_organization_not_found", "所选组织不属于该会话")
	case errors.Is(err, claudecookie.ErrInvalidSession):
		writeJSONError(w, http.StatusUnprocessableEntity, "claude_cookie_invalid", "Claude 会话无效、已过期或没有可用组织")
	case errors.Is(err, claudecookie.ErrUpstreamRejected), errors.Is(err, claudecookie.ErrInvalidResponse):
		writeJSONError(w, http.StatusBadGateway, "claude_cookie_exchange_failed", "Claude Cookie 转换失败，请检查会话状态后重试")
	case errors.Is(err, accountintake.ErrStagedCredentialNotFound):
		writeJSONError(w, http.StatusNotFound, "credential_flow_not_found", "短期凭据流程不存在")
	case errors.Is(err, accountintake.ErrStagedCredentialExpired):
		writeJSONError(w, http.StatusGone, "credential_flow_expired", "短期凭据流程已过期，请重新预检")
	case errors.Is(err, accountintake.ErrStagedCredentialReplay):
		writeJSONError(w, http.StatusConflict, "credential_flow_replayed", "短期凭据流程已被领取，不可重复执行")
	default:
		writeAdminAccountIntakeError(w, err)
	}
}
