package mediataskhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/mediatask"
)

const maxSubmitBodyBytes = 1 << 20

type Service interface {
	Submit(context.Context, int64, int64, mediatask.SubmitInput) (mediatask.Task, error)
	Status(context.Context, int64, int64, int64) (mediatask.Task, error)
	List(context.Context, int64, int64, int) ([]mediatask.Task, error)
}

type Deps struct {
	Service Service
}

type submitResponse struct {
	TaskID int64            `json:"task_id"`
	Status mediatask.Status `json:"status"`
}

type taskListResponse struct {
	Items []mediatask.Task `json:"items"`
}

func MountRoutes(r chi.Router, d Deps) {
	r.Post("/v1/media-tasks", newSubmitHandler(d))
	r.Get("/v1/media-tasks", newListHandler(d))
	r.Get("/v1/media-tasks/{id}", newStatusHandler(d))
}

func newSubmitHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Service == nil {
			writeError(w, http.StatusServiceUnavailable, "media_task_backend_error", "media task backend unavailable")
			return
		}
		ident, ok := sessionauth.SessionFromContext(r.Context())
		if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
			writeError(w, http.StatusUnauthorized, "session_required", "session bearer token is required")
			return
		}
		var req mediatask.SubmitInput
		r.Body = http.MaxBytesReader(w, r.Body, maxSubmitBodyBytes)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		if strings.TrimSpace(req.RequestID) == "" {
			writeError(w, http.StatusBadRequest, "request_id_required", "request_id is required")
			return
		}
		task, err := d.Service.Submit(r.Context(), ident.TenantID, ident.UserID, req)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, submitResponse{TaskID: task.ID, Status: task.Status})
	}
}

func newStatusHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Service == nil {
			writeError(w, http.StatusServiceUnavailable, "media_task_backend_error", "media task backend unavailable")
			return
		}
		ident, ok := sessionauth.SessionFromContext(r.Context())
		if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
			writeError(w, http.StatusUnauthorized, "session_required", "session bearer token is required")
			return
		}
		id, ok := parseID(w, chi.URLParam(r, "id"))
		if !ok {
			return
		}
		task, err := d.Service.Status(r.Context(), ident.TenantID, ident.UserID, id)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, task)
	}
}

func newListHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Service == nil {
			writeError(w, http.StatusServiceUnavailable, "media_task_backend_error", "media task backend unavailable")
			return
		}
		ident, ok := sessionauth.SessionFromContext(r.Context())
		if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
			writeError(w, http.StatusUnauthorized, "session_required", "session bearer token is required")
			return
		}
		limit := 100
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 || parsed > 200 {
				writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 200")
				return
			}
			limit = parsed
		}
		tasks, err := d.Service.List(r.Context(), ident.TenantID, ident.UserID, limit)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, taskListResponse{Items: tasks})
	}
}

func parseID(w http.ResponseWriter, raw string) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_task_id", "task id must be positive")
		return 0, false
	}
	return id, true
}

func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, mediatask.ErrDisabled), errors.Is(err, mediatask.ErrNotFound):
		writeError(w, http.StatusNotFound, "media_task_not_found", "media task is not available")
	case errors.Is(err, mediatask.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_media_task", "media task request is invalid")
	case errors.Is(err, mediatask.ErrProviderUnavailable):
		writeError(w, http.StatusBadRequest, "media_task_provider_unavailable", "media task provider is unavailable")
	case errors.Is(err, mediatask.ErrNoActiveAPIKey):
		writeError(w, http.StatusConflict, "media_task_api_key_required", "create an active API key before submitting media tasks")
	case errors.Is(err, mediatask.ErrAPIKeyAmbiguous):
		writeError(w, http.StatusConflict, "media_task_api_key_ambiguous", "select which active API key should be charged")
	case errors.Is(err, mediatask.ErrRequestIDConflict):
		writeError(w, http.StatusConflict, "media_task_request_conflict", "request_id belongs to a different media task")
	case errors.Is(err, billing.ErrInsufficientBalance):
		writeError(w, http.StatusPaymentRequired, "insufficient_balance", "insufficient balance")
	case errors.Is(err, billing.ErrTenantInactive):
		writeError(w, http.StatusForbidden, clienterr.CodeTenantInactive, clienterr.MessageFor(clienterr.CodeTenantInactive))
	default:
		writeError(w, http.StatusServiceUnavailable, "media_task_backend_error", "media task backend unavailable")
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]map[string]string{"error": {"code": code, "message": message}})
}
