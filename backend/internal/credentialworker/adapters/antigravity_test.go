package adapters

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type antigravityProjectResolverStub struct {
	projectID string
	calls     int
	token     string
}

func (s *antigravityProjectResolverStub) ResolveProjectID(_ context.Context, token string) (string, error) {
	s.calls++
	s.token = token
	return s.projectID, nil
}

type antigravityRoundTripFunc func(*http.Request) (*http.Response, error)

func (f antigravityRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestAntigravityRefreshResolvesMissingProject(t *testing.T) {
	client := &http.Client{Transport: antigravityRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"access-new","refresh_token":"refresh-new","expires_in":1800}`)),
		}, nil
	})}
	resolver := &antigravityProjectResolverStub{projectID: "project-from-resolver"}
	raw, _, err := (AntigravityRefresh{
		Gemini:          GeminiRefresh{HTTPClient: client},
		ProjectResolver: resolver,
	}).RefreshForProvider(context.Background(), 71, "antigravity", []byte(`{"access_token":"access-old","refresh_token":"refresh-old"}`))
	if err != nil {
		t.Fatalf("RefreshForProvider 失败：%v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("解析刷新载荷失败：%v", err)
	}
	if resolver.calls != 1 || resolver.token != "access-new" {
		t.Fatalf("resolver 调用不符：calls=%d token=%q", resolver.calls, resolver.token)
	}
	if payload["project_id"] != "project-from-resolver" || payload["project_metadata_status"] != "resolved" {
		t.Fatalf("刷新后 project 未解析：%s", raw)
	}
	if payload["project_metadata_status"] == "operator_attention" {
		t.Fatalf("resolver 成功时不得落人工处理状态：%s", raw)
	}
}
