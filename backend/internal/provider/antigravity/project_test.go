package antigravity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
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
		_, _ = w.Write([]byte(`{"cloudaicompanionProject":"project-loaded"}`))
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
	projectID, err := resolver.ResolveProjectID(context.Background(), "access-for-project")
	if err != nil {
		t.Fatalf("ResolveProjectID 失败：%v", err)
	}
	if projectID != "project-loaded" {
		t.Fatalf("project_id=%q，期望 project-loaded", projectID)
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
	projectID, err := resolver.ResolveProjectID(context.Background(), "access-for-project")
	if err != nil {
		t.Fatalf("ResolveProjectID 失败：%v", err)
	}
	if projectID != "project-onboarded" {
		t.Fatalf("project_id=%q，期望 project-onboarded", projectID)
	}
	if onboardCalls.Load() != 2 {
		t.Fatalf("onboardUser 调用次数=%d，期望 2", onboardCalls.Load())
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
