package videohttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/mediatask"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

func TestGrokVideoSubmitPersistsFullIdentityAndRouteBinding(t *testing.T) {
	env := newVideoHandlerTestEnv(t)
	request := httptest.NewRequest(http.MethodPost, "/v1/videos/generations", strings.NewReader(`{
		"model":"video-public","prompt":"a lighthouse","duration":5
	}`))
	request.Header.Set("Idempotency-Key", "customer-retry-1")
	response := httptest.NewRecorder()
	env.router.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if env.service.submitCalls != 1 {
		t.Fatalf("submit calls=%d", env.service.submitCalls)
	}
	input := env.service.lastInput
	if input.APIKeyID != 13 || input.ProviderAccountID != 41 || input.PoolGroupID != 29 {
		t.Fatalf("identity binding missing: %+v", input)
	}
	if input.ProtocolFamily != "grok_chat" || input.RequestedModel != "video-public" ||
		input.ProviderModelID != "grok-imagine-video" || input.RouteID != "registry:7:3:primary" {
		t.Fatalf("route binding missing: %+v", input)
	}
	if input.BindingID != 19 || input.BindingMaxParallelRequests != 3 {
		t.Fatalf("模型绑定合同没有进入任务: %+v", input)
	}
	if input.Provider != providerGrokVideo || input.TaskType != "video_generate" {
		t.Fatalf("provider/task=%q/%q", input.Provider, input.TaskType)
	}
	if !strings.Contains(string(input.InputParams), `"model":"video-public"`) {
		t.Fatalf("stored request must preserve public model: %s", input.InputParams)
	}
	if env.selector.last.APIKeyID != 13 || env.selector.last.BindingID != 19 ||
		env.selector.last.MaxParallelRequests != 3 || env.selector.last.EndpointFamily != "videos" {
		t.Fatalf("selector request missing binding gates: %+v", env.selector.last)
	}
	if env.selector.last.RateAccountingScope != pool.RateAccountingLogicalOnly {
		t.Fatalf("入口预选必须只消费逻辑请求预算: %+v", env.selector.last)
	}
	if got := env.selector.last.CapabilityFlags; len(got) != 0 {
		t.Fatalf("选号能力=%v,video 不得携带账号级媒体能力门(modality 由模型注册表判)", got)
	}
	if env.selector.released != 1 {
		t.Fatalf("preflight slot released=%d want 1", env.selector.released)
	}
	var payload map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["request_id"] == "" || payload["request_id"] != input.RequestID {
		t.Fatalf("response=%v input request id=%q", payload, input.RequestID)
	}
}

func TestGrokVideoIdempotentReplayDoesNotReselectAccount(t *testing.T) {
	env := newVideoHandlerTestEnv(t)
	requestID, ok := publicRequestID(httptest.NewRecorder(), requestWithIdempotency("same-key"), env.auth.identity)
	if !ok {
		t.Fatal("publicRequestID rejected valid key")
	}
	env.service.statusTask = mediatask.Task{
		ID: 5, TenantID: 7, UserID: 11, APIKeyID: 13, RequestID: requestID,
		TaskType: "video_generate", Provider: providerGrokVideo, RequestedModel: "video-public",
		InputParams: json.RawMessage(`{"model":"video-public","prompt":"a lighthouse"}`),
		Status:      mediatask.StatusInProgress,
	}
	request := requestWithIdempotency("same-key")
	response := httptest.NewRecorder()
	env.router.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if env.selector.calls != 0 || env.service.submitCalls != 0 {
		t.Fatalf("replay reselected or resubmitted: selector=%d submit=%d", env.selector.calls, env.service.submitCalls)
	}
}

func TestGrokVideoStatusIsScopedToExactAPIKeyAndReturnsCompletedPayload(t *testing.T) {
	env := newVideoHandlerTestEnv(t)
	env.service.statusTask = mediatask.Task{
		RequestID: "video_abc", Status: mediatask.StatusSucceeded,
		Result: json.RawMessage(`{"status":"done","video":{"url":"https://vidgen.x.ai/out.mp4"},"progress":100}`),
	}
	response := httptest.NewRecorder()
	env.router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/videos/video_abc", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"done"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if env.service.lastStatusTenant != 7 || env.service.lastStatusUser != 11 || env.service.lastStatusAPIKey != 13 {
		t.Fatalf("status lookup not key-scoped: %+v", env.service)
	}
}

func TestGrokVideoSubmitRejectsNonVideoModelBeforeSelection(t *testing.T) {
	env := newVideoHandlerTestEnv(t)
	env.registry.resolved.Capabilities = []string{"chat"}
	response := httptest.NewRecorder()
	env.router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/videos/generations",
		strings.NewReader(`{"model":"video-public","prompt":"a lighthouse"}`)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if env.selector.calls != 0 {
		t.Fatal("non-video model reached account selector")
	}
}

func TestGeminiVideoEditRejectsBeforeSelectionAndSubmit(t *testing.T) {
	env := newVideoHandlerTestEnv(t)
	env.registry.resolved.ProtocolFamily = "gemini_messages"
	response := httptest.NewRecorder()
	env.router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/videos/edits",
		strings.NewReader(`{"model":"video-public","prompt":"edit this"}`)))
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "video_operation_not_supported") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if env.selector.calls != 0 || env.service.submitCalls != 0 {
		t.Fatalf("不支持的 Gemini 编辑进入后续链路: selector=%d submit=%d", env.selector.calls, env.service.submitCalls)
	}
}

func TestVideoProviderFollowsResolvedProtocol(t *testing.T) {
	tests := []struct {
		protocol string
		want     string
		ok       bool
	}{
		{protocol: "grok_chat", want: providerGrokVideo, ok: true},
		{protocol: "gemini_messages", want: providerGeminiVideo, ok: true},
		{protocol: "antigravity_session", ok: false},
		{protocol: "openai_chat", ok: false},
	}
	for _, test := range tests {
		got, ok := videoProviderForProtocol(test.protocol)
		if got != test.want || ok != test.ok {
			t.Fatalf("protocol=%q provider=%q/%v，期望 %q/%v", test.protocol, got, ok, test.want, test.ok)
		}
	}
}

type videoHandlerTestEnv struct {
	router   chi.Router
	auth     *videoAuthStub
	registry *videoRegistryStub
	selector *videoSelectorStub
	service  *videoServiceStub
}

func newVideoHandlerTestEnv(t *testing.T) videoHandlerTestEnv {
	t.Helper()
	allowed := "video-public"
	authStub := &videoAuthStub{identity: auth.Identity{TenantID: 7, UserID: 11, APIKeyID: 13, AllowedModels: &allowed, UserGroup: "pro"}}
	maxParallel := int32(3)
	registryStub := &videoRegistryStub{resolved: registry.Resolved{
		PublicAlias: "video-public", CanonicalModelID: "grok/video", ProviderModelID: "grok-imagine-video",
		DefaultProviderModelID: "grok-imagine-video", ProtocolFamily: "grok_chat",
		Capabilities: []string{"video"}, PoolCandidates: []int64{29}, SnapshotVersion: "registry:7:3",
		BindingMetadata: []registry.BindingMetadata{{BindingID: 19, PoolGroupID: 29, Priority: 1, Weight: 1, MaxParallelRequests: &maxParallel}},
	}}
	selectorStub := &videoSelectorStub{}
	vault := provider.NewStaticVault()
	if err := vault.Set(41,
		provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "secret"},
		provider.AccountInfo{AccountID: 41, TenantID: 7, Platform: credentialstore.VendorGrok, AccountType: credentialstore.AuthModeAPIKey},
	); err != nil {
		t.Fatal(err)
	}
	serviceStub := &videoServiceStub{statusErr: mediatask.ErrNotFound}
	chiRouter := chi.NewRouter()
	MountRoutes(chiRouter, Deps{
		Auth: authStub, Registry: registryStub, Router: videoRoutePlanner{},
		Selector: selectorStub, CredentialVault: vault, Service: serviceStub,
	})
	return videoHandlerTestEnv{router: chiRouter, auth: authStub, registry: registryStub, selector: selectorStub, service: serviceStub}
}

func requestWithIdempotency(key string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/v1/videos/generations",
		strings.NewReader(`{"model":"video-public","prompt":"a lighthouse"}`))
	request.Header.Set("Idempotency-Key", key)
	return request
}

type videoAuthStub struct {
	identity auth.Identity
	err      error
}

func (s *videoAuthStub) Resolve(context.Context, *http.Request) (auth.Identity, error) {
	return s.identity, s.err
}

type videoRegistryStub struct {
	resolved registry.Resolved
	err      error
}

func (s *videoRegistryStub) ResolveModel(context.Context, string, int64) (registry.Resolved, error) {
	return s.resolved, s.err
}

type videoRoutePlanner struct{}

func (videoRoutePlanner) Plan(_ context.Context, input router.PlanInput) (router.RoutePlan, error) {
	meta := input.Model.PoolMetadata[0]
	return router.RoutePlan{
		SnapshotVersion: input.Model.SnapshotVersion, AttemptBudget: 1,
		Attempts: []router.AttemptPlan{{
			Index: 0, PoolGroupID: meta.PoolGroupID, BindingID: meta.BindingID,
			BindingRPMLimit: meta.BindingRPMLimit, BindingTPMLimit: meta.BindingTPMLimit,
			MaxParallelRequests: meta.MaxParallelRequests,
			UpstreamModelID: meta.ProviderModelID, Reason: "primary",
		}},
	}, nil
}

type videoSelectorStub struct {
	last     pool.SelectionRequest
	calls    int
	released int
}

func (s *videoSelectorStub) Select(_ context.Context, request pool.SelectionRequest) (*pool.SelectionResult, error) {
	s.calls++
	s.last = request
	return &pool.SelectionResult{
		AccountID: 41, AcquisitionToken: uuid.New(),
		Release: func(context.Context) error {
			s.released++
			return nil
		},
	}, nil
}

type videoServiceStub struct {
	lastInput         mediatask.SubmitInput
	submitCalls       int
	statusTask        mediatask.Task
	statusErr         error
	lastStatusTenant  int64
	lastStatusUser    int64
	lastStatusAPIKey  int64
	contentResult     mediatask.ContentResult
	contentErr        error
	lastContentAPIKey int64
}

func (s *videoServiceStub) ContentForAPIKey(_ context.Context, _, _, apiKeyID int64, _ string) (mediatask.ContentResult, error) {
	s.lastContentAPIKey = apiKeyID
	return s.contentResult, s.contentErr
}

func (s *videoServiceStub) Submit(_ context.Context, tenantID, userID int64, input mediatask.SubmitInput) (mediatask.Task, error) {
	s.submitCalls++
	s.lastInput = input
	return mediatask.Task{ID: 1, TenantID: tenantID, UserID: userID, APIKeyID: input.APIKeyID, RequestID: input.RequestID, Status: mediatask.StatusQueued}, nil
}

func (s *videoServiceStub) StatusForAPIKey(_ context.Context, tenantID, userID, apiKeyID int64, _ string) (mediatask.Task, error) {
	s.lastStatusTenant = tenantID
	s.lastStatusUser = userID
	s.lastStatusAPIKey = apiKeyID
	if s.statusTask.RequestID != "" {
		return s.statusTask, nil
	}
	return mediatask.Task{}, s.statusErr
}

// 变异刀:删掉 writeTaskResponse 里的 RequiresContentProxy 分支 → 响应会带上
// 用户拿不动的上游受凭据地址且缺代理地址,两条断言双双转红。
func TestGeminiVideoStatusHidesCredentialGatedUpstreamURI(t *testing.T) {
	env := newVideoHandlerTestEnv(t)
	env.service.statusTask = mediatask.Task{
		RequestID: "video_abc", Status: mediatask.StatusSucceeded, Provider: "gemini_video",
		RequestedModel: "video-public", Progress: 100,
		Result: json.RawMessage(`{"upstream_content":{"uri":"https://generativelanguage.googleapis.com/v1beta/files/out-1:download?alt=media"}}`),
	}
	recorder := httptest.NewRecorder()
	env.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/videos/video_abc", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if strings.Contains(body, "generativelanguage.googleapis.com") {
		t.Fatalf("上游受凭据地址泄给了用户: %s", body)
	}
	if !strings.Contains(body, "/v1/videos/video_abc/content") {
		t.Fatalf("缺网关代理下载地址: %s", body)
	}
}

func TestVideoContentProxyStreamsBytesForOwnKey(t *testing.T) {
	env := newVideoHandlerTestEnv(t)
	headers := http.Header{}
	headers.Set("Content-Type", "video/mp4")
	env.service.contentResult = mediatask.ContentResult{
		Body: strings.NewReader("MP4-BYTES"), Headers: headers, StatusCode: http.StatusOK,
	}
	recorder := httptest.NewRecorder()
	env.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/videos/video_abc/content", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "MP4-BYTES" {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "video/mp4" {
		t.Fatalf("Content-Type=%q", got)
	}
	if env.service.lastContentAPIKey != 13 {
		t.Fatalf("归属校验没有带调用方 API Key: %d", env.service.lastContentAPIKey)
	}
}

func TestVideoContentProxyHidesForeignTask(t *testing.T) {
	env := newVideoHandlerTestEnv(t)
	env.service.contentErr = mediatask.ErrNotFound
	recorder := httptest.NewRecorder()
	env.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/videos/video_other/content", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("非本 Key 任务必须 404: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestVideoContentProxyRejectsEmptyUpstreamBody(t *testing.T) {
	env := newVideoHandlerTestEnv(t)
	env.service.contentResult = mediatask.ContentResult{Headers: make(http.Header), StatusCode: http.StatusOK}
	recorder := httptest.NewRecorder()
	env.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/videos/video_abc/content", nil))
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("空产物流必须返回 502: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
