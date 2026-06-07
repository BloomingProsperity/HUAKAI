package invitevalidatehttp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

type Store interface {
	InviteCodeStatus(context.Context, int64, string) (userauth.InviteCodeStatus, error)
}

type Settings interface {
	Get(context.Context, platformsettings.SettingKey) (platformsettings.StoredSetting, error)
}

type Deps struct {
	Store    Store
	Settings Settings
}

type validateRequest struct {
	TenantID   int64  `json:"tenant_id"`
	InviteCode string `json:"invite_code"`
}

type validateResponse struct {
	Valid  bool   `json:"valid"`
	Reason string `json:"reason"`
}

func NewHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req validateRequest
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		if !invitationRequired(r.Context(), d.Settings) {
			writeJSON(w, http.StatusOK, validateResponse{Valid: true, Reason: string(userauth.InviteCodeStatusDisabled)})
			return
		}
		if req.TenantID <= 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid_request", "tenant_id must be positive")
			return
		}
		code := strings.TrimSpace(req.InviteCode)
		if code == "" {
			writeJSON(w, http.StatusOK, validateResponse{Valid: false, Reason: string(userauth.InviteCodeStatusNotFound)})
			return
		}
		if d.Store == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "invite validation dependency unset")
			return
		}
		status, err := d.Store.InviteCodeStatus(r.Context(), req.TenantID, code)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "invite_validation_backend_error", "invite validation service unavailable")
			return
		}
		writeJSON(w, http.StatusOK, validateResponse{
			Valid:  status == userauth.InviteCodeStatusValid,
			Reason: string(status),
		})
	}
}

func invitationRequired(ctx context.Context, settings Settings) bool {
	value, _ := platformsettings.DefaultValue(platformsettings.KeyInvitationRequired)
	if settings != nil {
		if setting, err := settings.Get(ctx, platformsettings.KeyInvitationRequired); err == nil && strings.TrimSpace(setting.Value) != "" {
			value = setting.Value
		}
	}
	return strings.TrimSpace(value) == "true"
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]map[string]string{
		"error": {"code": code, "message": message},
	})
}
