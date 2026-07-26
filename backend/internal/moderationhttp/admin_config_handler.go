package moderationhttp

import (
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/moderation"
)

type configRequest struct {
	TenantID            int64 `json:"tenant_id"`
	Enabled             bool  `json:"enabled"`
	FailClosed          bool  `json:"fail_closed"`
	SampleRatePct       int32 `json:"sample_rate_pct"`
	BanThreshold        int32 `json:"ban_threshold"`
	BanWindowSeconds    int32 `json:"ban_window_seconds"`
	AutoDisableKeyOnBan *bool `json:"auto_disable_key_on_ban"`
}

type configResponse struct {
	TenantID            int64  `json:"tenant_id"`
	Enabled             bool   `json:"enabled"`
	FailClosed          bool   `json:"fail_closed"`
	SampleRatePct       int32  `json:"sample_rate_pct"`
	BanThreshold        int32  `json:"ban_threshold"`
	BanWindowSeconds    int32  `json:"ban_window_seconds"`
	AutoDisableKeyOnBan bool   `json:"auto_disable_key_on_ban"`
	UpdatedBy           string `json:"updated_by,omitempty"`
	UpdatedAt           string `json:"updated_at,omitempty"`
}

func newConfigGetHandler(deps ModerationAdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(deps, w, r)
		if !ok {
			return
		}
		if !requirePlatformAdmin(w, ident) {
			return
		}
		tenantID, ok := tenantFromQuery(w, r, ident)
		if !ok {
			return
		}
		cfg, err := deps.Store.GetConfig(r.Context(), tenantID)
		if err != nil {
			writeModerationStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, configFromValue(cfg))
	}
}

func newConfigPutHandler(deps ModerationAdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(deps, w, r)
		if !ok {
			return
		}
		if !requirePlatformAdmin(w, ident) {
			return
		}
		var body configRequest
		if !readJSON(w, r, &body) {
			return
		}
		if !requireTenant(w, ident, body.TenantID) {
			return
		}
		cfg, ok := configFromRequest(w, body)
		if !ok {
			return
		}
		cfg.UpdatedBy = adminActorID(ident)
		cfg, err := deps.Store.UpsertConfig(r.Context(), cfg)
		if err != nil {
			writeModerationStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, configFromValue(cfg))
	}
}

func configFromRequest(w http.ResponseWriter, body configRequest) (moderation.ModerationConfig, bool) {
	if body.AutoDisableKeyOnBan == nil {
		writeError(w, http.StatusBadRequest, "auto_disable_key_on_ban_required",
			"auto_disable_key_on_ban is required")
		return moderation.ModerationConfig{}, false
	}
	if body.SampleRatePct < 0 || body.SampleRatePct > 100 {
		writeError(w, http.StatusBadRequest, "invalid_sample_rate_pct",
			"sample_rate_pct must be between 0 and 100")
		return moderation.ModerationConfig{}, false
	}
	if body.BanThreshold < 0 {
		writeError(w, http.StatusBadRequest, "invalid_ban_threshold",
			"ban_threshold must be non-negative")
		return moderation.ModerationConfig{}, false
	}
	if body.BanWindowSeconds <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_ban_window_seconds",
			"ban_window_seconds must be positive")
		return moderation.ModerationConfig{}, false
	}
	return moderation.ModerationConfig{
		TenantID:            body.TenantID,
		Enabled:             body.Enabled,
		FailClosed:          body.FailClosed,
		SampleRatePct:       body.SampleRatePct,
		BanThreshold:        body.BanThreshold,
		BanWindowSeconds:    body.BanWindowSeconds,
		AutoDisableKeyOnBan: *body.AutoDisableKeyOnBan,
	}, true
}

func configFromValue(cfg moderation.ModerationConfig) configResponse {
	return configResponse{
		TenantID:            cfg.TenantID,
		Enabled:             cfg.Enabled,
		FailClosed:          cfg.FailClosed,
		SampleRatePct:       cfg.SampleRatePct,
		BanThreshold:        cfg.BanThreshold,
		BanWindowSeconds:    cfg.BanWindowSeconds,
		AutoDisableKeyOnBan: cfg.AutoDisableKeyOnBan,
		UpdatedBy:           cfg.UpdatedBy,
		UpdatedAt:           formatTime(cfg.UpdatedAt),
	}
}
