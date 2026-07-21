package videoclient

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
	"github.com/BloomingProsperity/HUAKAI/internal/mediatask"
)

const maxRequestBodyBytes = 8 << 20

type Service interface {
	Submit(context.Context, int64, int64, mediatask.SubmitInput) (mediatask.Task, error)
	Status(context.Context, int64, int64, int64) (mediatask.Task, error)
	List(context.Context, int64, int64, int) ([]mediatask.Task, error)
}

type submitResponse struct {
	TaskID int64            `json:"task_id"`
	Status mediatask.Status `json:"status"`
}

type listResponse struct {
	Items []mediatask.Task `json:"items"`
}

func MountRoutes(r chi.Router, svc Service) {
	r.Post("/video/submit", newSubmitHandler(svc))
	r.Get("/video/fetch", newFetchHandler(svc))
	r.Get("/video/fetch/{id}", newStatusHandler(svc))
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
		input, err := translateSubmit(raw)
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

func newFetchHandler(svc Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "media_task_backend_error", "media task backend unavailable")
			return
		}
		ident, ok := requireSession(w, r)
		if !ok {
			return
		}
		if rawID := fetchQueryID(r); rawID != "" {
			id, ok := parseID(w, rawID)
			if !ok {
				return
			}
			writeTaskStatus(w, r, svc, ident, id)
			return
		}
		limit, ok := parseLimit(w, r)
		if !ok {
			return
		}
		tasks, err := svc.List(r.Context(), ident.TenantID, ident.UserID, limit)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		for i := range tasks {
			tasks[i] = sanitizeTask(tasks[i])
		}
		writeJSON(w, http.StatusOK, listResponse{Items: tasks})
	}
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
		id, ok := parseID(w, chi.URLParam(r, "id"))
		if !ok {
			return
		}
		writeTaskStatus(w, r, svc, ident, id)
	}
}

func writeTaskStatus(w http.ResponseWriter, r *http.Request, svc Service, ident sessionauth.SessionIdentity, id int64) {
	task, err := svc.Status(r.Context(), ident.TenantID, ident.UserID, id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sanitizeTask(task))
}

// sanitizeTask 把受凭据保护的上游产物地址替换成网关代理下载地址:上游地址
// 必须用生成账号凭据才能取,原样返回会让用户拿着打不开的链接当成功结果。
func sanitizeTask(task mediatask.Task) mediatask.Task {
	if mediatask.RequiresContentProxy(task) {
		task.Result = mediatask.ContentProxyResult(task)
	}
	return task
}

func fetchQueryID(r *http.Request) string {
	for _, name := range []string{"id", "task_id", "taskId"} {
		if value := strings.TrimSpace(r.URL.Query().Get(name)); value != "" {
			return value
		}
	}
	return ""
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

func parseID(w http.ResponseWriter, raw string) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_task_id", "task id must be positive")
		return 0, false
	}
	return id, true
}

func parseLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	limit := 100
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return limit, true
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 1 || parsed > 200 {
		writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 200")
		return 0, false
	}
	return parsed, true
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
