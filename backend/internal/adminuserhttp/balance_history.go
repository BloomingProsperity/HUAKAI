package adminuserhttp

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

type balanceHistoryBody struct {
	ID          int64  `json:"id"`
	EventType   string `json:"event_type"`
	Amount      string `json:"amount"`
	Fingerprint string `json:"fingerprint"`
	SourceType  string `json:"source_type"`
	SourceID    int64  `json:"source_id"`
	OccurredAt  string `json:"occurred_at"`
}

func newBalanceHistoryHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, ok := resolveTenant(w, r, d)
		if !ok {
			return
		}
		userID, ok := pathID(w, r)
		if !ok {
			return
		}
		limit, offset, ok := pagination(w, r)
		if !ok {
			return
		}
		if _, err := d.Store.AdminGetUserForTenant(r.Context(), admindb.AdminGetUserForTenantParams{
			TenantID: tenantID,
			UserID:   userID,
		}); errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "admin_user_not_found", "user not found")
			return
		} else if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error",
				fmt.Sprintf("get user failed: %v", err))
			return
		}
		rows, err := d.Store.AdminListUserBalanceHistoryForTenant(r.Context(), admindb.AdminListUserBalanceHistoryForTenantParams{
			TenantID:   tenantID,
			UserID:     userID,
			PageLimit:  limit,
			PageOffset: offset,
		})
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error",
				fmt.Sprintf("list balance history failed: %v", err))
			return
		}
		items := make([]balanceHistoryBody, 0, len(rows))
		for _, row := range rows {
			items = append(items, balanceHistoryBody{
				ID:          row.ID,
				EventType:   row.EventType,
				Amount:      row.Amount,
				Fingerprint: row.Fingerprint,
				SourceType:  row.SourceType,
				SourceID:    row.SourceID,
				OccurredAt:  timestamp(row.OccurredAt.Time),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items":  items,
			"limit":  limit,
			"offset": offset,
		})
	}
}
