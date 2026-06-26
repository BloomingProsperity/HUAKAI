package credentialacq

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func hashOAuthState(state string) []byte {
	sum := sha256.Sum256([]byte(state))
	return sum[:]
}

func completeOAuthCallback(store *memorySessionStore, flowID, state, code string, exchange func(string) (acqCandidate, error)) (acqCandidate, error) {
	row, err := store.Get(flowID)
	if err != nil {
		return acqCandidate{}, err
	}
	if !row.ConsumedAt.IsZero() || row.Status == statusFinalized {
		return acqCandidate{}, errFlowReplay
	}
	if store.now().After(row.ExpiresAt) {
		_ = store.UpdateStatus(flowID, statusExpired)
		return acqCandidate{}, errFlowExpired
	}
	if !bytes.Equal(row.StateHash, hashOAuthState(state)) {
		_ = store.UpdateStatus(flowID, statusFailed)
		return acqCandidate{}, errStateMismatch
	}
	_ = store.UpdateStatus(flowID, statusCallbackReceived)
	candidate, err := exchange(code)
	if err != nil {
		_ = store.UpdateStatus(flowID, statusFailed)
		return acqCandidate{}, err
	}
	if candidate.Vendor == "" {
		candidate.Vendor = row.Vendor
	}
	if candidate.AuthMode == "" {
		candidate.AuthMode = row.AuthMode
	}
	if err := store.UpdateStatus(flowID, statusValidated); err != nil {
		return acqCandidate{}, err
	}
	return candidate, nil
}

func TestOAuthCallbackRejectsStateMismatch(t *testing.T) {
	now := time.Date(2026, 5, 16, 5, 0, 0, 0, time.UTC)
	store := newMemorySessionStore(func() time.Time { return now })
	if err := store.Create(acqSession{
		ID: "flow-oauth", Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeChatGPTOAuth,
		StateHash: hashOAuthState("expected"), ExpiresAt: now.Add(10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	_, err := completeOAuthCallback(store, "flow-oauth", "wrong", "code", func(string) (acqCandidate, error) {
		t.Fatal("exchange must not run on state mismatch")
		return acqCandidate{}, nil
	})
	if !errors.Is(err, errStateMismatch) {
		t.Fatalf("err=%v want %v", err, errStateMismatch)
	}
	got, _ := store.Get("flow-oauth")
	if got.Status != statusFailed {
		t.Fatalf("status=%q want %q", got.Status, statusFailed)
	}
}

func TestCompleteOAuthCallbackRejectsCrossFlowStateReplay(t *testing.T) {
	now := time.Date(2026, 5, 24, 9, 0, 0, 0, time.UTC)
	keys, err := credentialstore.NewStaticKeyProvider("test-v1", bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresSessionStoreWithKeys(newTestSessionDB(now), keys).WithNow(func() time.Time { return now })

	// 用独立空 registry 起 flow,确保走通用 PKCE fallback(startPKCEOAuthFlow)。本测试验证的是
	// 跨 flow state-replay 防护这一通用机制,与具体 vendor 的生产 exchanger 解耦(copilot 现已 fail-closed)。
	victim, err := StartOAuthFlowWithRegistry(context.Background(), store, StartInput{
		TenantID: 1, ProviderAccountID: 101,
		Vendor: credentialstore.VendorCopilot, AuthMode: credentialstore.AuthModeCopilotOAuth,
		ActorID: "admin-1", ActorRole: "platform_admin",
	}, OAuthClientConfig{
		ClientID: "client-id", AuthURL: "https://auth.example.test/oauth", RedirectURI: "https://huakai.example.test/callback",
	}, NewExchangerRegistry())
	if err != nil {
		t.Fatalf("start victim flow: %v", err)
	}
	attacker, err := StartOAuthFlowWithRegistry(context.Background(), store, StartInput{
		TenantID: 1, ProviderAccountID: 202,
		Vendor: credentialstore.VendorCopilot, AuthMode: credentialstore.AuthModeCopilotOAuth,
		ActorID: "admin-1", ActorRole: "platform_admin",
	}, OAuthClientConfig{
		ClientID: "client-id", AuthURL: "https://auth.example.test/oauth", RedirectURI: "https://huakai.example.test/callback",
	}, NewExchangerRegistry())
	if err != nil {
		t.Fatalf("start attacker flow: %v", err)
	}
	if OAuthStateMatches(attacker.Session.StateHash, victim.State) {
		t.Fatal("fixture is not discriminating: replayed state unexpectedly matches attacker flow")
	}

	exchangeCalled := false
	_, session, err := CompleteOAuthCallback(context.Background(), store, attacker.Session.ID, victim.State, "attacker-code",
		func(context.Context, Session, string) (CredentialCandidate, error) {
			exchangeCalled = true
			return CredentialCandidate{Payload: []byte(`{"session_token":"should-not-run"}`)}, nil
		})
	if !errors.Is(err, ErrStateMismatch) {
		t.Fatalf("err=%v want %v", err, ErrStateMismatch)
	}
	if exchangeCalled {
		t.Fatal("exchange ran for replayed state")
	}
	if session.Status != StatusFailed || session.ErrorClass != "state_mismatch" {
		t.Fatalf("session status/class=%s/%s want failed/state_mismatch", session.Status, session.ErrorClass)
	}
}

// TestCompleteOAuthCallbackRejectsTerminalFlows 守护:生产环境的 CompleteOAuthCallback 必须
// 在执行 state/expiry/PKCE 检查之前,把落在终态 flow(cancelled/failed/expired-by-status)上的回调
// 当作 ErrFlowReplay 拒绝 —— 死 flow 不能通过重放原始 state+code 被复活回
// callback_received→validated。
//
// 它驱动的是*生产*版 CompleteOAuthCallback(而非本文件内的内存版 completeOAuthCallback helper,
// 后者是并行重实现,并不能证明生产守卫)。区分性设计:每个终态 flow 都用一个不匹配的 state 去打。
// 有终态守卫时,调用返回 ErrFlowReplay(守卫先触发);而 `started` 对照组(非终态)则继续走到 state
// 检查并返回 ErrStateMismatch —— 证明守卫是按状态精确判定的,而非一刀切拒绝。
//
// 变异检查:把 oauth.go 的守卫还原为 `session.Status == StatusFinalized`(丢弃
// isTerminalStatus)。cancelled/failed/expired 这几种情况就会穿透到 state 检查并返回
// ErrStateMismatch 而非 ErrFlowReplay → 那些断言变红。`started` 对照组保持绿色
// (它总会到达 state 检查),证明本测试隔离的正是终态状态这一回归。
func TestCompleteOAuthCallbackRejectsTerminalFlows(t *testing.T) {
	now := time.Date(2026, 5, 24, 9, 0, 0, 0, time.UTC)
	keys, err := credentialstore.NewStaticKeyProvider("test-v1", bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresSessionStoreWithKeys(newTestSessionDB(now), keys).WithNow(func() time.Time { return now })

	seed := func(id string, status FlowStatus) string {
		if _, err := store.Create(context.Background(), Session{
			ID: id, TenantID: 1, ProviderAccountID: 101,
			Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeChatGPTOAuth,
			Kind: FlowKindOAuth, Status: status, ActorID: "admin-1", ActorRole: "platform_admin",
			ClientIdentitySource: ClientSourcePublicCLI,
			StateHash:            HashOAuthState("real-state"),
			RedactedContext:      map[string]any{"path": "oauth"},
			ExpiresAt:            now.Add(10 * time.Minute), // future: clock-expiry never fires; only status drives the guard
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
		return id
	}

	cases := []struct {
		name    string
		status  FlowStatus
		wantErr error
	}{
		{"cancelled", StatusCancelled, ErrFlowReplay},
		{"failed", StatusFailed, ErrFlowReplay},
		{"expired-status", StatusExpired, ErrFlowReplay},
		{"finalized", StatusFinalized, ErrFlowReplay},
		{"started-control", StatusStarted, ErrStateMismatch}, // non-terminal: reaches state check
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := seed(tc.name, tc.status)
			exchangeCalled := false
			_, _, err := CompleteOAuthCallback(context.Background(), store, id, "wrong-state", "code",
				func(context.Context, Session, string) (CredentialCandidate, error) {
					exchangeCalled = true
					return CredentialCandidate{Payload: []byte(`{"session_token":"should-not-run"}`)}, nil
				})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("status=%s: err=%v want %v", tc.status, err, tc.wantErr)
			}
			if exchangeCalled {
				t.Fatalf("status=%s: exchange ran for a callback it should never reach", tc.status)
			}
		})
	}
}

func TestStartOAuthFlowPKCEVerifierEncryptedAtRest(t *testing.T) {
	now := time.Date(2026, 5, 16, 5, 0, 0, 0, time.UTC)
	keys, err := credentialstore.NewStaticKeyProvider("test-v1", bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresSessionStoreWithKeys(newTestSessionDB(now), keys).WithNow(func() time.Time { return now })
	// 独立空 registry → 通用 PKCE fallback。本测试验证 PKCE verifier 静态加密这一通用机制,
	// 与具体 vendor 的生产 exchanger 解耦(copilot 现已 fail-closed)。
	result, err := StartOAuthFlowWithRegistry(context.Background(), store, StartInput{
		TenantID: 1, ProviderAccountID: 2,
		Vendor: credentialstore.VendorCopilot, AuthMode: credentialstore.AuthModeCopilotOAuth,
		ActorID: "admin-1", ActorRole: "platform_admin",
	}, OAuthClientConfig{
		ClientID: "client-id", AuthURL: "https://auth.example.test/oauth", RedirectURI: "https://huakai.example.test/callback",
	}, NewExchangerRegistry())
	if err != nil {
		t.Fatalf("StartOAuthFlow: %v", err)
	}
	if result.CodeVerifier == "" {
		t.Fatal("CodeVerifier is empty")
	}
	if bytes.Contains(result.Session.EncryptedPKCEVerifier, []byte(result.CodeVerifier)) {
		t.Fatalf("encrypted_pkce_verifier leaked plaintext verifier")
	}
	if strings.Contains(string(result.Session.NonceHash), result.CodeVerifier) {
		t.Fatalf("pkce metadata leaked plaintext verifier")
	}
	plain, err := store.DecryptTransientPayload(context.Background(), result.Session.EncryptedPKCEVerifier, result.Session.NonceHash, pkceAADFromSession(result.Session))
	if err != nil {
		t.Fatalf("DecryptTransientPayload: %v", err)
	}
	if string(plain) != result.CodeVerifier {
		t.Fatal("decrypted verifier did not match generated verifier")
	}
}

func TestOAuthCallbackRejectsReplayAfterConsume(t *testing.T) {
	now := time.Date(2026, 5, 16, 5, 0, 0, 0, time.UTC)
	store := newMemorySessionStore(func() time.Time { return now })
	if err := store.Create(acqSession{
		ID: "flow-replay", Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeCodeAssist,
		StateHash: hashOAuthState("ok"), ExpiresAt: now.Add(10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := completeOAuthCallback(store, "flow-replay", "ok", "code", successfulOAuthExchange); err != nil {
		t.Fatal(err)
	}
	if err := store.Consume("flow-replay", 55); err != nil {
		t.Fatal(err)
	}
	_, err := completeOAuthCallback(store, "flow-replay", "ok", "code", successfulOAuthExchange)
	if !errors.Is(err, errFlowReplay) {
		t.Fatalf("err=%v want %v", err, errFlowReplay)
	}
}

func TestOAuthCallbackExchangeSuccessAndFailure(t *testing.T) {
	now := time.Date(2026, 5, 16, 5, 0, 0, 0, time.UTC)
	store := newMemorySessionStore(func() time.Time { return now })
	if err := store.Create(acqSession{
		ID: "flow-success", Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeChatGPTOAuth,
		StateHash: hashOAuthState("ok"), ExpiresAt: now.Add(10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	candidate, err := completeOAuthCallback(store, "flow-success", "ok", "code", successfulOAuthExchange)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Vendor != credentialstore.VendorOpenAI || candidate.AuthMode != credentialstore.AuthModeChatGPTOAuth {
		t.Fatalf("candidate target=%s/%s", candidate.Vendor, candidate.AuthMode)
	}
	got, _ := store.Get("flow-success")
	if got.Status != statusValidated {
		t.Fatalf("status=%q want %q", got.Status, statusValidated)
	}

	if err := store.Create(acqSession{
		ID: "flow-fail", Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeChatGPTOAuth,
		StateHash: hashOAuthState("ok"), ExpiresAt: now.Add(10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	exchangeErr := errors.New("redacted exchange failure")
	_, err = completeOAuthCallback(store, "flow-fail", "ok", "bad-code", func(string) (acqCandidate, error) {
		return acqCandidate{}, exchangeErr
	})
	if !errors.Is(err, exchangeErr) {
		t.Fatalf("err=%v want %v", err, exchangeErr)
	}
	got, _ = store.Get("flow-fail")
	if got.Status != statusFailed {
		t.Fatalf("status=%q want %q", got.Status, statusFailed)
	}
}

func TestWindsurfAcquisitionUsesTokenExchangeNotOAuthFake(t *testing.T) {
	// windsurf 的 ModePlan 是 FlowKindTokenExchange(types.go),其 acquisition 走
	// NewWindsurfCodeiumAuthTokenCandidate 直接构造 candidate,不经 OAuth callback registry.Exchange。
	// 此前注册的 windsurf/oauth fake exchanger 属 orphaned dangerous wiring(会把任意 JSON 当 session 凭据
	// 接受),已移除。断言:(1) 真实的 token-exchange acquisition 仍产出带 session 材料的 candidate;
	// (2) 默认 registry 不再注册 windsurf/oauth fake —— 移除后即便误走 OAuth callback 也只会 fail-closed
	// (ErrOAuthExchangerMissing),而非接受伪造 session。Mutation:还原 windsurf/oauth fake 注册 → (2) 转红。
	candidate, err := NewWindsurfCodeiumAuthTokenCandidate(1, 42, "operator-1", "windsurf-session-token")
	if err != nil {
		t.Fatalf("NewWindsurfCodeiumAuthTokenCandidate: %v", err)
	}
	if candidate.Vendor != "windsurf" || candidate.AuthMode != "oauth" || candidate.ProviderAccountID != 42 {
		t.Fatalf("candidate target=%s/%s account=%d", candidate.Vendor, candidate.AuthMode, candidate.ProviderAccountID)
	}
	if !strings.Contains(string(candidate.Payload), "windsurf-session-token") {
		t.Fatalf("candidate payload=%s, want session token material", string(candidate.Payload))
	}
	if _, ok := DefaultExchangerRegistry().Lookup("windsurf/oauth"); ok {
		t.Fatal("windsurf/oauth fake exchanger must be removed (orphaned; windsurf acquisition uses TokenExchange)")
	}
}

func TestDefaultExchangerRegistryIncludesGeminiOAuth(t *testing.T) {
	// 回归保护:通用的 Gemini OAuth callback 不能穿透到
	// exchanger_missing。变异自检:删除注册行
	// 会使 lookup 失败,回调完成时无法持久化捕获到的 token。
	registry := DefaultExchangerRegistry()
	if _, ok := registry.Lookup("gemini/oauth"); !ok {
		t.Fatal("gemini/oauth exchanger missing")
	}
	session := Session{
		TenantID:          1,
		ProviderAccountID: 42,
		Vendor:            "gemini",
		AuthMode:          "oauth",
		ActorID:           "operator-1",
	}
	candidate, err := registry.Exchange(context.Background(), session, `{"session_token":"gemini-session-token","refresh_token":"gemini-refresh-token"}`)
	if err != nil {
		t.Fatalf("Exchange gemini/oauth: %v", err)
	}
	if candidate.Vendor != "gemini" || candidate.AuthMode != "oauth" || candidate.ProviderAccountID != 42 {
		t.Fatalf("candidate target=%s/%s account=%d", candidate.Vendor, candidate.AuthMode, candidate.ProviderAccountID)
	}
	payload := string(candidate.Payload)
	if !strings.Contains(payload, "gemini-session-token") || !strings.Contains(payload, "gemini-refresh-token") {
		t.Fatalf("candidate payload=%s, want captured session and refresh token material", payload)
	}
}

func TestWindsurfCodeiumAuthTokenCandidateValidatesAsCredential(t *testing.T) {
	candidate, err := NewWindsurfCodeiumAuthTokenCandidate(1, 42, "owner", "codeium-token-from-windsurf")
	if err != nil {
		t.Fatalf("NewWindsurfCodeiumAuthTokenCandidate: %v", err)
	}
	if candidate.Vendor != "windsurf" || candidate.AuthMode != "oauth" || candidate.ProviderAccountID != 42 {
		t.Fatalf("candidate target=%s/%s account=%d", candidate.Vendor, candidate.AuthMode, candidate.ProviderAccountID)
	}
	if err := NewFinalizer(nil, credentialstore.DefaultHandlerRegistry(), nil, nil).ValidateCandidate(candidate); err != nil {
		t.Fatalf("ValidateCandidate: %v", err)
	}
	payload := string(candidate.Payload)
	if !strings.Contains(payload, "codeium-token-from-windsurf") || !strings.Contains(payload, "session_token") {
		t.Fatalf("payload=%s, want Windsurf session token material", payload)
	}

	_, err = NewWindsurfCodeiumAuthTokenCandidate(1, 42, "owner", " ")
	if !errors.Is(err, ErrInvalidTokenShape) {
		t.Fatalf("empty token err=%v want ErrInvalidTokenShape", err)
	}
}

func TestAuthorizationCodeExchangeRejectsRefreshOnlyTokenResponse(t *testing.T) {
	calls := 0
	exchanger := authorizationCodeOAuthExchanger{
		client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			switch calls {
			case 1:
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"refresh_token":"rt-without-access"}`)),
				}, nil
			case 2:
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"access_token":"at-present","refresh_token":"rt-present"}`)),
				}, nil
			default:
				t.Fatalf("unexpected token endpoint call %d", calls)
				return nil, nil
			}
		})},
	}
	payload := storedPKCEPayload{
		CodeVerifier: "verifier",
		TokenURL:     "https://oauth.example.test/token",
		ClientID:     "client-id",
		RedirectURI:  "https://huakai.example.test/callback",
	}

	_, err := exchanger.exchangeAuthorizationCode(context.Background(), payload, "auth-code")
	if !errors.Is(err, ErrInvalidTokenShape) {
		t.Fatalf("refresh-only token err=%v want ErrInvalidTokenShape", err)
	}

	token, err := exchanger.exchangeAuthorizationCode(context.Background(), payload, "auth-code")
	if err != nil {
		t.Fatalf("access+refresh token err=%v", err)
	}
	if token.AccessToken != "at-present" || token.RefreshToken != "rt-present" {
		t.Fatalf("token=%+v, want access and refresh material", token)
	}
}

func successfulOAuthExchange(code string) (acqCandidate, error) {
	if code == "" {
		return acqCandidate{}, errors.New("missing code")
	}
	return acqCandidate{
		Payload: []byte(`{"session_token":"session-value","refresh_token":"refresh-value"}`),
		RedactedContext: map[string]any{
			"account_email_hash": "sha256:example",
		},
	}, nil
}
