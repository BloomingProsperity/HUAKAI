package gatewayhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

type AdminCredentialAcquisitionDeps struct {
	Auth                     AdminCredentialAuth
	Sessions                 *credentialacq.PostgresSessionStore
	Credentials              credentialacq.CredentialCreator
	CredentialAudit          credentialacq.CredentialAuditWriter
	AuditStore               AdminPoolAccountStore
	Exchangers               *credentialacq.ExchangerRegistry
	AllowLongLivedSetupToken bool
}

type credentialAcqStartRequest struct {
	TenantID           int64                  `json:"tenant_id"`
	ProviderAccountID  int64                  `json:"provider_account_id,omitempty"`
	Vendor             string                 `json:"vendor"`
	AuthMode           string                 `json:"auth_mode"`
	FlowKind           credentialacq.FlowKind `json:"flow_kind,omitempty"`
	RedirectURI        string                 `json:"redirect_uri,omitempty"`
	RequestedScopes    []string               `json:"requested_scopes,omitempty"`
	RedactedContext    map[string]any         `json:"redacted_context,omitempty"`
	LongLivedRequested bool                   `json:"long_lived_requested,omitempty"`
	OAuthClient        oauthClientRequest     `json:"oauth_client,omitempty"`
	Reason             string                 `json:"reason,omitempty"`
}

type oauthClientRequest struct {
	ClientID     string   `json:"client_id,omitempty"`
	ClientSecret string   `json:"client_secret,omitempty"`
	AuthURL      string   `json:"auth_url,omitempty"`
	TokenURL     string   `json:"token_url,omitempty"`
	RedirectURI  string   `json:"redirect_uri,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
	Source       string   `json:"source,omitempty"`
}

type credentialAcqCallbackRequest struct {
	State       string          `json:"state"`
	Code        string          `json:"code"`
	Credentials json.RawMessage `json:"credentials,omitempty"`
}

type credentialAcqFinalizeRequest struct {
	Credentials json.RawMessage `json:"credentials"`
	Reason      string          `json:"reason,omitempty"`
}

type credentialAcqHelperRequest struct {
	TenantID          int64                  `json:"tenant_id"`
	ProviderAccountID int64                  `json:"provider_account_id"`
	Vendor            string                 `json:"vendor,omitempty"`
	AuthMode          string                 `json:"auth_mode,omitempty"`
	FlowKind          credentialacq.FlowKind `json:"flow_kind,omitempty"`
	Content           string                 `json:"content,omitempty"`
	Credentials       json.RawMessage        `json:"credentials,omitempty"`
	Finalize          bool                   `json:"finalize,omitempty"`
	Reason            string                 `json:"reason,omitempty"`
	RedactedContext   map[string]any         `json:"redacted_context,omitempty"`
}

func MountAdminCredentialAcquisitionRoutes(r chi.Router, d AdminCredentialAcquisitionDeps) {
	r.Post("/{id}/credential-acquisitions", newCredentialAcqStartHandler(d))
	r.Get("/{id}/credential-acquisitions/{flowID}", newCredentialAcqStatusHandler(d))
	r.Post("/{id}/credential-acquisitions/{flowID}/callback", newCredentialAcqCallbackHandler(d))
	r.Post("/{id}/credential-acquisitions/{flowID}/cancel", newCredentialAcqCancelHandler(d))
	r.Post("/{id}/credential-acquisitions/{flowID}/finalize", newCredentialAcqFinalizeHandler(d))
}

func MountAdminCredentialAcquisitionHelperRoutes(r chi.Router, d AdminCredentialAcquisitionDeps) {
	r.Post("/paste", newCredentialAcqImportHelperHandler(d, credentialacq.FlowKindPaste))
	r.Post("/cli-import", newCredentialAcqImportHelperHandler(d, credentialacq.FlowKindCLIImport))
	r.Post("/csv-import", newCredentialAcqImportHelperHandler(d, credentialacq.FlowKindCSVImport))
	r.Post("/json-import", newCredentialAcqImportHelperHandler(d, credentialacq.FlowKindJSONImport))
	r.Post("/oauth-init", newCredentialAcqOAuthInitHelperHandler(d))
	r.Get("/oauth-callback", newCredentialAcqOAuthCallbackHelperHandler(d))
}

func newCredentialAcqStartHandler(d AdminCredentialAcquisitionDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveCredentialAcqAdmin(w, r, d)
		if !ok {
			return
		}
		accountID, ok := parseAdminPoolID(w, r)
		if !ok {
			return
		}
		var req credentialAcqStartRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		req.ProviderAccountID = accountID
		startCredentialAcqFlow(w, r, d, ident, req)
	}
}

func newCredentialAcqStatusHandler(d AdminCredentialAcquisitionDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveCredentialAcqAdmin(w, r, d); !ok {
			return
		}
		session, err := d.Sessions.Get(r.Context(), chi.URLParam(r, "flowID"))
		if err != nil {
			writeCredentialAcqError(w, err)
			return
		}
		if !credentialAcqFlowMatchesPathAccount(w, r, session) {
			return
		}
		writeAuditJSON(w, http.StatusOK, map[string]any{"flow": session})
	}
}

func newCredentialAcqCallbackHandler(d AdminCredentialAcquisitionDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveCredentialAcqAdmin(w, r, d)
		if !ok {
			return
		}
		var req credentialAcqCallbackRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		flowID := chi.URLParam(r, "flowID")
		existing, err := d.Sessions.Get(r.Context(), flowID)
		if err != nil {
			writeCredentialAcqError(w, err)
			return
		}
		if !credentialAcqFlowMatchesPathAccount(w, r, existing) {
			return
		}
		actorID := fmt.Sprintf("%d", ident.TokenID)
		result, ok := completeCredentialAcqOAuthCallback(w, r, d, actorID, ident.Role, flowID, req.State, req.Code)
		if !ok {
			return
		}
		writeCredentialAcqAdminAudit(r, d, actorID, ident.Role, result.Session, credentialacq.EventCompleted, "完成 OAuth credential acquisition")
		writeAuditJSON(w, http.StatusOK, result)
	}
}

func newCredentialAcqCancelHandler(d AdminCredentialAcquisitionDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveCredentialAcqAdmin(w, r, d)
		if !ok {
			return
		}
		flowID := chi.URLParam(r, "flowID")
		existing, err := d.Sessions.Get(r.Context(), flowID)
		if err != nil {
			writeCredentialAcqError(w, err)
			return
		}
		if !credentialAcqFlowMatchesPathAccount(w, r, existing) {
			return
		}
		session, err := d.Sessions.Cancel(r.Context(), flowID)
		if err != nil {
			writeCredentialAcqError(w, err)
			return
		}
		actorID := fmt.Sprintf("%d", ident.TokenID)
		_ = credentialacq.EmitLifecycleAudit(r.Context(), d.CredentialAudit, session, credentialacq.EventCancelled, 0, actorID, middleware.GetReqID(r.Context()), nil)
		writeCredentialAcqAdminAudit(r, d, actorID, ident.Role, session, credentialacq.EventCancelled, "取消 credential acquisition")
		writeAuditJSON(w, http.StatusOK, map[string]any{"flow": session})
	}
}

func newCredentialAcqFinalizeHandler(d AdminCredentialAcquisitionDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveCredentialAcqAdmin(w, r, d)
		if !ok {
			return
		}
		var req credentialAcqFinalizeRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		if len(req.Credentials) == 0 {
			writeJSONError(w, http.StatusBadRequest, "credentials_required", "credentials JSON object is required for finalize")
			return
		}
		flowID := chi.URLParam(r, "flowID")
		session, err := d.Sessions.Get(r.Context(), flowID)
		if err != nil {
			writeCredentialAcqError(w, err)
			return
		}
		if !credentialAcqFlowMatchesPathAccount(w, r, session) {
			return
		}
		finalizer := credentialacq.NewFinalizer(d.Sessions, credentialstore.DefaultHandlerRegistry(), d.Credentials, d.CredentialAudit)
		actorID := fmt.Sprintf("%d", ident.TokenID)
		result, err := finalizer.Finalize(r.Context(), flowID, credentialacq.CredentialCandidate{
			TenantID: session.TenantID, ProviderAccountID: session.ProviderAccountID,
			Vendor: session.Vendor, AuthMode: session.AuthMode, Payload: req.Credentials, ActorID: actorID,
		}, actorID, middleware.GetReqID(r.Context()))
		if err != nil {
			writeCredentialAcqError(w, err)
			return
		}
		writeCredentialAcqAdminAudit(r, d, actorID, ident.Role, result.Session, credentialacq.EventCompleted, firstReason(req.Reason, "完成 credential acquisition"))
		writeAuditJSON(w, http.StatusOK, result)
	}
}

func newCredentialAcqImportHelperHandler(d AdminCredentialAcquisitionDeps, kind credentialacq.FlowKind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveCredentialAcqAdmin(w, r, d)
		if !ok {
			return
		}
		var req credentialAcqHelperRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		req.FlowKind = kind
		candidates, err := helperCandidates(req)
		if err != nil {
			writeCredentialAcqError(w, err)
			return
		}
		flows := make([]credentialacq.Session, 0, len(candidates))
		results := make([]credentialacq.FinalizeResult, 0, len(candidates))
		actorID := fmt.Sprintf("%d", ident.TokenID)
		for _, candidate := range candidates {
			start := credentialAcqStartRequest{
				TenantID: req.TenantID, ProviderAccountID: req.ProviderAccountID,
				Vendor: firstNonEmptyGateway(req.Vendor, candidate.Vendor), AuthMode: firstNonEmptyGateway(req.AuthMode, candidate.AuthMode),
				FlowKind: kind, RedactedContext: mergeRedactedContext(req.RedactedContext, candidate.RedactedContext),
				Reason: req.Reason,
			}
			session, err := createCredentialAcqSession(r.Context(), d, ident, start)
			if err != nil {
				writeCredentialAcqError(w, err)
				return
			}
			flows = append(flows, session)
			if !req.Finalize {
				continue
			}
			candidate.TenantID = req.TenantID
			candidate.ProviderAccountID = req.ProviderAccountID
			candidate.ActorID = actorID
			finalizer := credentialacq.NewFinalizer(d.Sessions, credentialstore.DefaultHandlerRegistry(), d.Credentials, d.CredentialAudit)
			result, err := finalizer.Finalize(r.Context(), session.ID, candidate, actorID, middleware.GetReqID(r.Context()))
			if err != nil {
				writeCredentialAcqError(w, err)
				return
			}
			results = append(results, result)
		}
		writeAuditJSON(w, http.StatusCreated, map[string]any{"flows": flows, "finalized": results})
	}
}

func newCredentialAcqOAuthInitHelperHandler(d AdminCredentialAcquisitionDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveCredentialAcqAdmin(w, r, d)
		if !ok {
			return
		}
		var req credentialAcqStartRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		req.FlowKind = credentialacq.FlowKindOAuth
		startCredentialAcqFlow(w, r, d, ident, req)
	}
}

func newCredentialAcqOAuthCallbackHelperHandler(d AdminCredentialAcquisitionDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Sessions == nil || d.Credentials == nil || d.AuditStore == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "admin credential acquisition dependency unset")
			return
		}
		flowID := strings.TrimSpace(r.URL.Query().Get("flow_id"))
		state := strings.TrimSpace(r.URL.Query().Get("state"))
		code := strings.TrimSpace(r.URL.Query().Get("code"))
		if flowID == "" {
			writeJSONError(w, http.StatusBadRequest, "invalid_request", "flow_id is required")
			return
		}
		session, err := d.Sessions.Get(r.Context(), flowID)
		if err != nil {
			writeCredentialAcqError(w, err)
			return
		}
		result, ok := completeCredentialAcqOAuthCallback(w, r, d, session.ActorID, session.ActorRole, flowID, state, code)
		if !ok {
			return
		}
		writeCredentialAcqAdminAudit(r, d, session.ActorID, session.ActorRole, result.Session, credentialacq.EventCompleted, "完成 OAuth credential acquisition")
		writeAuditJSON(w, http.StatusOK, result)
	}
}

func completeCredentialAcqOAuthCallback(w http.ResponseWriter, r *http.Request, d AdminCredentialAcquisitionDeps, actorID, actorRole, flowID, state, code string) (credentialacq.FinalizeResult, bool) {
	requestID := middleware.GetReqID(r.Context())
	candidate, session, err := credentialacq.CompleteOAuthCallbackWithRegistry(r.Context(), d.Sessions, flowID, state, code, d.Exchangers)
	if err != nil {
		_ = credentialacq.EmitLifecycleAudit(r.Context(), d.CredentialAudit, session, credentialacq.EventFailed, 0, actorID, requestID, map[string]any{"error_class": "callback_failed"})
		writeCredentialAcqError(w, err)
		return credentialacq.FinalizeResult{Session: session}, false
	}
	finalizer := credentialacq.NewFinalizer(d.Sessions, credentialstore.DefaultHandlerRegistry(), d.Credentials, d.CredentialAudit)
	result, err := finalizer.Finalize(r.Context(), flowID, candidate, actorID, requestID)
	if err != nil {
		writeCredentialAcqError(w, err)
		return result, false
	}
	return result, true
}

func startCredentialAcqFlow(w http.ResponseWriter, r *http.Request, d AdminCredentialAcquisitionDeps, ident admin.AdminIdentity, req credentialAcqStartRequest) {
	session, result, err := createOrStartCredentialAcqSession(r.Context(), d, ident, req, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeCredentialAcqError(w, err)
		return
	}
	writeCredentialAcqAdminAudit(r, d, fmt.Sprintf("%d", ident.TokenID), ident.Role, session, credentialacq.EventStarted, firstReason(req.Reason, "启动 credential acquisition"))
	resp := map[string]any{"flow": session}
	if result.AuthorizeURL != "" {
		resp["authorize_url"] = result.AuthorizeURL
		resp["state"] = result.State
		resp["code_challenge"] = result.CodeChallenge
	}
	writeAuditJSON(w, http.StatusCreated, resp)
}

func createOrStartCredentialAcqSession(ctx context.Context, d AdminCredentialAcquisitionDeps, ident admin.AdminIdentity, req credentialAcqStartRequest, idem string) (credentialacq.Session, credentialacq.OAuthStartResult, error) {
	if req.LongLivedRequested && !d.AllowLongLivedSetupToken {
		return credentialacq.Session{}, credentialacq.OAuthStartResult{}, credentialacq.ErrFeatureDisabled
	}
	if req.ProviderAccountID <= 0 || req.TenantID <= 0 {
		return credentialacq.Session{}, credentialacq.OAuthStartResult{}, errors.New("tenant_id and provider_account_id are required")
	}
	start := credentialacq.StartInput{
		TenantID: req.TenantID, ProviderAccountID: req.ProviderAccountID,
		Vendor: req.Vendor, AuthMode: req.AuthMode, Kind: req.FlowKind,
		ActorID: fmt.Sprintf("%d", ident.TokenID), ActorRole: ident.Role,
		RedirectURI: req.RedirectURI, RequestedScopes: req.RequestedScopes,
		RedactedContext: req.RedactedContext, LongLivedRequested: req.LongLivedRequested,
		IdempotencyKey: idem,
	}
	if start.Kind == credentialacq.FlowKindOAuth {
		oauthReq := req.OAuthClient
		clientSecret := oauthReq.ClientSecret
		// Owner 2026-05-27：Gemini OAuth acquisition 的 client_secret
		// 统一由生产 wiring 从 HUAKAI_GEMINI_OAUTH_CLIENT_SECRET 注入。
		// ChatGPT OAuth 是 PKCE-only，同样忽略 request body 中的 client_secret。
		vendor := credentialstore.Normalize(req.Vendor)
		authMode := credentialstore.Normalize(req.AuthMode)
		if vendor == credentialstore.VendorGemini || (vendor == credentialstore.VendorOpenAI && authMode == credentialstore.AuthModeChatGPTOAuth) {
			clientSecret = ""
		}
		result, err := credentialacq.StartOAuthFlowWithRegistry(ctx, d.Sessions, start, credentialacq.OAuthClientConfig{
			ClientID: oauthReq.ClientID, ClientSecret: clientSecret, AuthURL: oauthReq.AuthURL, TokenURL: oauthReq.TokenURL,
			RedirectURI: firstNonEmptyGateway(oauthReq.RedirectURI, req.RedirectURI), Scopes: oauthReq.Scopes, Source: oauthReq.Source,
		}, d.Exchangers)
		if err == nil {
			_ = credentialacq.EmitLifecycleAudit(ctx, d.CredentialAudit, result.Session, credentialacq.EventStarted, 0, start.ActorID, "", nil)
		}
		return result.Session, result, err
	}
	session, err := d.Sessions.CreateFromStart(ctx, start)
	if err != nil {
		return credentialacq.Session{}, credentialacq.OAuthStartResult{}, err
	}
	_ = credentialacq.EmitLifecycleAudit(ctx, d.CredentialAudit, session, credentialacq.EventStarted, 0, start.ActorID, "", nil)
	return session, credentialacq.OAuthStartResult{}, nil
}

func createCredentialAcqSession(ctx context.Context, d AdminCredentialAcquisitionDeps, ident admin.AdminIdentity, req credentialAcqStartRequest) (credentialacq.Session, error) {
	session, _, err := createOrStartCredentialAcqSession(ctx, d, ident, req, "")
	return session, err
}

func helperCandidates(req credentialAcqHelperRequest) ([]credentialacq.CredentialCandidate, error) {
	vendor := firstNonEmptyGateway(req.Vendor, credentialstore.VendorOpenAI)
	mode := firstNonEmptyGateway(req.AuthMode, credentialstore.AuthModeCodexCLIOAuth)
	switch req.FlowKind {
	case credentialacq.FlowKindCSVImport:
		return credentialacq.ParseCSVImportContent(firstNonEmptyGateway(req.Content, string(req.Credentials)), vendor, mode)
	case credentialacq.FlowKindJSONImport, credentialacq.FlowKindCLIImport:
		return credentialacq.ParseImportContent(firstNonEmptyGateway(req.Content, string(req.Credentials)), vendor, mode)
	case credentialacq.FlowKindPaste, credentialacq.FlowKindTokenExchange, credentialacq.FlowKindCloudBootstrap:
		if len(req.Credentials) == 0 {
			return nil, credentialacq.ErrInvalidImportBody
		}
		return []credentialacq.CredentialCandidate{{
			Vendor: credentialstore.Normalize(vendor), AuthMode: credentialstore.Normalize(mode),
			Payload: req.Credentials, RedactedContext: map[string]any{"shape": "json_object"},
		}}, nil
	default:
		return nil, credentialacq.ErrInvalidImportBody
	}
}

func resolveCredentialAcqAdmin(w http.ResponseWriter, r *http.Request, d AdminCredentialAcquisitionDeps) (admin.AdminIdentity, bool) {
	if d.Auth == nil || d.Sessions == nil || d.Credentials == nil || d.AuditStore == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "admin credential acquisition dependency unset")
		return admin.AdminIdentity{}, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		if errors.Is(err, admin.ErrAdminBackend) {
			writeJSONError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
		} else {
			writeJSONError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
		}
		return admin.AdminIdentity{}, false
	}
	if ident.Role != admin.RolePlatformAdmin {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "platform_admin role required")
		return admin.AdminIdentity{}, false
	}
	return ident, true
}

func writeCredentialAcqAdminAudit(r *http.Request, d AdminCredentialAcquisitionDeps, actorID, actorRole string, session credentialacq.Session, action, reason string) {
	if d.AuditStore == nil {
		return
	}
	payload, _ := json.Marshal(credentialacq.AuditSanitizePayload(map[string]any{
		"tenant_id": session.TenantID, "flow_id": session.ID, "vendor": session.Vendor,
		"auth_mode": session.AuthMode, "flow_kind": string(session.Kind), "status": string(session.Status),
	}))
	tenantID := session.TenantID
	targetID := session.ProviderAccountID
	reqID := middleware.GetReqID(r.Context())
	_, _ = d.AuditStore.InsertAdminAuditEvent(r.Context(), admindb.InsertAdminAuditEventParams{
		TenantID: &tenantID, ActorID: actorID, ActorRole: actorRole,
		Action: action, TargetType: "provider_account", TargetID: &targetID,
		RequestID: &reqID, Reason: chineseReason(reason, reason), Payload: payload,
	})
}

func credentialAcqFlowMatchesPathAccount(w http.ResponseWriter, r *http.Request, session credentialacq.Session) bool {
	accountID, ok := parseAdminPoolID(w, r)
	if !ok {
		return false
	}
	if session.ProviderAccountID != accountID {
		writeJSONError(w, http.StatusForbidden, "credential_acquisition_account_mismatch", "credential acquisition flow does not belong to provider account")
		return false
	}
	return true
}

func writeCredentialAcqError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, credentialacq.ErrFlowNotFound):
		writeJSONError(w, http.StatusNotFound, "credential_acquisition_not_found", "credential acquisition flow not found")
	case errors.Is(err, credentialacq.ErrFlowExpired):
		writeJSONError(w, http.StatusGone, "credential_acquisition_expired", "credential acquisition flow expired")
	case errors.Is(err, credentialacq.ErrFlowReplay):
		writeJSONError(w, http.StatusConflict, "credential_acquisition_replay", "credential acquisition flow already consumed")
	case errors.Is(err, credentialacq.ErrStateMismatch):
		writeJSONError(w, http.StatusBadRequest, "oauth_state_mismatch", "oauth state mismatch")
	case errors.Is(err, credentialacq.ErrUnknownMode):
		writeJSONError(w, http.StatusBadRequest, "unknown_credential_mode", "unknown vendor/auth_mode")
	case errors.Is(err, credentialacq.ErrInvalidImportBody):
		writeJSONError(w, http.StatusBadRequest, "invalid_import_body", "invalid import body")
	case errors.Is(err, credentialacq.ErrFeatureDisabled):
		writeJSONError(w, http.StatusForbidden, "credential_acquisition_feature_disabled", "credential acquisition feature disabled")
	case errors.Is(err, credentialacq.ErrSecretInContext):
		writeJSONError(w, http.StatusBadRequest, "redacted_context_secret", "redacted_context contains secret-shaped material")
	case errors.Is(err, credentialacq.ErrOAuthExchangerMissing):
		writeJSONError(w, http.StatusUnprocessableEntity, "oauth_exchanger_missing", "oauth exchanger missing for credential acquisition flow")
	default:
		writeJSONError(w, http.StatusBadRequest, "credential_acquisition_failed", err.Error())
	}
}

func mergeRedactedContext(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func firstNonEmptyGateway(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstReason(values ...string) string {
	if got := firstNonEmptyGateway(values...); got != "" {
		return got
	}
	return "credential acquisition"
}

func parsePositiveQueryInt64(r *http.Request, name string) int64 {
	value, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get(name)), 10, 64)
	return value
}
