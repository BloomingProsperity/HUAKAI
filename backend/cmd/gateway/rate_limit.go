package main

import (
	"encoding/json"
	"math"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
)

// Inbound rate limiting.
//
// Two always-on token-bucket tiers, keyed on the RealIP-derived client address,
// stop request floods before any route handler / provider-account / quota use.
// The anti-enumeration argon2 path is expensive enough that an unbounded
// /v1/auth/* flood is a self-amplifying DoS; the strict auth tier caps that.
//
// Tier 1 (global front door): one bucket per client IP, applied to EVERY request.
// Tier 2 (auth-strict): an additional, tighter bucket per client IP, applied only
// to the sensitive auth/session mutation endpoints. A request to those endpoints
// must pass BOTH tiers.
//
// The buckets reuse the existing token-bucket primitive; this package owns only
// the per-IP registry, the env-tunable thresholds, and the wiring. The registry
// is bounded so an IP-spoofed flood (many distinct source addresses) cannot grow
// the map without limit — once the cap is reached the registry is reset, which
// trades a small fairness hiccup for a hard memory ceiling.

const (
	// envRateLimitDisable fully disables both tiers when set truthy. The limiter
	// is otherwise always-on (no opt-in needed) per the approved policy.
	envRateLimitDisable = "HUAKAI_RL_DISABLE"

	// Global front-door defaults: 180 requests / 180s per IP => sustained 1 req/s
	// with a 180-request burst cushion.
	defaultGlobalRate  = 1.0
	defaultGlobalBurst = 180.0

	// Per-class auth-strict defaults, expressed as requests-per-minute. Burst is
	// the same integer count (a one-minute bucket).
	defaultAuthLoginPerMin    = 20.0
	defaultAuthRegisterPerMin = 5.0
	defaultAuthVerifyPerMin   = 5.0
	defaultAuthResetPerMin    = 5.0
	defaultAuthOAuthPerMin    = 20.0
	defaultRefreshPerMin      = 30.0

	// maxBucketsPerTier bounds each tier's per-IP registry so a spoofed-source
	// flood can't grow it without limit. Reaching the cap clears the registry.
	maxBucketsPerTier = 50000
)

// ipBucketRegistry is a concurrency-safe, size-bounded map of client-IP ->
// TokenBucket for a single tier. It is intentionally simple: when the entry
// count would exceed maxEntries the whole map is dropped (a coarse but O(1)
// eviction that guarantees a hard memory ceiling under an IP-spoofed flood).
type ipBucketRegistry struct {
	rate       float64
	burst      float64
	maxEntries int

	mu      sync.Mutex
	buckets map[string]*gateway.TokenBucket
}

func newIPBucketRegistry(rate, burst float64) *ipBucketRegistry {
	return &ipBucketRegistry{
		rate:       rate,
		burst:      burst,
		maxEntries: maxBucketsPerTier,
		buckets:    make(map[string]*gateway.TokenBucket),
	}
}

// bucketFor returns the TokenBucket for key, creating it on first use. If adding
// a new entry would breach the cap the registry is reset first; the bound is on
// resident entries, not on total distinct IPs ever seen.
func (r *ipBucketRegistry) bucketFor(key string) *gateway.TokenBucket {
	r.mu.Lock()
	defer r.mu.Unlock()
	if b, ok := r.buckets[key]; ok {
		return b
	}
	if len(r.buckets) >= r.maxEntries {
		// Hard ceiling reached: drop the table. New callers (including the one
		// being served) get a fresh full bucket, which is fail-open for a single
		// request but preserves the memory bound against IP spoofing.
		r.buckets = make(map[string]*gateway.TokenBucket)
	}
	b := gateway.NewTokenBucket(r.rate, r.burst)
	r.buckets[key] = b
	return b
}

// allow reports whether a request from key may proceed (consumes one token).
func (r *ipBucketRegistry) allow(key string, now time.Time) bool {
	return r.bucketFor(key).TryAcquire(now)
}

// rateLimiter holds the global tier plus a path-class lookup for the auth-strict
// tier. nowFn is injectable so tests can drive a deterministic clock. resolver is
// the gateway's fail-closed trusted-proxy IP resolver (nil-safe): it derives the
// per-IP key from the socket peer plus only-trusted forwarded hops, so a client
// cannot mint fresh buckets by forging forwarding headers.
type rateLimiter struct {
	global      *ipBucketRegistry
	authStrict  map[string]*authStrictTier // request path -> tier
	resolver    *clientip.Resolver
	logger      *zap.Logger
	nowFn       func() time.Time
	retryAfterS int
}

// authStrictTier couples a per-IP registry with its Retry-After hint. The hint is
// derived from the configured rate (not hard-coded), so a tuned override yields an
// accurate retry delay.
type authStrictTier struct {
	registry   *ipBucketRegistry
	retryAfter int
}

// authClass describes one auth-strict class: one bucket policy shared by one or
// more request paths. Paths that share a single env knob (e.g. both OAuth routes)
// belong to the SAME class so they share ONE registry — otherwise alternating
// between the paths would multiply the effective per-class budget.
type authClass struct {
	paths     []string // request paths that share this class's bucket
	envPerMin string   // env var holding a requests-per-minute override
	defPerMin float64  // default requests-per-minute
}

// authClasses is the static policy table. login + 2fa share the single login
// endpoint that exists today; verify-email is the "send verification code"
// class; reset-password is the "forgot password" class; social login routes
// share one class (one budget); sessions/refresh is the "refresh token" class.
var authClasses = []authClass{
	{paths: []string{"/v1/auth/login", "/v1/auth/passkey/login/begin", "/v1/auth/passkey/login/finish"}, envPerMin: "HUAKAI_RL_AUTH_LOGIN_PER_MIN", defPerMin: defaultAuthLoginPerMin},
	{paths: []string{"/v1/auth/register"}, envPerMin: "HUAKAI_RL_AUTH_REGISTER_PER_MIN", defPerMin: defaultAuthRegisterPerMin},
	{paths: []string{"/v1/auth/verify-email"}, envPerMin: "HUAKAI_RL_AUTH_VERIFY_PER_MIN", defPerMin: defaultAuthVerifyPerMin},
	{paths: []string{"/v1/auth/reset-password"}, envPerMin: "HUAKAI_RL_AUTH_RESET_PER_MIN", defPerMin: defaultAuthResetPerMin},
	{paths: []string{"/v1/auth/oauth-init", "/v1/auth/oauth-callback", "/v1/auth/telegram-login"}, envPerMin: "HUAKAI_RL_AUTH_OAUTH_PER_MIN", defPerMin: defaultAuthOAuthPerMin},
	{paths: []string{"/v1/sessions/refresh"}, envPerMin: "HUAKAI_RL_AUTH_REFRESH_PER_MIN", defPerMin: defaultRefreshPerMin},
}

// retryAfterForRatePerSec converts a tokens-per-second refill rate into a
// whole-second Retry-After hint: roughly the time until the next token refills
// (1/ratePerSec), rounded up and floored at 1s. Tracks env overrides so a
// tuned-down rate (e.g. 0.1 req/s) reports a realistic ~10s delay, not a stale 1s.
func retryAfterForRatePerSec(ratePerSec float64) int {
	if ratePerSec <= 0 {
		return 60
	}
	secs := int(math.Ceil(1.0 / ratePerSec))
	if secs < 1 {
		secs = 1
	}
	return secs
}

// retryAfterForRate converts a requests-per-minute rate into a whole-second
// Retry-After hint. A tuned-down rate (e.g. 1/min) reports a realistic ~60s delay.
func retryAfterForRate(perMin float64) int {
	return retryAfterForRatePerSec(perMin / 60.0)
}

// newRateLimiter builds the limiter from env (falling back to the approved
// defaults). The returned limiter is always-on unless HUAKAI_RL_DISABLE is set.
// resolver is the gateway's trusted-proxy IP resolver used for the bucket key
// (nil is safe: keys then fall back to the socket peer). logger is used to emit
// operator-visible evidence on each denial (nil-safe: a no-op logger is used).
func newRateLimiter(resolver *clientip.Resolver, logger *zap.Logger) *rateLimiter {
	if logger == nil {
		logger = zap.NewNop()
	}
	gRate := envFloat("HUAKAI_RL_GLOBAL_RATE", defaultGlobalRate)
	// A global burst below one token would reject every request (the limiter asks
	// for exactly one token per request), so a too-small override falls back to
	// the safe default rather than bricking the front door closed.
	gBurst := envBurst("HUAKAI_RL_GLOBAL_BURST", defaultGlobalBurst)

	authStrict := make(map[string]*authStrictTier, len(authClasses))
	for _, c := range authClasses {
		perMin := envFloat(c.envPerMin, c.defPerMin)
		// rate = requests per second; burst = the per-minute count (clamped >= 1).
		burst := perMin
		if burst < 1 {
			burst = 1
		}
		// One registry per CLASS, shared by every path in the class.
		tier := &authStrictTier{
			registry:   newIPBucketRegistry(perMin/60.0, burst),
			retryAfter: retryAfterForRate(perMin),
		}
		for _, p := range c.paths {
			authStrict[p] = tier
		}
	}

	return &rateLimiter{
		global:     newIPBucketRegistry(gRate, gBurst),
		authStrict: authStrict,
		resolver:   resolver,
		logger:     logger,
		nowFn:      time.Now,
		// Global-tier Retry-After is derived from the configured rate so a tuned-down
		// HUAKAI_RL_GLOBAL_RATE (e.g. 0.1 req/s) reports a realistic delay, not a
		// stale 1s that would make honoring clients retry too early.
		retryAfterS: retryAfterForRatePerSec(gRate),
	}
}

// middleware returns the chi middleware enforcing both tiers. Tier 1 (global) is
// checked first on every request; Tier 2 (auth-strict) is checked only for the
// configured auth/session classes. Either tier exhausting yields 429 and the
// request never reaches the wrapped handler (no provider-account / quota use).
func (rl *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := rl.clientKey(r)
		now := rl.nowFn()

		if !rl.global.allow(key, now) {
			rl.denied(r, "global", key, rl.retryAfterS)
			rl.reject(w, rl.retryAfterS)
			return
		}

		if tier := rl.authStrictFor(r); tier != nil {
			if !tier.registry.allow(key, now) {
				rl.denied(r, "auth_strict", key, tier.retryAfter)
				rl.reject(w, tier.retryAfter)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// denied emits operator-visible evidence for a rate-limit denial (AT-SEC-001):
// which tier fired, the per-IP key, and the request method/path, so operators can
// review the event and distinguish abuse from a false positive. The per-IP key is
// the resolved client IP — not credentials — so this is safe to log.
func (rl *rateLimiter) denied(r *http.Request, tier, key string, retryAfterS int) {
	rl.logger.Warn("inbound rate limit triggered",
		zap.String("tier", tier),
		zap.String("client_ip", key),
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
		zap.Int("retry_after_seconds", retryAfterS),
	)
}

// authStrictFor resolves the auth-strict tier for the request, if any. The
// limiter runs before chi route matching, so it matches on the request path
// (falling back to the resolved route pattern when present). Returns nil when
// the request is not an auth-strict class.
//
// The auth-strict tier is charged ONLY for POST — the single mutation method
// these auth/session routes accept. Any other method (CORS OPTIONS preflight,
// or a cheap cross-site GET/HEAD that the route would 405 anyway) must NOT drain
// the much tighter auth bucket; charging it would let a browser preflight or an
// induced cross-site GET 429 the real login/register/refresh POST for an IP. The
// wider global tier still bounds floods of those other methods.
func (rl *rateLimiter) authStrictFor(r *http.Request) *authStrictTier {
	if r.Method != http.MethodPost {
		return nil
	}
	if tier, ok := rl.authStrict[r.URL.Path]; ok {
		return tier
	}
	if rctx := chi.RouteContext(r.Context()); rctx != nil {
		if tier, ok := rl.authStrict[rctx.RoutePattern()]; ok {
			return tier
		}
	}
	return nil
}

// reject writes the gateway's standard JSON error body with HTTP 429.
func (rl *rateLimiter) reject(w http.ResponseWriter, retryAfterS int) {
	if retryAfterS > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfterS))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	body, err := json.Marshal(map[string]map[string]string{
		"error": {
			"code":    "rate_limited",
			"message": "too many requests; slow down and retry",
		},
	})
	if err != nil {
		body = []byte(`{"error":{"code":"rate_limited","message":"too many requests"}}`)
	}
	_, _ = w.Write(body)
}

// clientKey derives the per-IP bucket key. It uses the gateway's trusted-proxy
// resolver so the key is the real client as seen past only-trusted forwarding
// hops — a client cannot mint fresh buckets by forging X-Forwarded-For/X-Real-IP.
// With no trusted proxies configured the resolver returns the socket peer (the
// safe default for direct exposure). The port is already stripped by the
// resolver; a bare RemoteAddr fallback covers the nil-resolver path.
// clientid.Identity is deliberately NOT used: it is a coarse client-TYPE enum and
// would collapse all callers into a single bucket, defeating per-IP isolation.
func (rl *rateLimiter) clientKey(r *http.Request) string {
	if key := strings.TrimSpace(rl.resolver.ClientIP(r)); key != "" {
		return key
	}
	addr := strings.TrimSpace(r.RemoteAddr)
	if addr == "" {
		return "unknown"
	}
	if host, _, err := net.SplitHostPort(addr); err == nil && host != "" {
		return host
	}
	return addr
}

// envFloat reads a positive, finite float env override, falling back when the var
// is unset/blank/invalid, non-positive, or non-finite (NaN/Inf). A NaN/Inf burst
// would never exhaust the bucket (silently disabling the always-on limiter), so
// such values are rejected in favor of the safe default.
func envFloat(name string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return fallback
	}
	return v
}

// envBurst reads a burst override but additionally rejects values below one token:
// the limiter consumes exactly one token per request, so a burst in (0,1) would
// reject every request and brick the front door closed. Such values fall back to
// the safe default.
func envBurst(name string, fallback float64) float64 {
	v := envFloat(name, fallback)
	if v < 1 {
		return fallback
	}
	return v
}

// rateLimitDisabled reports whether the limiter is turned off via env.
func rateLimitDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(envRateLimitDisable))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
