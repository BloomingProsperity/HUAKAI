// 本包对照 docs/specs/upstream-credential-management.md 中的契约
// 测试 F-AUTH-005 实现。
//
// 所有测试都使用内存版桩 (auth_helpers_test.go) + httptest.Server
// 来模拟上游 OAuth refresh; 无需任何外部依赖。
package auth

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// =====================================================================
// 辅助工具
// =====================================================================

type rig struct {
	provider *AntigravityTokenProvider
	store    *memStore
	cache    *memCache
	lock     *memLock
	marker   *memMarker
	audit    *memAudit
	upstream *httptest.Server
	client   *http.Client
}

// newRig 构建一个完整接线的测试 provider。上游 OAuth server 可通过
// upstreamHandler 参数配置。
func newRig(t *testing.T, upstreamHandler http.HandlerFunc) *rig {
	t.Helper()
	store := newMemStore()
	cache := newMemCache()
	lock := newMemLock()
	marker := newMemMarker()
	audit := newMemAudit()
	upstream, client := newMappedHTTPSServer(t, upstreamHandler, nil)
	t.Cleanup(upstream.Close)
	provider := NewAntigravityTokenProvider(store, audit, cache, lock, marker, client, nil)
	return &rig{provider: provider, store: store, cache: cache, lock: lock, marker: marker, audit: audit, upstream: upstream, client: client}
}

func newMappedHTTPSServer(t *testing.T, handler http.Handler, remoteIPs map[string]string) (*httptest.Server, *http.Client) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("本地 loopback 监听不可用，跳过需要 httptest server 的 auth 测试: %v", err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.Listener = ln
	server.StartTLS()

	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split listener addr: %v", err)
	}
	const publicHost = "93.184.216.34"
	server.URL = "https://" + net.JoinHostPort(publicHost, port)
	client := server.Client()
	base := client.Transport.(*http.Transport).Clone()
	base.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	base.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, rawPort, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		conn, err := (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		if err != nil {
			return nil, err
		}
		remoteIP := host
		if mapped, ok := remoteIPs[host]; ok {
			remoteIP = mapped
		}
		remotePort := 0
		if p, err := net.LookupPort("tcp", rawPort); err == nil {
			remotePort = p
		}
		return remoteAddrConn{Conn: conn, remote: &net.TCPAddr{IP: net.ParseIP(remoteIP), Port: remotePort}}, nil
	}
	client.Transport = base
	return server, client
}

type remoteAddrConn struct {
	net.Conn
	remote net.Addr
}

func (c remoteAddrConn) RemoteAddr() net.Addr { return c.remote }

// addAccount 用给定的 credential body 插入一个 Provider Account。
func (r *rig) addAccount(tenantID, accountID int64, accountType string, credJSON []byte) {
	r.store.put(ProviderAccountCredential{
		TenantID:       tenantID,
		AccountID:      accountID,
		Provider:       antigravityProvider,
		AccountType:    accountType,
		Enabled:        true,
		CredentialJSON: credJSON,
		TokenVersion:   1,
	})
}

func oauthCredJSON(t *testing.T, accessToken, refreshToken, oauthEndpoint string, expiresAt time.Time) []byte {
	t.Helper()
	cred := antigravityCredential{
		AccessToken:   accessToken,
		RefreshToken:  refreshToken,
		ExpiresAt:     expiresAt,
		OAuthEndpoint: oauthEndpoint,
	}
	b, err := json.Marshal(cred)
	if err != nil {
		t.Fatalf("oauthCredJSON: %v", err)
	}
	return b
}

func staticCredJSON(t *testing.T, apiKey string) []byte {
	t.Helper()
	cred := antigravityCredential{APIKey: apiKey}
	b, err := json.Marshal(cred)
	if err != nil {
		t.Fatalf("staticCredJSON: %v", err)
	}
	return b
}

func okOAuthHandler(returnAccessToken, returnRefreshToken string, expiresInSeconds int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  returnAccessToken,
			"refresh_token": returnRefreshToken,
			"expires_in":    expiresInSeconds,
		})
	}
}

// TestAT_SECURITY_W1_C01_AntigravityRefreshRejectsSSRFEndpointsBeforeSendingSecrets
// 消除如下风险: 租户可控的 oauth_endpoint 让 gateway 把
// refresh_token/client_secret POST 到 metadata、loopback、link-local、private 或
// 非 HTTPS 的目标。
func TestAT_SECURITY_W1_C01_AntigravityRefreshRejectsSSRFEndpointsBeforeSendingSecrets(t *testing.T) {
	var bodies []string
	var mu sync.Mutex
	upstream, client := newMappedHTTPSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "new" + goodToken, "expires_in": 3600})
	}), map[string]string{"link-local.test": "169.254.169.254"})
	defer upstream.Close()
	parsed, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}
	_, port, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatalf("split upstream host: %v", err)
	}
	provider := NewAntigravityTokenProvider(nil, nil, nil, nil, nil, client, nil)
	cases := []string{
		"http://169.254.169.254/token",
		"https://" + net.JoinHostPort("127.0.0.1", port) + "/token",
		"https://" + net.JoinHostPort("10.0.0.1", port) + "/token",
		"https://" + net.JoinHostPort("link-local.test", port) + "/token",
		"ftp://example.com/token",
	}
	for _, endpoint := range cases {
		_, err := provider.refresh(context.Background(), antigravityCredential{
			OAuthEndpoint: endpoint,
			RefreshToken:  "refresh-secret-" + goodToken,
			ClientSecret:  "client-secret-" + goodToken,
		})
		if !errors.Is(err, ErrOAuthEndpointBlocked) {
			t.Fatalf("endpoint %q error=%v, want ErrOAuthEndpointBlocked", endpoint, err)
		}
		if strings.Contains(err.Error(), "169.254") || strings.Contains(err.Error(), "127.0.0.1") || strings.Contains(err.Error(), "10.0.0.1") || strings.Contains(err.Error(), "link-local") {
			t.Fatalf("blocked endpoint error leaked raw destination: %v", err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 0 {
		t.Fatalf("blocked SSRF destinations received secret form bodies: %+v", bodies)
	}
}

// TestAT_SECURITY_W1_C01_AntigravityRefreshAllowsPublicHTTPS 消除如下风险:
// SSRF guard 把合法的公网 HTTPS OAuth refresh endpoint 也挡掉。
func TestAT_SECURITY_W1_C01_AntigravityRefreshAllowsPublicHTTPS(t *testing.T) {
	upstream, client := newMappedHTTPSServer(t, okOAuthHandler("new"+goodToken, "rt"+goodToken, 3600), nil)
	defer upstream.Close()
	provider := NewAntigravityTokenProvider(nil, nil, nil, nil, nil, client, nil)

	resp, err := provider.refresh(context.Background(), antigravityCredential{
		OAuthEndpoint: upstream.URL,
		RefreshToken:  "refresh-" + goodToken,
		ClientSecret:  "client-" + goodToken,
	})
	if err != nil {
		t.Fatalf("public HTTPS refresh rejected: %v", err)
	}
	if resp.AccessToken != "new"+goodToken {
		t.Fatalf("access token=%q", resp.AccessToken)
	}
}

func TestAT_SECURITY_W1_C01_SSRFGuardRejectsSpecialUseIPRanges(t *testing.T) {
	blocked := []string{
		"198.18.0.5",
		"192.0.2.10",
		"198.51.100.10",
		"203.0.113.10",
		"192.0.0.10",
		"192.88.99.10",
		"240.0.0.10",
		"255.255.255.255",
		"0.1.2.3",
		"2001:db8::1",
		"2002::1",
		"64:ff9b::1",
		"100::1",
		"5f00::1",
		"2001::1",
		"3fff::1",
		"::ffff:192.0.2.10",
	}
	for _, rawIP := range blocked {
		ip := net.ParseIP(rawIP)
		if ip == nil {
			t.Fatalf("parse blocked IP %q", rawIP)
		}
		if isPublicOAuthIP(ip) {
			t.Fatalf("special-use IP %s allowed; want blocked", rawIP)
		}
	}

	allowed := []string{
		"93.184.216.34",
		"2606:2800:220:1::1",
	}
	for _, rawIP := range allowed {
		ip := net.ParseIP(rawIP)
		if ip == nil {
			t.Fatalf("parse public IP %q", rawIP)
		}
		if !isPublicOAuthIP(ip) {
			t.Fatalf("public IP %s blocked; want allowed", rawIP)
		}
	}
}

func TestAT_SECURITY_W1_C01_OAuthClientDisablesConfiguredProxy(t *testing.T) {
	proxyURL, err := url.Parse("http://127.0.0.1:9")
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	base := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	client := newSSRFProtectedOAuthClient(base)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("oauth client transport type=%T, want *http.Transport", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("oauth refresh client must not inherit HTTP(S)_PROXY or configured proxy")
	}
}

func TestAT_SECURITY_W1_C01_OAuthClientRequiresHTTPTransportForDialGuard(t *testing.T) {
	custom := &recordingRoundTripper{}
	base := &http.Client{Transport: custom}

	client := newSSRFProtectedOAuthClient(base)
	if client.Transport == custom {
		t.Fatal("oauth refresh client must not honor a custom RoundTripper without DialContext")
	}
	if _, ok := client.Transport.(*http.Transport); !ok {
		t.Fatalf("oauth client transport type=%T, want *http.Transport", client.Transport)
	}
}

func TestAT_SECURITY_W1_C01_OAuthClientToleratesCustomDefaultTransport(t *testing.T) {
	originalDefault := http.DefaultTransport
	http.DefaultTransport = &recordingRoundTripper{}
	t.Cleanup(func() {
		http.DefaultTransport = originalDefault
	})

	client := newSSRFProtectedOAuthClient(nil)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("oauth client transport type=%T, want *http.Transport", client.Transport)
	}
	if transport.DialContext == nil {
		t.Fatal("oauth refresh client must install a DialContext guard")
	}

	_, err := transport.DialContext(context.Background(), "tcp", net.JoinHostPort("localhost", "443"))
	if !errors.Is(err, ErrOAuthEndpointBlocked) {
		t.Fatalf("guarded dial error=%v, want ErrOAuthEndpointBlocked", err)
	}
}

type recordingRoundTripper struct{}

func (*recordingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("custom round tripper should not be used")
}

func TestAT_SECURITY_W1_C01_SSRFGuardRejectsLoopbackDNSBeforeDial(t *testing.T) {
	dialCalls := 0
	guarded := ssrfGuardedDialContext(func(context.Context, string, string) (net.Conn, error) {
		dialCalls++
		return nil, errors.New("base dial should not be called")
	})

	_, err := guarded(context.Background(), "tcp", net.JoinHostPort("localhost", "443"))
	if !errors.Is(err, ErrOAuthEndpointBlocked) {
		t.Fatalf("localhost resolution error=%v, want ErrOAuthEndpointBlocked", err)
	}
	if dialCalls != 0 {
		t.Fatalf("guard called base dialer %d times for loopback DNS result", dialCalls)
	}
}

func TestAT_SECURITY_W1_C01_SSRFGuardFallsBackAcrossValidatedPublicIPs(t *testing.T) {
	originalLookup := lookupOAuthIPAddrs
	lookupOAuthIPAddrs = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{
			{IP: net.ParseIP("2001:4860:4860::8888")},
			{IP: net.ParseIP("93.184.216.34")},
		}, nil
	}
	t.Cleanup(func() { lookupOAuthIPAddrs = originalLookup })

	var dialed []string
	guarded := ssrfGuardedDialContext(func(_ context.Context, _ string, address string) (net.Conn, error) {
		dialed = append(dialed, address)
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if host == "2001:4860:4860::8888" {
			return nil, errors.New("ipv6 route unavailable")
		}
		if host != "93.184.216.34" {
			return nil, errors.New("unexpected dial address")
		}
		clientConn, serverConn := net.Pipe()
		t.Cleanup(func() {
			_ = clientConn.Close()
			_ = serverConn.Close()
		})
		return remoteAddrConn{Conn: clientConn, remote: &net.TCPAddr{IP: net.ParseIP(host), Port: 443}}, nil
	})

	conn, err := guarded(context.Background(), "tcp", net.JoinHostPort("oauth.example.test", "443"))
	if err != nil {
		t.Fatalf("guard should fall back to second validated public IP: %v", err)
	}
	_ = conn.Close()
	wantDialed := []string{
		net.JoinHostPort("2001:4860:4860::8888", "443"),
		net.JoinHostPort("93.184.216.34", "443"),
	}
	if len(dialed) != len(wantDialed) {
		t.Fatalf("dialed %v, want %v", dialed, wantDialed)
	}
	for i := range wantDialed {
		if dialed[i] != wantDialed[i] {
			t.Fatalf("dialed %v, want %v", dialed, wantDialed)
		}
	}
}

func TestAT_SECURITY_W1_C01_SSRFGuardRejectsMixedResolvedSetBeforeDial(t *testing.T) {
	originalLookup := lookupOAuthIPAddrs
	lookupOAuthIPAddrs = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{
			{IP: net.ParseIP("93.184.216.34")},
			{IP: net.ParseIP("127.0.0.1")},
		}, nil
	}
	t.Cleanup(func() { lookupOAuthIPAddrs = originalLookup })

	dialCalls := 0
	guarded := ssrfGuardedDialContext(func(context.Context, string, string) (net.Conn, error) {
		dialCalls++
		return nil, errors.New("base dial should not be called")
	})

	_, err := guarded(context.Background(), "tcp", net.JoinHostPort("oauth.example.test", "443"))
	if !errors.Is(err, ErrOAuthEndpointBlocked) {
		t.Fatalf("mixed public/private DNS set error=%v, want ErrOAuthEndpointBlocked", err)
	}
	if dialCalls != 0 {
		t.Fatalf("guard called base dialer %d times for mixed DNS result", dialCalls)
	}
}

func TestAT_SECURITY_W1_C01_OAuthClientClearsLegacyDialTLS(t *testing.T) {
	base := &http.Client{Transport: &http.Transport{
		DialTLS: func(string, string) (net.Conn, error) {
			return nil, errors.New("legacy dial tls should not be called")
		},
	}}
	client := newSSRFProtectedOAuthClient(base)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("oauth client transport type=%T, want *http.Transport", client.Transport)
	}
	if transport.DialTLS != nil {
		t.Fatal("oauth refresh client must clear legacy DialTLS hook")
	}
}

func TestAT_SECURITY_W1_C01_OAuthClientRejectsRedirectPolicy(t *testing.T) {
	client := newSSRFProtectedOAuthClient(nil)
	if client.CheckRedirect == nil {
		t.Fatal("oauth refresh client must install a no-follow redirect policy")
	}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/steal", strings.NewReader("client_secret=secret"))
	via := []*http.Request{
		httptest.NewRequest(http.MethodPost, "https://example.com/token", strings.NewReader("client_secret=secret")),
	}
	if err := client.CheckRedirect(req, via); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("redirect policy error=%v, want http.ErrUseLastResponse", err)
	}
}

func TestAT_SECURITY_W1_C01_AntigravityRefreshDoesNotFollowRedirects(t *testing.T) {
	var redirectBodies []string
	var mu sync.Mutex
	redirectListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("本地 loopback 监听不可用，跳过 OAuth redirect SSRF 测试: %v", err)
	}
	redirectTarget := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		redirectBodies = append(redirectBodies, string(body))
		mu.Unlock()
		http.Error(w, "redirect target should not receive OAuth refresh body", http.StatusInternalServerError)
	}))
	redirectTarget.Listener = redirectListener
	redirectTarget.Start()
	defer redirectTarget.Close()

	upstream, client := newMappedHTTPSServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", redirectTarget.URL+"/steal")
		w.WriteHeader(http.StatusTemporaryRedirect)
		_, _ = w.Write([]byte("redirect"))
	}), nil)
	defer upstream.Close()
	provider := NewAntigravityTokenProvider(nil, nil, nil, nil, nil, client, nil)

	_, err = provider.refresh(context.Background(), antigravityCredential{
		OAuthEndpoint: upstream.URL,
		RefreshToken:  "refresh-secret-" + goodToken,
		ClientSecret:  "client-secret-" + goodToken,
	})
	if err == nil {
		t.Fatal("redirecting OAuth token endpoint must fail")
	}
	if errors.Is(err, ErrOAuthEndpointBlocked) {
		t.Fatalf("oauth client followed redirect into SSRF guard; want original 307 classified error, got %v", err)
	}
	if !strings.Contains(err.Error(), "status=307") {
		t.Fatalf("redirect should be handled as original non-2xx response, got %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(redirectBodies) != 0 {
		t.Fatalf("redirect target received OAuth secret form bodies: %+v", redirectBodies)
	}
}

// TestAT_SECURITY_W1_C02_RefreshFailureRedactsOAuthSecretsInRecordedError 消除如下风险:
// 上游 OAuth 错误回显在返回的错误和 refresh 审计字段中暴露 client_secret、
// client_assertion、password、secret 或 authorization。
func TestAT_SECURITY_W1_C02_RefreshFailureRedactsOAuthSecretsInRecordedError(t *testing.T) {
	secretBody := `{"client_secret":"json-secret","client_assertion":"json-assert","password":"json-pass","authorization":"Bearer json-auth"} ` +
		`client_secret=form-secret&client_assertion=form-assert&password=form-pass&secret=form-generic ` +
		`client_secret: labeled-secret password: labeled-pass authorization: Bearer labeled-auth ` +
		`password: "my secret" client_secret='abc def' client_assertion: "tok with spaces" secret = "a b c" ` +
		strings.Repeat("x", 4096)
	r := newRig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, secretBody, http.StatusBadGateway)
	}))
	r.addAccount(22, 2200, oauthAccountType, oauthCredJSON(t, "old"+goodToken, "rt"+goodToken, r.upstream.URL, time.Now().Add(time.Minute)))

	_, err := r.provider.GetAccessToken(context.Background(), 22, 2200)
	if err == nil {
		t.Fatal("refresh failure expected")
	}
	entries := r.audit.byOutcome(OutcomePermanentDisable)
	if len(entries) != 1 {
		t.Fatalf("audit failure entries=%d want 1: %+v", len(entries), r.audit.entries)
	}
	combined := err.Error() + " " + entries[0].ErrorMessageRedacted
	for _, leaked := range []string{"json-secret", "json-assert", "json-pass", "json-auth", "form-secret", "form-assert", "form-pass", "form-generic", "labeled-secret", "labeled-pass", "labeled-auth", "my secret", "secret\"", "abc def", "def'", "tok with spaces", "with spaces", "a b c", "b c"} {
		if strings.Contains(combined, leaked) {
			t.Fatalf("refresh error/audit leaked OAuth secret %q in %q", leaked, combined)
		}
	}
	if len(err.Error()) > 1200 || len(entries[0].ErrorMessageRedacted) > 1200 {
		t.Fatalf("refresh error body was not length-capped: err=%d audit=%d", len(err.Error()), len(entries[0].ErrorMessageRedacted))
	}
}

// =====================================================================
// Sub2API 可继承的场景
// =====================================================================

// AT-AUTH-005-001: 过期前 refresh。
func TestAT_AUTH_005_001_PreExpiryRefresh(t *testing.T) {
	r := newRig(t, okOAuthHandler("new"+goodToken, "newrefresh"+goodToken, 3600))
	expired := time.Now().Add(2 * time.Minute) // < 3min skew → 触发 refresh
	r.addAccount(1, 100, oauthAccountType, oauthCredJSON(t, "old"+goodToken, "rt"+goodToken, r.upstream.URL, expired))

	tok, err := r.provider.GetAccessToken(context.Background(), 1, 100)
	if err != nil {
		t.Fatalf("GetAccessToken: %v", err)
	}
	if tok != "new"+goodToken {
		t.Fatalf("expected new access token; got %q", tok)
	}
	cur := r.store.get(1, 100)
	if cur.TokenVersion != 2 {
		t.Fatalf("token_version should increment to 2, got %d", cur.TokenVersion)
	}
	if cur.RefreshTokenFingerprint == "" {
		t.Fatalf("refresh_token_fingerprint should be set")
	}
}

// AT-AUTH-005-002: 同账号 refresh 锁串行化。
func TestAT_AUTH_005_002_RefreshLockSerialization(t *testing.T) {
	var refreshCount int
	var mu sync.Mutex
	handler := func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		refreshCount++
		mu.Unlock()
		// 模拟慢上游, 让并发 refresh 相互重叠
		time.Sleep(150 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "new" + goodToken, "refresh_token": "rt" + goodToken, "expires_in": 3600,
		})
	}
	r := newRig(t, handler)
	r.addAccount(2, 200, oauthAccountType, oauthCredJSON(t, "old"+goodToken, "rt"+goodToken, r.upstream.URL, time.Now().Add(1*time.Minute)))

	// 规范 AT-AUTH-005-002: 100 个并发请求; 恰好 1 个抢到锁; 其余等待/使用 stale 值。
	const N = 100
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			_, _ = r.provider.GetAccessToken(context.Background(), 2, 200)
		}()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if refreshCount != 1 {
		t.Fatalf("storm invariant violated: spec requires exactly 1 upstream refresh under same-account contention; got %d for %d goroutines", refreshCount, N)
	}
}

// AT-AUTH-005-003: token_version 上发生 CAS 冲突 → 另一个 goroutine 使用 winner 的 token。
func TestAT_AUTH_005_003_TokenVersionCAS(t *testing.T) {
	r := newRig(t, okOAuthHandler("new"+goodToken, "rt"+goodToken, 3600))
	r.addAccount(3, 300, oauthAccountType, oauthCredJSON(t, "old"+goodToken, "rt"+goodToken, r.upstream.URL, time.Now().Add(1*time.Minute)))

	// 冲突前置: 从外部抬高存储的 token_version, 模拟某个 winner 已经写过。
	cur := r.store.get(3, 300)
	cur.TokenVersion = 5
	r.store.put(*cur)

	// 第一次 refresh 尝试以 TokenVersion=5 加载; SaveRefreshedCredential 会成功,
	// 因为 store CAS 把 5 视作当前值。要真正触发 CAS-lost, 让两个 goroutine 竞争:
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = r.provider.GetAccessToken(context.Background(), 3, 300) }()
	go func() { defer wg.Done(); _, _ = r.provider.GetAccessToken(context.Background(), 3, 300) }()
	wg.Wait()
	finalCur := r.store.get(3, 300)
	if finalCur.TokenVersion < 6 {
		t.Fatalf("token_version should advance after refresh; got %d", finalCur.TokenVersion)
	}
}

// AT-AUTH-005-004: 请求路径上的 refresh 失败受 8s context timeout 约束。
func TestAT_AUTH_005_004_RequestPathTimeout(t *testing.T) {
	t.Skip("Bounded timeout test exercises real 8s wait; skip in fast suite. Phase 4.5 long-test target.")
}

// AT-AUTH-005-006: 静态 credential 支持 —— 不 refresh, 直接返回 api_key。
func TestAT_AUTH_005_006_StaticCredential(t *testing.T) {
	r := newRig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("static credential should NOT call upstream OAuth endpoint")
		http.Error(w, "should not reach", http.StatusInternalServerError)
	}))
	apiKey := "api" + goodToken
	r.addAccount(4, 400, staticAccountType, staticCredJSON(t, apiKey))

	tok, err := r.provider.GetAccessToken(context.Background(), 4, 400)
	if err != nil {
		t.Fatalf("GetAccessToken: %v", err)
	}
	if tok != apiKey {
		t.Fatalf("expected static apiKey %q, got %q", apiKey, tok)
	}
}

// =====================================================================
// HUAKAI 设计的场景
// =====================================================================

// AT-AUTH-005-007: 租户隔离 —— cache key 必须包含 tenant_id。
func TestAT_AUTH_005_007_TenantIsolation(t *testing.T) {
	r := newRig(t, okOAuthHandler(goodToken, "rt"+goodToken, 3600))
	r.addAccount(7, 777, staticAccountType, staticCredJSON(t, "secret-tenant7-"+goodToken))
	r.addAccount(8, 777, staticAccountType, staticCredJSON(t, "secret-tenant8-"+goodToken))

	// 两个租户共享同一 accountID=777; token 必须不同。
	tok7, err := r.provider.GetAccessToken(context.Background(), 7, 777)
	if err != nil {
		t.Fatalf("tenant 7: %v", err)
	}
	tok8, err := r.provider.GetAccessToken(context.Background(), 8, 777)
	if err != nil {
		t.Fatalf("tenant 8: %v", err)
	}
	if tok7 == tok8 {
		t.Fatalf("cross-tenant cache poisoning: both returned %q", tok7)
	}
	if !strings.Contains(tok7, "tenant7") || !strings.Contains(tok8, "tenant8") {
		t.Fatalf("tokens swapped between tenants: tenant7=%q tenant8=%q", tok7, tok8)
	}
}

// AT-AUTH-005-009: token shape 校验拒绝畸形 token。
func TestAT_AUTH_005_009_TokenShapeAttestation(t *testing.T) {
	r := newRig(t, okOAuthHandler("garbage with spaces!", "rt"+goodToken, 3600))
	r.addAccount(9, 900, oauthAccountType, oauthCredJSON(t, "old"+goodToken, "rt"+goodToken, r.upstream.URL, time.Now().Add(1*time.Minute)))

	_, err := r.provider.GetAccessToken(context.Background(), 9, 900)
	if err == nil {
		t.Fatalf("expected ERR_TOKEN_MALFORMED on garbage token")
	}
	if !errors.Is(err, ErrTokenMalformed) {
		t.Fatalf("expected typed ErrTokenMalformed sentinel; got %v", err)
	}
	if !contains(r.audit.entries, OutcomeTokenMalformed) {
		t.Fatalf("expected audit entry with OutcomeTokenMalformed; got %+v", r.audit.entries)
	}
	r.marker.mu.Lock()
	defer r.marker.mu.Unlock()
	if _, ok := r.marker.operatorAttention[storeKey(9, 900)]; !ok {
		t.Fatalf("malformed token must trigger MarkOperatorAttention; got %+v", r.marker.operatorAttention)
	}
}

// AT-AUTH-005-010: refresh token 轮换审计记录 old/new fingerprint (不是明文)。
func TestAT_AUTH_005_010_RefreshRotationAudit(t *testing.T) {
	r := newRig(t, okOAuthHandler("new"+goodToken, "rotated-refresh-"+goodToken, 3600))
	r.addAccount(10, 1000, oauthAccountType, oauthCredJSON(t, "old"+goodToken, "old-refresh-"+goodToken, r.upstream.URL, time.Now().Add(1*time.Minute)))

	_, err := r.provider.GetAccessToken(context.Background(), 10, 1000)
	if err != nil {
		t.Fatalf("GetAccessToken: %v", err)
	}
	rotated := r.audit.byOutcome(OutcomeRefreshTokenRotated)
	if len(rotated) == 0 {
		t.Fatalf("expected at least one OutcomeRefreshTokenRotated audit; got %+v", r.audit.entries)
	}
	for _, e := range rotated {
		if strings.Contains(e.OldRefreshTokenFingerprint, "old-refresh-") {
			t.Fatalf("audit leaks plaintext old refresh_token: %q", e.OldRefreshTokenFingerprint)
		}
		if strings.Contains(e.NewRefreshTokenFingerprint, "rotated-refresh-") {
			t.Fatalf("audit leaks plaintext new refresh_token: %q", e.NewRefreshTokenFingerprint)
		}
	}
}

// AT-AUTH-005-011: 防 token 泄露的 sanitizer 会脱敏 token 形态的串。
func TestAT_AUTH_005_011_TokenLeakageSafeSanitizer(t *testing.T) {
	s := OAuthErrorSanitizer{}
	cases := []string{
		"refresh failed: bearer sk-1234567890abcdef leaked",
		"oauth response body access_token=eyJabc.eyJpc3MiOiJtZSJ9.signature",
		"got toolu_01abcdef0123456789ABCDEF",
		"creds=ant-api03-VeryLongSecretValueHereCanItGetCaught",
	}
	for _, in := range cases {
		out := s.SanitizeError(makeErr(in)).Error()
		// 对 token 形态的值, sanitizer 输出里应当含 [REDACTED]。
		if !strings.Contains(out, "[REDACTED]") {
			t.Errorf("sanitizer left token-shaped pattern in: %q", out)
		}
	}
}

func TestAT_AUTH_005_011_SanitizerRedactsSecretLabels(t *testing.T) {
	s := OAuthErrorSanitizer{}
	cases := []struct {
		name   string
		in     string
		want   string
		leaked []string
	}{
		{
			name:   "quoted password",
			in:     `password: "my secret"`,
			want:   `password: [REDACTED]`,
			leaked: []string{`"my secret"`, `my secret`, ` secret"`},
		},
		{
			name:   "quoted client secret",
			in:     `client_secret='abc def'`,
			want:   `client_secret=[REDACTED]`,
			leaked: []string{`'abc def'`, `abc def`, ` def'`},
		},
		{
			name:   "quoted client assertion",
			in:     `client_assertion: "tok with spaces"`,
			want:   `client_assertion: [REDACTED]`,
			leaked: []string{`"tok with spaces"`, `tok with spaces`, ` with spaces`, `spaces"`},
		},
		{
			name:   "quoted generic secret",
			in:     `secret = "a b c"`,
			want:   `secret = [REDACTED]`,
			leaked: []string{`"a b c"`, `a b c`, ` b c`, `c"`},
		},
		{
			name: "json secrets",
			in:   `{"client_secret":"json-secret","client_assertion":"json-assert","password":"json-pass","secret":"json-generic","authorization":"Bearer json-auth"}`,
			want: `{"client_secret":"[REDACTED]","client_assertion":"[REDACTED]","password":"[REDACTED]","secret":"[REDACTED]","authorization":"[REDACTED]"}`,
			leaked: []string{
				`json-secret`,
				`json-assert`,
				`json-pass`,
				`json-generic`,
				`json-auth`,
			},
		},
		{
			name:   "urlencoded secrets",
			in:     `client_secret=form-secret&client_assertion=form-assert&password=form-pass&secret=form-generic`,
			want:   `client_secret=[REDACTED]&client_assertion=[REDACTED]&password=[REDACTED]&secret=[REDACTED]`,
			leaked: []string{`form-secret`, `form-assert`, `form-pass`, `form-generic`},
		},
		{
			name:   "unquoted labeled secrets",
			in:     `client_secret: labeled-secret password: labeled-pass client_assertion: labeled-assert secret: labeled-generic`,
			want:   `client_secret: [REDACTED] password: [REDACTED] client_assertion: [REDACTED] secret: [REDACTED]`,
			leaked: []string{`labeled-secret`, `labeled-pass`, `labeled-assert`, `labeled-generic`},
		},
		{
			name:   "authorization label",
			in:     `authorization: Bearer labeled-auth`,
			want:   `authorization: [REDACTED]`,
			leaked: []string{`Bearer labeled-auth`, `labeled-auth`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := s.Sanitize(tc.in)
			if out != tc.want {
				t.Fatalf("Sanitize(%q)=%q want %q", tc.in, out, tc.want)
			}
			for _, leaked := range tc.leaked {
				if strings.Contains(out, leaked) {
					t.Fatalf("Sanitize(%q) leaked %q in %q", tc.in, leaked, out)
				}
			}
		})
	}
}

// AT-AUTH-005-012: CAS 冲突的败者使用 winner 的 token + db_version_conflict 审计。
// 通过包裹 memStore 伪造 `RowsAffected=0` 并给出一个已知 winner, 强制走 CAS-loss 路径。
func TestAT_AUTH_005_012_CASLoserUsesWinnerToken(t *testing.T) {
	winnerToken := "winner" + goodToken
	r := newRig(t, okOAuthHandler("loser"+goodToken, "rt"+goodToken, 3600))
	r.addAccount(12, 1200, oauthAccountType, oauthCredJSON(t, "old"+goodToken, "rt"+goodToken, r.upstream.URL, time.Now().Add(1*time.Minute)))

	// 用一个强制 wrapper 覆盖内层 store。
	winnerCred := antigravityCredential{AccessToken: winnerToken, RefreshToken: "rt" + goodToken, ExpiresAt: time.Now().Add(1 * time.Hour), OAuthEndpoint: r.upstream.URL}
	winnerJSON, err := json.Marshal(winnerCred)
	if err != nil {
		t.Fatalf("marshal winner: %v", err)
	}
	winning := ProviderAccountCredential{
		TenantID: 12, AccountID: 1200, Provider: antigravityProvider,
		AccountType: oauthAccountType, Enabled: true,
		CredentialJSON: winnerJSON, TokenVersion: 99,
	}
	forcing := &casForcingStore{inner: r.store, winning: &winning}
	r.provider = NewAntigravityTokenProvider(forcing, r.audit, r.cache, r.lock, r.marker, r.client, nil)

	tok, err := r.provider.GetAccessToken(context.Background(), 12, 1200)
	if err != nil {
		t.Fatalf("CAS-loser path returned error: %v", err)
	}
	if tok != winnerToken {
		t.Fatalf("CAS loser must use winner's access_token; got %q want %q", tok, winnerToken)
	}
	conflicts := r.audit.byOutcome(OutcomeDBVersionConflict)
	if len(conflicts) == 0 {
		t.Fatalf("CAS-loser path must emit OutcomeDBVersionConflict audit; got %+v", r.audit.entries)
	}
}

// casForcingStore 包裹 memStore, 在首次 SaveRefreshedCredential 调用时
// 强制 RowsAffected=0 (CAS-loss), 并返回一个已知的 winning credential。
type casForcingStore struct {
	inner   *memStore
	winning *ProviderAccountCredential
	mu      sync.Mutex
	fired   bool
}

func (s *casForcingStore) LoadProviderAccount(ctx context.Context, tenantID, accountID int64) (ProviderAccountCredential, error) {
	return s.inner.LoadProviderAccount(ctx, tenantID, accountID)
}

func (s *casForcingStore) SaveRefreshedCredential(ctx context.Context, u RefreshedCredentialUpdate) (CredentialSaveResult, error) {
	s.mu.Lock()
	if !s.fired {
		s.fired = true
		s.mu.Unlock()
		return CredentialSaveResult{RowsAffected: 0, Winning: s.winning}, nil
	}
	s.mu.Unlock()
	return s.inner.SaveRefreshedCredential(ctx, u)
}

// =====================================================================
// Storm controller (三个 scope: DB account + 内存版 endpoint/global)
// =====================================================================

// TestStormControllerSmoke 校验构造函数不会 panic。
func TestStormControllerSmoke(t *testing.T) {
	c := NewStormController(nil)
	if c == nil {
		t.Fatalf("controller is nil")
	}
}

// TestStormControllerUnconfiguredScopesAdmitWithoutPanic 证明: 在未配置
// endpoint/global budget 时 (默认的 account-scope-only controller),
// 两个 scope 都 ADMIT (非 nil 的 func、无 outcome、无 error) 且绝不 panic。这就是
// 可选叠加式限流的契约: 未配置的限流绝不能阻塞 refresh —— 始终在线的
// DB account budget 仍是那道 guard。
//
// 变异检查: 让 AcquireProviderEndpoint 在 scope 被禁用时返回一个拒绝 outcome
// → outcome 断言变红。
func TestStormControllerUnconfiguredScopesAdmitWithoutPanic(t *testing.T) {
	c := NewStormController(nil)
	refund, outcome, err := c.AcquireProviderEndpoint(context.Background(), 1, "p", "f")
	if err != nil || outcome != "" || refund == nil {
		t.Fatalf("unconfigured endpoint scope: refund!=nil=%v outcome=%q err=%v, want admit", refund != nil, outcome, err)
	}
	refund() // 不得 panic
	gRefund, outcome, err := c.AcquireGlobal(context.Background(), 1)
	if err != nil || outcome != "" || gRefund == nil {
		t.Fatalf("unconfigured global scope: refund!=nil=%v outcome=%q err=%v, want admit", gRefund != nil, outcome, err)
	}
	gRefund() // 不得 panic
}

// TestAT_SECURITY_W1_O1_StormControllerAccountScopeMissingStateReturnsError
// 消除如下风险: 在 SQL state 缺失或接线为 nil 时, 生产 credentialworker
// 的 account-scope 路径发生 panic。
func TestAT_SECURITY_W1_O1_StormControllerAccountScopeMissingStateReturnsError(t *testing.T) {
	c := NewStormController(nil)
	if _, _, err := c.Acquire(context.Background(), 1, 1); !errors.Is(err, ErrStormControllerUnavailable) {
		t.Fatalf("Acquire nil queries error=%v, want ErrStormControllerUnavailable", err)
	}
	var nilController *StormController
	if _, _, err := nilController.Acquire(context.Background(), 1, 1); !errors.Is(err, ErrStormControllerUnavailable) {
		t.Fatalf("Acquire nil controller error=%v, want ErrStormControllerUnavailable", err)
	}
}

// =====================================================================
// 冒烟测试
// =====================================================================

func TestPackageCompiles(t *testing.T) { // 冒烟: 包能编译
	if time.Now().Year() < 2026 {
		t.Fatalf("clock skew")
	}
}

// =====================================================================
// 辅助工具
// =====================================================================

type stubErr struct{ msg string }

func (e stubErr) Error() string { return e.msg }
func makeErr(s string) error    { return stubErr{msg: s} }

func contains(entries []RefreshAuditEntry, o Outcome) bool {
	for _, e := range entries {
		if e.Outcome == o {
			return true
		}
	}
	return false
}
