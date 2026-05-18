package gatewayhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/community/invitation"
)

type InvitationService interface {
	Generate(context.Context, invitation.GenerateInvitationParams) (invitation.GenerateInvitationOutput, error)
}

type InvitationDeps struct {
	Service InvitationService
}

type invitationCreateRequest struct {
	MaxUsage             *int    `json:"max_usage,omitempty"`
	ExpiresInDays        *int    `json:"expires_in_days,omitempty"`
	ClientIdempotencyKey *string `json:"client_idempotency_key,omitempty"`
}

type invitationCreateResponse struct {
	Code          string    `json:"code"`
	InviterUserID int64     `json:"inviter_user_id"`
	ExpiresAt     time.Time `json:"expires_at"`
	MaxUsage      int       `json:"max_usage"`
}

func NewInvitationCreateHandler(d InvitationDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Service == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "invitation dependency unset")
			return
		}
		ident, ok := sessionauth.SessionFromContext(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized", "session bearer token is required")
			return
		}
		var req invitationCreateRequest
		if !decodeInvitationJSON(w, r, &req) {
			return
		}
		maxUsage := invitation.DefaultMaxUsage
		if req.MaxUsage != nil {
			maxUsage = *req.MaxUsage
		}
		expiresInDays := invitation.DefaultExpiryDays
		if req.ExpiresInDays != nil {
			expiresInDays = *req.ExpiresInDays
		}
		if maxUsage <= 0 || maxUsage > invitation.MaxUsageLimit || expiresInDays <= 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid_request", "max_usage or expires_in_days is invalid")
			return
		}
		result, err := d.Service.Generate(r.Context(), invitation.GenerateInvitationParams{
			TenantID: ident.TenantID, InviterUserID: ident.UserID,
			MaxUsage: maxUsage, ExpiresInDays: expiresInDays, ClientIdempotencyKey: req.ClientIdempotencyKey,
		})
		if err != nil {
			writeInvitationError(w, err)
			return
		}
		writeAuditJSON(w, http.StatusCreated, invitationCreateResponse{
			Code:          result.Code,
			InviterUserID: result.InviterUserID,
			ExpiresAt:     result.ExpiresAt,
			MaxUsage:      result.MaxUsage,
		})
	}
}

func decodeInvitationJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return true
		}
		writeJSONError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return false
	}
	return true
}

func writeInvitationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, invitation.ErrQuotaExceeded):
		writeJSONError(w, http.StatusTooManyRequests, "quota_exceeded", "monthly invitation quota exceeded")
	case errors.Is(err, invitation.ErrInvalidInput):
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "invitation request is invalid")
	case errors.Is(err, invitation.ErrInvitationExpiresOverLimit):
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "invitation expires_in_days is over limit")
	case errors.Is(err, invitation.ErrStoreNotConfigured):
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "invitation dependency unset")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "invitation_backend_error", "invitation service unavailable")
	}
}
