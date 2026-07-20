package windsurf

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
)

func TestWindsurfRefreshAdapterSuccessMergesTokenAndPreservesConfig(t *testing.T) {
	// 守住的回归：刷新后的 Windsurf 访问凭据必须成为运行时 session token，
	// 同时保留 operator 提供的 OAuth 配置。
	now := time.Date(2026, 5, 24, 11, 0, 0, 0, time.UTC)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("method=%s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); !strings.Contains(ct, "application/x-www-form-urlencoded") {
			t.Fatalf("content-type=%q, want form", ct)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		form, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatalf("parse request form: %v", err)
		}
		assertQueryValue(t, form, "grant_type", "refresh_token")
		assertQueryValue(t, form, "refresh_token", "windsurf-refresh-old")
		assertQueryValue(t, form, "client_id", "windsurf-client")
		return windsurfJSONResponse(http.StatusOK, `{
			"access_token":"windsurf-access-new",
			"refresh_token":"windsurf-refresh-new",
			"token_type":"Bearer",
			"expires_in":1200,
			"scope":"openid offline_access"
		}`), nil
	})}

	raw, expiresAt, err := (RefreshAdapter{
		TokenURL:   "https://windsurf-oauth.example.test/token",
		ClientID:   "windsurf-client",
		HTTPClient: client,
		Now:        func() time.Time { return now },
	}).RefreshForProvider(context.Background(), 42, "windsurf", []byte(`{
		"refresh_token":"windsurf-refresh-old",
		"keep":"yes"
	}`))
	if err != nil {
		t.Fatalf("RefreshForProvider: %v", err)
	}
	if want := now.Add(1200 * time.Second); !expiresAt.Equal(want) {
		t.Fatalf("expiresAt=%s, want %s", expiresAt, want)
	}
	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal refreshed credential: %v", err)
	}
	if got["access_token"] != "windsurf-access-new" || got["session_token"] != "windsurf-access-new" {
		t.Fatalf("runtime token fields=%v, want new access token promoted", got)
	}
	if got["refresh_token"] != "windsurf-refresh-new" || got["keep"] != "yes" || got["client_id"] != "windsurf-client" {
		t.Fatalf("preserved/rotated fields=%v", got)
	}
}

func TestWindsurfRefreshAdapterRequiresConfiguredTokenEndpoint(t *testing.T) {
	// 守住的回归：攻击者可控的 credential JSON 不得决定 refresh token POST 到哪里。
	// 变异自检：从 credential 读取 oauth_token_endpoint/token_endpoint 会调用本
	// HTTP client，使测试变红。
	calledURL := ""
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calledURL = "http-called"
		return windsurfJSONResponse(http.StatusOK, `{"access_token":"unexpected"}`), nil
	})}

	_, _, err := (RefreshAdapter{
		ClientID:   "windsurf-client",
		HTTPClient: client,
	}).RefreshForProvider(context.Background(), 43, "windsurf", []byte(`{
		"refresh_token":"windsurf-refresh-old",
		"oauth_token_endpoint":"http://evil.attacker.com/token"
	}`))
	if !errors.Is(err, ErrWindsurfOAuthConfigRequired) {
		t.Fatalf("RefreshForProvider err=%v, want ErrWindsurfOAuthConfigRequired", err)
	}
	if calledURL != "" {
		t.Fatalf("credential-supplied token endpoint was used: %s", calledURL)
	}
}

func TestWindsurfRefreshAdapterRejectsCredentialSuppliedClientIDAndScope(t *testing.T) {
	t.Run("client id from credential is ignored", func(t *testing.T) {
		called := false
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			called = true
			return windsurfJSONResponse(http.StatusOK, `{"access_token":"unexpected","expires_in":60}`), nil
		})}

		_, _, err := (RefreshAdapter{
			TokenURL:   "https://windsurf-oauth.example.test/token",
			HTTPClient: client,
		}).RefreshForProvider(context.Background(), 44, "windsurf", []byte(`{
			"refresh_token":"windsurf-refresh-old",
			"client_id":"attacker-client"
		}`))
		if !errors.Is(err, ErrWindsurfOAuthConfigRequired) {
			t.Fatalf("RefreshForProvider err=%v, want ErrWindsurfOAuthConfigRequired", err)
		}
		if called {
			t.Fatal("refresh called HTTP client despite missing operator client_id")
		}
	})

	t.Run("scope from credential is ignored", func(t *testing.T) {
		client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				return nil, err
			}
			form, err := url.ParseQuery(string(body))
			if err != nil {
				return nil, err
			}
			if got := form.Get("scope"); got != "" {
				return nil, errors.New("credential scope was sent: " + got)
			}
			return windsurfJSONResponse(http.StatusOK, `{"access_token":"windsurf-access-new","expires_in":60}`), nil
		})}

		_, _, err := (RefreshAdapter{
			TokenURL:   "https://windsurf-oauth.example.test/token",
			ClientID:   "windsurf-client",
			HTTPClient: client,
		}).RefreshForProvider(context.Background(), 45, "windsurf", []byte(`{
			"refresh_token":"windsurf-refresh-old",
			"scope":"credential-controlled-scope"
		}`))
		if err != nil {
			t.Fatalf("RefreshForProvider: %v", err)
		}
	})
}

func TestWindsurfRefreshAdapterClassifiesHTTPFailures(t *testing.T) {
	// 守住的回归：Windsurf 刷新失败必须映射到互不相同的 worker outcome。
	// 变异自检：去掉 body 解析会让 400 invalid_grant 这一 case 变成 unknown；
	// 把 status 处理压平会破坏 401、429、5xx 中至少一个。
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       auth.RefreshOutcome
	}{
		{name: "unauthorized", statusCode: http.StatusUnauthorized, body: `{"error":"bad_credentials"}`, want: auth.OutcomeAuthExpired},
		{name: "invalid_grant_body", statusCode: http.StatusBadRequest, body: `{"error":"invalid_grant"}`, want: auth.OutcomeAuthExpired},
		{name: "rate_limited", statusCode: http.StatusTooManyRequests, body: `{"error":"slow_down"}`, want: auth.OutcomeRateLimit},
		{name: "server_error", statusCode: http.StatusBadGateway, body: `{"error":"upstream_unavailable"}`, want: auth.OutcomeTransientError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return windsurfJSONResponse(tt.statusCode, tt.body), nil
			})}
			_, _, err := (RefreshAdapter{
				TokenURL:   "https://windsurf-oauth.example.test/token",
				ClientID:   "windsurf-client",
				HTTPClient: client,
			}).RefreshForProvider(context.Background(), 77, "windsurf", []byte(`{"refresh_token":"windsurf-refresh-old"}`))
			if err == nil {
				t.Fatal("RefreshForProvider err=nil, want classified error")
			}
			var refreshErr *RefreshError
			if !errors.As(err, &refreshErr) {
				t.Fatalf("err=%T %v, want *RefreshError", err, err)
			}
			if got := auth.ClassifyRefreshError(err, "windsurf", refreshErr.StatusCode); got != tt.want {
				t.Fatalf("classified outcome=%q, want %q; err=%v", got, tt.want, err)
			}
			if refreshErr.Outcome != string(tt.want) {
				t.Fatalf("local outcome=%q, want %q", refreshErr.Outcome, tt.want)
			}
		})
	}
}

func windsurfJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
