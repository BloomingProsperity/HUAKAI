package hermeshttp

import (
	"errors"
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
)

type enableSettingsRequest struct {
	APISource string `json:"api_source,omitempty"`
	ProfileID *int64 `json:"profile_id,omitempty"`
	Model     string `json:"model"`
}

func (h handler) getSettings(w http.ResponseWriter, r *http.Request) {
	ident, ok := h.requireIdentity(w, r)
	if !ok {
		return
	}
	settings, err := h.svc.GetSettings(r.Context(), ident.TenantID, ident.UserID)
	if err != nil {
		writeHermesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (h handler) enableSettings(w http.ResponseWriter, r *http.Request) {
	ident, ok := h.requireIdentity(w, r)
	if !ok {
		return
	}
	var req enableSettingsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	apiSource := req.APISource
	if apiSource == "" {
		apiSource = hermes.APISourceExternal
	}
	args := map[string]any{"api_source": apiSource, "profile_id": req.ProfileID, "model": req.Model}
	settings, err := h.svc.EnableForUserWithAudit(
		r.Context(), ident.TenantID, ident.UserID, apiSource, req.ProfileID, req.Model,
		auditFields(r, ident, hermes.ActionEnable, args, hermes.AuditResultSuccess),
	)
	if err != nil {
		if errors.Is(err, hermes.ErrAuditRecordFailed) {
			writeHermesError(w, err)
			return
		}
		h.auditFailureThenError(w, r, ident, hermes.ActionEnable, args, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (h handler) disableSettings(w http.ResponseWriter, r *http.Request) {
	ident, ok := h.requireIdentity(w, r)
	if !ok {
		return
	}
	args := map[string]any{"enabled": false}
	settings, err := h.svc.DisableForUserWithAudit(
		r.Context(), ident.TenantID, ident.UserID,
		auditFields(r, ident, hermes.ActionDisable, args, hermes.AuditResultSuccess),
	)
	if err != nil {
		if errors.Is(err, hermes.ErrAuditRecordFailed) {
			writeHermesError(w, err)
			return
		}
		h.auditFailureThenError(w, r, ident, hermes.ActionDisable, args, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}
