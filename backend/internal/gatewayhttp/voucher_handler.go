package gatewayhttp

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/voucher"
)

type VoucherAdminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type VoucherService interface {
	Create(context.Context, voucher.CreateInput) (voucher.CreateResult, error)
	CreateBatch(context.Context, voucher.BatchCreateInput) (voucher.BatchCreateResult, error)
	Redeem(context.Context, voucher.RedeemInput) (voucher.RedeemResult, error)
	Revoke(context.Context, voucher.RevokeInput) (voucher.Voucher, error)
	List(context.Context, voucher.ListInput) ([]voucher.Voucher, error)
	GetBatch(context.Context, int64, int64) (voucher.GetBatchResult, error)
}

type VoucherAdminDeps struct {
	Auth    VoucherAdminAuth
	Service VoucherService
}

type VoucherUserDeps struct {
	Service VoucherService
}

type voucherCreateRequest struct {
	TenantID         int64     `json:"tenant_id"`
	Code             string    `json:"code,omitempty"`
	AmountCents      int64     `json:"amount_cents"`
	CurrencyCode     string    `json:"currency_code,omitempty"`
	ValidFrom        time.Time `json:"valid_from"`
	ValidUntil       time.Time `json:"valid_until"`
	MaxRedemptions   int       `json:"max_redemptions,omitempty"`
	SingleUsePerUser *bool     `json:"single_use_per_user,omitempty"`
	EligibleUserID   *int64    `json:"eligible_user_id,omitempty"`
}

type voucherBatchCreateRequest struct {
	TenantID         int64     `json:"tenant_id"`
	Count            int       `json:"count"`
	AmountCents      int64     `json:"amount_cents"`
	CurrencyCode     string    `json:"currency_code,omitempty"`
	ValidFrom        time.Time `json:"valid_from"`
	ValidUntil       time.Time `json:"valid_until"`
	MaxRedemptions   int       `json:"max_redemptions,omitempty"`
	SingleUsePerUser *bool     `json:"single_use_per_user,omitempty"`
	EligibleUserID   *int64    `json:"eligible_user_id,omitempty"`
}

type voucherRevokeRequest struct {
	TenantID int64  `json:"tenant_id"`
	Reason   string `json:"reason,omitempty"`
}

type voucherRedeemRequest struct {
	Code           string `json:"code"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

func MountVoucherAdminRoutes(r chi.Router, d VoucherAdminDeps) {
	r.Get("/", newVoucherListHandler(d))
	r.Post("/", newVoucherCreateHandler(d))
	r.Post("/batch", newVoucherBatchCreateHandler(d))
	r.Post("/{id}/revoke", newVoucherRevokeHandler(d))
}

func MountVoucherUserRoutes(r chi.Router, d VoucherUserDeps) {
	r.Post("/redeem", newVoucherRedeemHandler(d))
}

func newVoucherCreateHandler(d VoucherAdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveVoucherAdmin(w, r, d)
		if !ok {
			return
		}
		var req voucherCreateRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		result, err := d.Service.Create(r.Context(), voucher.CreateInput{
			TenantID: req.TenantID, AdminID: ident.TokenID, Code: req.Code,
			AmountCents: req.AmountCents, CurrencyCode: req.CurrencyCode,
			ValidFrom: req.ValidFrom, ValidUntil: req.ValidUntil,
			MaxRedemptions: req.MaxRedemptions, SingleUsePerUser: boolDefault(req.SingleUsePerUser, true),
			EligibleUserID: req.EligibleUserID,
		})
		if err != nil {
			writeVoucherError(w, err)
			return
		}
		writeAuditJSON(w, http.StatusCreated, result)
	}
}

func newVoucherBatchCreateHandler(d VoucherAdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveVoucherAdmin(w, r, d)
		if !ok {
			return
		}
		var req voucherBatchCreateRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		result, err := d.Service.CreateBatch(r.Context(), voucher.BatchCreateInput{
			TenantID: req.TenantID, AdminID: ident.TokenID, Count: req.Count,
			AmountCents: req.AmountCents, CurrencyCode: req.CurrencyCode,
			ValidFrom: req.ValidFrom, ValidUntil: req.ValidUntil,
			MaxRedemptions: req.MaxRedemptions, SingleUsePerUser: boolDefault(req.SingleUsePerUser, true),
			EligibleUserID: req.EligibleUserID,
		})
		if err != nil {
			writeVoucherError(w, err)
			return
		}
		writeAuditJSON(w, http.StatusCreated, result)
	}
}

func newVoucherRevokeHandler(d VoucherAdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveVoucherAdmin(w, r, d)
		if !ok {
			return
		}
		id, ok := parseVoucherPathID(w, r)
		if !ok {
			return
		}
		var req voucherRevokeRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		v, err := d.Service.Revoke(r.Context(), voucher.RevokeInput{
			TenantID: req.TenantID, ID: id, AdminID: ident.TokenID, Reason: req.Reason,
		})
		if err != nil {
			writeVoucherError(w, err)
			return
		}
		writeAuditJSON(w, http.StatusOK, map[string]any{"voucher": v})
	}
}

func newVoucherListHandler(d VoucherAdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveVoucherAdmin(w, r, d); !ok {
			return
		}
		tenantID, ok := parsePositiveQueryInt(w, r, "tenant_id")
		if !ok {
			return
		}
		limit := 50
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n <= 0 || n > 200 {
				writeJSONError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 200")
				return
			}
			limit = n
		}
		rows, err := d.Service.List(r.Context(), voucher.ListInput{TenantID: tenantID, Limit: limit})
		if err != nil {
			writeVoucherError(w, err)
			return
		}
		writeAuditJSON(w, http.StatusOK, map[string]any{"vouchers": rows})
	}
}

func newVoucherGetBatchHandler(d VoucherAdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveVoucherAdmin(w, r, d); !ok {
			return
		}
		tenantID, ok := parsePositiveQueryInt(w, r, "tenant_id")
		if !ok {
			return
		}
		id, ok := parseVoucherPathID(w, r)
		if !ok {
			return
		}
		result, err := d.Service.GetBatch(r.Context(), tenantID, id)
		if err != nil {
			writeVoucherError(w, err)
			return
		}
		writeAuditJSON(w, http.StatusOK, result)
	}
}

func newVoucherRedeemHandler(d VoucherUserDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Service == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "voucher dependency unset")
			return
		}
		ident, ok := sessionauth.SessionFromContext(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "session_token_required", "session bearer token is required")
			return
		}
		var req voucherRedeemRequest
		if !decodeAdminPoolJSON(w, r, &req) {
			return
		}
		result, err := d.Service.Redeem(r.Context(), voucher.RedeemInput{
			TenantID: ident.TenantID, UserID: ident.UserID, Code: req.Code,
			IdempotencyKey: req.IdempotencyKey, SourceIP: clientIP(r),
			RequestID: r.Header.Get("X-Request-Id"),
		})
		if err != nil {
			writeVoucherError(w, err)
			return
		}
		writeAuditJSON(w, http.StatusOK, result)
	}
}

func resolveVoucherAdmin(w http.ResponseWriter, r *http.Request, d VoucherAdminDeps) (admin.AdminIdentity, bool) {
	if d.Auth == nil || d.Service == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "voucher admin dependency unset")
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

func parseVoucherPathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_voucher_id", "id must be a positive int64")
		return 0, false
	}
	return id, true
}

func parsePositiveQueryInt(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		writeJSONError(w, http.StatusBadRequest, name+"_required", name+" query parameter must be positive")
		return 0, false
	}
	return n, true
}

func boolDefault(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}

func writeVoucherError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, voucher.ErrInvalidInput):
		writeJSONError(w, http.StatusBadRequest, "invalid_voucher_request", "voucher request is invalid")
	case errors.Is(err, voucher.ErrVoucherDuplicate):
		writeJSONError(w, http.StatusConflict, "voucher_duplicate", "voucher code already exists")
	case errors.Is(err, voucher.ErrVoucherNotFound):
		writeJSONError(w, http.StatusNotFound, "voucher_not_found", "voucher is not valid")
	case errors.Is(err, voucher.ErrVoucherNotYetValid):
		writeJSONError(w, http.StatusConflict, "voucher_not_yet_valid", "voucher is not currently redeemable")
	case errors.Is(err, voucher.ErrVoucherExpired):
		writeJSONError(w, http.StatusConflict, "voucher_expired", "voucher is not currently redeemable")
	case errors.Is(err, voucher.ErrVoucherExhausted):
		writeJSONError(w, http.StatusConflict, "voucher_exhausted", "voucher is not currently redeemable")
	case errors.Is(err, voucher.ErrVoucherRevoked):
		writeJSONError(w, http.StatusConflict, "voucher_revoked", "voucher is not currently redeemable")
	case errors.Is(err, voucher.ErrVoucherWrongUser):
		writeJSONError(w, http.StatusConflict, "voucher_wrong_user", "voucher is not currently redeemable")
	case errors.Is(err, voucher.ErrAlreadyRedeemed):
		writeJSONError(w, http.StatusConflict, "voucher_already_redeemed", "voucher is not currently redeemable")
	case errors.Is(err, voucher.ErrIdempotencyConflict):
		writeJSONError(w, http.StatusConflict, "voucher_idempotency_conflict", "idempotency key conflicts with a prior redemption")
	case errors.Is(err, voucher.ErrBurstLimited):
		writeJSONError(w, http.StatusTooManyRequests, "voucher_attempt_limited", "too many voucher attempts")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "voucher_backend_error", "voucher service unavailable")
	}
}
