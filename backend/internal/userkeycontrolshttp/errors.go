package userkeycontrolshttp

import (
	"errors"
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/userkeycontrols"
)

func writeControlsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, userkeycontrols.ErrInvalidQuota):
		writeError(w, http.StatusBadRequest, "invalid_quota", "limit_usd must be a non-negative decimal")
	case errors.Is(err, userkeycontrols.ErrInvalidGroup):
		writeError(w, http.StatusBadRequest, "invalid_group_id", "group_id must be a positive int64 or null")
	case errors.Is(err, userkeycontrols.ErrKeyNotFound):
		writeError(w, http.StatusNotFound, "api_key_not_found", "api_key not found")
	case errors.Is(err, userkeycontrols.ErrQuotaPolicyNotFound):
		writeError(w, http.StatusNotFound, "api_key_quota_not_found", "api_key quota policy not found")
	case errors.Is(err, userkeycontrols.ErrGroupNotFound):
		writeError(w, http.StatusNotFound, "api_key_group_not_found", "api_key group not found")
	case errors.Is(err, userkeycontrols.ErrServiceMisconfig):
		writeError(w, http.StatusServiceUnavailable, "userkey_controls_service_unavailable", "user api key controls service unavailable")
	default:
		writeError(w, http.StatusServiceUnavailable, "userkey_controls_backend_error", "user api key controls backend transient failure")
	}
}
