package accountintakehttp

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
)

type oauthAccountIntakeClientRequest struct {
	ClientID     string   `json:"client_id,omitempty"`
	ClientSecret string   `json:"client_secret,omitempty"`
	AuthURL      string   `json:"auth_url,omitempty"`
	TokenURL     string   `json:"token_url,omitempty"`
	RedirectURI  string   `json:"redirect_uri,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
	Source       string   `json:"source,omitempty"`
}

type oauthAccountIntakeStartRequest struct {
	TenantID        int64                           `json:"tenant_id"`
	Vendor          string                          `json:"vendor"`
	AuthMode        string                          `json:"auth_mode"`
	Account         accountintake.AccountDefaults   `json:"account"`
	RedirectURI     string                          `json:"redirect_uri,omitempty"`
	RequestedScopes []string                        `json:"requested_scopes,omitempty"`
	OAuthClient     oauthAccountIntakeClientRequest `json:"oauth_client,omitempty"`
	Reason          string                          `json:"reason,omitempty"`
}

type oauthAccountIntakeCallbackRequest struct {
	FlowID string `json:"flow_id"`
	State  string `json:"state"`
	Code   string `json:"code"`
}

type oauthAccountIntakeFlowRequest struct {
	TenantID int64  `json:"tenant_id"`
	FlowID   string `json:"flow_id"`
}

type oauthAccountIntakeExecuteRequest struct {
	TenantID      int64    `json:"tenant_id"`
	FlowID        string   `json:"flow_id"`
	PlanHash      string   `json:"plan_hash"`
	Confirmations []string `json:"confirmations,omitempty"`
	Reason        string   `json:"reason,omitempty"`
}

func newOAuthAccountIntakeStartHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdminAccountIntake(w, r, d)
		if !ok {
			return
		}
		if d.OAuthService == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "oauth account intake dependency unset")
			return
		}
		var req oauthAccountIntakeStartRequest
		if !decodeAccountIntakeJSON(w, r, &req) {
			return
		}
		defer func() { req.OAuthClient.ClientSecret = "" }()
		if !validateAccountIntakeTenant(w, ident, req.TenantID) {
			return
		}
		result, err := d.OAuthService.Start(r.Context(), accountintake.OAuthStartInput{
			TenantID: req.TenantID, Vendor: req.Vendor, AuthMode: req.AuthMode, Account: req.Account,
			ActorID: ident.AuditActor(), ActorRole: ident.Role, RequestID: middleware.GetReqID(r.Context()), Reason: req.Reason,
			RedirectURI: req.RedirectURI, RequestedScopes: append([]string(nil), req.RequestedScopes...),
			Client: credentialacq.OAuthClientConfig{
				ClientID: req.OAuthClient.ClientID, ClientSecret: req.OAuthClient.ClientSecret,
				AuthURL: req.OAuthClient.AuthURL, TokenURL: req.OAuthClient.TokenURL,
				RedirectURI: req.OAuthClient.RedirectURI, Scopes: append([]string(nil), req.OAuthClient.Scopes...),
				Source: req.OAuthClient.Source,
			},
		})
		if err != nil {
			writeAdminAccountIntakeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

func newOAuthAccountIntakeCallbackHandler(d Deps, authenticated bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.OAuthService == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "oauth account intake dependency unset")
			return
		}
		var req oauthAccountIntakeCallbackRequest
		if authenticated {
			ident, ok := resolveAdminAccountIntake(w, r, d)
			if !ok {
				return
			}
			if !decodeAccountIntakeJSON(w, r, &req) {
				return
			}
			result, err := d.OAuthService.CallbackForActor(
				r.Context(), req.FlowID, req.State, req.Code,
				ident.ScopeTenantID, ident.AuditActor(), ident.Role,
			)
			if err != nil {
				writeAdminAccountIntakeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, result)
			return
		}
		req.FlowID = strings.TrimSpace(r.URL.Query().Get("flow_id"))
		req.State = strings.TrimSpace(r.URL.Query().Get("state"))
		req.Code = strings.TrimSpace(r.URL.Query().Get("code"))
		result, err := d.OAuthService.Callback(r.Context(), req.FlowID, req.State, req.Code)
		if err != nil {
			writeAdminAccountIntakeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func newOAuthAccountIntakePollHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, req, ok := resolveOAuthFlowRequest(w, r, d)
		if !ok {
			return
		}
		result, retryAfter, err := d.OAuthService.Poll(r.Context(), req.TenantID, ident.AuditActor(), req.FlowID, middleware.GetReqID(r.Context()))
		if errors.Is(err, credentialacq.ErrDevicePollPending) || errors.Is(err, credentialacq.ErrDevicePollInProgress) {
			seconds := int((retryAfter + time.Second - 1) / time.Second)
			writeJSON(w, http.StatusAccepted, map[string]any{"flow": result.Flow, "retry_after_seconds": seconds})
			return
		}
		if err != nil {
			writeAdminAccountIntakeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func newOAuthAccountIntakePlanHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, req, ok := resolveOAuthFlowRequest(w, r, d)
		if !ok {
			return
		}
		result, err := d.OAuthService.Plan(r.Context(), req.TenantID, ident.AuditActor(), req.FlowID)
		if err != nil {
			writeAdminAccountIntakeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func newOAuthAccountIntakeExecuteHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdminAccountIntake(w, r, d)
		if !ok {
			return
		}
		if d.OAuthService == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "oauth account intake dependency unset")
			return
		}
		var req oauthAccountIntakeExecuteRequest
		if !decodeAccountIntakeJSON(w, r, &req) || !validateAccountIntakeTenant(w, ident, req.TenantID) {
			return
		}
		result, err := d.OAuthService.Execute(r.Context(), accountintake.OAuthExecuteInput{
			TenantID: req.TenantID, FlowID: req.FlowID, PlanHash: req.PlanHash, Confirmations: req.Confirmations,
			ActorID: ident.AuditActor(), ActorRole: ident.Role, RequestID: middleware.GetReqID(r.Context()), Reason: req.Reason,
		})
		if err != nil {
			writeAdminAccountIntakeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func resolveOAuthFlowRequest(w http.ResponseWriter, r *http.Request, d Deps) (admin.AdminIdentity, oauthAccountIntakeFlowRequest, bool) {
	ident, ok := resolveAdminAccountIntake(w, r, d)
	if !ok {
		return admin.AdminIdentity{}, oauthAccountIntakeFlowRequest{}, false
	}
	if d.OAuthService == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "oauth account intake dependency unset")
		return admin.AdminIdentity{}, oauthAccountIntakeFlowRequest{}, false
	}
	var req oauthAccountIntakeFlowRequest
	if !decodeAccountIntakeJSON(w, r, &req) || !validateAccountIntakeTenant(w, ident, req.TenantID) {
		return admin.AdminIdentity{}, oauthAccountIntakeFlowRequest{}, false
	}
	return ident, req, true
}
