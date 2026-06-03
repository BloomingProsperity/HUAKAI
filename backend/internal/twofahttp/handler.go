package twofahttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
	"github.com/BloomingProsperity/HUAKAI/internal/twofa"
)

type Service interface {
	Setup(context.Context, twofa.SetupInput) (twofa.SetupResult, error)
	Enable(context.Context, twofa.VerifyInput) (twofa.Status, error)
	Disable(context.Context, int64, int64) error
	Status(context.Context, int64, int64) (twofa.Status, error)
	RegenerateBackupCodes(context.Context, twofa.VerifyInput) (twofa.BackupCodesResult, error)
}

type Settings interface {
	Get(context.Context, platformsettings.SettingKey) (platformsettings.StoredSetting, error)
}

type Deps struct {
	Service  Service
	Settings Settings
}

type setupRequest struct {
	AccountName string `json:"account_name"`
}

type codeRequest struct {
	Code string `json:"code"`
}

func MountRoutes(r chi.Router, d Deps) {
	r.Post("/setup", newSetupHandler(d))
	r.Post("/enable", newEnableHandler(d))
	r.Get("/status", newStatusHandler(d))
	r.Post("/disable", newDisableHandler(d))
	r.Post("/backup-codes/regenerate", newRegenerateBackupCodesHandler(d))
}

func newSetupHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := sessionIdentity(w, r)
		if !ok {
			return
		}
		if !platformTwoFAEnabled(r.Context(), d.Settings) {
			writeError(w, http.StatusForbidden, "two_factor_disabled", "two-factor authentication is disabled")
			return
		}
		if d.Service == nil {
			writeError(w, http.StatusServiceUnavailable, "two_factor_not_configured", "two-factor service dependency unset")
			return
		}
		var req setupRequest
		if !decodeOptionalJSON(w, r, &req) {
			return
		}
		result, err := d.Service.Setup(r.Context(), twofa.SetupInput{
			TenantID: ident.TenantID, UserID: ident.UserID, AccountName: req.AccountName,
		})
		if err != nil {
			writeTwoFAError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

func newEnableHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := sessionIdentity(w, r)
		if !ok {
			return
		}
		if !platformTwoFAEnabled(r.Context(), d.Settings) {
			writeError(w, http.StatusForbidden, "two_factor_disabled", "two-factor authentication is disabled")
			return
		}
		if d.Service == nil {
			writeError(w, http.StatusServiceUnavailable, "two_factor_not_configured", "two-factor service dependency unset")
			return
		}
		var req codeRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		status, err := d.Service.Enable(r.Context(), twofa.VerifyInput{
			TenantID: ident.TenantID, UserID: ident.UserID, Code: req.Code,
		})
		if err != nil {
			writeTwoFAError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, status)
	}
}

func newStatusHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := sessionIdentity(w, r)
		if !ok {
			return
		}
		if d.Service == nil {
			writeError(w, http.StatusServiceUnavailable, "two_factor_not_configured", "two-factor service dependency unset")
			return
		}
		available := platformTwoFAEnabled(r.Context(), d.Settings)
		status, err := d.Service.Status(r.Context(), ident.TenantID, ident.UserID)
		if err != nil {
			writeTwoFAError(w, err)
			return
		}
		resp := map[string]any{
			"available":              available,
			"enabled":                status.Enabled,
			"backup_codes_remaining": status.BackupCodesRemaining,
		}
		if status.LockedUntil != nil {
			resp["locked_until"] = status.LockedUntil
		}
		if status.LastUsedAt != nil {
			resp["last_used_at"] = status.LastUsedAt
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func newDisableHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := sessionIdentity(w, r)
		if !ok {
			return
		}
		if d.Service == nil {
			writeError(w, http.StatusServiceUnavailable, "two_factor_not_configured", "two-factor service dependency unset")
			return
		}
		if err := d.Service.Disable(r.Context(), ident.TenantID, ident.UserID); err != nil {
			writeTwoFAError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
	}
}

func newRegenerateBackupCodesHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := sessionIdentity(w, r)
		if !ok {
			return
		}
		if !platformTwoFAEnabled(r.Context(), d.Settings) {
			writeError(w, http.StatusForbidden, "two_factor_disabled", "two-factor authentication is disabled")
			return
		}
		if d.Service == nil {
			writeError(w, http.StatusServiceUnavailable, "two_factor_not_configured", "two-factor service dependency unset")
			return
		}
		var req codeRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := d.Service.RegenerateBackupCodes(r.Context(), twofa.VerifyInput{
			TenantID: ident.TenantID, UserID: ident.UserID, Code: req.Code,
		})
		if err != nil {
			writeTwoFAError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func sessionIdentity(w http.ResponseWriter, r *http.Request) (sessionauth.SessionIdentity, bool) {
	ident, ok := sessionauth.SessionFromContext(r.Context())
	if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
		writeError(w, http.StatusUnauthorized, "session_token_required", "session bearer token is required")
		return sessionauth.SessionIdentity{}, false
	}
	return ident, true
}

func platformTwoFAEnabled(ctx context.Context, settings Settings) bool {
	if settings == nil {
		return false
	}
	setting, err := settings.Get(ctx, platformsettings.KeyTwoFactorEnabled)
	return err == nil && setting.Value == "true"
}

func decodeJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(out); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_two_factor_request", "invalid JSON body")
		return false
	}
	return true
}

func decodeOptionalJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(out); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_two_factor_request", "invalid JSON body")
		return false
	}
	return true
}

func writeTwoFAError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, twofa.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_two_factor_request", "two-factor request is invalid")
	case errors.Is(err, twofa.ErrInvalidCode):
		writeError(w, http.StatusUnauthorized, "two_factor_invalid", "two-factor code is invalid")
	case errors.Is(err, twofa.ErrLocked):
		writeError(w, http.StatusTooManyRequests, "two_factor_locked", "two-factor verification is temporarily locked")
	case errors.Is(err, twofa.ErrDisabled):
		writeError(w, http.StatusForbidden, "two_factor_disabled", "two-factor authentication is disabled")
	case errors.Is(err, twofa.ErrNotSetup):
		writeError(w, http.StatusNotFound, "two_factor_not_setup", "two-factor authentication is not setup")
	case errors.Is(err, twofa.ErrAlreadyEnabled):
		writeError(w, http.StatusConflict, "two_factor_already_enabled", "two-factor authentication is already enabled")
	default:
		writeError(w, http.StatusServiceUnavailable, "two_factor_backend_error", "two-factor service unavailable")
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    strings.TrimSpace(code),
			"message": strings.TrimSpace(message),
		},
	})
}
