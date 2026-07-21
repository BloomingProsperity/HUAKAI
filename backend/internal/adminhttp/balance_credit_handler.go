package adminhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauth"
	"github.com/BloomingProsperity/HUAKAI/internal/balanceledger"
)

type AdminBalanceCreditDeps struct {
	Auth    adminBalanceCreditAuth
	Service adminBalanceCreditService
}

type adminBalanceCreditAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type adminBalanceCreditService interface {
	AdminAdjustBalance(context.Context, balanceledger.AdminBalanceAdjustmentInput) (balanceledger.AdminBalanceAdjustmentResult, error)
	GetTenantWallet(context.Context, int64) (balanceledger.TenantWalletSnapshot, error)
	ListBalanceTransactions(context.Context, balanceledger.ListTransactionsInput) ([]balanceledger.BalanceTransaction, error)
}

func MountBalanceCreditRoutes(r chi.Router, d AdminBalanceCreditDeps) {
	// 人工额度调整允许已登录管理员操作，业务服务继续按部署者与租户管理员的作用域裁决。
	r.With(adminsessionauth.AllowSessionWrite(adminsessionauth.SessionSafe)).Post("/adjustments", newBalanceCreditHandler(d))
	r.Get("/tenant-wallet", newTenantWalletHandler(d))
	r.Get("/transactions", newBalanceTransactionsHandler(d))
}

type balanceCreditRequestBody struct {
	TenantID       int64           `json:"tenant_id"`
	UserID         int64           `json:"user_id"`
	Amount         decimal.Decimal `json:"amount"`
	CurrencyCode   string          `json:"currency_code,omitempty"`
	Reason         string          `json:"reason,omitempty"`
	IdempotencyKey string          `json:"idempotency_key"`
}

type tenantWalletResponseBody struct {
	TenantID     int64  `json:"tenant_id"`
	Balance      string `json:"balance"`
	CurrencyCode string `json:"currency_code"`
	UpdatedAt    string `json:"updated_at"`
}

type balanceTransactionResponseBody struct {
	ID           int64  `json:"id"`
	TenantID     int64  `json:"tenant_id"`
	Operation    string `json:"operation"`
	TargetUserID *int64 `json:"target_user_id,omitempty"`
	SignedAmount string `json:"signed_amount"`
	CurrencyCode string `json:"currency_code"`
	ActorRole    string `json:"actor_role"`
	ActorRef     string `json:"actor_ref"`
	Reason       string `json:"reason"`
	RequestID    string `json:"request_id,omitempty"`
	CreatedAt    string `json:"created_at"`
}

type balanceCreditResponseBody struct {
	TransactionID int64  `json:"transaction_id"`
	TenantID      int64  `json:"tenant_id"`
	UserID        *int64 `json:"user_id,omitempty"`
	TargetKind    string `json:"target_kind"`
	NetBalance    string `json:"net_balance"`
	CurrencyCode  string `json:"currency_code"`
	Idempotent    bool   `json:"idempotent"`
}

func newTenantWalletHandler(d AdminBalanceCreditDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveBalanceReadIdentity(w, r, d)
		if !ok {
			return
		}
		if !canReadBalanceTenant(ident, tenantID) {
			writeAdminError(w, admin.ErrAdminForbidden)
			return
		}
		snapshot, err := d.Service.GetTenantWallet(r.Context(), tenantID)
		if err != nil {
			writeBalanceCreditError(w, err)
			return
		}
		writeBalanceJSON(w, http.StatusOK, tenantWalletResponseBody{
			TenantID: snapshot.TenantID, Balance: snapshot.Balance.StringFixed(8),
			CurrencyCode: snapshot.CurrencyCode, UpdatedAt: snapshot.UpdatedAt.Format(time.RFC3339Nano),
		})
	}
}

func newBalanceTransactionsHandler(d AdminBalanceCreditDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveBalanceReadIdentity(w, r, d)
		if !ok {
			return
		}
		if !canReadBalanceTenant(ident, tenantID) {
			writeAdminError(w, admin.ErrAdminForbidden)
			return
		}
		userID, ok := optionalPositiveInt64Query(w, r, "user_id")
		if !ok {
			return
		}
		limit, ok := boundedIntQuery(w, r, "limit", 50, 0, 200)
		if !ok {
			return
		}
		offset, ok := boundedIntQuery(w, r, "offset", 0, -1, 1_000_000)
		if !ok {
			return
		}
		transactions, err := d.Service.ListBalanceTransactions(r.Context(), balanceledger.ListTransactionsInput{
			TenantID: tenantID, UserID: userID, Limit: limit, Offset: offset,
		})
		if err != nil {
			writeBalanceCreditError(w, err)
			return
		}
		items := make([]balanceTransactionResponseBody, 0, len(transactions))
		for _, transaction := range transactions {
			item := balanceTransactionResponseBody{
				ID: transaction.ID, TenantID: transaction.TenantID, Operation: transaction.Operation,
				SignedAmount: transaction.SignedAmount.StringFixed(8), CurrencyCode: transaction.CurrencyCode,
				ActorRole: transaction.ActorRole, ActorRef: transaction.ActorRef, Reason: transaction.Reason,
				RequestID: transaction.RequestID, CreatedAt: transaction.CreatedAt.Format(time.RFC3339Nano),
			}
			if transaction.TargetUserID > 0 {
				item.TargetUserID = &transaction.TargetUserID
			}
			items = append(items, item)
		}
		writeBalanceJSON(w, http.StatusOK, map[string]any{"items": items, "limit": limit, "offset": offset})
	}
}

func resolveBalanceReadIdentity(w http.ResponseWriter, r *http.Request, d AdminBalanceCreditDeps) (admin.AdminIdentity, int64, bool) {
	if d.Auth == nil || d.Service == nil {
		writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "admin balance dependency unset")
		return admin.AdminIdentity{}, 0, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		writeAdminAuthError(w, err)
		return admin.AdminIdentity{}, 0, false
	}
	tenantID, err := strconv.ParseInt(r.URL.Query().Get("tenant_id"), 10, 64)
	if err != nil || tenantID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_tenant_id", "tenant_id must be a positive integer")
		return admin.AdminIdentity{}, 0, false
	}
	return ident, tenantID, true
}

func canReadBalanceTenant(identity admin.AdminIdentity, tenantID int64) bool {
	return identity.Role == admin.RolePlatformAdmin ||
		(identity.Role == admin.RoleTenantOperator && identity.ScopeTenantID == tenantID)
}

func optionalPositiveInt64Query(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, true
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_"+name, name+" must be a positive integer")
		return 0, false
	}
	return value, true
}

func boundedIntQuery(w http.ResponseWriter, r *http.Request, name string, fallback, invalidAtOrBelow, maximum int) (int, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= invalidAtOrBelow || value > maximum {
		writeError(w, http.StatusBadRequest, "invalid_"+name, name+" is outside the allowed range")
		return 0, false
	}
	return value, true
}

func writeBalanceJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
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
		if ident.Role != admin.RolePlatformAdmin && ident.Role != admin.RoleTenantOperator {
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
		if req.TenantID <= 0 || req.Amount.IsZero() || req.Reason == "" || req.IdempotencyKey == "" {
			writeError(w, http.StatusBadRequest, "missing_fields",
				"tenant_id, non-zero amount, reason, and idempotency_key are required")
			return
		}
		result, err := d.Service.AdminAdjustBalance(r.Context(), balanceledger.AdminBalanceAdjustmentInput{
			TenantID:           req.TenantID,
			UserID:             req.UserID,
			Amount:             req.Amount,
			CurrencyCode:       req.CurrencyCode,
			ActorRole:          ident.Role,
			ActorScopeTenantID: ident.ScopeTenantID,
			ActorRef:           ident.AuditActor(),
			Reason:             req.Reason,
			RequestID:          middleware.GetReqID(r.Context()),
			IdempotencyKey:     req.IdempotencyKey,
		})
		if err != nil {
			writeBalanceCreditError(w, err)
			return
		}

		resp := balanceCreditResponseBody{
			TransactionID: result.TransactionID,
			TenantID:      result.TenantID,
			TargetKind:    result.TargetKind,
			NetBalance:    result.NewBalance.StringFixed(8),
			CurrencyCode:  result.CurrencyCode,
			Idempotent:    result.Idempotent,
		}
		if result.UserID > 0 {
			resp.UserID = &result.UserID
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func writeBalanceCreditError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, balanceledger.ErrBalanceAdjustmentForbidden):
		writeError(w, http.StatusForbidden, "balance_adjustment_forbidden",
			"the authenticated administrator cannot adjust this target")
	case errors.Is(err, balanceledger.ErrBalanceInsufficient):
		writeError(w, http.StatusConflict, "balance_insufficient",
			"the source account has insufficient available balance")
	case errors.Is(err, balanceledger.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_balance_adjustment", err.Error())
	case errors.Is(err, balanceledger.ErrUserNotFound):
		writeError(w, http.StatusNotFound, "user_not_found", "target user not found")
	case errors.Is(err, balanceledger.ErrTenantNotFound):
		writeError(w, http.StatusNotFound, "tenant_not_found", "target tenant not found")
	case errors.Is(err, balanceledger.ErrAccountInactive):
		writeError(w, http.StatusBadRequest, "account_inactive", "target tenant or user is inactive")
	case errors.Is(err, balanceledger.ErrExternalTradeConflict):
		writeError(w, http.StatusConflict, "balance_adjustment_idempotency_conflict",
			"idempotency_key was already used for a different balance adjustment")
	case errors.Is(err, balanceledger.ErrStoreNotConfigured):
		writeError(w, http.StatusServiceUnavailable, "balance_backend_unavailable", "balance service unavailable")
	default:
		writeError(w, http.StatusServiceUnavailable, "balance_backend_error",
			fmt.Sprintf("admin balance adjustment failed: %v", err))
	}
}
