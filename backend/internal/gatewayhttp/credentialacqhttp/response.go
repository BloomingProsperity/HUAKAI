package credentialacqhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/projectenrich"
)

func writeCredentialAcqError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, credentialacq.ErrFlowNotFound):
		writeJSONError(w, http.StatusNotFound, "credential_acquisition_not_found", "credential acquisition flow not found")
	case errors.Is(err, credentialacq.ErrFlowExpired):
		writeJSONError(w, http.StatusGone, "credential_acquisition_expired", "credential acquisition flow expired")
	case errors.Is(err, credentialacq.ErrFlowReplay):
		writeJSONError(w, http.StatusConflict, "credential_acquisition_replay", "credential acquisition flow already consumed")
	case errors.Is(err, credentialacq.ErrStateMismatch):
		writeJSONError(w, http.StatusBadRequest, "oauth_state_mismatch", "oauth state mismatch")
	case errors.Is(err, credentialacq.ErrUnknownMode):
		writeJSONError(w, http.StatusBadRequest, "unknown_credential_mode", "unknown vendor/auth_mode")
	case errors.Is(err, credentialacq.ErrInvalidImportBody):
		writeJSONError(w, http.StatusBadRequest, "invalid_import_body", "invalid import body")
	case errors.Is(err, credentialacq.ErrFeatureDisabled):
		writeJSONError(w, http.StatusForbidden, "credential_acquisition_feature_disabled", "credential acquisition feature disabled")
	case errors.Is(err, credentialacq.ErrSecretInContext):
		writeJSONError(w, http.StatusBadRequest, "redacted_context_secret", "redacted_context contains secret-shaped material")
	case errors.Is(err, credentialacq.ErrOAuthExchangerMissing):
		writeJSONError(w, http.StatusUnprocessableEntity, "oauth_exchanger_missing", "oauth exchanger missing for credential acquisition flow")
	case errors.Is(err, credentialacq.ErrOAuthRequiresCallback):
		writeJSONError(w, http.StatusConflict, "oauth_requires_callback", "OAuth flow must be validated via callback before finalize")
	case errors.Is(err, credentialacq.ErrDevicePollInProgress):
		writeJSONError(w, http.StatusConflict, "credential_acquisition_poll_in_progress", "credential acquisition poll already in progress")
	case errors.Is(err, credentialacq.ErrDevicePollTransient):
		writeJSONError(w, http.StatusServiceUnavailable, "credential_acquisition_poll_deferred", "upstream device authorization is temporarily unavailable")
	case errors.Is(err, credentialacq.ErrDeviceAccessDenied):
		writeJSONError(w, http.StatusForbidden, "credential_acquisition_authorization_denied", "device authorization was denied")
	case errors.Is(err, credentialacq.ErrDeviceExchangeAmbiguous):
		writeJSONError(w, http.StatusBadGateway, "credential_acquisition_exchange_ambiguous", "device token exchange outcome is ambiguous; start a new flow")
	case errors.Is(err, credentialacq.ErrInvalidTokenShape), errors.Is(err, credentialacq.ErrResponseTooLarge):
		writeJSONError(w, http.StatusUnprocessableEntity, "credential_acquisition_invalid_upstream_response", "device authorization response failed validation")
	case errors.Is(err, projectenrich.ErrProjectMetadataConflict):
		writeJSONError(w, http.StatusConflict, "project_metadata_conflict", "账号项目身份与上游识别结果冲突，需要人工消歧")
	case errors.Is(err, projectenrich.ErrProjectMetadataUnavailable):
		writeJSONError(w, http.StatusServiceUnavailable, "project_metadata_unavailable", "账号项目身份暂时无法确认，凭据未写入")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		writeJSONError(w, http.StatusRequestTimeout, "credential_acquisition_poll_cancelled", "credential acquisition poll was cancelled and may be retried")
	default:
		writeJSONError(w, http.StatusBadRequest, "credential_acquisition_failed", err.Error())
	}
}

func mergeRedactedContext(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func firstNonEmptyGateway(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstReason(values ...string) string {
	if got := firstNonEmptyGateway(values...); got != "" {
		return got
	}
	return "credential acquisition"
}

func decodeAdminPoolJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	return true
}

func parseAdminPoolID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_provider_account_id", "id must be a positive int64")
		return 0, false
	}
	return id, true
}

func writeAuditJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeAuditJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func chineseReason(got, fallback string) *string {
	if reason := strings.TrimSpace(got); reason != "" {
		return &reason
	}
	return &fallback
}
