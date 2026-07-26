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
)

type TwoFAService interface {
	Setup(context.Context, twofa.SetupInput) (twofa.SetupResult, error)
	Enable(context.Context, twofa.VerifyInput) (twofa.Status, error)
	EnableWithSessionInvalidation(context.Context, twofa.VerifyInput, string, twofa.SessionInvalidator) (twofa.Status, error)
	Disable(context.Context, int64, int64) error
	DisableWithSessionInvalidation(context.Context, twofa.VerifyInput, string, twofa.SessionInvalidator) (int64, error)
	Status(context.Context, int64, int64) (twofa.Status, error)
	RegenerateBackupCodes(context.Context, twofa.VerifyInput) (twofa.BackupCodesResult, error)
	VerifyLogin(context.Context, twofa.VerifyInput) (twofa.VerifyResult, error)
}

type twoFASessionGuardedService interface {
	SetupWithSessionGuard(context.Context, twofa.SetupInput, string, int) (twofa.SetupResult, error)
	RegenerateBackupCodesWithSessionGuard(
		context.Context,
		twofa.VerifyInput,
		string,
		int,
	) (twofa.BackupCodesResult, error)
}

type TwoFASettings interface {
	Get(context.Context, platformsettings.SettingKey) (platformsettings.StoredSetting, error)
}

type TwoFADeps struct {
	Service  TwoFAService
	Settings TwoFASettings
	Sessions twofa.SessionInvalidator
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
		setupInput := twofa.SetupInput{
			TenantID: ident.TenantID, UserID: ident.UserID, AccountName: req.AccountName,
		}
		var (
			result twofa.SetupResult
			err    error
		)
		if guarded, ok := d.Service.(twoFASessionGuardedService); ok && ident.AuthVersion > 0 {
			result, err = guarded.SetupWithSessionGuard(
				r.Context(), setupInput, ident.FamilyID, ident.AuthVersion,
			)
		} else {
			result, err = d.Service.Setup(r.Context(), setupInput)
		}
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
		status, err := d.Service.EnableWithSessionInvalidation(r.Context(), twofa.VerifyInput{
			TenantID: ident.TenantID, UserID: ident.UserID, Code: req.Code,
		}, ident.FamilyID, d.Sessions)
		if err != nil {
			writeTwoFAError(w, err)
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
		revoked, err := d.Service.DisableWithSessionInvalidation(r.Context(), twofa.VerifyInput{
			TenantID: ident.TenantID, UserID: ident.UserID, Code: req.Code,
		}, ident.FamilyID, d.Sessions)
		if err != nil {
			writeTwoFAError(w, err)
			return
		}
		twoFAWriteJSON(w, http.StatusOK, map[string]any{"enabled": false, "sessions_revoked": revoked})
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
		verifyInput := twofa.VerifyInput{
			TenantID: ident.TenantID, UserID: ident.UserID, Code: req.Code,
		}
		var (
			result twofa.BackupCodesResult
			err    error
		)
		if guarded, ok := d.Service.(twoFASessionGuardedService); ok && ident.AuthVersion > 0 {
			result, err = guarded.RegenerateBackupCodesWithSessionGuard(
				r.Context(), verifyInput, ident.FamilyID, ident.AuthVersion,
			)
		} else {
			result, err = d.Service.RegenerateBackupCodes(r.Context(), verifyInput)
		}
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
	case errors.Is(err, twofa.ErrCodeReused):
		// 码有效但已被消费过(防重放):按校验失败处理(401),用独立 code 让前端提示
		// "该验证码已使用过,请用下一个",而不是落到默认 503 backend_error。
		twoFAWriteError(w, http.StatusUnauthorized, "two_factor_code_reused", "two-factor code has already been used")
	case errors.Is(err, twofa.ErrLocked):
		twoFAWriteError(w, http.StatusTooManyRequests, "two_factor_locked", "two-factor verification is temporarily locked")
	case errors.Is(err, twofa.ErrAuthenticationStale):
		twoFAWriteError(w, http.StatusUnauthorized, "authentication_stale", "account security changed; authenticate again")
	case errors.Is(err, twofa.ErrDisabled):
		twoFAWriteError(w, http.StatusForbidden, "two_factor_disabled", "two-factor authentication is disabled")
	case errors.Is(err, twofa.ErrNotSetup):
		twoFAWriteError(w, http.StatusNotFound, "two_factor_not_setup", "two-factor authentication is not setup")
	case errors.Is(err, twofa.ErrAlreadyEnabled):
		twoFAWriteError(w, http.StatusConflict, "two_factor_already_enabled", "two-factor authentication is already enabled")
	case errors.Is(err, twofa.ErrSessionInvalidation):
		twoFAWriteError(w, http.StatusServiceUnavailable, "session_revoke_failed", "session revocation failed after two-factor state change")
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
