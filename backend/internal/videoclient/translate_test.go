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
	// 变异:translate 丢掉 duration 或把 Provider 改成非 video;
	// 下面捕获的 mediatask input 将不再符合契约。
	body := `{"apiKeyId":83,"model":"kling-v1","prompt":"wide cinematic skyline","duration":5}`
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
	if call.input.APIKeyID != 83 {
		t.Fatalf("submit api_key_id=%d want 83", call.input.APIKeyID)
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
	// 变异:/video/fetch 忽略 query 中的 id,并用零值或不同的 task id 调用 Status;
	// 下面捕获的 service id 会抓到这次回归。
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

func TestVideoMultipleKeysRequireExplicitSelection(t *testing.T) {
	rec := httptest.NewRecorder()
	writeServiceError(rec, mediatask.ErrAPIKeyAmbiguous)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"media_task_api_key_ambiguous"`) {
		t.Fatalf("status=%d body=%s want 409 media_task_api_key_ambiguous", rec.Code, rec.Body.String())
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

// 变异刀:删掉 sanitizeTask(状态或列表任一处)→ 对应断言抓到上游受凭据地址泄露转红。
func TestVideoSessionSurfacesHideCredentialGatedUpstreamURI(t *testing.T) {
	gated := taskFixture(888, json.RawMessage(`{"prompt":"x"}`))
	gated.Provider = "gemini_video"
	gated.Status = mediatask.StatusSucceeded
	gated.RequestID = "video_g1"
	gated.Result = json.RawMessage(`{"upstream_content":{"uri":"https://generativelanguage.googleapis.com/v1beta/files/out-1:download?alt=media"}}`)
	service := &serviceStub{statusResult: gated, listResult: []mediatask.Task{gated}}
	mux := mountWithSession(service)

	for _, path := range []string{"/video/fetch?id=888", "/video/fetch"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if strings.Contains(body, "generativelanguage.googleapis.com") {
			t.Fatalf("%s 泄露上游受凭据地址: %s", path, body)
		}
		if !strings.Contains(body, "/v1/videos/video_g1/content") {
			t.Fatalf("%s 缺网关代理下载地址: %s", path, body)
		}
	}
}
