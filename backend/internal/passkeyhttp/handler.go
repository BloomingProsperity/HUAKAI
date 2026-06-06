package passkeyhttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
	"github.com/BloomingProsperity/HUAKAI/internal/passkey"
	"github.com/BloomingProsperity/HUAKAI/internal/twofa"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

type Deps struct {
	Passkeys         *passkey.Service
	Sessions         *usersession.Service
	Users            UserStore
	StepUp           StepUpVerifier
	ClientIPResolver *clientip.Resolver
}

type registerBeginRequest struct {
	Name   string      `json:"name,omitempty"`
	StepUp StepUpProof `json:"step_up,omitempty"`
}

type registerFinishRequest struct {
	SessionID  string          `json:"session_id"`
	Name       string          `json:"name,omitempty"`
	StepUp     StepUpProof     `json:"step_up,omitempty"`
	Credential json.RawMessage `json:"credential"`
}

type loginBeginRequest struct {
	TenantID int64 `json:"tenant_id"`
}

type loginFinishRequest struct {
	TenantID   int64           `json:"tenant_id"`
	SessionID  string          `json:"session_id"`
	Credential json.RawMessage `json:"credential"`
	DeviceInfo map[string]any  `json:"device_info,omitempty"`
}

type deleteRequest struct {
	StepUp StepUpProof `json:"step_up,omitempty"`
}

func MountUserRoutes(r chi.Router, d Deps) {
	r.Post("/register/begin", newRegisterBeginHandler(d))
	r.Post("/register/finish", newRegisterFinishHandler(d))
	r.Get("/", newListHandler(d))
	r.Delete("/{id}", newDeleteHandler(d))
}

func MountLoginRoutes(r chi.Router, d Deps) {
	r.Post("/login/begin", newLoginBeginHandler(d))
	r.Post("/login/finish", newLoginFinishHandler(d))
}

func newRegisterBeginHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := sessionIdentity(w, r)
		if !ok {
			return
		}
		if !checkOrigin(w, r, d, true) {
			return
		}
		var req registerBeginRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if !verifyStepUp(w, r, d, ident, req.StepUp) {
			return
		}
		user, err := userFromSession(r, d, ident)
		if err != nil {
			writePasskeyError(w, err)
			return
		}
		begin, err := d.Passkeys.RegisterBegin(r.Context(), passkey.RegisterBeginInput{
			TenantID: ident.TenantID, User: user, Name: req.Name,
		})
		if err != nil {
			writePasskeyError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, begin)
	}
}

func newRegisterFinishHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := sessionIdentity(w, r)
		if !ok {
			return
		}
		if !checkOrigin(w, r, d, true) {
			return
		}
		var req registerFinishRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if !verifyStepUp(w, r, d, ident, req.StepUp) {
			return
		}
		user, err := userFromSession(r, d, ident)
		if err != nil {
			writePasskeyError(w, err)
			return
		}
		credential, err := d.Passkeys.RegisterFinish(r.Context(), passkey.RegisterFinishInput{
			TenantID: ident.TenantID, User: user, SessionID: strings.TrimSpace(req.SessionID),
			CredentialJSON: req.Credential, Name: req.Name,
		})
		if err != nil {
			writePasskeyError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"passkey": summaryFromRecord(credential)})
	}
}

func newListHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := sessionIdentity(w, r)
		if !ok {
			return
		}
		if d.Passkeys == nil {
			writeError(w, http.StatusServiceUnavailable, "passkey_not_configured", "passkey service dependency unset")
			return
		}
		items, err := d.Passkeys.ListCredentials(r.Context(), ident.TenantID, ident.UserID)
		if err != nil {
			writePasskeyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"passkeys": items})
	}
}

func newDeleteHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := sessionIdentity(w, r)
		if !ok {
			return
		}
		if d.Passkeys == nil {
			writeError(w, http.StatusServiceUnavailable, "passkey_not_configured", "passkey service dependency unset")
			return
		}
		var req deleteRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if !verifyStepUp(w, r, d, ident, req.StepUp) {
			return
		}
		id, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(r, "id")), 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_passkey_request", "passkey id is invalid")
			return
		}
		if err := d.Passkeys.DeleteCredential(r.Context(), passkey.DeleteCredentialInput{
			TenantID: ident.TenantID, UserID: ident.UserID, ID: id,
		}); err != nil {
			writePasskeyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	}
}

func newLoginBeginHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !checkOrigin(w, r, d, false) {
			return
		}
		var req loginBeginRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		begin, err := d.Passkeys.LoginBegin(r.Context(), passkey.LoginBeginInput{TenantID: req.TenantID})
		if err != nil {
			writePasskeyError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, begin)
	}
}

func newLoginFinishHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Sessions == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "session dependency unset")
			return
		}
		if !checkOrigin(w, r, d, false) {
			return
		}
		var req loginFinishRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := d.Passkeys.LoginFinish(r.Context(), passkey.LoginFinishInput{
			TenantID: req.TenantID, SessionID: strings.TrimSpace(req.SessionID), CredentialJSON: req.Credential,
		})
		if err != nil {
			writePasskeyError(w, err)
			return
		}
		tokens, err := d.Sessions.Create(r.Context(), usersession.CreateInput{
			TenantID: result.User.TenantID, UserID: result.User.ID, DeviceInfo: req.DeviceInfo,
			IP: clientIP(d, r), UserAgent: r.UserAgent(), AuthMethod: "passkey",
		})
		if err != nil {
			writeSessionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"user": publicUser(result.User), "session": tokens})
	}
}

func checkOrigin(w http.ResponseWriter, r *http.Request, d Deps, registration bool) bool {
	if d.Passkeys == nil {
		writeError(w, http.StatusServiceUnavailable, "passkey_not_configured", "passkey service dependency unset")
		return false
	}
	if err := d.Passkeys.CheckOrigin(r.Context(), r.Header.Get("Origin"), registration); err != nil {
		writePasskeyError(w, err)
		return false
	}
	return true
}

func verifyStepUp(w http.ResponseWriter, r *http.Request, d Deps, ident sessionauth.SessionIdentity, proof StepUpProof) bool {
	if d.StepUp == nil {
		writeError(w, http.StatusServiceUnavailable, "passkey_step_up_not_configured", "passkey step-up dependency unset")
		return false
	}
	if err := d.StepUp.VerifyStepUp(r.Context(), ident.TenantID, ident.UserID, proof); err != nil {
		writeStepUpError(w, err)
		return false
	}
	return true
}

func sessionIdentity(w http.ResponseWriter, r *http.Request) (sessionauth.SessionIdentity, bool) {
	ident, ok := sessionauth.SessionFromContext(r.Context())
	if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
		writeError(w, http.StatusUnauthorized, "session_token_required", "session bearer token is required")
		return sessionauth.SessionIdentity{}, false
	}
	return ident, true
}

func sessionUser(ident sessionauth.SessionIdentity) userauth.User {
	return userauth.User{ID: ident.UserID, TenantID: ident.TenantID, Status: userauth.UserStatusActive, EmailVerified: true}
}

func clientIP(d Deps, r *http.Request) string {
	if d.ClientIPResolver == nil {
		return ""
	}
	return d.ClientIPResolver.ClientIP(r)
}

func userFromSession(r *http.Request, d Deps, ident sessionauth.SessionIdentity) (userauth.User, error) {
	if d.Users == nil {
		return sessionUser(ident), nil
	}
	user, err := d.Users.GetUserByID(r.Context(), ident.TenantID, ident.UserID)
	if err != nil {
		return userauth.User{}, err
	}
	return user, nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(out); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_passkey_request", "invalid JSON body")
		return false
	}
	return true
}

func writeStepUpError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrStepUpRequired):
		writeError(w, http.StatusForbidden, "passkey_step_up_required", "recent password or two-factor verification is required")
	case errors.Is(err, ErrStepUpInvalid), errors.Is(err, userauth.ErrInvalidCredentials), errors.Is(err, twofa.ErrInvalidCode):
		writeError(w, http.StatusUnauthorized, "passkey_step_up_invalid", "step-up verification failed")
	case errors.Is(err, ErrStepUpNotConfigured):
		writeError(w, http.StatusServiceUnavailable, "passkey_step_up_not_configured", "passkey step-up dependency unset")
	case errors.Is(err, twofa.ErrLocked):
		writeError(w, http.StatusTooManyRequests, "two_factor_locked", "two-factor verification is temporarily locked")
	default:
		writeError(w, http.StatusServiceUnavailable, "passkey_step_up_backend_error", "step-up verification unavailable")
	}
}

func writePasskeyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, passkey.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_passkey_request", "passkey request is invalid")
	case errors.Is(err, passkey.ErrFeatureDisabled):
		writeError(w, http.StatusForbidden, "passkey_disabled", "passkey login is disabled")
	case errors.Is(err, passkey.ErrRegistrationDisabled):
		writeError(w, http.StatusForbidden, "passkey_registration_disabled", "passkey registration is disabled")
	case errors.Is(err, passkey.ErrOriginNotAllowed), errors.Is(err, passkey.ErrConfigNotConfigured):
		writeError(w, http.StatusForbidden, "passkey_origin_forbidden", "passkey origin is not allowed")
	case errors.Is(err, passkey.ErrCredentialNotFound), errors.Is(err, passkey.ErrCeremonyNotFound), errors.Is(err, passkey.ErrCeremonyExpired):
		writeError(w, http.StatusUnauthorized, "passkey_challenge_invalid", "passkey challenge or credential is invalid")
	case errors.Is(err, passkey.ErrDuplicateCredential):
		writeError(w, http.StatusConflict, "passkey_duplicate", "passkey credential already exists")
	case errors.Is(err, passkey.ErrCloneDetected):
		writeError(w, http.StatusForbidden, "passkey_clone_detected", "passkey authenticator clone detected")
	case errors.Is(err, userauth.ErrUserNotFound):
		writeError(w, http.StatusForbidden, "account_not_active", "account is no longer active")
	default:
		writeError(w, http.StatusServiceUnavailable, "passkey_backend_error", "passkey service unavailable")
	}
}

func writeSessionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, usersession.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_session_request", "session request is invalid")
	case errors.Is(err, usersession.ErrSigningKeyMissing):
		writeError(w, http.StatusServiceUnavailable, "session_auth_not_configured", "session signing key is not configured")
	case errors.Is(err, usersession.ErrDeviceLimitExceeded):
		writeError(w, http.StatusForbidden, "session_device_limit_exceeded", "too many active devices")
	default:
		writeError(w, http.StatusServiceUnavailable, "session_backend_error", "session backend transient failure")
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

func publicUser(user userauth.User) map[string]any {
	return map[string]any{
		"id": user.ID, "tenant_id": user.TenantID, "email": user.Email, "display_name": user.DisplayName,
		"email_verified": user.EmailVerified, "status": user.Status, "created_at": user.CreatedAt, "updated_at": user.UpdatedAt,
	}
}

func summaryFromRecord(record passkey.CredentialRecord) passkey.CredentialSummary {
	items := summariesOne(record)
	return items[0]
}

func summariesOne(record passkey.CredentialRecord) []passkey.CredentialSummary {
	return []passkey.CredentialSummary{{
		ID: record.ID, Name: record.Name, Transports: record.Transports,
		AttestationType: record.AttestationType, CloneWarning: record.CloneWarning,
		SignCount: record.SignCount, CreatedAt: record.CreatedAt, LastUsedAt: record.LastUsedAt,
	}}
}
