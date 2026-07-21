package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/projectenrich"
)

type antigravityProjectResolverStub struct {
	projectID string
	calls     int
	token     string
}

type antigravityMetadataResolverStub struct {
	projectID string
	tier      string
	err       error
	calls     int
	token     string
}

func (s *antigravityMetadataResolverStub) ResolveProjectID(ctx context.Context, token string) (string, error) {
	projectID, _, err := s.ResolveProjectMetadata(ctx, token)
	return projectID, err
}

func (s *antigravityMetadataResolverStub) ResolveProjectMetadata(_ context.Context, token string) (string, string, error) {
	s.calls++
	s.token = token
	return s.projectID, s.tier, s.err
}

func (s *antigravityProjectResolverStub) ResolveProjectID(_ context.Context, token string) (string, error) {
	s.calls++
	s.token = token
	return s.projectID, nil
}

func TestAntigravityRefreshRejectsProjectConflictBeforeSavingNewToken(t *testing.T) {
	client := &http.Client{Transport: antigravityRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"access-new","expires_in":1800}`)),
		}, nil
	})}
	resolver := &antigravityMetadataResolverStub{projectID: "project-observed", tier: "g1-ultra-tier"}
	raw, _, err := (AntigravityRefresh{
		Gemini: GeminiRefresh{HTTPClient: client}, ProjectResolver: resolver,
	}).RefreshForProvider(context.Background(), 72, "antigravity", []byte(`{
		"session_token":"access-old","access_token":"access-old","refresh_token":"refresh-old",
		"project_id":"project-persisted","subscription_tier_raw":"g1-pro-tier"
	}`))
	if !errors.Is(err, projectenrich.ErrProjectMetadataConflict) {
		t.Fatalf("err=%v", err)
	}
	if resolver.calls != 1 || resolver.token != "access-new" {
		t.Fatalf("元数据解析调用不符：calls=%d token=%q", resolver.calls, resolver.token)
	}
	if raw != nil {
		t.Fatalf("项目冲突时不得返回可持久化的新凭据：%s", raw)
	}
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
	if payload["access_token"] != "access-new" || payload["session_token"] != "access-new" {
		t.Fatalf("刷新后运行时令牌未同步：%s", raw)
	}
	if payload["project_metadata_status"] == "operator_attention" {
		t.Fatalf("resolver 成功时不得落人工处理状态：%s", raw)
	}
}

func TestAntigravityRefreshFailsClosedWhenProjectRemainsMissing(t *testing.T) {
	client := &http.Client{Transport: antigravityRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"access-new","expires_in":1800}`)),
		}, nil
	})}
	raw, _, err := (AntigravityRefresh{
		Gemini: GeminiRefresh{HTTPClient: client}, ProjectResolver: &antigravityMetadataResolverStub{},
	}).RefreshForProvider(context.Background(), 73, "antigravity", []byte(`{"access_token":"old","refresh_token":"refresh-old"}`))
	if !errors.Is(err, projectenrich.ErrProjectMetadataUnavailable) {
		t.Fatalf("err=%v", err)
	}
	if raw != nil {
		t.Fatalf("缺少 project 时不得返回可持久化凭据：%s", raw)
	}
}

func TestAntigravityRefreshPreservesExistingProjectWhenSubscriptionLookupFails(t *testing.T) {
	client := &http.Client{Transport: antigravityRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"access-new","expires_in":1800}`)),
		}, nil
	})}
	resolver := &antigravityMetadataResolverStub{err: errors.New("套餐接口暂时不可用")}
	raw, _, err := (AntigravityRefresh{
		Gemini: GeminiRefresh{HTTPClient: client}, ProjectResolver: resolver,
	}).RefreshForProvider(context.Background(), 74, "antigravity", []byte(`{
		"access_token":"old","refresh_token":"refresh-old","project_id":"project-known","subscription_tier_raw":"g1-pro-tier"
	}`))
	if err != nil {
		t.Fatalf("已有 project 时不得因套餐查询失败阻断刷新：%v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["project_id"] != "project-known" || payload["project_metadata_status"] != "preserved_stale" ||
		payload["subscription_tier_raw"] != "g1-pro-tier" || payload["subscription_metadata_status"] != "preserved_stale" {
		t.Fatalf("已有项目和套餐事实未保留：%s", raw)
	}
}
