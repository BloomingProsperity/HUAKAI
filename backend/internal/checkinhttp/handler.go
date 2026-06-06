package checkinhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/checkin"
	"github.com/BloomingProsperity/HUAKAI/internal/payment"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

type Service interface {
	DoCheckin(context.Context, int64, int64) (checkin.Result, error)
	GetStatus(context.Context, int64, int64, string) (checkin.Status, error)
}

type Deps struct {
	Service Service
}

type postResponse struct {
	RewardCents int64  `json:"reward_cents"`
	CheckinDate string `json:"checkin_date"`
	NewBalance  int64  `json:"new_balance"`
}

type statusResponse struct {
	Enabled        bool         `json:"enabled"`
	MinCents       int64        `json:"min_cents"`
	MaxCents       int64        `json:"max_cents"`
	Month          string       `json:"month"`
	CheckedInToday bool         `json:"checked_in_today"`
	Records        []recordView `json:"records"`
}

type recordView struct {
	CheckinDate    string `json:"checkin_date"`
	RewardCents    int64  `json:"reward_cents"`
	CurrencyCode   string `json:"currency_code"`
	BillingEventID int64  `json:"billing_event_id,omitempty"`
	CreatedAt      string `json:"created_at"`
}

func MountRoutes(r chi.Router, d Deps) {
	r.Get("/checkin", newStatusHandler(d))
	r.Post("/checkin", newPostHandler(d))
}

func newPostHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Service == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "check-in dependency unset")
			return
		}
		ident, ok := sessionauth.SessionFromContext(r.Context())
		if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
			writeJSONError(w, http.StatusUnauthorized, "session_token_required", "session bearer token is required")
			return
		}
		res, err := d.Service.DoCheckin(r.Context(), ident.TenantID, ident.UserID)
		if err != nil {
			writeCheckinError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, postResponse{
			RewardCents: res.RewardCents,
			CheckinDate: formatDate(res.CheckinDate),
			NewBalance:  res.NewBalance,
		})
	}
}

func newStatusHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Service == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "check-in dependency unset")
			return
		}
		ident, ok := sessionauth.SessionFromContext(r.Context())
		if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
			writeJSONError(w, http.StatusUnauthorized, "session_token_required", "session bearer token is required")
			return
		}
		status, err := d.Service.GetStatus(r.Context(), ident.TenantID, ident.UserID, strings.TrimSpace(r.URL.Query().Get("month")))
		if err != nil {
			writeCheckinError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toStatusResponse(status))
	}
}

func toStatusResponse(status checkin.Status) statusResponse {
	records := make([]recordView, 0, len(status.Records))
	for _, rec := range status.Records {
		records = append(records, recordView{
			CheckinDate:    formatDate(rec.CheckinDate),
			RewardCents:    rec.RewardCents,
			CurrencyCode:   rec.CurrencyCode,
			BillingEventID: rec.BillingEventID,
			CreatedAt:      rec.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return statusResponse{
		Enabled:        status.Enabled,
		MinCents:       status.MinCents,
		MaxCents:       status.MaxCents,
		Month:          status.Month,
		CheckedInToday: status.CheckedInToday,
		Records:        records,
	}
}

func writeCheckinError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, checkin.ErrDisabled):
		writeJSONError(w, http.StatusNotFound, "daily_checkin_disabled", "daily check-in is disabled")
	case errors.Is(err, checkin.ErrAlreadyCheckedIn):
		writeJSONError(w, http.StatusConflict, "daily_checkin_already_claimed", "daily check-in already claimed for this UTC day")
	case errors.Is(err, checkin.ErrInvalidInput):
		writeJSONError(w, http.StatusBadRequest, "invalid_checkin_request", "check-in request is invalid")
	case errors.Is(err, checkin.ErrInvalidConfig), errors.Is(err, platformsettings.ErrInvalidValue):
		writeJSONError(w, http.StatusServiceUnavailable, "daily_checkin_config_invalid", "daily check-in configuration is invalid")
	case errors.Is(err, checkin.ErrStoreNotConfigured), errors.Is(err, payment.ErrStoreNotConfigured):
		writeJSONError(w, http.StatusServiceUnavailable, "checkin_backend_unavailable", "check-in service unavailable")
	case errors.Is(err, payment.ErrUserNotFound), errors.Is(err, payment.ErrAccountInactive):
		writeJSONError(w, http.StatusForbidden, "account_not_active", "account is not active")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "checkin_backend_error", "check-in backend unavailable")
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func formatDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}
