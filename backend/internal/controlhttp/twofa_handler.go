package controlhttp

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
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

type TwoFAService interface {
	Setup(context.Context, twofa.SetupInput) (twofa.SetupResult, error)
	Enable(context.Context, twofa.VerifyInput) (twofa.Status, error)
	Disable(context.Context, int64, int64) error
	Status(context.Context, int64, int64) (twofa.Status, error)
	RegenerateBackupCodes(context.Context, twofa.VerifyInput) (twofa.BackupCodesResult, error)
	VerifyLogin(context.Context, twofa.VerifyInput) (twofa.VerifyResult, error)
}

type TwoFASettings interface {
	Get(context.Context, platformsettings.SettingKey) (platformsettings.StoredSetting, error)
}

type TwoFASessionRevoker interface {
	RevokeOthers(context.Context, usersession.RevokeOthersInput) (int64, error)
}

type TwoFADeps struct {
	Service  TwoFAService
	Settings TwoFASettings
	Sessions TwoFASessionRevoker
}

type twoFASetupRequest struct {
	AccountName string `json:"account_name"`
}

type twoFACodeRequest struct {
	Code string `json:"code"`
}

func MountTwoFARoutes(r chi.Router, d TwoFADeps) {
	r.Post("/setup", newSetupHandler(d))
	r.Post("/enable", newEnableHandler(d))
	r.Get("/status", newStatusHandler(d))
	r.Post("/disable", newDisableHandler(d))
	r.Post("/backup-codes/regenerate", newRegenerateBackupCodesHandler(d))
}

func newSetupHandler(d TwoFADeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := twoFASessionIdentity(w, r)
		if !ok {
			return
		}
		if !platformTwoFAEnabled(r.Context(), d.Settings) {
			twoFAWriteError(w, http.StatusForbidden, "two_factor_disabled", "two-factor authentication is disabled")
			return
		}
		if d.Service == nil {
			twoFAWriteError(w, http.StatusServiceUnavailable, "two_factor_not_configured", "two-factor service dependency unset")
			return
		}
		var req twoFASetupRequest
		if !twoFADecodeOptionalJSON(w, r, &req) {
			return
		}
		result, err := d.Service.Setup(r.Context(), twofa.SetupInput{
			TenantID: ident.TenantID, UserID: ident.UserID, AccountName: req.AccountName,
		})
		if err != nil {
			writeTwoFAError(w, err)
			return
		}
		twoFAWriteJSON(w, http.StatusCreated, result)
	}
}

func newEnableHandler(d TwoFADeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := twoFASessionIdentity(w, r)
		if !ok {
			return
		}
		if !platformTwoFAEnabled(r.Context(), d.Settings) {
			twoFAWriteError(w, http.StatusForbidden, "two_factor_disabled", "two-factor authentication is disabled")
			return
		}
		if d.Service == nil {
			twoFAWriteError(w, http.StatusServiceUnavailable, "two_factor_not_configured", "two-factor service dependency unset")
			return
		}
		var req twoFACodeRequest
		if !twoFADecodeJSON(w, r, &req) {
			return
		}
		status, err := d.Service.Enable(r.Context(), twofa.VerifyInput{
			TenantID: ident.TenantID, UserID: ident.UserID, Code: req.Code,
		})
		if err != nil {
			writeTwoFAError(w, err)
			return
		}
		if err := revokeSessionsAfterTwoFAChange(r.Context(), d.Sessions, ident); err != nil {
			twoFAWriteError(w, http.StatusServiceUnavailable, "session_revoke_failed", "session revocation failed after two-factor state change")
			return
		}
		twoFAWriteJSON(w, http.StatusOK, status)
	}
}

func newStatusHandler(d TwoFADeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := twoFASessionIdentity(w, r)
		if !ok {
			return
		}
		if d.Service == nil {
			twoFAWriteError(w, http.StatusServiceUnavailable, "two_factor_not_configured", "two-factor service dependency unset")
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
		twoFAWriteJSON(w, http.StatusOK, resp)
	}
}

func newDisableHandler(d TwoFADeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := twoFASessionIdentity(w, r)
		if !ok {
			return
		}
		if d.Service == nil {
			twoFAWriteError(w, http.StatusServiceUnavailable, "two_factor_not_configured", "two-factor service dependency unset")
			return
		}
		var req twoFACodeRequest
		if !twoFADecodeJSON(w, r, &req) {
			return
		}
		if _, err := d.Service.VerifyLogin(r.Context(), twofa.VerifyInput{
			TenantID: ident.TenantID, UserID: ident.UserID, Code: req.Code,
		}); err != nil {
			writeTwoFAError(w, err)
			return
		}
		if err := d.Service.Disable(r.Context(), ident.TenantID, ident.UserID); err != nil {
			writeTwoFAError(w, err)
			return
		}
		if err := revokeSessionsAfterTwoFAChange(r.Context(), d.Sessions, ident); err != nil {
			twoFAWriteError(w, http.StatusServiceUnavailable, "session_revoke_failed", "session revocation failed after two-factor state change")
			return
		}
		twoFAWriteJSON(w, http.StatusOK, map[string]any{"enabled": false})
	}
}

func newRegenerateBackupCodesHandler(d TwoFADeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := twoFASessionIdentity(w, r)
		if !ok {
			return
		}
		if !platformTwoFAEnabled(r.Context(), d.Settings) {
			twoFAWriteError(w, http.StatusForbidden, "two_factor_disabled", "two-factor authentication is disabled")
			return
		}
		if d.Service == nil {
			twoFAWriteError(w, http.StatusServiceUnavailable, "two_factor_not_configured", "two-factor service dependency unset")
			return
		}
		var req twoFACodeRequest
		if !twoFADecodeJSON(w, r, &req) {
			return
		}
		result, err := d.Service.RegenerateBackupCodes(r.Context(), twofa.VerifyInput{
			TenantID: ident.TenantID, UserID: ident.UserID, Code: req.Code,
		})
		if err != nil {
			writeTwoFAError(w, err)
			return
		}
		twoFAWriteJSON(w, http.StatusOK, result)
	}
}

func twoFASessionIdentity(w http.ResponseWriter, r *http.Request) (sessionauth.SessionIdentity, bool) {
	ident, ok := sessionauth.SessionFromContext(r.Context())
	if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
		twoFAWriteError(w, http.StatusUnauthorized, "session_token_required", "session bearer token is required")
		return sessionauth.SessionIdentity{}, false
	}
	return ident, true
}

func platformTwoFAEnabled(ctx context.Context, settings TwoFASettings) bool {
	if settings == nil {
		return false
	}
	setting, err := settings.Get(ctx, platformsettings.KeyTwoFactorEnabled)
	return err == nil && setting.Value == "true"
}

func revokeSessionsAfterTwoFAChange(ctx context.Context, revoker TwoFASessionRevoker, ident sessionauth.SessionIdentity) error {
	if revoker == nil {
		return nil
	}
	// 保留当前 family,避免用户完成 2FA 开关后被自己的操作登出。
	_, err := revoker.RevokeOthers(ctx, usersession.RevokeOthersInput{
		TenantID:        ident.TenantID,
		UserID:          ident.UserID,
		CurrentFamilyID: ident.FamilyID,
		Reason:          "two_factor_state_changed",
	})
	return err
}

func twoFADecodeJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(out); err != nil {
		twoFAWriteError(w, http.StatusBadRequest, "invalid_two_factor_request", "invalid JSON body")
		return false
	}
	return true
}

func twoFADecodeOptionalJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(out); err != nil && !errors.Is(err, io.EOF) {
		twoFAWriteError(w, http.StatusBadRequest, "invalid_two_factor_request", "invalid JSON body")
		return false
	}
	return true
}

func writeTwoFAError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, twofa.ErrInvalidInput):
		twoFAWriteError(w, http.StatusBadRequest, "invalid_two_factor_request", "two-factor request is invalid")
	case errors.Is(err, twofa.ErrInvalidCode):
		twoFAWriteError(w, http.StatusUnauthorized, "two_factor_invalid", "two-factor code is invalid")
	case errors.Is(err, twofa.ErrLocked):
		twoFAWriteError(w, http.StatusTooManyRequests, "two_factor_locked", "two-factor verification is temporarily locked")
	case errors.Is(err, twofa.ErrDisabled):
		twoFAWriteError(w, http.StatusForbidden, "two_factor_disabled", "two-factor authentication is disabled")
	case errors.Is(err, twofa.ErrNotSetup):
		twoFAWriteError(w, http.StatusNotFound, "two_factor_not_setup", "two-factor authentication is not setup")
	case errors.Is(err, twofa.ErrAlreadyEnabled):
		twoFAWriteError(w, http.StatusConflict, "two_factor_already_enabled", "two-factor authentication is already enabled")
	default:
		twoFAWriteError(w, http.StatusServiceUnavailable, "two_factor_backend_error", "two-factor service unavailable")
	}
}

func twoFAWriteJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func twoFAWriteError(w http.ResponseWriter, status int, code, message string) {
	twoFAWriteJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    strings.TrimSpace(code),
			"message": strings.TrimSpace(message),
		},
	})
}
