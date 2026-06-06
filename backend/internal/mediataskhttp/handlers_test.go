package mediataskhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/mediatask"
)

func TestSubmitUsesSessionIdentityAndReturns202(t *testing.T) {
	// Mutation: read tenant_id/user_id from JSON body instead of session; the
	// service call below observes spoofed values and this test fails.
	service := &serviceStub{submitResult: taskFixture(11, 7, 42)}
	mux := mountWithSession(service, sessionauth.SessionIdentity{TenantID: 7, UserID: 42})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/media-tasks", strings.NewReader(`{
		"tenant_id":999,
		"user_id":666,
		"request_id":"req-11",
		"task_type":"image_generation",
		"provider":"http",
		"input_params":{"prompt":"x"}
	}`))

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s want 202", rec.Code, rec.Body.String())
	}
	if len(service.submitCalls) != 1 {
		t.Fatalf("submit calls=%d want 1", len(service.submitCalls))
	}
	call := service.submitCalls[0]
	if call.tenantID != 7 || call.userID != 42 || call.input.RequestID != "req-11" {
		t.Fatalf("submit call=%+v", call)
	}
	var body submitResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.TaskID != 11 || body.Status != mediatask.StatusQueued {
		t.Fatalf("response=%+v", body)
	}
}

func TestDisabledSubmitReturns404AndDoesNotCreateTask(t *testing.T) {
	// Mutation: map ErrDisabled after calling through to the store; submitCalls
	// would be non-zero or status would not be 404.
	service := &serviceStub{submitErr: mediatask.ErrDisabled}
	mux := mountWithSession(service, sessionauth.SessionIdentity{TenantID: 7, UserID: 42})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/media-tasks", strings.NewReader(`{
		"request_id":"req-disabled",
		"task_type":"image_generation",
		"provider":"http",
		"input_params":{"prompt":"x"}
	}`))

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s want 404", rec.Code, rec.Body.String())
	}
	if len(service.submitCalls) != 1 {
		t.Fatalf("handler must call service once and service gate store, got %d", len(service.submitCalls))
	}
}

func TestStatusAndListUseSessionScope(t *testing.T) {
	// Mutation: omit user_id from status/list service calls; the captured scope
	// below is not the session user.
	service := &serviceStub{
		statusResult: taskFixture(12, 7, 42),
		listResult:   []mediatask.Task{taskFixture(12, 7, 42)},
	}
	mux := mountWithSession(service, sessionauth.SessionIdentity{TenantID: 7, UserID: 42})

	statusRec := httptest.NewRecorder()
	mux.ServeHTTP(statusRec, httptest.NewRequest(http.MethodGet, "/v1/media-tasks/12", nil))
	if statusRec.Code != http.StatusOK {
		t.Fatalf("status code=%d body=%s want 200", statusRec.Code, statusRec.Body.String())
	}

	listRec := httptest.NewRecorder()
	mux.ServeHTTP(listRec, httptest.NewRequest(http.MethodGet, "/v1/media-tasks", nil))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list code=%d body=%s want 200", listRec.Code, listRec.Body.String())
	}

	if service.statusTenant != 7 || service.statusUser != 42 || service.statusID != 12 {
		t.Fatalf("status scope tenant/user/id=%d/%d/%d", service.statusTenant, service.statusUser, service.statusID)
	}
	if service.listTenant != 7 || service.listUser != 42 {
		t.Fatalf("list scope tenant/user=%d/%d", service.listTenant, service.listUser)
	}
}

func TestSubmitRequiresRequestID(t *testing.T) {
	service := &serviceStub{}
	mux := mountWithSession(service, sessionauth.SessionIdentity{TenantID: 7, UserID: 42})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/media-tasks", strings.NewReader(`{
		"task_type":"image_generation",
		"provider":"http",
		"input_params":{"prompt":"x"}
	}`))

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s want 400", rec.Code, rec.Body.String())
	}
	if len(service.submitCalls) != 0 {
		t.Fatalf("invalid request reached service: %+v", service.submitCalls)
	}
}

func mountWithSession(service *serviceStub, ident sessionauth.SessionIdentity) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(sessionauth.ContextWithSession(req.Context(), ident)))
		})
	})
	MountRoutes(r, Deps{Service: service})
	return r
}

func taskFixture(id, tenantID, userID int64) mediatask.Task {
	now := time.Date(2026, 6, 6, 13, 0, 0, 0, time.UTC)
	return mediatask.Task{
		ID: id, TenantID: tenantID, UserID: userID, RequestID: "req-11",
		TaskType: "image_generation", Status: mediatask.StatusQueued, Provider: "http",
		InputParams: json.RawMessage(`{"prompt":"x"}`), EstimatedCents: 123,
		CreatedAt: now, UpdatedAt: now,
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
	listTenant, listUser               int64
}

type submitCall struct {
	tenantID int64
	userID   int64
	input    mediatask.SubmitInput
}

func (s *serviceStub) Submit(ctx context.Context, tenantID, userID int64, input mediatask.SubmitInput) (mediatask.Task, error) {
	s.submitCalls = append(s.submitCalls, submitCall{tenantID: tenantID, userID: userID, input: input})
	if s.submitErr != nil {
		return mediatask.Task{}, s.submitErr
	}
	return s.submitResult, nil
}

func (s *serviceStub) Status(ctx context.Context, tenantID, userID, id int64) (mediatask.Task, error) {
	s.statusTenant, s.statusUser, s.statusID = tenantID, userID, id
	if s.statusErr != nil {
		return mediatask.Task{}, s.statusErr
	}
	return s.statusResult, nil
}

func (s *serviceStub) List(ctx context.Context, tenantID, userID int64, limit int) ([]mediatask.Task, error) {
	s.listTenant, s.listUser = tenantID, userID
	if s.listErr != nil {
		return nil, s.listErr
	}
	return append([]mediatask.Task(nil), s.listResult...), nil
}

var _ Service = (*serviceStub)(nil)

func TestServiceErrorsMapToHTTP(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{name: "not_found", err: mediatask.ErrNotFound, want: http.StatusNotFound},
		{name: "provider_unavailable", err: mediatask.ErrProviderUnavailable, want: http.StatusBadRequest},
		{name: "no_active_key", err: mediatask.ErrNoActiveAPIKey, want: http.StatusConflict},
		{name: "request_conflict", err: mediatask.ErrRequestIDConflict, want: http.StatusConflict},
		{name: "backend", err: errors.New("db down"), want: http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := &serviceStub{statusErr: tc.err}
			mux := mountWithSession(service, sessionauth.SessionIdentity{TenantID: 7, UserID: 42})
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/media-tasks/1", nil))
			if rec.Code != tc.want {
				t.Fatalf("status=%d body=%s want %d", rec.Code, rec.Body.String(), tc.want)
			}
		})
	}
}
