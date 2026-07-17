package claudecookie

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestClientExchangeUsesThreeStepContractWithoutForwardingCookieToTokenEndpoint(t *testing.T) {
	var mutex sync.Mutex
	var authorizeBody map[string]string
	var tokenBody map[string]string
	var paths []string
	var handlerErrors []string
	recordHandlerError := func(w http.ResponseWriter, message string) {
		mutex.Lock()
		handlerErrors = append(handlerErrors, message)
		mutex.Unlock()
		http.Error(w, "test handler rejected request", http.StatusInternalServerError)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutex.Lock()
		paths = append(paths, r.URL.Path)
		mutex.Unlock()
		switch r.URL.Path {
		case "/api/organizations":
			cookie, err := r.Cookie("sessionKey")
			if err != nil || cookie.Value != "session-secret" {
				recordHandlerError(w, "组织请求 Cookie 不符合约定")
				return
			}
			_, _ = w.Write([]byte(`[{"uuid":"org-1","name":"Primary","raven_type":"team"}]`))
		case "/v1/oauth/org-1/authorize":
			cookie, err := r.Cookie("sessionKey")
			if err != nil || cookie.Value != "session-secret" {
				recordHandlerError(w, "授权请求 Cookie 不符合约定")
				return
			}
			var decoded map[string]string
			if err := json.NewDecoder(r.Body).Decode(&decoded); err != nil {
				recordHandlerError(w, "授权请求体无法解码")
				return
			}
			mutex.Lock()
			authorizeBody = decoded
			mutex.Unlock()
			redirect := "https://platform.claude.com/oauth/code/callback?code=authorization-code&state=" + decoded["state"]
			_ = json.NewEncoder(w).Encode(map[string]string{"redirect_uri": redirect})
		case "/token":
			if _, err := r.Cookie("sessionKey"); !errors.Is(err, http.ErrNoCookie) {
				recordHandlerError(w, "token 请求携带了 Cookie")
				return
			}
			var decoded map[string]string
			if err := json.NewDecoder(r.Body).Decode(&decoded); err != nil {
				recordHandlerError(w, "token 请求体无法解码")
				return
			}
			mutex.Lock()
			tokenBody = decoded
			mutex.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "access-secret", "refresh_token": "refresh-secret",
				"token_type": "Bearer", "scope": claudeFullScope, "expires_in": 3600,
				"account": map[string]string{"uuid": "account-1", "email_address": "owner@example.com"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.Client())
	client.webBaseURL = server.URL
	client.tokenURL = server.URL + "/token"
	result, err := client.Exchange(t.Context(), "session-secret", "")
	if err != nil {
		mutex.Lock()
		errorsSnapshot := append([]string(nil), handlerErrors...)
		mutex.Unlock()
		t.Fatalf("Exchange 失败：%v，handlerErrors=%v", err, errorsSnapshot)
	}
	mutex.Lock()
	errorsSnapshot := append([]string(nil), handlerErrors...)
	pathsSnapshot := append([]string(nil), paths...)
	authorizeSnapshot := cloneStringMap(authorizeBody)
	tokenSnapshot := cloneStringMap(tokenBody)
	mutex.Unlock()
	if len(errorsSnapshot) != 0 {
		t.Fatalf("测试服务器拒绝了请求：%v", errorsSnapshot)
	}
	if result.AccessToken != "access-secret" || result.RefreshToken != "refresh-secret" ||
		result.AccountUUID != "account-1" || result.Organization.ID != "org-1" {
		t.Fatalf("result=%+v", result)
	}
	if got := strings.Join(pathsSnapshot, ","); got != "/api/organizations,/v1/oauth/org-1/authorize,/token" {
		t.Fatalf("请求顺序=%s", got)
	}
	if authorizeSnapshot["client_id"] != claudeClientID || authorizeSnapshot["redirect_uri"] != claudeRedirect ||
		authorizeSnapshot["scope"] != claudeFullScope || authorizeSnapshot["code_challenge_method"] != "S256" {
		t.Fatalf("authorize body=%+v", authorizeSnapshot)
	}
	verifier := tokenSnapshot["code_verifier"]
	digest := sha256.Sum256([]byte(verifier))
	if authorizeSnapshot["code_challenge"] != base64.RawURLEncoding.EncodeToString(digest[:]) {
		t.Fatalf("PKCE challenge 未绑定 token verifier")
	}
	if tokenSnapshot["code"] != "authorization-code" || tokenSnapshot["state"] == "" ||
		tokenSnapshot["state"] != authorizeSnapshot["state"] || tokenSnapshot["redirect_uri"] != claudeRedirect {
		t.Fatalf("token body=%+v authorize=%+v", tokenSnapshot, authorizeSnapshot)
	}
}

func TestClientProfileMatchesCurrentApprovedClaudeEndpoints(t *testing.T) {
	if claudeTokenURL != "https://platform.claude.com/v1/oauth/token" {
		t.Fatalf("claudeTokenURL=%q", claudeTokenURL)
	}
	if claudeRedirect != "https://platform.claude.com/oauth/code/callback" {
		t.Fatalf("claudeRedirect=%q", claudeRedirect)
	}
}

func TestClientRequiresExplicitSelectionWhenCookieHasMultipleOrganizations(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`[
			{"uuid":"org-personal","name":"Personal","raven_type":null},
			{"uuid":"org-team","name":"Team","raven_type":"team"}
		]`))
	}))
	defer server.Close()
	client := NewClient(server.Client())
	client.webBaseURL = server.URL
	_, err := client.Exchange(t.Context(), "session-secret", "")
	var selection *OrganizationSelectionError
	if !errors.As(err, &selection) || len(selection.Organizations) != 2 {
		t.Fatalf("err=%v selection=%+v", err, selection)
	}
	if requests.Load() != 1 {
		t.Fatalf("未选择组织时只能请求组织列表，requests=%d", requests.Load())
	}
}

func TestClientNeverFollowsUpstreamRedirects(t *testing.T) {
	var redirectHits atomic.Int64
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirectHits.Add(1)
	}))
	defer redirectTarget.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	client := NewClient(server.Client())
	client.webBaseURL = server.URL
	_, err := client.Exchange(t.Context(), "session-secret", "")
	if !errors.Is(err, ErrUpstreamUnavailable) || redirectHits.Load() != 0 {
		t.Fatalf("err=%v redirectHits=%d", err, redirectHits.Load())
	}
}

func TestClientRejectsNonExactAuthorizationRedirect(t *testing.T) {
	redirects := []string{
		"https://platform.claude.com:444/oauth/code/callback",
		"https://user@platform.claude.com/oauth/code/callback",
		"https://platform.claude.com/oauth/code/callback#fragment",
	}
	for _, redirect := range redirects {
		t.Run(redirect, func(t *testing.T) {
			var tokenCalls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/organizations":
					_, _ = w.Write([]byte(`[{"uuid":"org-1","name":"Primary"}]`))
				case "/v1/oauth/org-1/authorize":
					var body map[string]string
					if json.NewDecoder(r.Body).Decode(&body) != nil {
						http.Error(w, "bad request", http.StatusBadRequest)
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]string{
						"redirect_uri": redirect + "?code=authorization-code&state=" + body["state"],
					})
				case "/token":
					tokenCalls.Add(1)
				}
			}))
			defer server.Close()
			client := NewClient(server.Client())
			client.webBaseURL = server.URL
			client.tokenURL = server.URL + "/token"
			_, err := client.Exchange(t.Context(), "session-secret", "")
			if !errors.Is(err, ErrUpstreamUnavailable) || tokenCalls.Load() != 0 {
				t.Fatalf("err=%v tokenCalls=%d", err, tokenCalls.Load())
			}
		})
	}
}

func TestClientRejectsMismatchedStateBeforeTokenExchange(t *testing.T) {
	var tokenCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/organizations":
			_, _ = w.Write([]byte(`[{"uuid":"org-1","name":"Primary"}]`))
		case "/v1/oauth/org-1/authorize":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"redirect_uri": "https://platform.claude.com/oauth/code/callback?code=authorization-code&state=attacker-state",
			})
		case "/token":
			tokenCalls.Add(1)
		}
	}))
	defer server.Close()
	client := NewClient(server.Client())
	client.webBaseURL = server.URL
	client.tokenURL = server.URL + "/token"
	_, err := client.Exchange(t.Context(), "session-secret", "")
	if !errors.Is(err, ErrUpstreamUnavailable) || tokenCalls.Load() != 0 {
		t.Fatalf("err=%v tokenCalls=%d", err, tokenCalls.Load())
	}
}

func cloneStringMap(input map[string]string) map[string]string {
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func TestClientRejectsUnknownOrganizationInsteadOfPickingFirst(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"uuid":"org-1","name":"Primary"}]`))
	}))
	defer server.Close()
	client := NewClient(server.Client())
	client.webBaseURL = server.URL
	_, err := client.Exchange(t.Context(), "session-secret", "org-attacker")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err=%v want ErrInvalidInput", err)
	}
}
