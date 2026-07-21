package antigravity

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/projectenrich"
)

// TestProjectResolverReturnsLoadedProject 守住已有 project 的短路分支：
// loadCodeAssist 命中后必须直接返回，绝不能多发 onboardUser。
func TestProjectResolverReturnsLoadedProject(t *testing.T) {
	var loadCalls atomic.Int32
	loadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		loadCalls.Add(1)
		assertProjectRequestHeaders(t, req)
		if req.URL.Path != "/v1internal:loadCodeAssist" {
			t.Errorf("load path=%q", req.URL.Path)
		}
		var body struct {
			Metadata struct {
				IDEType string `json:"ideType"`
			} `json:"metadata"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Errorf("解码 loadCodeAssist body 失败：%v", err)
			http.Error(w, "invalid test request", http.StatusBadRequest)
			return
		}
		if body.Metadata.IDEType != "ANTIGRAVITY" {
			t.Errorf("metadata.ideType=%q，期望 ANTIGRAVITY", body.Metadata.IDEType)
		}
		_, _ = w.Write([]byte(`{
			"cloudaicompanionProject":"project-loaded",
			"currentTier":{"id":"free-tier"},
			"paidTier":{"id":"g1-pro-tier"}
		}`))
	}))
	defer loadServer.Close()

	var onboardCalls atomic.Int32
	onboardServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		onboardCalls.Add(1)
		_, _ = w.Write([]byte(`{"cloudaicompanionProject":"unexpected"}`))
	}))
	defer onboardServer.Close()

	resolver := &ProjectResolver{
		Endpoint:      loadServer.URL,
		DailyEndpoint: onboardServer.URL,
		HTTPClient:    loadServer.Client(),
		PollInterval:  -1,
	}
	projectID, tier, err := resolver.ResolveProjectMetadata(context.Background(), "access-for-project")
	if err != nil {
		t.Fatalf("ResolveProjectID 失败：%v", err)
	}
	if projectID != "project-loaded" {
		t.Fatalf("project_id=%q，期望 project-loaded", projectID)
	}
	if tier != "g1-pro-tier" {
		t.Fatalf("subscription tier=%q，期望付费层级 g1-pro-tier", tier)
	}
	if loadCalls.Load() != 1 || onboardCalls.Load() != 0 {
		t.Fatalf("load/onboard 调用次数=(%d,%d)，期望 (1,0)", loadCalls.Load(), onboardCalls.Load())
	}
}

// TestProjectResolverOnboardsAndPolls 守住首次入池分支：load 无 project 后，
// 必须改打 daily-cloudcode 对应的 onboardUser，并轮询到最终 project_id。
// 删除 onboard 分支或只调用一次时，本测试分别红在调用错误或 project_id 为空。
func TestProjectResolverOnboardsAndPolls(t *testing.T) {
	loadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		assertProjectRequestHeaders(t, req)
		if req.URL.Path != "/v1internal:loadCodeAssist" {
			t.Errorf("load path=%q", req.URL.Path)
		}
		_, _ = w.Write([]byte(`{"currentTier":{"id":"g1-pro"}}`))
	}))
	defer loadServer.Close()

	var onboardCalls atomic.Int32
	onboardServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		call := onboardCalls.Add(1)
		assertProjectRequestHeaders(t, req)
		if req.URL.Path != "/v1internal:onboardUser" {
			t.Errorf("onboard path=%q", req.URL.Path)
		}
		var body map[string]string
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Errorf("解码 onboardUser body 失败：%v", err)
			http.Error(w, "invalid test request", http.StatusBadRequest)
			return
		}
		want := map[string]string{
			"ide_type":    "ANTIGRAVITY",
			"ide_version": antigravityIDEVersion,
			"ide_name":    "antigravity",
		}
		for key, value := range want {
			if body[key] != value {
				t.Errorf("onboard %s=%q，期望 %q；body=%v", key, body[key], value, body)
			}
		}
		if call == 1 {
			_, _ = w.Write([]byte(`{"done":false}`))
			return
		}
		_, _ = w.Write([]byte(`{"done":true,"response":{"cloudaicompanionProject":"project-onboarded"}}`))
	}))
	defer onboardServer.Close()

	resolver := &ProjectResolver{
		Endpoint:      loadServer.URL,
		DailyEndpoint: onboardServer.URL,
		HTTPClient:    loadServer.Client(),
		PollAttempts:  3,
		PollInterval:  -1,
	}
	projectID, tier, err := resolver.ResolveProjectMetadata(context.Background(), "access-for-project")
	if err != nil {
		t.Fatalf("ResolveProjectID 失败：%v", err)
	}
	if projectID != "project-onboarded" {
		t.Fatalf("project_id=%q，期望 project-onboarded", projectID)
	}
	if tier != "g1-pro" {
		t.Fatalf("onboard 期间必须保留 loadCodeAssist 已观测套餐：%q", tier)
	}
	if onboardCalls.Load() != 2 {
		t.Fatalf("onboardUser 调用次数=%d，期望 2", onboardCalls.Load())
	}
}

func TestProjectResolverLoadsGeminiCodeAssistProject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1internal:loadCodeAssist" || req.Method != http.MethodPost {
			t.Fatalf("请求=%s %s", req.Method, req.URL.Path)
		}
		if got := req.Header.Get("User-Agent"); !strings.HasPrefix(got, "GeminiCLI/") ||
			!strings.Contains(got, "/gemini-2.5-pro (") || strings.Contains(strings.ToLower(got), "huakai") {
			t.Fatalf("Code Assist User-Agent=%q", got)
		}
		var body struct {
			Metadata map[string]string `json:"metadata"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Metadata["pluginType"] != "GEMINI" || body.Metadata["ideType"] != "IDE_UNSPECIFIED" {
			t.Fatalf("Code Assist metadata=%v", body.Metadata)
		}
		_, _ = w.Write([]byte(`{"cloudaicompanionProject":"gemini-managed-project","currentTier":{"id":"free-tier"}}`))
	}))
	defer server.Close()

	resolver := &ProjectResolver{Endpoint: server.URL, HTTPClient: server.Client(), PollInterval: -1}
	projectID, tier, err := resolver.ResolveProjectMetadataForProfile(
		context.Background(), ProjectProfileGeminiCodeAssist, "access-for-project",
	)
	if err != nil {
		t.Fatal(err)
	}
	if projectID != "gemini-managed-project" || tier != "free-tier" {
		t.Fatalf("project/tier=%q/%q", projectID, tier)
	}
}

func TestProjectResolverCompletesGeminiCodeAssistAsyncOnboarding(t *testing.T) {
	var onboardCalls atomic.Int32
	var operationCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/v1internal:loadCodeAssist":
			_, _ = w.Write([]byte(`{"allowedTiers":[{"id":"free-tier","isDefault":true}]}`))
		case req.Method == http.MethodPost && req.URL.Path == "/v1internal:onboardUser":
			onboardCalls.Add(1)
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["tierId"] != "free-tier" {
				t.Fatalf("tierId=%v", body["tierId"])
			}
			_, _ = w.Write([]byte(`{"name":"operations/setup-1","done":false}`))
		case req.Method == http.MethodGet && req.URL.Path == "/v1internal/operations/setup-1":
			operationCalls.Add(1)
			_, _ = w.Write([]byte(`{"done":true,"response":{"cloudaicompanionProject":{"id":"gemini-onboarded-project"}}}`))
		default:
			t.Fatalf("意外请求=%s %s", req.Method, req.URL.Path)
		}
	}))
	defer server.Close()

	resolver := &ProjectResolver{
		Endpoint: server.URL, HTTPClient: server.Client(), PollAttempts: 2, PollInterval: -1,
	}
	projectID, tier, err := resolver.ResolveProjectMetadataForProfile(
		context.Background(), ProjectProfileGeminiCodeAssist, "access-for-project",
	)
	if err != nil {
		t.Fatal(err)
	}
	if projectID != "gemini-onboarded-project" || tier != "free-tier" {
		t.Fatalf("project/tier=%q/%q", projectID, tier)
	}
	if onboardCalls.Load() != 1 || operationCalls.Load() != 1 {
		t.Fatalf("onboard/operation 调用=%d/%d", onboardCalls.Load(), operationCalls.Load())
	}
}

func TestProjectResolverGeminiCodeAssistRequiresOperatorProjectForStandardTier(t *testing.T) {
	var onboardCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/v1internal:loadCodeAssist":
			_, _ = w.Write([]byte(`{"allowedTiers":[{"id":"standard-tier","isDefault":true,"userDefinedCloudaicompanionProject":true}]}`))
		case "/v1internal:onboardUser":
			onboardCalls.Add(1)
			t.Fatal("缺 project_id 时不得发起必然失败的初始化")
		}
	}))
	defer server.Close()

	resolver := &ProjectResolver{Endpoint: server.URL, HTTPClient: server.Client(), PollInterval: -1}
	_, _, err := resolver.ResolveProjectMetadataForProfile(
		context.Background(), ProjectProfileGeminiCodeAssist, "access-for-project",
	)
	if !errors.Is(err, projectenrich.ErrProjectInputRequired) {
		t.Fatalf("err=%v，期望 ErrProjectInputRequired", err)
	}
	if onboardCalls.Load() != 0 {
		t.Fatalf("onboardUser 调用次数=%d，期望 0", onboardCalls.Load())
	}
}

func TestProjectResolverGeminiCodeAssistValidatesOperatorProject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1internal:loadCodeAssist" {
			t.Fatalf("意外路径=%s", req.URL.Path)
		}
		var body struct {
			Project  string            `json:"cloudaicompanionProject"`
			Metadata map[string]string `json:"metadata"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Project != "operator-project" || body.Metadata["duetProject"] != "operator-project" {
			t.Fatalf("项目验证请求=%+v", body)
		}
		_, _ = w.Write([]byte(`{"currentTier":{"id":"standard-tier"}}`))
	}))
	defer server.Close()

	resolver := &ProjectResolver{Endpoint: server.URL, HTTPClient: server.Client(), PollInterval: -1}
	projectID, tier, err := resolver.ResolveProjectMetadataForProfileAndProject(
		context.Background(), ProjectProfileGeminiCodeAssist, "access-for-project", "operator-project",
	)
	if err != nil {
		t.Fatal(err)
	}
	if projectID != "operator-project" || tier != "standard-tier" {
		t.Fatalf("project/tier=%q/%q", projectID, tier)
	}
}

func assertProjectRequestHeaders(t *testing.T, req *http.Request) {
	t.Helper()
	want := map[string]string{
		"Authorization":     "Bearer access-for-project",
		"Content-Type":      "application/json",
		"Accept":            "application/json",
		"User-Agent":        defaultAntigravityUserAgent,
		"X-Goog-Api-Client": defaultAntigravityAPIClient,
	}
	for key, value := range want {
		if got := req.Header.Get(key); got != value {
			t.Errorf("%s=%q，期望 %q", key, got, value)
		}
	}
}
