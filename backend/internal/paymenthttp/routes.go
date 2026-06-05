package paymenthttp

import (
	"context"
	"encoding/json"
	"errors"
	"expvar"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

const (
	maxRechargeBodyBytes = 16 << 10
	maxWebhookBodyBytes  = 256 << 10

	legacyPaymentWebhookPath    = "/v1/payment/webhooks/{provider}"
	canonicalPaymentWebhookPath = "/v1/payments/webhooks/{provider}"
)

var (
	defaultDailyAmountLimit             = decimal.RequireFromString("500.00000000")
	legacyPaymentWebhookDeprecatedTotal = expvar.NewInt("payment_webhook_legacy_path_deprecated_total")
)

type PaymentService interface {
	OpenRecharge(context.Context, payment.OpenInput) (payment.OpenResult, error)
	FulfillVerifiedCallback(context.Context, payment.VerifiedCallback) (payment.VerifiedCallbackResult, error)
}

type Deps struct {
	Service             PaymentService
	Providers           map[string]ProviderBinding
	Clock               func() time.Time
	ExternalTradeSuffix func() (string, error)
	MaxPendingPerUser   int
	DailyAmountLimit    decimal.Decimal
}

type createRechargeRequest struct {
	Amount    decimal.Decimal `json:"amount"`
	Currency  string          `json:"currency"`
	Provider  string          `json:"provider"`
	ReturnURL string          `json:"return_url,omitempty"`
}

type createRechargeResponse struct {
	Order rechargeOrderView `json:"order"`
}

type rechargeOrderView struct {
	ID              int64  `json:"id"`
	ExternalTradeNo string `json:"external_trade_no"`
	RechargeRef     string `json:"recharge_ref"`
	Status          string `json:"status"`
	Amount          string `json:"amount"`
	Currency        string `json:"currency"`
	Provider        string `json:"provider"`
	CreatedAt       string `json:"created_at"`
}

type webhookResponse struct {
	OrderID     int64  `json:"order_id,omitempty"`
	UserID      int64  `json:"user_id,omitempty"`
	NewBalance  string `json:"new_balance,omitempty"`
	Idempotent  bool   `json:"idempotent"`
	Completed   bool   `json:"completed"`
	AuditReason string `json:"audit_reason"`
}

func MountRoutes(r chi.Router, d Deps) {
	MountUserRoutes(r, d)
	MountWebhookRoutes(r, d)
}

func MountUserRoutes(r chi.Router, d Deps) {
	r.Post("/v1/users/me/recharges", newCreateRechargeHandler(d))
}

func MountWebhookRoutes(r chi.Router, d Deps) {
	r.Post(legacyPaymentWebhookPath, newLegacyWebhookHandler(d))
}

func newCreateRechargeHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Service == nil {
			writeError(w, http.StatusServiceUnavailable, "payment_service_unavailable", "payment service unset")
			return
		}
		ident, ok := sessionauth.SessionFromContext(r.Context())
		if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
			writeError(w, http.StatusUnauthorized, "session_required", "session bearer token is required")
			return
		}
		var req createRechargeRequest
		if !decodeStrictJSON(w, r, maxRechargeBodyBytes, &req) {
			return
		}
		providerName := normalizeProviderName(req.Provider)
		if providerName == "" {
			writeError(w, http.StatusBadRequest, "payment_provider_required", "provider is required")
			return
		}
		if _, ok := d.Providers[providerName]; !ok {
			writeError(w, http.StatusBadRequest, "payment_provider_unavailable", "payment provider is not configured")
			return
		}
		suffixGen := d.ExternalTradeSuffix
		if suffixGen == nil {
			suffixGen = randomExternalTradeSuffix
		}
		suffix, err := suffixGen()
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "external_trade_no_failed", "failed to generate recharge order id")
			return
		}
		maxPending := d.MaxPendingPerUser
		if maxPending <= 0 {
			maxPending = 3
		}
		dailyLimit := d.DailyAmountLimit
		if dailyLimit.IsZero() {
			dailyLimit = defaultDailyAmountLimit
		}
		now := nowUTC(d)
		result, err := d.Service.OpenRecharge(r.Context(), payment.OpenInput{
			TenantID:          ident.TenantID,
			UserID:            ident.UserID,
			ExternalTradeNo:   externalTradeNoForTenant(ident.TenantID, suffix),
			Provider:          providerName,
			Amount:            req.Amount,
			CurrencyCode:      req.Currency,
			MaxPendingPerUser: maxPending,
			DailyAmountLimit:  dailyLimit,
			Now:               now,
		})
		if err != nil {
			writePaymentOpenError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, createRechargeResponse{Order: rechargeOrderViewFromOrder(result.Order, providerName)})
	}
}

func newLegacyWebhookHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		providerName := normalizeProviderName(chi.URLParam(r, "provider"))
		recordLegacyWebhookDeprecation(r.Context(), providerName)
		binding, ok := d.Providers[providerName]
		if !ok || binding.Provider == nil {
			writeError(w, http.StatusNotFound, "payment_provider_not_found", "payment provider is not configured")
			return
		}
		if d.Service == nil {
			writeError(w, http.StatusServiceUnavailable, "payment_service_unavailable", "payment service unset")
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBodyBytes))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_webhook_body", "webhook body is too large or unreadable")
			return
		}
		cb, err := binding.Provider.VerifyWebhook(raw, r.Header, binding.Secret)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_payment_signature", "payment webhook signature is invalid")
			return
		}
		if cb.Provider == "" {
			cb.Provider = providerName
		}
		if cb.Provider != providerName {
			writeJSON(w, http.StatusOK, webhookResponse{AuditReason: payment.AuditReasonProviderMismatch})
			return
		}
		tenantID, ok := tenantIDFromExternalTradeNo(cb.ExternalTradeNo)
		if !ok {
			writeJSON(w, http.StatusOK, webhookResponse{AuditReason: payment.AuditReasonOrderNotFound})
			return
		}
		cb.TenantID = tenantID
		if cb.Timestamp.IsZero() {
			cb.Timestamp = nowUTC(d)
		}
		result, err := d.Service.FulfillVerifiedCallback(r.Context(), cb)
		status := result.HTTPStatus
		if status == 0 {
			status = http.StatusOK
		}
		if err != nil && !webhookErrorIsAuditOnly(err) {
			writePaymentWebhookError(w, err)
			return
		}
		writeJSON(w, status, webhookResultView(result))
	}
}

func recordLegacyWebhookDeprecation(ctx context.Context, providerName string) {
	legacyPaymentWebhookDeprecatedTotal.Add(1)
	slog.WarnContext(ctx, "payment webhook legacy path used",
		slog.String("event", "payment_webhook_legacy_path_deprecated"),
		slog.String("legacy_path", legacyPaymentWebhookPath),
		slog.String("canonical_path", canonicalPaymentWebhookPath),
		slog.String("provider", providerName),
	)
}

func decodeStrictJSON(w http.ResponseWriter, r *http.Request, limit int64, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return false
	}
	return true
}

func webhookErrorIsAuditOnly(err error) bool {
	return errors.Is(err, payment.ErrPaymentAmountMismatch) ||
		errors.Is(err, payment.ErrPaymentProviderMismatch) ||
		errors.Is(err, payment.ErrOrderNotFound) ||
		errors.Is(err, payment.ErrOrderStateConflict)
}

func writePaymentOpenError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, payment.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_recharge_request", "recharge request is invalid")
	case errors.Is(err, payment.ErrUserNotFound):
		writeError(w, http.StatusNotFound, "user_not_found", "session user not found")
	case errors.Is(err, payment.ErrAccountInactive):
		writeError(w, http.StatusBadRequest, "account_inactive", "tenant or user is inactive")
	case errors.Is(err, payment.ErrPendingLimit):
		writeError(w, http.StatusConflict, "recharge_pending_limit", "too many pending recharge orders")
	case errors.Is(err, payment.ErrDailyAmountLimit):
		writeError(w, http.StatusConflict, "recharge_daily_limit", "daily recharge amount limit reached")
	case errors.Is(err, payment.ErrExternalTradeConflict):
		writeError(w, http.StatusConflict, "recharge_trade_conflict", "external trade number conflict")
	case errors.Is(err, payment.ErrStoreNotConfigured):
		writeError(w, http.StatusServiceUnavailable, "payment_backend_unavailable", "payment backend unavailable")
	default:
		writeError(w, http.StatusServiceUnavailable, "payment_backend_error", "payment backend transient failure")
	}
}

func writePaymentWebhookError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, payment.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_webhook_payload", "payment webhook payload is invalid")
	case errors.Is(err, payment.ErrStoreNotConfigured):
		writeError(w, http.StatusServiceUnavailable, "payment_backend_unavailable", "payment backend unavailable")
	default:
		writeError(w, http.StatusServiceUnavailable, "payment_backend_error", "payment backend transient failure")
	}
}

func webhookResultView(result payment.VerifiedCallbackResult) webhookResponse {
	resp := webhookResponse{
		OrderID:     result.OrderID,
		UserID:      result.UserID,
		Idempotent:  result.Idempotent,
		Completed:   result.Completed,
		AuditReason: result.AuditReason,
	}
	if !result.NewBalance.IsZero() {
		resp.NewBalance = result.NewBalance.StringFixed(8)
	}
	return resp
}

func rechargeOrderViewFromOrder(order payment.Order, providerName string) rechargeOrderView {
	return rechargeOrderView{
		ID:              order.ID,
		ExternalTradeNo: order.OutTradeNo,
		RechargeRef:     "pay_" + strings.TrimSpace(order.OutTradeNo),
		Status:          string(order.Status),
		Amount:          decimal.NewFromInt(order.AmountCents).Div(decimal.NewFromInt(100)).StringFixed(2),
		Currency:        order.CurrencyCode,
		Provider:        providerName,
		CreatedAt:       order.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func nowUTC(d Deps) time.Time {
	if d.Clock != nil {
		return d.Clock().UTC()
	}
	return time.Now().UTC()
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"` + strings.ReplaceAll(message, `"`, `'`) + `"}}`))
}
