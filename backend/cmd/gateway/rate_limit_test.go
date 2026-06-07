// Per-IP inbound rate limit (token-bucket, two tiers).
//
// Discriminating intent: these tests assert the SPECIFIC 429 exhaustion + per-IP
// isolation behavior, not a status class the login path would already return.
// The login route here is a stub that always returns 200, so any 429 observed is
// produced ONLY by the limiter — if the limiter is removed or its burst widened,
// the "request burst+1 is 429" assertion goes red (the mutation check). Likewise,
// a different source IP under the limit must NOT see 429, proving the bucket key
// is per-IP and not a shared/global counter.
package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
)

// stubOKHandler stands in for the real auth handler: it ALWAYS returns 200, so a
// 429 in these tests can only come from the limiter (discriminating fixture —
// the expected output differs from what unguarded code produces).
func stubOKHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
}

// clearRateLimitEnv neutralizes every HUAKAI_RL_* override so the limiter is built
// from the package defaults regardless of the ambient developer/CI environment.
// Several assertions assume the default thresholds (and that the auth tier is
// narrower than the global tier), so the tests must be hermetic against exported
// overrides.
func clearRateLimitEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"HUAKAI_RL_DISABLE",
		"HUAKAI_RL_GLOBAL_RATE", "HUAKAI_RL_GLOBAL_BURST",
		"HUAKAI_RL_AUTH_LOGIN_PER_MIN", "HUAKAI_RL_AUTH_REGISTER_PER_MIN",
		"HUAKAI_RL_AUTH_VERIFY_PER_MIN", "HUAKAI_RL_AUTH_RESET_PER_MIN",
		"HUAKAI_RL_AUTH_OAUTH_PER_MIN", "HUAKAI_RL_AUTH_REFRESH_PER_MIN",
	} {
		t.Setenv(name, "")
	}
}

// fixedClockLimiter builds a limiter whose clock never advances, so no token
// refill masks the exhaustion under test. A nil resolver keys on the socket peer
// (RemoteAddr), which these tests set explicitly. Env overrides are cleared so the
// default thresholds the assertions rely on are in effect.
func fixedClockLimiter(t *testing.T) *rateLimiter {
	t.Helper()
	clearRateLimitEnv(t)
	rl := newRateLimiter(nil, nil)
	frozen := time.Unix(1_700_000_000, 0)
	rl.nowFn = func() time.Time { return frozen }
	return rl
}

// doLogin sends one POST /v1/auth/login from the given source address through the
// limiter-wrapped stub handler and returns the status code.
func doLogin(rl *rateLimiter, remoteAddr string) int {
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	rl.middleware(stubOKHandler()).ServeHTTP(rec, req)
	return rec.Code
}

// TestRateLimit_AuthLogin_BurstThen429 fires burst+k rapid requests from ONE IP
// at the login path: the first `burst` pass (non-429), and requests
// burst+1..burst+k are 429. A request from a DIFFERENT IP under the limit is NOT
// 429 (per-IP isolation).
//
// Mutation check: remove `router.Use(newRateLimiter().middleware)` wiring OR
// widen the login burst to a huge number and the "want 429" assertions below go
// red.
func TestRateLimit_AuthLogin_BurstThen429(t *testing.T) {
	rl := fixedClockLimiter(t)

	// The login class default is 20/min => burst 20. Read it from the live tier
	// so the test tracks the configured policy rather than a hard-coded copy.
	tier := rl.authStrict["/v1/auth/login"]
	if tier == nil {
		t.Fatal("login auth-strict tier not configured")
	}
	burst := int(tier.registry.burst)
	if burst < 1 {
		t.Fatalf("unexpected non-positive login burst %d", burst)
	}

	const ipA = "203.0.113.7:51000"
	const k = 5

	// First `burst` requests from ipA must pass (not 429). The global tier
	// (burst 180) is wider than the login burst, so the login tier is the binding
	// constraint here.
	for i := 0; i < burst; i++ {
		if code := doLogin(rl, ipA); code == http.StatusTooManyRequests {
			t.Fatalf("request %d/%d from ipA unexpectedly 429 (within burst)", i+1, burst)
		}
	}

	// Requests burst+1..burst+k from the SAME ip must be 429.
	for i := 0; i < k; i++ {
		if code := doLogin(rl, ipA); code != http.StatusTooManyRequests {
			t.Fatalf("request %d past burst from ipA: want 429 got %d", burst+i+1, code)
		}
	}

	// Per-IP isolation: a DIFFERENT ip, under its own limit, must NOT be 429 even
	// though ipA is exhausted. If the key were global/shared this would be 429.
	const ipB = "198.51.100.42:42000"
	if code := doLogin(rl, ipB); code == http.StatusTooManyRequests {
		t.Fatalf("request from ipB (different IP, under limit) was 429 — per-IP isolation broken")
	}
}

// TestRateLimit_429HasJSONBodyAndRetryAfter proves the exhaustion response is the
// gateway JSON error shape with the rate_limited code and a Retry-After header —
// not a bare status. Mutation: drop the body write / Retry-After in reject() and
// the matching assertion goes red.
func TestRateLimit_429HasJSONBodyAndRetryAfter(t *testing.T) {
	rl := fixedClockLimiter(t)
	tier := rl.authStrict["/v1/auth/login"]
	burst := int(tier.registry.burst)

	const ip = "203.0.113.9:5000"
	for i := 0; i < burst; i++ {
		_ = doLogin(rl, ip)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	req.RemoteAddr = ip
	rec := httptest.NewRecorder()
	rl.middleware(stubOKHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429 got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: want application/json got %q", ct)
	}
	if ra := rec.Header().Get("Retry-After"); ra == "" {
		t.Error("Retry-After header missing on 429")
	}
	body := rec.Body.String()
	if !containsAll(body, `"error"`, `"code"`, `"rate_limited"`) {
		t.Errorf("429 body is not the gateway JSON error shape: %s", body)
	}
}

// TestRateLimit_GlobalTier_FloodsNonAuthPath proves the global front-door tier
// caps a flood on a NON-auth path (the auth-strict tier never engages there, so
// only the global bucket can produce 429). Mutation: remove the global.allow
// check in middleware() and this goes red.
func TestRateLimit_GlobalTier_FloodsNonAuthPath(t *testing.T) {
	rl := fixedClockLimiter(t)
	globalBurst := int(rl.global.burst)
	if globalBurst < 1 {
		t.Fatalf("unexpected non-positive global burst %d", globalBurst)
	}

	const ip = "203.0.113.11:6000"
	send := func() int {
		req := httptest.NewRequest(http.MethodGet, "/v1/pricing/rate-table", nil)
		req.RemoteAddr = ip
		rec := httptest.NewRecorder()
		rl.middleware(stubOKHandler()).ServeHTTP(rec, req)
		return rec.Code
	}

	for i := 0; i < globalBurst; i++ {
		if code := send(); code == http.StatusTooManyRequests {
			t.Fatalf("request %d/%d unexpectedly 429 within global burst", i+1, globalBurst)
		}
	}
	if code := send(); code != http.StatusTooManyRequests {
		t.Fatalf("request past global burst: want 429 got %d", code)
	}
}

// TestRateLimit_RegistryBound proves the per-IP registry has a hard ceiling so an
// IP-spoofed flood can't grow it without limit. Mutation: remove the cap reset in
// bucketFor and len(buckets) exceeds maxEntries.
func TestRateLimit_RegistryBound(t *testing.T) {
	reg := newIPBucketRegistry(1, 1)
	reg.maxEntries = 4
	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < 100; i++ {
		reg.allow(string(rune('a'+i%26))+itoa(i), now)
	}
	if len(reg.buckets) > reg.maxEntries {
		t.Fatalf("registry grew past cap: have %d want <= %d", len(reg.buckets), reg.maxEntries)
	}
}

// TestRateLimit_WiredIntoRouter drives the REAL newRouter() middleware chain (not
// a hand-built limiter) and floods the login path until it sees 429. This guards
// the production wiring directly: if `router.Use(newRateLimiter(...).middleware)`
// is deleted from newRouter, the login flood reaches the route handler instead of
// being shed and this assertion goes red.
//
// Mutation check: delete the limiter router.Use line in newRouter and this test
// fails (no 429 within the burst+slack window).
func TestRateLimit_WiredIntoRouter(t *testing.T) {
	clearRateLimitEnv(t)
	router := newRouter(minimalDeps(), zap.NewNop())

	const ip = "203.0.113.200:7000"
	// Login burst default is 20; fire enough to exhaust it well within one second
	// (refill ~0.33 tok/s, negligible across a tight loop). The global tier (180)
	// is wider, so login is the binding constraint here.
	saw429 := false
	for i := 0; i < int(defaultAuthLoginPerMin)+10; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
		req.RemoteAddr = ip
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			saw429 = true
			break
		}
	}
	if !saw429 {
		t.Fatal("login flood through real router never hit 429 — limiter not wired into newRouter")
	}

	// End-to-end spoof resistance: the SAME socket peer is now exhausted; rotating
	// X-Forwarded-For (which chi's RealIP would otherwise promote to RemoteAddr)
	// must NOT mint a fresh bucket. Because the limiter runs BEFORE RealIP and the
	// peer is not a trusted proxy, the forged header is ignored and these stay 429.
	// Mutation: wire the limiter AFTER RealIP and these forged waves pass (no 429).
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
		req.RemoteAddr = ip
		req.Header.Set("X-Forwarded-For", "10.1.2."+itoa(i+1))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("forged X-Forwarded-For wave %d minted a fresh bucket (got %d) — limiter keyed off RealIP-spoofable address", i+1, rec.Code)
		}
	}
}

// TestRateLimit_429CarriesCORSHeadersForAllowlistedOrigin proves a 429 to an
// allowlisted browser origin still carries Access-Control-Allow-Origin (the
// frontend sees the JSON 429 + Retry-After, not an opaque CORS failure). This
// guards the middleware ORDER: corsMiddleware must run before the limiter's
// early-exit. Mutation: move corsMiddleware after the limiter in newRouter and
// the ACAO assertion on the 429 goes red.
func TestRateLimit_429CarriesCORSHeadersForAllowlistedOrigin(t *testing.T) {
	const origin = "https://app.example.com"
	clearRateLimitEnv(t)
	t.Setenv("HUAKAI_CORS_ALLOWED_ORIGINS", origin)
	router := newRouter(minimalDeps(), zap.NewNop())

	const ip = "203.0.113.210:7200"
	var last *httptest.ResponseRecorder
	for i := 0; i < int(defaultGlobalBurst)+int(defaultAuthLoginPerMin)+50; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
		req.RemoteAddr = ip
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		last = rec
		if rec.Code == http.StatusTooManyRequests {
			break
		}
	}
	if last.Code != http.StatusTooManyRequests {
		t.Fatal("never observed a 429 to assert CORS headers on")
	}
	if got := last.Header().Get("Access-Control-Allow-Origin"); got != origin {
		t.Fatalf("429 missing Access-Control-Allow-Origin: want %q got %q — CORS runs after limiter", origin, got)
	}
}

// TestRateLimit_DisableEnvBypassesWiring proves HUAKAI_RL_DISABLE removes the
// limiter from the chain (so a flood is NOT shed). Pairs with the test above:
// together they pin the always-on default AND the documented kill switch.
func TestRateLimit_DisableEnvBypassesWiring(t *testing.T) {
	clearRateLimitEnv(t)
	t.Setenv("HUAKAI_RL_DISABLE", "true")
	router := newRouter(minimalDeps(), zap.NewNop())

	const ip = "203.0.113.201:7100"
	for i := 0; i < int(defaultAuthLoginPerMin)+10; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
		req.RemoteAddr = ip
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("HUAKAI_RL_DISABLE=true but request %d still got 429 — kill switch ignored", i+1)
		}
	}
}

// TestRateLimit_KeyIgnoresForgedForwardingHeaders proves the bucket key cannot be
// reset by forging X-Forwarded-For when the socket peer is NOT a trusted proxy.
// With no trusted proxies configured, the resolver keys on the socket peer, so
// rotating XFF does not mint fresh buckets and an exhausted peer stays 429.
//
// Mutation: if clientKey keyed on a forged header / RealIP-rewritten value
// instead of the resolver's trusted-peer output, the second IP-spoofed wave would
// pass (no 429) and this assertion would go red.
func TestRateLimit_KeyIgnoresForgedForwardingHeaders(t *testing.T) {
	clearRateLimitEnv(t)
	resolver, err := clientip.NewResolver(nil) // no trusted proxies => socket peer only
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	rl := newRateLimiter(resolver, nil)
	frozen := time.Unix(1_700_000_000, 0)
	rl.nowFn = func() time.Time { return frozen }

	tier := rl.authStrict["/v1/auth/login"]
	burst := int(tier.registry.burst)
	const peer = "203.0.113.250:8000"

	send := func(xff string) int {
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
		req.RemoteAddr = peer
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		rec := httptest.NewRecorder()
		rl.middleware(stubOKHandler()).ServeHTTP(rec, req)
		return rec.Code
	}

	for i := 0; i < burst; i++ {
		if code := send(""); code == http.StatusTooManyRequests {
			t.Fatalf("request %d/%d unexpectedly 429 within burst", i+1, burst)
		}
	}
	// Same socket peer, but forge a brand-new client IP per request. An untrusted
	// peer's XFF must be ignored: the peer's bucket is exhausted, so each remains
	// 429 regardless of the forged header.
	for i := 0; i < 5; i++ {
		forged := "10.0.0." + itoa(i+1)
		if code := send(forged); code != http.StatusTooManyRequests {
			t.Fatalf("forged X-Forwarded-For=%q minted a fresh bucket (got %d) — spoofing bypass", forged, code)
		}
	}
}

// TestRateLimit_EnvOverrideRejectsNonFiniteAndSubToken proves a malformed
// HUAKAI_RL_* override cannot silently disable or brick the always-on limiter:
// NaN/Inf/<=0 fall back to the safe default, and a sub-token global burst falls
// back rather than rejecting every request.
//
// Mutation: drop the math.IsNaN/IsInf guard in envFloat and the NaN case below
// yields the bricked-open default-less value; drop the envBurst floor and the
// 0.5 case bricks the global tier closed.
func TestRateLimit_EnvOverrideRejectsNonFiniteAndSubToken(t *testing.T) {
	for _, bad := range []string{"NaN", "Inf", "+Inf", "-Inf", "0", "-5", "abc"} {
		t.Setenv("HUAKAI_RL_TEST_FLOAT", bad)
		if got := envFloat("HUAKAI_RL_TEST_FLOAT", 42); got != 42 {
			t.Errorf("envFloat(%q) did not fall back to default (got %v)", bad, got)
		}
	}
	// Sub-token burst must fall back to the default (a (0,1) burst rejects all).
	t.Setenv("HUAKAI_RL_TEST_BURST", "0.5")
	if got := envBurst("HUAKAI_RL_TEST_BURST", 180); got != 180 {
		t.Errorf("envBurst(0.5) = %v, want fallback 180", got)
	}
	// A valid override is honored.
	t.Setenv("HUAKAI_RL_TEST_BURST", "50")
	if got := envBurst("HUAKAI_RL_TEST_BURST", 180); got != 50 {
		t.Errorf("envBurst(50) = %v, want 50", got)
	}
}

// minimalDeps builds the smallest deps that lets newRouter()/mountRoutes() mount
// without nil-panicking at mount time. Only cfg is dereferenced eagerly (by
// chatHandlerDeps); every other field is consumed lazily as a nil-safe interface
// value. The limiter rejects the login flood before any handler touches these.
func minimalDeps() *deps {
	return &deps{cfg: &Config{BillingPolicyVersion: "test", RequestClass: "standard"}}
}

// TestRateLimit_NonPostDoesNotDrainAuthStrict proves only POST is charged to the
// tight auth-strict bucket: a flood of OPTIONS (CORS preflight) AND GET (cheap
// cross-site, which the POST-only route would 405) to the login path leaves the
// login bucket full, so a subsequent burst of real POSTs is not pre-throttled.
//
// Mutation: change the `r.Method != http.MethodPost` guard in authStrictFor to
// only exclude OPTIONS and the GET wave drains the login bucket, making the
// trailing POST assertion go red.
func TestRateLimit_NonPostDoesNotDrainAuthStrict(t *testing.T) {
	rl := fixedClockLimiter(t)
	tier := rl.authStrict["/v1/auth/login"]
	burst := int(tier.registry.burst)

	const ip = "203.0.113.123:9000"
	// Fire more OPTIONS + GET than the login burst; none must be 429 (they are well
	// under the wider global burst, and must not touch the auth-strict tier).
	for _, method := range []string{http.MethodOptions, http.MethodGet, http.MethodHead} {
		for i := 0; i < burst+5; i++ {
			req := httptest.NewRequest(method, "/v1/auth/login", nil)
			req.RemoteAddr = ip
			rec := httptest.NewRecorder()
			rl.middleware(stubOKHandler()).ServeHTTP(rec, req)
			if rec.Code == http.StatusTooManyRequests {
				t.Fatalf("%s request %d got 429 — non-mutation method charged to auth-strict tier", method, i+1)
			}
		}
	}
	// The login bucket must be untouched: a full burst of real POSTs still passes.
	for i := 0; i < burst; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
		req.RemoteAddr = ip
		rec := httptest.NewRecorder()
		rl.middleware(stubOKHandler()).ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("POST %d/%d got 429 — non-POST traffic wrongly drained the login bucket", i+1, burst)
		}
	}
}

// TestRateLimit_OAuthClassSharesOneBucket proves the two OAuth routes share a
// single per-class bucket (one HUAKAI_RL_AUTH_OAUTH_PER_MIN knob), so alternating
// between /v1/auth/oauth-init and /v1/auth/oauth-callback does NOT double the
// allowed OAuth budget.
//
// Mutation: give each OAuth path its own registry (revert the shared-class wiring)
// and the alternating flood below stays under each path's separate burst, so the
// "must 429 by burst+1 combined" assertion goes red.
func TestRateLimit_OAuthClassSharesOneBucket(t *testing.T) {
	rl := fixedClockLimiter(t)
	initTier := rl.authStrict["/v1/auth/oauth-init"]
	cbTier := rl.authStrict["/v1/auth/oauth-callback"]
	telegramTier := rl.authStrict["/v1/auth/telegram-login"]
	if initTier != cbTier || initTier != telegramTier {
		t.Fatal("social login routes do not share one tier — class budget can be doubled")
	}
	burst := int(initTier.registry.burst)

	const ip = "203.0.113.140:9100"
	paths := []string{"/v1/auth/oauth-init", "/v1/auth/oauth-callback", "/v1/auth/telegram-login"}
	// Alternate paths for `burst` total requests: all must pass (one shared bucket).
	for i := 0; i < burst; i++ {
		req := httptest.NewRequest(http.MethodPost, paths[i%2], nil)
		req.RemoteAddr = ip
		rec := httptest.NewRecorder()
		rl.middleware(stubOKHandler()).ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("alternating OAuth request %d/%d unexpectedly 429 within shared burst", i+1, burst)
		}
	}
	// The very next alternating request must be 429 — the shared bucket is drained,
	// proving the budget was NOT doubled across the two paths.
	req := httptest.NewRequest(http.MethodPost, paths[burst%2], nil)
	req.RemoteAddr = ip
	rec := httptest.NewRecorder()
	rl.middleware(stubOKHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("OAuth request past shared burst: want 429 got %d — class budget doubled across paths", rec.Code)
	}
}

// TestRateLimit_RetryAfterTracksConfiguredRate proves the 429 Retry-After hint is
// derived from the configured rate, not a stale hard-coded default: tuning
// register down to 1/min yields ~60s, not the default's small value.
//
// Mutation: revert retryAfterForRate to a constant and the tuned-rate assertion
// (want 60) goes red.
func TestRateLimit_RetryAfterTracksConfiguredRate(t *testing.T) {
	clearRateLimitEnv(t)
	t.Setenv("HUAKAI_RL_AUTH_REGISTER_PER_MIN", "1")
	rl := newRateLimiter(nil, nil)
	frozen := time.Unix(1_700_000_000, 0)
	rl.nowFn = func() time.Time { return frozen }

	const ip = "203.0.113.150:9200"
	// burst is clamped to >= 1; the first request passes, the second is 429.
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/register", nil)
		req.RemoteAddr = ip
		rec := httptest.NewRecorder()
		rl.middleware(stubOKHandler()).ServeHTTP(rec, req)
		if i == 1 {
			if rec.Code != http.StatusTooManyRequests {
				t.Fatalf("second register at 1/min: want 429 got %d", rec.Code)
			}
			if ra := rec.Header().Get("Retry-After"); ra != "60" {
				t.Fatalf("Retry-After at 1/min: want 60 got %q — hint not derived from configured rate", ra)
			}
		}
	}
}

// TestRateLimit_GlobalRetryAfterTracksConfiguredRate proves the GLOBAL-tier 429
// Retry-After is derived from HUAKAI_RL_GLOBAL_RATE, not a hard-coded 1s: at
// 0.1 req/s the next token is ~10s away.
//
// Mutation: hard-code retryAfterS=1 and the want-10 assertion goes red.
func TestRateLimit_GlobalRetryAfterTracksConfiguredRate(t *testing.T) {
	clearRateLimitEnv(t)
	t.Setenv("HUAKAI_RL_GLOBAL_RATE", "0.1")
	t.Setenv("HUAKAI_RL_GLOBAL_BURST", "1")
	rl := newRateLimiter(nil, nil)
	frozen := time.Unix(1_700_000_000, 0)
	rl.nowFn = func() time.Time { return frozen }

	const ip = "203.0.113.160:9300"
	// Use a non-auth path so only the global tier can 429.
	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/v1/pricing/rate-table", nil)
		req.RemoteAddr = ip
		rec := httptest.NewRecorder()
		rl.middleware(stubOKHandler()).ServeHTTP(rec, req)
		return rec
	}
	_ = send() // consume the single burst token
	rec := send()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second global request at 0.1 req/s: want 429 got %d", rec.Code)
	}
	if ra := rec.Header().Get("Retry-After"); ra != "10" {
		t.Fatalf("global Retry-After at 0.1 req/s: want 10 got %q — hint not derived from configured rate", ra)
	}
}

// TestRateLimit_DenialEmitsOperatorEvidence proves a rate-limit denial emits
// operator-visible evidence (AT-SEC-001): a structured log carrying the tier and
// client IP, so operators can review bursts and distinguish abuse from false
// positives. Mutation: remove the rl.denied(...) call on the denial path and the
// "expected one denial log" assertion goes red.
func TestRateLimit_DenialEmitsOperatorEvidence(t *testing.T) {
	clearRateLimitEnv(t)
	core, logs := observer.New(zap.WarnLevel)
	rl := newRateLimiter(nil, zap.New(core))
	frozen := time.Unix(1_700_000_000, 0)
	rl.nowFn = func() time.Time { return frozen }

	tier := rl.authStrict["/v1/auth/login"]
	burst := int(tier.registry.burst)
	const ip = "203.0.113.170:9400"
	// Exhaust the login bucket, then trigger one denial.
	for i := 0; i < burst+1; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
		req.RemoteAddr = ip
		rec := httptest.NewRecorder()
		rl.middleware(stubOKHandler()).ServeHTTP(rec, req)
	}

	entries := logs.FilterMessage("inbound rate limit triggered").All()
	if len(entries) == 0 {
		t.Fatal("expected at least one rate-limit denial log — no operator evidence emitted")
	}
	fields := entries[0].ContextMap()
	if fields["tier"] != "auth_strict" {
		t.Errorf("denial log tier: want auth_strict got %v", fields["tier"])
	}
	if fields["client_ip"] != "203.0.113.170" {
		t.Errorf("denial log client_ip: want %q got %v", "203.0.113.170", fields["client_ip"])
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
