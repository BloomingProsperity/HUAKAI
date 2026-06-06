package controlhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

const platformSettingsMaxRequestBodyBytes = 64 << 10

type PlatformSettingsAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type PlatformSettingsService interface {
	Get(context.Context, platformsettings.SettingKey) (platformsettings.StoredSetting, error)
	List(context.Context) ([]platformsettings.StoredSetting, error)
	Upsert(context.Context, platformsettings.UpsertInput) (platformsettings.StoredSetting, error)
}

type PlatformSettingsDeps struct {
	Auth                    PlatformSettingsAuth
	Service                 PlatformSettingsService
	CaptchaSecretConfigured bool
}

type platformSettingsPutRequest struct {
	Value  *string `json:"value"`
	Reason string  `json:"reason,omitempty"`
}

type platformSettingsResponse struct {
	Key       string                  `json:"key"`
	Value     string                  `json:"value"`
	Source    string                  `json:"source"`
	UpdatedAt *time.Time              `json:"updated_at"`
	UpdatedBy *string                 `json:"updated_by"`
	Health    *platformSettingsHealth `json:"health,omitempty"`
}

type platformSettingsHealth struct {
	Status                  string `json:"status"`
	Issue                   string `json:"issue,omitempty"`
	CaptchaSecretConfigured bool   `json:"captcha_secret_configured"`
}

type platformSettingsListResponse struct {
	Items []platformSettingsResponse `json:"items"`
}

func MountPlatformSettingsRoutes(r chi.Router, d PlatformSettingsDeps) {
	r.Get("/", newPlatformSettingsListHandler(d))
	r.Get("/{key}", newPlatformSettingsGetHandler(d))
	r.Put("/{key}", newPlatformSettingsPutHandler(d))
}

func newPlatformSettingsListHandler(d PlatformSettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolvePlatformSettingsAdmin(w, r, d); !ok {
			return
		}
		items, err := d.Service.List(r.Context())
		if err != nil {
			platformSettingsWriteServiceError(w, err)
			return
		}
		out := make([]platformSettingsResponse, 0, len(items))
		for _, item := range items {
			out = append(out, platformSettingsResponseFromStored(item, d))
		}
		platformSettingsWriteJSON(w, http.StatusOK, platformSettingsListResponse{Items: out})
	}
}

func newPlatformSettingsGetHandler(d PlatformSettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolvePlatformSettingsAdmin(w, r, d); !ok {
			return
		}
		key, ok := platformSettingsKeyFromPath(w, r)
		if !ok {
			return
		}
		setting, err := d.Service.Get(r.Context(), key)
		if err != nil {
			platformSettingsWriteServiceError(w, err)
			return
		}
		platformSettingsWriteJSON(w, http.StatusOK, platformSettingsResponseFromStored(setting, d))
	}
}

func newPlatformSettingsPutHandler(d PlatformSettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolvePlatformSettingsAdmin(w, r, d)
		if !ok {
			return
		}
		key, ok := platformSettingsKeyFromPath(w, r)
		if !ok {
			return
		}
		var req platformSettingsPutRequest
		if !platformSettingsDecodeJSON(w, r, &req) {
			return
		}
		if req.Value == nil {
			platformSettingsWriteJSONError(w, http.StatusBadRequest, "platform_setting_value_required", "value is required")
			return
		}
		if key == platformsettings.KeyCaptchaEnabled && strings.TrimSpace(*req.Value) == "true" && !d.CaptchaSecretConfigured {
			platformSettingsWriteJSONError(w, http.StatusBadRequest, "captcha_secret_required", "cannot enable CAPTCHA until Turnstile secret is configured")
			return
		}
		actorID := fmt.Sprintf("%d", ident.TokenID)
		setting, err := d.Service.Upsert(r.Context(), platformsettings.UpsertInput{
			Key:       key,
			Value:     *req.Value,
			UpdatedBy: actorID,
			ActorID:   actorID,
			ActorRole: ident.Role,
			Reason:    strings.TrimSpace(req.Reason),
			RequestID: strings.TrimSpace(r.Header.Get("X-Request-ID")),
		})
		if err != nil {
			platformSettingsWriteServiceError(w, err)
			return
		}
		platformSettingsWriteJSON(w, http.StatusOK, platformSettingsResponseFromStored(setting, d))
	}
}

func resolvePlatformSettingsAdmin(w http.ResponseWriter, r *http.Request, d PlatformSettingsDeps) (admin.AdminIdentity, bool) {
	if d.Auth == nil || d.Service == nil {
		platformSettingsWriteJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "platform settings dependency unset")
		return admin.AdminIdentity{}, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		if errors.Is(err, admin.ErrAdminBackend) {
			platformSettingsWriteJSONError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
		} else {
			platformSettingsWriteJSONError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
		}
		return admin.AdminIdentity{}, false
	}
	if ident.Role != admin.RolePlatformAdmin {
		platformSettingsWriteJSONError(w, http.StatusForbidden, "admin_forbidden", "platform_admin role required")
		return admin.AdminIdentity{}, false
	}
	return ident, true
}

func platformSettingsKeyFromPath(w http.ResponseWriter, r *http.Request) (platformsettings.SettingKey, bool) {
	key, err := platformsettings.ParseKey(chi.URLParam(r, "key"))
	if err != nil {
		platformSettingsWriteJSONError(w, http.StatusBadRequest, "platform_setting_unknown_key", "setting key is not allow-listed")
		return "", false
	}
	return key, true
}

func platformSettingsDecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, platformSettingsMaxRequestBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			platformSettingsWriteJSONError(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body must be <= 64 KiB")
			return false
		}
		platformSettingsWriteJSONError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	return true
}

func platformSettingsResponseFromStored(setting platformsettings.StoredSetting, d PlatformSettingsDeps) platformSettingsResponse {
	resp := platformSettingsResponse{
		Key:    string(setting.Key),
		Value:  setting.Value,
		Source: setting.Source,
	}
	if setting.Key == platformsettings.KeyCaptchaEnabled {
		resp.Health = platformSettingsCaptchaHealth(setting, d)
	}
	if !setting.UpdatedAt.IsZero() {
		updatedAt := setting.UpdatedAt.UTC()
		resp.UpdatedAt = &updatedAt
	}
	if updatedBy := strings.TrimSpace(setting.UpdatedBy); updatedBy != "" {
		resp.UpdatedBy = &updatedBy
	}
	return resp
}

func platformSettingsCaptchaHealth(setting platformsettings.StoredSetting, d PlatformSettingsDeps) *platformSettingsHealth {
	health := &platformSettingsHealth{
		Status:                  "ok",
		CaptchaSecretConfigured: d.CaptchaSecretConfigured,
	}
	if strings.TrimSpace(setting.Value) == "true" && !d.CaptchaSecretConfigured {
		health.Status = "degraded"
		health.Issue = "turnstile_secret_missing"
	}
	return health
}

func platformSettingsWriteServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, platformsettings.ErrUnknownKey):
		platformSettingsWriteJSONError(w, http.StatusBadRequest, "platform_setting_unknown_key", "setting key is not allow-listed")
	case errors.Is(err, platformsettings.ErrInvalidValue):
		platformSettingsWriteJSONError(w, http.StatusBadRequest, "platform_setting_invalid_value", err.Error())
	case errors.Is(err, platformsettings.ErrStoreNotConfigured):
		platformSettingsWriteJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "platform settings dependency unset")
	default:
		platformSettingsWriteJSONError(w, http.StatusServiceUnavailable, "platform_settings_backend_error", err.Error())
	}
}

func platformSettingsWriteJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func platformSettingsWriteJSONError(w http.ResponseWriter, status int, code, message string) {
	platformSettingsWriteJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
