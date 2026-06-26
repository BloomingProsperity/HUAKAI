package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func TestCopilotRefreshParsesEndpointAPIAndRoutesSessionAdapter(t *testing.T) {
	// 消除的回归:service-token 响应里的 endpoint.api 必须被保留,并在后续
	// 用于路由。变异自检:若删掉 endpoint 解析,endpoint_api 会变空,请求就会
	// 回退到 api.githubcopilot.com。
	now := time.Date(2026, 5, 24, 9, 0, 0, 0, time.UTC)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			t.Fatalf("service token method=%s, want GET", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "token github-oauth-token" {
			t.Fatalf("authorization=%q, want GitHub token auth", got)
		}
		return copilotJSONResponse(http.StatusOK, `{
			"token":"copilot-service-token",
			"expires_in":900,
			"endpoint":{"api":"https://copilot-proxy.test"}
		}`), nil
	})}

	raw, expiresAt, err := (CopilotRefreshAdapter{
		TokenURL:   "https://api.github.test/copilot_internal/v2/token",
		HTTPClient: client,
		Now:        func() time.Time { return now },
	}).RefreshForProvider(context.Background(), 42, "copilot", []byte(`{"github_access_token":"github-oauth-token","keep":"yes"}`))
	if err != nil {
		t.Fatalf("RefreshForProvider: %v", err)
	}
	if want := now.Add(900 * time.Second); !expiresAt.Equal(want) {
		t.Fatalf("expiresAt=%s, want %s", expiresAt, want)
	}
	var cred map[string]any
	if err := json.Unmarshal(raw, &cred); err != nil {
		t.Fatalf("unmarshal refreshed credential: %v", err)
	}
	if stringField(cred, "access_token") != "copilot-service-token" || stringField(cred, "session_token") != "copilot-service-token" || stringField(cred, "github_access_token") != "github-oauth-token" {
		t.Fatalf("token fields not preserved/promoted: %v", cred)
	}
	if stringField(cred, "endpoint_api") != "https://copilot-proxy.test" || stringField(cred, "base_url") != "https://copilot-proxy.test" {
		t.Fatalf("endpoint fields=%v, want copilot-proxy.test", cred)
	}

	req, err := (&CopilotSessionAdapter{}).BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "gpt-4o",
		InboundBody:     []byte(`{"messages":[]}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: stringField(cred, "session_token"),
			Extra: map[string]string{"endpoint_api": stringField(cred, "endpoint_api")},
		},
	})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if got, want := req.URL.String(), "https://copilot-proxy.test/chat/completions"; got != want {
		t.Fatalf("route endpoint=%q, want %q", got, want)
	}
}

func TestCopilotRefreshRejectsTokenWithoutEndpointAPI(t *testing.T) {
	// 消除的回归:缺少 endpoint.api 的 service token 无法路由。
	// 变异自检:若接受这种响应,HUAKAI 就会静默地把 token 发到错误的 host,
	// 因此本测试要求返回硬错误。
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return copilotJSONResponse(http.StatusOK, `{"token":"copilot-service-token","expires_in":900}`), nil
	})}

	_, _, err := (CopilotRefreshAdapter{
		TokenURL:   "https://api.github.test/copilot_internal/v2/token",
		HTTPClient: client,
		Now:        func() time.Time { return time.Date(2026, 5, 24, 9, 0, 0, 0, time.UTC) },
	}).RefreshForProvider(context.Background(), 43, "copilot", []byte(`{"github_access_token":"github-oauth-token"}`))
	if err == nil || !strings.Contains(err.Error(), "endpoint api") {
		t.Fatalf("RefreshForProvider err=%v, want endpoint api error", err)
	}
}

func TestCopilotRefresherRecordsAuthExpiredOn401(t *testing.T) {
	// 消除的回归:GitHub Copilot token 交换返回的 401 必须被归类,送入
	// credential-worker 审计路径。变异自检:若把 401 当作瞬时错误,或跳过
	// sidecar 失败钩子,就不会留下 auth_expired 证据,本测试随即变红。
	store := &memoryCopilotRefreshStore{raw: []byte(`{"github_access_token":"expired-github-token"}`)}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return copilotJSONResponse(http.StatusUnauthorized, `{"message":"bad credentials"}`), nil
	})}
	refresher := &CopilotRefresher{
		Store: store,
		Adapter: CopilotRefreshAdapter{
			TokenURL:   "https://api.github.test/copilot_internal/v2/token",
			HTTPClient: client,
			Now:        func() time.Time { return time.Date(2026, 5, 24, 9, 0, 0, 0, time.UTC) },
		},
	}

	err := refresher.Refresh(context.Background(), 44)
	if !errors.Is(err, ErrCopilotAuthExpired) {
		t.Fatalf("Refresh err=%v, want ErrCopilotAuthExpired", err)
	}
	if store.failureOutcome != "auth_expired" || store.failureAccountID != 44 {
		t.Fatalf("failure hook=(account=%d outcome=%q), want auth_expired for account 44", store.failureAccountID, store.failureOutcome)
	}
	if len(store.saved) != 0 {
		t.Fatalf("expired token must not save refreshed credential: %s", string(store.saved))
	}
}

func TestCopilotRefresherRedactsTokenMaterialFromRecordedFailure(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		leaked   []string
		expected []string
	}{
		{
			name:     "session token json field",
			body:     `{"message":"bad credentials","session_token":"st_test_abc123"}`,
			leaked:   []string{"st_test_abc123"},
			expected: []string{`"session_token":"<redacted>"`},
		},
		{
			name:     "github user token",
			body:     `{"message":"bad credentials ghu_AAAAAAAA"}`,
			leaked:   []string{"ghu_AAAAAAAA"},
			expected: []string{"ghu_<redacted>"},
		},
		{
			name:     "github app oauth token",
			body:     `{"message":"bad credentials gho_BBBBBBBB"}`,
			leaked:   []string{"gho_BBBBBBBB"},
			expected: []string{"gho_<redacted>"},
		},
		{
			name:     "github classic personal token",
			body:     `{"message":"bad credentials ghp_CCCCCCCC"}`,
			leaked:   []string{"ghp_CCCCCCCC"},
			expected: []string{"ghp_<redacted>"},
		},
		{
			name:     "github server token",
			body:     `{"message":"bad credentials ghs_DDDDDDDD"}`,
			leaked:   []string{"ghs_DDDDDDDD"},
			expected: []string{"ghs_<redacted>"},
		},
		{
			name:     "github refresh token",
			body:     `{"message":"bad credentials ghr_EEEEEEEE"}`,
			leaked:   []string{"ghr_EEEEEEEE"},
			expected: []string{"ghr_<redacted>"},
		},
		{
			name:     "github fine grained personal token",
			body:     `{"message":"bad credentials github_pat_FFFFFFFF_12345678"}`,
			leaked:   []string{"github_pat_FFFFFFFF_12345678"},
			expected: []string{"github_pat_<redacted>"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &memoryCopilotRefreshStore{raw: []byte(`{"github_access_token":"expired-github-token"}`)}
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return copilotJSONResponse(http.StatusUnauthorized, tc.body), nil
			})}
			refresher := &CopilotRefresher{
				Store: store,
				Adapter: CopilotRefreshAdapter{
					TokenURL:   "https://api.github.test/copilot_internal/v2/token",
					HTTPClient: client,
					Now:        func() time.Time { return time.Date(2026, 5, 24, 9, 7, 0, 0, time.UTC) },
				},
			}

			err := refresher.Refresh(context.Background(), 47)
			if err == nil {
				t.Fatal("Refresh err=nil, want sanitized auth failure")
			}
			var refreshErr *CopilotRefreshError
			if !errors.As(err, &refreshErr) {
				t.Fatalf("Refresh err=%T, want CopilotRefreshError", err)
			}
			auditMessage := err.Error() + " " + store.failureCause
			for _, leaked := range tc.leaked {
				if strings.Contains(auditMessage, leaked) {
					t.Fatalf("audit error leaked token material %q in %q", leaked, auditMessage)
				}
			}
			for _, expected := range tc.expected {
				if !strings.Contains(refreshErr.Body, expected) {
					t.Fatalf("redacted body=%q, want sanitized marker %q", refreshErr.Body, expected)
				}
			}
		})
	}
}

func TestCopilotRefresherRecordsRateLimitOn429(t *testing.T) {
	// 消除的回归:service-token endpoint 返回的 429 必须持久化为共享的
	// rate-limit 结果。变异自检:若强行让分类桥接返回 unknown,结果会停留在
	// refresh_failed,本测试随即变红。
	store := &memoryCopilotRefreshStore{raw: []byte(`{"github_access_token":"rate-limited-github-token"}`)}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return copilotJSONResponse(http.StatusTooManyRequests, `{"message":"too many requests"}`), nil
	})}
	refresher := &CopilotRefresher{
		Store: store,
		Adapter: CopilotRefreshAdapter{
			TokenURL:   "https://api.github.test/copilot_internal/v2/token",
			HTTPClient: client,
			Now:        func() time.Time { return time.Date(2026, 5, 24, 9, 5, 0, 0, time.UTC) },
		},
	}

	err := refresher.Refresh(context.Background(), 45)
	if err == nil {
		t.Fatal("Refresh err=nil, want rate-limit failure")
	}
	if store.failureOutcome != "rate_limit_exceeded" || store.failureAccountID != 45 {
		t.Fatalf("failure hook=(account=%d outcome=%q), want rate_limit_exceeded for account 45", store.failureAccountID, store.failureOutcome)
	}
	if len(store.saved) != 0 {
		t.Fatalf("rate-limited token must not save refreshed credential: %s", string(store.saved))
	}
}

func TestCopilotRefresherRecordsRiskControlOn403RiskBody(t *testing.T) {
	// 消除的回归:403 风控响应体不得被压平成通用的 refresh_failed。
	// 变异自检:若从已分类错误中丢掉安全的响应体,结果会变成 unknown,
	// 本测试随即变红。
	store := &memoryCopilotRefreshStore{raw: []byte(`{"github_access_token":"risk-github-token"}`)}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return copilotJSONResponse(http.StatusForbidden, `{"message":"risk control triggered"}`), nil
	})}
	refresher := &CopilotRefresher{
		Store: store,
		Adapter: CopilotRefreshAdapter{
			TokenURL:   "https://api.github.test/copilot_internal/v2/token",
			HTTPClient: client,
			Now:        func() time.Time { return time.Date(2026, 5, 24, 9, 6, 0, 0, time.UTC) },
		},
	}

	err := refresher.Refresh(context.Background(), 46)
	if err == nil {
		t.Fatal("Refresh err=nil, want risk-control failure")
	}
	if store.failureOutcome != "risk_control_triggered" || store.failureAccountID != 46 {
		t.Fatalf("failure hook=(account=%d outcome=%q), want risk_control_triggered for account 46", store.failureAccountID, store.failureOutcome)
	}
	if len(store.saved) != 0 {
		t.Fatalf("risk-control token must not save refreshed credential: %s", string(store.saved))
	}
}

func TestCopilotSessionAdapterIntegrationIDRequiredByMockBackend(t *testing.T) {
	// 消除的回归:严格的上游要求必须带 Copilot-Integration-Id 请求头。
	// 变异自检:若移除该请求头,mock 后端会返回 400。
	req, err := (&CopilotSessionAdapter{Endpoint: "https://copilot-upstream.test/chat/completions"}).BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "gpt-4o",
		InboundBody:     []byte(`{"messages":[]}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: "copilot-service-token",
		},
	})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("Copilot-Integration-Id"); got != "vscode-chat" {
			return copilotJSONResponse(http.StatusBadRequest, `{"error":"missing integration id"}`), nil
		}
		return copilotJSONResponse(http.StatusOK, `{"ok":true}`), nil
	})}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("mock upstream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("mock upstream status=%d body=%s; want 200", resp.StatusCode, body)
	}
}

type memoryCopilotRefreshStore struct {
	raw              []byte
	saved            []byte
	savedExpiresAt   time.Time
	failureAccountID int64
	failureOutcome   string
	failureCause     string
}

func (s *memoryCopilotRefreshStore) LoadCopilotCredential(_ context.Context, accountID int64) ([]byte, error) {
	return append([]byte(nil), s.raw...), nil
}

func (s *memoryCopilotRefreshStore) SaveCopilotCredential(_ context.Context, accountID int64, credential []byte, expiresAt time.Time) error {
	s.saved = append([]byte(nil), credential...)
	s.savedExpiresAt = expiresAt
	return nil
}

func (s *memoryCopilotRefreshStore) RecordCopilotRefreshFailure(_ context.Context, accountID int64, outcome string, cause error) error {
	s.failureAccountID = accountID
	s.failureOutcome = outcome
	if cause != nil {
		s.failureCause = cause.Error()
	}
	return nil
}

func copilotJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func stringField(values map[string]any, key string) string {
	v, _ := values[key].(string)
	return v
}
