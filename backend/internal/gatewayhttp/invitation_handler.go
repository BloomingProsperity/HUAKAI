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
	ReferralSummary(context.Context, int64, int64) (invitation.ReferralSummary, error)
	GetOrCreateSelfReferralCode(ctx context.Context, tenantID, inviterUserID int64, now time.Time) (invitation.GenerateInvitationOutput, error)
}

type myReferralCodeResponse struct {
	Code          string `json:"code"`
	InviterUserID int64  `json:"inviter_user_id"`
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

type invitationSummaryResponse struct {
	QualifiedCount     int64 `json:"qualified_count"`
	RewardedCount      int64 `json:"rewarded_count"`
	RewardsEarnedCents int64 `json:"rewards_earned_cents"`
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

func NewInvitationSummaryHandler(d InvitationDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Service == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "invitation dependency unset")
			return
		}
		ident, ok := sessionauth.SessionFromContext(r.Context())
		if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized", "session bearer token is required")
			return
		}
		summary, err := d.Service.ReferralSummary(r.Context(), ident.TenantID, ident.UserID)
		if err != nil {
			writeInvitationError(w, err)
			return
		}
		writeAuditJSON(w, http.StatusOK, invitationSummaryResponse{
			QualifiedCount:     summary.QualifiedCount,
			RewardedCount:      summary.RewardedCount,
			RewardsEarnedCents: summary.RewardsEarnedCents,
		})
	}
}

// NewMyReferralCodeHandler 处理 GET /v1/me/invitation-code:调用者唯一稳定的自助邀请
// 码,在首次调用时惰性铸造。它是对自己身份码的纯读取,且「不」受每月活动配额约束——
// 已登录用户必须始终能取到自己的码,即使共享的单租户部署当月已耗尽活动上限(本端点
// 不得复现的那个缺陷)。
func NewMyReferralCodeHandler(d InvitationDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Service == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "invitation dependency unset")
			return
		}
		ident, ok := sessionauth.SessionFromContext(r.Context())
		if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized", "session bearer token is required")
			return
		}
		out, err := d.Service.GetOrCreateSelfReferralCode(r.Context(), ident.TenantID, ident.UserID, time.Time{})
		if err != nil {
			writeInvitationError(w, err)
			return
		}
		writeAuditJSON(w, http.StatusOK, myReferralCodeResponse{
			Code:          out.Code,
			InviterUserID: out.InviterUserID,
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
	case errors.Is(err, invitation.ErrReservedIdempotencyKey):
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "client_idempotency_key uses a reserved prefix")
	case errors.Is(err, invitation.ErrInvitationExpiresOverLimit):
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "invitation expires_in_days is over limit")
	case errors.Is(err, invitation.ErrStoreNotConfigured):
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "invitation dependency unset")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "invitation_backend_error", "invitation service unavailable")
	}
}
