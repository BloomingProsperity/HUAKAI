package sunoclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/mediatask"
)

const maxRequestBodyBytes = 8 << 20

type Service interface {
	Submit(context.Context, int64, int64, mediatask.SubmitInput) (mediatask.Task, error)
	Status(context.Context, int64, int64, int64) (mediatask.Task, error)
}

type submitResponse struct {
	TaskID int64            `json:"task_id"`
	Status mediatask.Status `json:"status"`
}

func MountRoutes(r chi.Router, svc Service) {
	r.Post("/suno/submit", newSubmitHandler(svc))
	r.Post("/suno/submit/{action}", newSubmitHandler(svc))
	r.Get("/suno/fetch", newStatusHandler(svc))
	r.Get("/suno/fetch/{id}", newStatusHandler(svc))
}

func newSubmitHandler(svc Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := requireSession(w, r)
		if !ok {
			return
		}
		raw, ok := readJSONBody(w, r)
		if !ok {
			return
		}
		input, err := translateSubmit(chi.URLParam(r, "action"), raw)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		submitTask(w, r, svc, ident, input)
	}
}

func submitTask(w http.ResponseWriter, r *http.Request, svc Service, ident sessionauth.SessionIdentity, input mediatask.SubmitInput) {
	if svc == nil {
		writeError(w, http.StatusServiceUnavailable, "media_task_backend_error", "media task backend unavailable")
		return
	}
	task, err := svc.Submit(r.Context(), ident.TenantID, ident.UserID, input)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, submitResponse{TaskID: task.ID, Status: task.Status})
}

func newStatusHandler(svc Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "media_task_backend_error", "media task backend unavailable")
			return
		}
		ident, ok := requireSession(w, r)
		if !ok {
			return
		}
		id, ok := fetchID(w, r)
		if !ok {
			return
		}
		task, err := svc.Status(r.Context(), ident.TenantID, ident.UserID, id)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, task)
	}
}

func requireSession(w http.ResponseWriter, r *http.Request) (sessionauth.SessionIdentity, bool) {
	ident, ok := sessionauth.SessionFromContext(r.Context())
	if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
		writeError(w, http.StatusUnauthorized, "session_required", "session bearer token is required")
		return sessionauth.SessionIdentity{}, false
	}
	return ident, true
}

func readJSONBody(w http.ResponseWriter, r *http.Request) (json.RawMessage, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return nil, false
	}
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || !json.Valid(raw) {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return nil, false
	}
	return json.RawMessage(raw), true
}

func fetchID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := strings.TrimSpace(chi.URLParam(r, "id"))
	if raw == "" {
		raw = strings.TrimSpace(r.URL.Query().Get("id"))
	}
	if raw == "" {
		raw = strings.TrimSpace(r.URL.Query().Get("task_id"))
	}
	return parseID(w, raw)
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
