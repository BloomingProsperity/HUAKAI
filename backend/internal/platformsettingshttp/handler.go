package platformsettingshttp

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

const maxRequestBodyBytes = 64 << 10

type Auth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type Service interface {
	Get(context.Context, platformsettings.SettingKey) (platformsettings.StoredSetting, error)
	List(context.Context) ([]platformsettings.StoredSetting, error)
	Upsert(context.Context, platformsettings.UpsertInput) (platformsettings.StoredSetting, error)
}

type Deps struct {
	Auth    Auth
	Service Service
}

type putRequest struct {
	Value  *string `json:"value"`
	Reason string  `json:"reason,omitempty"`
}

type settingResponse struct {
	Key       string     `json:"key"`
	Value     string     `json:"value"`
	Source    string     `json:"source"`
	UpdatedAt *time.Time `json:"updated_at"`
	UpdatedBy *string    `json:"updated_by"`
}

type listResponse struct {
	Items []settingResponse `json:"items"`
}

func MountPlatformSettingsRoutes(r chi.Router, d Deps) {
	r.Get("/", newListHandler(d))
	r.Get("/{key}", newGetHandler(d))
	r.Put("/{key}", newPutHandler(d))
}

func newListHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolvePlatformAdmin(w, r, d); !ok {
			return
		}
		items, err := d.Service.List(r.Context())
		if err != nil {
			writeServiceError(w, err)
			return
		}
		out := make([]settingResponse, 0, len(items))
		for _, item := range items {
			out = append(out, settingResponseFromStored(item))
		}
		writeJSON(w, http.StatusOK, listResponse{Items: out})
	}
}

func newGetHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolvePlatformAdmin(w, r, d); !ok {
			return
		}
		key, ok := keyFromPath(w, r)
		if !ok {
			return
		}
		setting, err := d.Service.Get(r.Context(), key)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, settingResponseFromStored(setting))
	}
}

func newPutHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolvePlatformAdmin(w, r, d)
		if !ok {
			return
		}
		key, ok := keyFromPath(w, r)
		if !ok {
			return
		}
		var req putRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.Value == nil {
			writeJSONError(w, http.StatusBadRequest, "platform_setting_value_required", "value is required")
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
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, settingResponseFromStored(setting))
	}
}

func resolvePlatformAdmin(w http.ResponseWriter, r *http.Request, d Deps) (admin.AdminIdentity, bool) {
	if d.Auth == nil || d.Service == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "platform settings dependency unset")
		return admin.AdminIdentity{}, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		if errors.Is(err, admin.ErrAdminBackend) {
			writeJSONError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
		} else {
			writeJSONError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
		}
		return admin.AdminIdentity{}, false
	}
	if ident.Role != admin.RolePlatformAdmin {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "platform_admin role required")
		return admin.AdminIdentity{}, false
	}
	return ident, true
}

func keyFromPath(w http.ResponseWriter, r *http.Request) (platformsettings.SettingKey, bool) {
	key, err := platformsettings.ParseKey(chi.URLParam(r, "key"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "platform_setting_unknown_key", "setting key is not allow-listed")
		return "", false
	}
	return key, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body must be <= 64 KiB")
			return false
		}
		writeJSONError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	return true
}

func settingResponseFromStored(setting platformsettings.StoredSetting) settingResponse {
	resp := settingResponse{
		Key:    string(setting.Key),
		Value:  setting.Value,
		Source: setting.Source,
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

func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, platformsettings.ErrUnknownKey):
		writeJSONError(w, http.StatusBadRequest, "platform_setting_unknown_key", "setting key is not allow-listed")
	case errors.Is(err, platformsettings.ErrInvalidValue):
		writeJSONError(w, http.StatusBadRequest, "platform_setting_invalid_value", err.Error())
	case errors.Is(err, platformsettings.ErrStoreNotConfigured):
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "platform settings dependency unset")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "platform_settings_backend_error", err.Error())
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
