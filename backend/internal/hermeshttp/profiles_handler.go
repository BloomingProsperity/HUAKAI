package hermeshttp

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
)

type createProfileRequest struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	APIKeyID    *int64 `json:"api_key_id,omitempty"`
	PoolGroupID *int64 `json:"pool_group_id,omitempty"`
}

type profileListResponse struct {
	Profiles []hermes.Profile `json:"profiles"`
	Count    int              `json:"count"`
}

func (h handler) createProfile(w http.ResponseWriter, r *http.Request) {
	ident, ok := h.requireIdentity(w, r)
	if !ok {
		return
	}
	var req createProfileRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	args := map[string]any{
		"name": req.Name, "kind": req.Kind,
		"api_key_id": req.APIKeyID, "pool_group_id": req.PoolGroupID,
	}
	profile, err := h.svc.CreateProfileWithAudit(
		r.Context(), ident.TenantID, ident.UserID, req.Name, req.Kind, req.APIKeyID, req.PoolGroupID,
		auditFields(r, ident, hermes.ActionProfileCreate, args, hermes.AuditResultSuccess),
	)
	if err != nil {
		if errors.Is(err, hermes.ErrAuditRecordFailed) {
			writeHermesError(w, err)
			return
		}
		h.auditFailureThenError(w, r, ident, hermes.ActionProfileCreate, args, err)
		return
	}
	writeJSON(w, http.StatusCreated, profile)
}

func (h handler) listProfiles(w http.ResponseWriter, r *http.Request) {
	ident, ok := h.requireIdentity(w, r)
	if !ok {
		return
	}
	profiles, err := h.svc.ListProfilesByOwner(r.Context(), ident.TenantID, ident.UserID)
	if err != nil {
		writeHermesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, profileListResponse{Profiles: profiles, Count: len(profiles)})
}

func (h handler) getProfile(w http.ResponseWriter, r *http.Request) {
	ident, ok := h.requireIdentity(w, r)
	if !ok {
		return
	}
	id, ok := parseProfileID(w, r)
	if !ok {
		return
	}
	profile, err := h.svc.GetProfile(r.Context(), id, ident.TenantID)
	if err != nil {
		writeHermesError(w, err)
		return
	}
	if profile.OwnerUserID != ident.UserID {
		writeError(w, http.StatusNotFound, "hermes_not_found", "hermes resource not found")
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (h handler) deleteProfile(w http.ResponseWriter, r *http.Request) {
	ident, ok := h.requireIdentity(w, r)
	if !ok {
		return
	}
	id, ok := parseProfileID(w, r)
	if !ok {
		return
	}
	args := map[string]any{"operation": "delete", "profile_id": id}
	err := h.svc.DeleteProfileWithAudit(
		r.Context(), id, ident.TenantID, ident.UserID,
		auditFields(r, ident, hermes.ActionProfileRotate, args, hermes.AuditResultSuccess),
	)
	if err != nil {
		if errors.Is(err, hermes.ErrAuditRecordFailed) {
			writeHermesError(w, err)
			return
		}
		h.auditFailureThenError(w, r, ident, hermes.ActionProfileRotate, args, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}

func parseProfileID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_profile_id", "profile_id must be a positive int64")
		return 0, false
	}
	return id, true
}
