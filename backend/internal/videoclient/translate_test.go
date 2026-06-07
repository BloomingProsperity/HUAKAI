package videoclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/mediatask"
)

func TestVideoSubmitTranslate(t *testing.T) {
	// MUTATION: translate drops duration or changes Provider away from video;
	// the captured mediatask input below must no longer match the contract.
	body := `{"model":"kling-v1","prompt":"wide cinematic skyline","duration":5}`
	service := &serviceStub{submitResult: taskFixture(501, json.RawMessage(body))}
	mux := mountWithSession(service)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/video/submit", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s want 202", rec.Code, rec.Body.String())
	}
	if len(service.submitCalls) != 1 {
		t.Fatalf("submit calls=%d want 1", len(service.submitCalls))
	}
	call := service.submitCalls[0]
	if call.tenantID != 7 || call.userID != 42 {
		t.Fatalf("submit scope tenant/user=%d/%d want 7/42", call.tenantID, call.userID)
	}
	if !strings.Contains(call.input.Provider, "video") || call.input.TaskType != "video_generate" {
		t.Fatalf("submit provider/task=%q/%q want video/video_generate", call.input.Provider, call.input.TaskType)
	}
	if strings.TrimSpace(call.input.RequestID) == "" {
		t.Fatal("request_id must be generated when the video client omits one")
	}
	params := decodeParams(t, call.input.InputParams)
	if params["model"] != "kling-v1" || params["prompt"] != "wide cinematic skyline" {
		t.Fatalf("InputParams lost model/prompt: %s", string(call.input.InputParams))
	}
	if params["duration"] != float64(5) {
		t.Fatalf("InputParams duration=%v want 5 in %s", params["duration"], string(call.input.InputParams))
	}
}

func TestVideoFetchQueryUsesMediaTaskID(t *testing.T) {
	// MUTATION: /video/fetch ignores the query id and calls Status with zero or
	// a different task id; the captured service id below catches the regression.
	service := &serviceStub{statusResult: taskFixture(777, json.RawMessage(`{"prompt":"x"}`))}
	mux := mountWithSession(service)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/video/fetch?id=777", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("fetch status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if service.statusTenant != 7 || service.statusUser != 42 || service.statusID != 777 {
		t.Fatalf("fetch service scope tenant/user/id=%d/%d/%d want 7/42/777", service.statusTenant, service.statusUser, service.statusID)
	}
}

func mountWithSession(service *serviceStub) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ident := sessionauth.SessionIdentity{TenantID: 7, UserID: 42}
			next.ServeHTTP(w, req.WithContext(sessionauth.ContextWithSession(req.Context(), ident)))
		})
	})
	MountRoutes(r, service)
	return r
}

func decodeParams(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatalf("decode InputParams %s: %v", string(raw), err)
	}
	return params
}

func taskFixture(id int64, input json.RawMessage) mediatask.Task {
	now := time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC)
	return mediatask.Task{
		ID: id, TenantID: 7, UserID: 42, RequestID: "req-video",
		TaskType: "video_generate", Status: mediatask.StatusQueued, Provider: "video",
		InputParams: input, EstimatedCents: 1000, CreatedAt: now, UpdatedAt: now,
	}
}

type serviceStub struct {
	submitResult mediatask.Task
	submitErr    error
	statusResult mediatask.Task
	statusErr    error
	listResult   []mediatask.Task
	listErr      error

	submitCalls                        []submitCall
	statusTenant, statusUser, statusID int64
	listTenant, listUser, listLimit    int64
}

type submitCall struct {
	tenantID int64
	userID   int64
	input    mediatask.SubmitInput
}

func (s *serviceStub) Submit(_ context.Context, tenantID, userID int64, input mediatask.SubmitInput) (mediatask.Task, error) {
	s.submitCalls = append(s.submitCalls, submitCall{tenantID: tenantID, userID: userID, input: input})
	if s.submitErr != nil {
		return mediatask.Task{}, s.submitErr
	}
	return s.submitResult, nil
}

func (s *serviceStub) Status(_ context.Context, tenantID, userID, id int64) (mediatask.Task, error) {
	s.statusTenant, s.statusUser, s.statusID = tenantID, userID, id
	if s.statusErr != nil {
		return mediatask.Task{}, s.statusErr
	}
	return s.statusResult, nil
}

func (s *serviceStub) List(_ context.Context, tenantID, userID int64, limit int) ([]mediatask.Task, error) {
	s.listTenant, s.listUser, s.listLimit = tenantID, userID, int64(limit)
	if s.listErr != nil {
		return nil, s.listErr
	}
	return append([]mediatask.Task(nil), s.listResult...), nil
}
