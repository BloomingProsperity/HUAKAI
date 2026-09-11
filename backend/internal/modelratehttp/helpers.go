package modelratehttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]map[string]string{
		"error": {"code": code, "message": message},
	})
}

func writeAdminAuthError(w http.ResponseWriter, err error) {
	if errors.Is(err, admin.ErrAdminBackend) {
		writeError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
		return
	}
	writeError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
}

func writeAdminError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, admin.ErrAdminUnauthorized):
		writeError(w, http.StatusUnauthorized, "admin_unauthorized", "")
	case errors.Is(err, admin.ErrAdminForbidden):
		writeError(w, http.StatusForbidden, "admin_forbidden", "caller cannot act on this resource")
	case errors.Is(err, admin.ErrAdminBackend):
		writeError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin backend transient failure")
	default:
		writeError(w, http.StatusInternalServerError, "admin_unknown_error", err.Error())
	}
}

func parseLimitOffset(w http.ResponseWriter, r *http.Request) (int32, int32, bool) {
	limit := int32(50)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || value < 1 || value > 500 {
			writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be a positive integer between 1 and 500")
			return 0, 0, false
		}
		limit = int32(value)
	}
	offset := int32(0)
	if raw := r.URL.Query().Get("offset"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || value < 0 {
			writeError(w, http.StatusBadRequest, "invalid_offset", "offset must be a non-negative integer")
			return 0, 0, false
		}
		offset = int32(value)
	}
	return limit, offset, true
}

func paginate[T any](items []T, limit, offset int32) []T {
	if int(offset) >= len(items) {
		return nil
	}
	end := int(offset + limit)
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}
