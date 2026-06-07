package mjclient

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

func TestMJSubmitImagineTranslate(t *testing.T) {
	// MUTATION: map every /mj/submit/{action} to mj_imagine; the describe
	// subcase below must go red because the path action is the task contract.
	cases := []struct {
		name     string
		path     string
		body     string
		taskType string
	}{
		{
			name:     "imagine",
			path:     "/mj/submit/imagine",
			body:     `{"prompt":"draw a quiet control room","botType":"MID_JOURNEY","notifyHook":"https://hook.example/mj"}`,
			taskType: "mj_imagine",
		},
		{
			name:     "describe",
			path:     "/mj/submit/describe",
			body:     `{"base64Array":["data:image/png;base64,abc"],"botType":"NIJI_JOURNEY","notifyHook":"https://hook.example/describe"}`,
			taskType: "mj_describe",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := &serviceStub{submitResult: taskFixture(101, tc.taskType, json.RawMessage(tc.body))}
			mux := mountWithSession(service)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
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
			if call.input.Provider != "midjourney" || call.input.TaskType != tc.taskType {
				t.Fatalf("submit provider/task=%q/%q want midjourney/%s", call.input.Provider, call.input.TaskType, tc.taskType)
			}
			params := decodeParams(t, call.input.InputParams)
			if params["notifyHook"] == "" || params["botType"] == "" {
				t.Fatalf("InputParams lost notifyHook/botType: %s", string(call.input.InputParams))
			}
		})
	}
}

func TestMJSwapFace(t *testing.T) {
	// MUTATION: drop targetBase64 while translating the swap request; the
	// preservation assertion below catches the missing target image.
	body := `{"sourceBase64":"data:image/png;base64,source","targetBase64":"data:image/png;base64,target"}`
	service := &serviceStub{submitResult: taskFixture(202, "mj_swap_face", json.RawMessage(body))}
	mux := mountWithSession(service)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mj/insight-face/swap", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s want 202", rec.Code, rec.Body.String())
	}
	if len(service.submitCalls) != 1 {
		t.Fatalf("submit calls=%d want 1", len(service.submitCalls))
	}
	call := service.submitCalls[0]
	if call.input.Provider != "midjourney" || call.input.TaskType != "mj_swap_face" {
		t.Fatalf("submit provider/task=%q/%q want midjourney/mj_swap_face", call.input.Provider, call.input.TaskType)
	}
	params := decodeParams(t, call.input.InputParams)
	if params["sourceBase64"] == "" || params["targetBase64"] == "" {
		t.Fatalf("InputParams lost source/target images: %s", string(call.input.InputParams))
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

func taskFixture(id int64, taskType string, input json.RawMessage) mediatask.Task {
	now := time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC)
	return mediatask.Task{
		ID: id, TenantID: 7, UserID: 42, RequestID: "req-mj",
		TaskType: taskType, Status: mediatask.StatusQueued, Provider: "midjourney",
		InputParams: input, EstimatedCents: 123, CreatedAt: now, UpdatedAt: now,
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
