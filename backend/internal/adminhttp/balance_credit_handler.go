package adminhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

type AdminBalanceCreditDeps struct {
	Auth    adminBalanceCreditAuth
	Service adminBalanceCreditService
}

type adminBalanceCreditAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type adminBalanceCreditService interface {
	AdminAdjustBalance(context.Context, payment.AdminBalanceAdjustmentInput) (payment.AdminBalanceAdjustmentResult, error)
}

func MountBalanceCreditRoutes(r chi.Router, d AdminBalanceCreditDeps) {
	r.Post("/adjustments", newBalanceCreditHandler(d))
}

type balanceCreditRequestBody struct {
	TenantID       int64           `json:"tenant_id"`
	UserID         int64           `json:"user_id"`
	Amount         decimal.Decimal `json:"amount"`
	CurrencyCode   string          `json:"currency_code,omitempty"`
	Reason         string          `json:"reason,omitempty"`
	IdempotencyKey string          `json:"idempotency_key"`
}

type balanceCreditResponseBody struct {
	TenantID        int64  `json:"tenant_id"`
	UserID          int64  `json:"user_id"`
	NetBalance      string `json:"net_balance"`
	CurrencyCode    string `json:"currency_code"`
	RechargeOrderID *int64 `json:"recharge_order_id,omitempty"`
}

func newBalanceCreditHandler(d AdminBalanceCreditDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Service == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured",
				"admin balance adjustment dependency unset")
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if err != nil {
			writeAdminAuthError(w, err)
			return
		}
		if ident.Role != admin.RolePlatformAdmin {
			writeAdminError(w, admin.ErrAdminForbidden)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "body_read_error", err.Error())
			return
		}
		var req balanceCreditRequestBody
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		if req.TenantID <= 0 || req.UserID <= 0 || req.Amount.IsZero() || req.Reason == "" || req.IdempotencyKey == "" {
			writeError(w, http.StatusBadRequest, "missing_fields",
				"tenant_id, user_id, non-zero amount, reason, and idempotency_key are required")
			return
		}
		result, err := d.Service.AdminAdjustBalance(r.Context(), payment.AdminBalanceAdjustmentInput{
			TenantID:        req.TenantID,
			UserID:          req.UserID,
			Amount:          req.Amount,
			CurrencyCode:    req.CurrencyCode,
			// 双身份归属(money-via-login Stage 1):ActorID 仍传纯数字 TokenID 喂旧 bigint 列
			//(token 通道向后兼容;session-admin 为 0→NULL,不能传 AuditActor() 否则 ParseInt 成 0);
			// ActorRef 传 AuditActor() 串落新 text 列,两通道都可归属(admin_token:N / admin_user:N)。
			ActorID:         fmt.Sprintf("%d", ident.TokenID),
			ActorRef:        ident.AuditActor(),
			Reason:          req.Reason,
			RequestID:       middleware.GetReqID(r.Context()),
			ExternalTradeNo: req.IdempotencyKey,
		})
		if err != nil {
			writeBalanceCreditError(w, err)
			return
		}

		resp := balanceCreditResponseBody{
			TenantID:     result.TenantID,
			UserID:       result.UserID,
			NetBalance:   result.NewBalance.StringFixed(8),
			CurrencyCode: result.CurrencyCode,
		}
		if result.RechargeOrderID > 0 {
			resp.RechargeOrderID = &result.RechargeOrderID
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func writeBalanceCreditError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, payment.ErrAdminDebitNotSupported):
		writeError(w, http.StatusBadRequest, "admin_debit_not_yet_supported",
			"manual debit is gated until durable debit billing events are supported")
	case errors.Is(err, payment.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_balance_adjustment", err.Error())
	case errors.Is(err, payment.ErrUserNotFound):
		writeError(w, http.StatusNotFound, "user_not_found", "target user not found")
	case errors.Is(err, payment.ErrAccountInactive):
		writeError(w, http.StatusBadRequest, "account_inactive", "target tenant or user is inactive")
	case errors.Is(err, payment.ErrExternalTradeConflict):
		writeError(w, http.StatusConflict, "balance_adjustment_idempotency_conflict",
			"idempotency_key was already used for a different balance adjustment")
	case errors.Is(err, payment.ErrStoreNotConfigured):
		writeError(w, http.StatusServiceUnavailable, "payment_backend_unavailable", "payment service unavailable")
	default:
		writeError(w, http.StatusServiceUnavailable, "payment_backend_error",
			fmt.Sprintf("admin balance adjustment failed: %v", err))
	}
}
