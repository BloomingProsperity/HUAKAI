package gatewayhttp

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

type SessionHandlerDeps struct {
	Sessions  *usersession.Service
	EventSink AuthEventSink
}

type sessionRefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type sessionRevokeRequest struct {
	FamilyID     string `json:"family_id,omitempty"`
	SessionToken string `json:"session_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

type sessionListRequest struct{}

func MountSessionRoutes(r chi.Router, d SessionHandlerDeps) {
	r.Post("/refresh", newSessionRefreshHandler(d))
	r.Post("/revoke", newSessionRevokeHandler(d))
	r.Post("/list", newSessionListHandler(d))
}

func newSessionRefreshHandler(d SessionHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Sessions == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "session dependency unset")
			return
		}
		var req sessionRefreshRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		ident, ok := sessionauth.SessionFromContext(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "session_token_required", "session bearer token is required")
			return
		}
		result, err := d.Sessions.Refresh(r.Context(), usersession.RefreshInput{
			TenantID: ident.TenantID, UserID: ident.UserID, RefreshToken: req.RefreshToken,
			IP: clientIP(r), UserAgent: r.UserAgent(),
		})
		if err != nil {
			recordAuthEvent(r.Context(), d.EventSink, AuthEvent{
				EventType: "session_refresh_failed", TenantID: ident.TenantID, UserID: ident.UserID,
				Outcome: "failure", ReasonClass: sessionReasonClass(err),
			})
			writeSessionError(w, err)
			return
		}
		recordAuthEvent(r.Context(), d.EventSink, AuthEvent{
			EventType: "session_refreshed", TenantID: ident.TenantID, UserID: ident.UserID, Outcome: "success",
		})
		writeAuditJSON(w, http.StatusOK, map[string]any{"session": result})
	}
}

func newSessionRevokeHandler(d SessionHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Sessions == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "session dependency unset")
			return
		}
		var req sessionRevokeRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		ident, ok := sessionauth.SessionFromContext(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "session_token_required", "session bearer token is required")
			return
		}
		if strings.TrimSpace(req.FamilyID) != "" {
			owned, err := sessionFamilyBelongsToCurrentUser(r, d.Sessions, ident, req.FamilyID)
			if err != nil {
				writeSessionError(w, err)
				return
			}
			if !owned {
				writeJSONError(w, http.StatusForbidden, "session_family_forbidden", "session family does not belong to current user")
				return
			}
		}
		count, err := d.Sessions.Revoke(r.Context(), usersession.RevokeInput{
			TenantID: ident.TenantID, UserID: ident.UserID, FamilyID: req.FamilyID,
			SessionToken: req.SessionToken, RefreshToken: req.RefreshToken, Reason: req.Reason,
		})
		if err != nil {
			writeSessionError(w, err)
			return
		}
		writeAuditJSON(w, http.StatusOK, map[string]any{"revoked": count})
	}
}

func sessionFamilyBelongsToCurrentUser(r *http.Request, sessions *usersession.Service, ident sessionauth.SessionIdentity, familyID string) (bool, error) {
	families, err := sessions.List(r.Context(), ident.TenantID, ident.UserID)
	if err != nil {
		return false, err
	}
	familyID = strings.TrimSpace(familyID)
	for _, family := range families {
		if family.ID == familyID {
			return true, nil
		}
	}
	return false, nil
}

func newSessionListHandler(d SessionHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Sessions == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "session dependency unset")
			return
		}
		var req sessionListRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		ident, ok := sessionauth.SessionFromContext(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "session_token_required", "session bearer token is required")
			return
		}
		families, err := d.Sessions.List(r.Context(), ident.TenantID, ident.UserID)
		if err != nil {
			writeSessionError(w, err)
			return
		}
		writeAuditJSON(w, http.StatusOK, map[string]any{"families": families})
	}
}

func writeSessionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, usersession.ErrInvalidInput):
		writeJSONError(w, http.StatusBadRequest, "invalid_session_request", "session request is invalid")
	case errors.Is(err, usersession.ErrTokenNotFound):
		writeJSONError(w, http.StatusUnauthorized, "session_token_not_found", "refresh token is invalid")
	case errors.Is(err, usersession.ErrRefreshReplay):
		writeJSONError(w, http.StatusConflict, "refresh_token_replay", "refresh token was already consumed")
	case errors.Is(err, usersession.ErrSessionUserMismatch):
		writeJSONError(w, http.StatusUnauthorized, "refresh_token_cross_user_attempt", "refresh token is invalid")
	case errors.Is(err, usersession.ErrTokenExpired):
		writeJSONError(w, http.StatusUnauthorized, "refresh_token_expired", "refresh token is expired")
	case errors.Is(err, usersession.ErrFamilyRevoked), errors.Is(err, usersession.ErrFamilyNotFound):
		writeJSONError(w, http.StatusUnauthorized, "session_family_revoked", "session family is revoked or missing")
	case errors.Is(err, usersession.ErrAnomalyRejected):
		writeJSONError(w, http.StatusForbidden, "session_anomaly_rejected", "session context changed too much")
	case errors.Is(err, usersession.ErrDeviceLimitExceeded):
		writeJSONError(w, http.StatusForbidden, "session_device_limit_exceeded", "too many active devices")
	case errors.Is(err, usersession.ErrDeviceConfirmationRequired):
		writeJSONError(w, http.StatusForbidden, "session_device_confirmation_required", "device confirmation is required")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "session_backend_error", "session backend transient failure")
	}
}

func sessionReasonClass(err error) string {
	switch {
	case errors.Is(err, usersession.ErrInvalidInput):
		return "invalid_session_request"
	case errors.Is(err, usersession.ErrTokenNotFound):
		return "session_token_not_found"
	case errors.Is(err, usersession.ErrRefreshReplay):
		return "refresh_token_replay"
	case errors.Is(err, usersession.ErrSessionUserMismatch):
		return "refresh_token_cross_user_attempt"
	case errors.Is(err, usersession.ErrTokenExpired):
		return "refresh_token_expired"
	case errors.Is(err, usersession.ErrFamilyRevoked), errors.Is(err, usersession.ErrFamilyNotFound):
		return "session_family_revoked"
	case errors.Is(err, usersession.ErrAnomalyRejected):
		return "session_anomaly_rejected"
	case errors.Is(err, usersession.ErrDeviceLimitExceeded):
		return "session_device_limit_exceeded"
	case errors.Is(err, usersession.ErrDeviceConfirmationRequired):
		return "session_device_confirmation_required"
	default:
		return "session_backend_error"
	}
}
