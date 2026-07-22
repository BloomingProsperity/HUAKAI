package gatewayhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminhttpcore"
	"github.com/go-chi/chi/v5"
)

func decodeAdminPoolJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	return adminhttpcore.DecodeJSON(w, r, dst)
}

func writeAuditJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func chineseReason(got, fallback string) *string {
	return adminhttpcore.Reason(got, fallback)
}

func writeProviderAccountAudit(ctx context.Context, r *http.Request, store adminhttpcore.AuditStore, ident admin.AdminIdentity, tenantID int64, action string, targetID int64, reason *string, payload []byte) error {
	return adminhttpcore.WriteAudit(ctx, r, store, ident, &tenantID, action, "provider_account", &targetID, reason, payload)
}

func parseAdminPoolID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_provider_account_id", "id must be a positive int64")
		return 0, false
	}
	return id, true
}
