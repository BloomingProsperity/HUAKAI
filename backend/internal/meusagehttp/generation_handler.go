package meusagehttp

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

type GenerationStore interface {
	GetUsageRecordByRequestID(context.Context, dbbilling.GetUsageRecordByRequestIDParams) (dbbilling.GetUsageRecordByRequestIDRow, error)
}

type GenerationDeps struct {
	Auth  AuthResolver
	Store GenerationStore
}

func NewGenerationHandler(d GenerationDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Store == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "generation dependency unset")
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if errors.Is(err, auth.ErrAuthMisconfigured) {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "auth tables unavailable")
			return
		}
		if errors.Is(err, auth.ErrAuthBackend) {
			writeJSONError(w, http.StatusServiceUnavailable, "auth_backend_error", "auth backend transient failure")
			return
		}
		if errors.Is(err, auth.ErrForbidden) {
			writeJSONError(w, http.StatusForbidden, "forbidden", "api key policy forbids this request")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized", "invalid bearer")
			return
		}
		requestID := strings.TrimSpace(r.URL.Query().Get("id"))
		if requestID == "" {
			writeJSONError(w, http.StatusBadRequest, "invalid_request_id", "id query parameter is required")
			return
		}
		row, err := d.Store.GetUsageRecordByRequestID(r.Context(), dbbilling.GetUsageRecordByRequestIDParams{
			TenantID:  ident.TenantID,
			UserID:    ident.UserID,
			APIKeyID:  ident.APIKeyID,
			RequestID: requestID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSONError(w, http.StatusNotFound, "generation_not_found", "generation not found")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "usage_query_failed", "usage backend unavailable")
			return
		}
		writeJSON(w, http.StatusOK, mapGenerationUsageRecord(row, ident.TenantID))
	}
}
