package sunoclient

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

func TestSunoSubmitTranslate(t *testing.T) {
	// 变异:翻译请求时把 mv 丢掉;下面的保留性断言必然变红,因为 VPARM-004 的调用方依赖它。
	body := `{"api_key_id":82,"prompt":"write a synthpop chorus","mv":"chirp-v4","title":"Night Relay"}`
	service := &serviceStub{submitResult: taskFixture(501, "suno_generate", json.RawMessage(body))}
	mux := mountWithSession(service)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/suno/submit", strings.NewReader(body))
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
	if call.input.Provider != "suno" || call.input.TaskType != "suno_generate" {
		t.Fatalf("submit provider/task=%q/%q want suno/suno_generate", call.input.Provider, call.input.TaskType)
	}
	if call.input.APIKeyID != 82 {
		t.Fatalf("submit api_key_id=%d want 82", call.input.APIKeyID)
	}
	params := decodeParams(t, call.input.InputParams)
	if params["prompt"] != "write a synthpop chorus" || params["mv"] != "chirp-v4" || params["title"] != "Night Relay" {
		t.Fatalf("InputParams lost prompt/mv/title: %s", string(call.input.InputParams))
	}
}

func TestSunoCustomModeVariant(t *testing.T) {
	// 变异:对 GoAPI 风格的变体丢掉 notify_hook;本测试必然失败,因为 callback 元数据必须原样透传。
	body := `{"custom_mode":true,"input":"Verse one\nChorus hook","notify_hook":"https://hook.example/suno","tags":"electropop","make_instrumental":false}`
	service := &serviceStub{submitResult: taskFixture(502, "suno_custom", json.RawMessage(body))}
	mux := mountWithSession(service)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/suno/submit", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s want 202", rec.Code, rec.Body.String())
	}
	if len(service.submitCalls) != 1 {
		t.Fatalf("submit calls=%d want 1", len(service.submitCalls))
	}
	call := service.submitCalls[0]
	if call.input.Provider != "suno" || call.input.TaskType != "suno_custom" {
		t.Fatalf("submit provider/task=%q/%q want suno/suno_custom", call.input.Provider, call.input.TaskType)
	}
	params := decodeParams(t, call.input.InputParams)
	if params["custom_mode"] != true || params["input"] == "" || params["notify_hook"] != "https://hook.example/suno" {
		t.Fatalf("InputParams lost custom_mode/input/notify_hook: %s", string(call.input.InputParams))
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
		ID: id, TenantID: 7, UserID: 42, RequestID: "req-suno",
		TaskType: taskType, Status: mediatask.StatusQueued, Provider: "suno",
		InputParams: input, EstimatedCents: 123, CreatedAt: now, UpdatedAt: now,
	}
}

type serviceStub struct {
	submitResult mediatask.Task
	submitErr    error
	statusResult mediatask.Task
	statusErr    error

	submitCalls                        []submitCall
	statusTenant, statusUser, statusID int64
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
