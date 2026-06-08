// HUAKAI · iKun

// Package voucherhttp exposes voucher user HTTP endpoints that are not part of
// the legacy gatewayhttp voucher handler bundle.
package voucherhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/voucher"
)

type Service interface {
	ListRedemptionsByUser(context.Context, int64, int64, int) ([]voucher.Redemption, error)
}

type Deps struct {
	Service Service
}

type redemptionView struct {
	VoucherID      int64     `json:"voucher_id"`
	AmountCents    int64     `json:"amount_cents"`
	CurrencyCode   string    `json:"currency_code"`
	Status         string    `json:"status"`
	RedeemedAt     time.Time `json:"redeemed_at"`
	BillingEventID int64     `json:"billing_event_id"`
}

func NewRedemptionHistoryHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Service == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "voucher dependency unset")
			return
		}
		ident, ok := sessionauth.SessionFromContext(r.Context())
		if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
			writeJSONError(w, http.StatusUnauthorized, "session_token_required", "session bearer token is required")
			return
		}
		limit, ok := parseLimit(w, r)
		if !ok {
			return
		}
		rows, err := d.Service.ListRedemptionsByUser(r.Context(), ident.TenantID, ident.UserID, limit)
		if err != nil {
			writeVoucherError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"redemptions": toRedemptionViews(rows)})
	}
}

func toRedemptionViews(rows []voucher.Redemption) []redemptionView {
	out := make([]redemptionView, 0, len(rows))
	for _, row := range rows {
		out = append(out, redemptionView{
			VoucherID:      row.VoucherID,
			AmountCents:    row.AmountCents,
			CurrencyCode:   row.CurrencyCode,
			Status:         row.Status,
			RedeemedAt:     row.RedeemedAt.UTC(),
			BillingEventID: row.BillingEventID,
		})
	}
	return out
}

func parseLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return 50, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 || limit > 200 {
		writeJSONError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 200")
		return 0, false
	}
	return limit, true
}

func writeVoucherError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, voucher.ErrInvalidInput):
		writeJSONError(w, http.StatusBadRequest, "invalid_voucher_request", "voucher request is invalid")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "voucher_backend_error", "voucher service unavailable")
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]map[string]string{
		"error": {"code": code, "message": message},
	})
}
