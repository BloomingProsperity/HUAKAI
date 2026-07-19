// 按 IP 的入站限流(令牌桶,两级)。
//
// 区分性意图:这些测试断言的是特定的 429 耗尽 + 按 IP 隔离行为,
// 而非登录路径本就会返回的某类状态码。
// 这里的登录路由是一个始终返回 200 的桩,因此观察到的任何 429 都
// 只可能由限流器产生——如果移除限流器或放宽其 burst,
// 「请求 burst+1 应为 429」的断言就会变红(即变异检查)。同样地,
// 限额之内的另一个来源 IP 必须不会看到 429,以证明桶的 key
// 是按 IP 而非共享/全局计数器。
package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
	"github.com/BloomingProsperity/HUAKAI/internal/inboundlimit"
)

// stubOKHandler 代替真实的鉴权处理器:它始终返回 200,因此这些测试中的
// 429 只可能来自限流器(区分性夹具——其预期输出与未加防护的代码
// 所产生的输出不同)。
func stubOKHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
}

// clearRateLimitEnv 中和每一个 HUAKAI_RL_* 覆盖项,使限流器无论处于何种
// 开发/CI 环境都从包内默认值构建。
// 若干断言依赖默认阈值(以及 auth 级比 global 级更窄这一点),因此这些
// 测试必须对外部导出的覆盖项保持封闭隔离。
func clearRateLimitEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"HUAKAI_RL_DISABLE",
		"HUAKAI_RL_GLOBAL_RATE", "HUAKAI_RL_GLOBAL_BURST",
		"HUAKAI_RL_AUTH_LOGIN_PER_MIN", "HUAKAI_RL_AUTH_REGISTER_PER_MIN",
		"HUAKAI_RL_AUTH_VERIFY_PER_MIN", "HUAKAI_RL_AUTH_RESET_PER_MIN",
		"HUAKAI_RL_AUTH_OAUTH_PER_MIN", "HUAKAI_RL_AUTH_REFRESH_PER_MIN",
		"HUAKAI_RL_MEDIA_PER_MIN",
	} {
		t.Setenv(name, "")
	}
}

// fixedClockLimiter 构建一个时钟永不前进的限流器,这样就没有令牌补充会
// 掩盖被测的耗尽状态。nil 的 resolver 以套接字对端(RemoteAddr)为 key,
// 这些测试会显式设置该值。环境覆盖项被清空,使断言所依赖的默认阈值
// 真正生效。
func fixedClockLimiter(t *testing.T) *rateLimiter {
	t.Helper()
	clearRateLimitEnv(t)
	rl := newRateLimiter(nil, nil)
	frozen := time.Unix(1_700_000_000, 0)
	rl.nowFn = func() time.Time { return frozen }
	return rl
}

// doLogin 从给定来源地址,经由被限流器包裹的桩处理器发送一次
// POST /v1/auth/login,并返回状态码。
func doLogin(rl *rateLimiter, remoteAddr string) int {
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	rl.middleware(stubOKHandler()).ServeHTTP(rec, req)
	return rec.Code
}

type countingSharedLimitStore struct {
	counts map[string]int
	err    error
	calls  int
}

func (s *countingSharedLimitStore) Allow(_ context.Context, tier, subject string, _ inboundlimit.Limit) (inboundlimit.Decision, error) {
	s.calls++
	if s.err != nil {
		return inboundlimit.Decision{}, s.err
	}
	if s.counts == nil {
		s.counts = make(map[string]int)
	}
	key := tier + ":" + subject
	s.counts[key]++
	if s.counts[key] > 1 {
		return inboundlimit.Decision{Allowed: false, RetryAfter: 3 * time.Second}, nil
	}
	return inboundlimit.Decision{Allowed: true}, nil
}

func TestRateLimit_SharedStoreAggregatesAcrossInstances(t *testing.T) {
	clearRateLimitEnv(t)
	shared := &countingSharedLimitStore{}
	first := newRateLimiter(nil, nil, withSharedRateLimitStore(shared))
	second := newRateLimiter(nil, nil, withSharedRateLimitStore(shared))
	frozen := time.Unix(1_700_000_000, 0)
	first.nowFn = func() time.Time { return frozen }
	second.nowFn = func() time.Time { return frozen }

	if got := doLogin(first, "198.51.100.20:1000"); got != http.StatusOK {
		t.Fatalf("第一个副本首次请求=%d，want 200", got)
	}
	if got := doLogin(second, "198.51.100.20:2000"); got != http.StatusTooManyRequests {
		t.Fatalf("第二个副本必须命中同一共享桶，got=%d want 429", got)
	}
}

func TestRateLimit_SharedStoreFailureFailsClosed(t *testing.T) {
	clearRateLimitEnv(t)
	shared := &countingSharedLimitStore{err: errors.New("redis unavailable")}
	rl := newRateLimiter(nil, nil, withSharedRateLimitStore(shared))
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.RemoteAddr = "198.51.100.21:1000"
	rec := httptest.NewRecorder()
	rl.middleware(stubOKHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("共享限流后端失败必须拒绝业务请求，got=%d want 503", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After=%q want 1", got)
	}
}

// TestRateLimit_AuthLogin_BurstThen429 从同一个 IP 向登录路径快速发起
// burst+k 个请求:前 `burst` 个通过(非 429),而 burst+1..burst+k 个
// 为 429。来自限额之内的另一个 IP 的请求不会是 429(按 IP 隔离)。
//
// 变异检查:移除 `router.Use(newRateLimiter().middleware)` 接线,或
// 将登录 burst 放大到一个极大值,下方的「want 429」断言就会变红。
func TestRateLimit_AuthLogin_BurstThen429(t *testing.T) {
	rl := fixedClockLimiter(t)

	// 登录类的默认值是 20/min => burst 20。从活动的级别中读取它,
	// 使测试跟随已配置的策略,而非硬编码的副本。
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

	// 来自 ipA 的前 `burst` 个请求必须通过(非 429)。global 级
	//(burst 180)比登录 burst 更宽,因此此处登录级才是起约束作用的
	// 限制条件。
	for i := 0; i < burst; i++ {
		if code := doLogin(rl, ipA); code == http.StatusTooManyRequests {
			t.Fatalf("request %d/%d from ipA unexpectedly 429 (within burst)", i+1, burst)
		}
	}

	// 来自同一个 ip 的 burst+1..burst+k 个请求必须是 429。
	for i := 0; i < k; i++ {
		if code := doLogin(rl, ipA); code != http.StatusTooManyRequests {
			t.Fatalf("request %d past burst from ipA: want 429 got %d", burst+i+1, code)
		}
	}

	// 按 IP 隔离:即便 ipA 已耗尽,另一个处于自身限额之内的 ip 也
	// 必须不是 429。如果 key 是全局/共享的,这里就会是 429。
	const ipB = "198.51.100.42:42000"
	if code := doLogin(rl, ipB); code == http.StatusTooManyRequests {
		t.Fatalf("request from ipB (different IP, under limit) was 429 — per-IP isolation broken")
	}
}

// TestRateLimit_429HasJSONBodyAndRetryAfter 证明耗尽响应是带有 rate_limited
// 错误码和 Retry-After 头的网关 JSON 错误结构,而非裸状态码。
// 变异:去掉 reject() 中的响应体写入 / Retry-After,对应断言就会变红。
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

// TestRateLimit_GlobalTier_FloodsNonAuthPath 证明全局前门级会对非 auth
// 路径上的洪泛设上限(auth-strict 级在那里从不介入,因此只有 global 桶
// 能产生 429)。变异:移除 middleware() 中的 global.allow 检查,本测试
// 就会变红。
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

// TestRateLimit_RegistryBound 证明按 IP 的注册表有一个硬上限,使得伪造 IP
// 的洪泛无法令其无限增长。变异:移除 bucketFor 中的上限重置,
// len(buckets) 就会超过 maxEntries。
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

// TestRateLimit_WiredIntoRouter 驱动真实的 newRouter() 中间件链(而非
// 手工搭建的限流器),并对登录路径洪泛直至看到 429。它直接守护生产
// 接线:如果从 newRouter 中删掉 `router.Use(newRateLimiter(...).middleware)`,
// 登录洪泛就会抵达路由处理器而非被丢弃,此断言便会变红。
//
// 变异检查:删掉 newRouter 中的限流器 router.Use 那一行,本测试就会
// 失败(在 burst+余量窗口内没有出现 429)。
func TestRateLimit_WiredIntoRouter(t *testing.T) {
	clearRateLimitEnv(t)
	router := newRouter(minimalDeps(), zap.NewNop())

	const ip = "203.0.113.200:7000"
	// 登录 burst 默认是 20;发送足够的请求以在一秒内将其耗尽
	//(补充约 0.33 token/s,在紧凑循环中可忽略不计)。global 级(180)
	// 更宽,因此此处登录才是起约束作用的限制条件。
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

	// 端到端的抗伪造能力:同一个套接字对端现已耗尽;轮换
	// X-Forwarded-For(chi 的 RealIP 否则会将其提升为 RemoteAddr)
	// 必须不会铸造出一个新桶。由于限流器在 RealIP 之前运行,且该对端
	// 不是受信任代理,伪造的头会被忽略,这些请求仍保持 429。
	// 变异:把限流器接在 RealIP 之后,这些伪造波次就会通过(无 429)。
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

func TestRateLimit_ProbeKeepsLocalProtectionButBypassesBrokenSharedStore(t *testing.T) {
	clearRateLimitEnv(t)
	shared := &countingSharedLimitStore{err: errors.New("redis unavailable")}
	rl := newRateLimiter(nil, zap.NewNop(), withSharedRateLimitStore(shared))
	rl.global = newIPBucketRegistry(1, 1)
	rl.nowFn = func() time.Time { return time.Unix(1_700_000_000, 0) }
	handler := rl.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	for _, path := range []string{"/healthz", "/readyz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "203.0.113.11:9000"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusTeapot {
			t.Fatalf("%s 未抵达健康处理器，status=%d body=%s", path, rec.Code, rec.Body.String())
		}
		if shared.calls != 0 {
			t.Fatalf("%s 错误调用共享桶，calls=%d", path, shared.calls)
		}

		second := httptest.NewRecorder()
		handler.ServeHTTP(second, req)
		if second.Code != http.StatusTooManyRequests {
			t.Fatalf("%s 未受每实例内存桶保护，status=%d", path, second.Code)
		}

		// 每个路径使用独立来源，避免上一轮耗尽同一个全局桶影响下一用例。
		rl.global = newIPBucketRegistry(1, 1)
	}
}

func TestRateLimit_BusinessRequestStillFailsClosedWhenSharedStoreUnavailable(t *testing.T) {
	clearRateLimitEnv(t)
	shared := &countingSharedLimitStore{err: errors.New("redis unavailable")}
	rl := newRateLimiter(nil, zap.NewNop(), withSharedRateLimitStore(shared))
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.RemoteAddr = "203.0.113.12:9000"
	rec := httptest.NewRecorder()

	rl.middleware(stubOKHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("业务请求未在共享桶故障时关闭，status=%d body=%s", rec.Code, rec.Body.String())
	}
	if shared.calls != 1 {
		t.Fatalf("业务请求共享桶 calls=%d，want 1", shared.calls)
	}
}

// TestRateLimit_429CarriesCORSHeadersForAllowlistedOrigin 证明对一个在允许
// 名单内的浏览器 origin 返回的 429 仍带有 Access-Control-Allow-Origin
// (前端看到的是 JSON 的 429 + Retry-After,而非一个不透明的 CORS 失败)。
// 它守护中间件顺序:corsMiddleware 必须在限流器的提前退出之前运行。
// 变异:在 newRouter 中把 corsMiddleware 移到限流器之后,针对 429 的
// ACAO 断言就会变红。
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

// TestRateLimit_DisableEnvBypassesWiring 证明 HUAKAI_RL_DISABLE 会把限流器
// 从链中移除(因此洪泛不会被丢弃)。与上面的测试成对:两者一起锁定了
// 默认始终开启以及文档中记载的紧急关闭开关。
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

// TestRateLimit_KeyIgnoresForgedForwardingHeaders 证明当套接字对端不是
// 受信任代理时,无法通过伪造 X-Forwarded-For 来重置桶的 key。
// 在未配置任何受信任代理的情况下,resolver 以套接字对端为 key,因此
// 轮换 XFF 不会铸造出新桶,已耗尽的对端仍保持 429。
//
// 变异:如果 clientKey 以伪造的头 / RealIP 重写后的值为 key,而非
// resolver 的受信任对端输出,则第二波伪造 IP 的请求就会通过(无 429),
// 此断言便会变红。
func TestRateLimit_KeyIgnoresForgedForwardingHeaders(t *testing.T) {
	clearRateLimitEnv(t)
	resolver, err := clientip.NewResolver(nil) // 无受信任代理 => 仅用套接字对端
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
	// 同一个套接字对端,但每个请求都伪造一个全新的客户端 IP。不受信任
	// 对端的 XFF 必须被忽略:该对端的桶已耗尽,因此无论伪造的头如何,
	// 每个请求都仍是 429。
	for i := 0; i < 5; i++ {
		forged := "10.0.0." + itoa(i+1)
		if code := send(forged); code != http.StatusTooManyRequests {
			t.Fatalf("forged X-Forwarded-For=%q minted a fresh bucket (got %d) — spoofing bypass", forged, code)
		}
	}
}

// TestRateLimit_EnvOverrideRejectsNonFiniteAndSubToken 证明格式错误的
// HUAKAI_RL_* 覆盖项无法静默地禁用或卡死这个始终开启的限流器:
// NaN/Inf/<=0 回退到安全默认值,而小于一个令牌的 global burst 会回退,
// 而非拒绝每一个请求。
//
// 变异:去掉 envFloat 中的 math.IsNaN/IsInf 防护,下方的 NaN 用例就会
// 产生卡成全开、无默认值的取值;去掉 envBurst 的下限,0.5 用例就会把
// global 级卡成全关闭。
func TestRateLimit_EnvOverrideRejectsNonFiniteAndSubToken(t *testing.T) {
	for _, bad := range []string{"NaN", "Inf", "+Inf", "-Inf", "0", "-5", "abc"} {
		t.Setenv("HUAKAI_RL_TEST_FLOAT", bad)
		if got := envFloat("HUAKAI_RL_TEST_FLOAT", 42); got != 42 {
			t.Errorf("envFloat(%q) did not fall back to default (got %v)", bad, got)
		}
	}
	// 小于一个令牌的 burst 必须回退到默认值((0,1) 区间的 burst 会拒绝所有请求)。
	t.Setenv("HUAKAI_RL_TEST_BURST", "0.5")
	if got := envBurst("HUAKAI_RL_TEST_BURST", 180); got != 180 {
		t.Errorf("envBurst(0.5) = %v, want fallback 180", got)
	}
	// 一个有效的覆盖项会被采纳。
	t.Setenv("HUAKAI_RL_TEST_BURST", "50")
	if got := envBurst("HUAKAI_RL_TEST_BURST", 180); got != 50 {
		t.Errorf("envBurst(50) = %v, want 50", got)
	}
}

// minimalDeps 构建能让 newRouter()/mountRoutes() 在挂载时不发生 nil 崩溃的
// 最小 deps。只有 cfg 会被(chatHandlerDeps)提前解引用;其余每个字段都
// 作为 nil 安全的接口值被惰性消费。限流器会在任何处理器触及这些字段之前
// 拒绝登录洪泛。
func minimalDeps() *deps {
	return &deps{cfg: &Config{BillingPolicyVersion: "test", RequestClass: "standard"}}
}

// TestRateLimit_NonPostDoesNotDrainAuthStrict 证明只有 POST 才会计入收紧的
// auth-strict 桶:向登录路径发送一波 OPTIONS(CORS 预检)以及 GET(廉价的
// 跨站请求,仅接受 POST 的路由会对其返回 405)不会消耗登录桶,因此
// 随后一波真正的 POST 不会被预先限流。
//
// 变异:把 authStrictFor 中的 `r.Method != http.MethodPost` 防护改成只排除
// OPTIONS,那波 GET 就会消耗登录桶,使尾部的 POST 断言变红。
func TestRateLimit_NonPostDoesNotDrainAuthStrict(t *testing.T) {
	rl := fixedClockLimiter(t)
	tier := rl.authStrict["/v1/auth/login"]
	burst := int(tier.registry.burst)

	const ip = "203.0.113.123:9000"
	// 发送比登录 burst 更多的 OPTIONS + GET;它们都必须不是 429(它们远低于
	// 更宽的 global burst,且必须不触及 auth-strict 级)。
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
	// 登录桶必须未被消耗:一整波真正的 POST 仍然通过。
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

// TestRateLimit_OAuthClassSharesOneBucket 证明两个 OAuth 路由共享同一个
// 按类的桶(同一个 HUAKAI_RL_AUTH_OAUTH_PER_MIN 旋钮),因此在
// /v1/auth/oauth-init 与 /v1/auth/oauth-callback 之间交替不会使所允许的
// OAuth 配额翻倍。
//
// 变异:给每个 OAuth 路径各自一个注册表(撤销共享类接线),下方的交替
// 洪泛就会各自停留在每条路径单独的 burst 之内,使「合计在 burst+1 处
// 必为 429」的断言变红。
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
	// 在各路径间交替,共发送 `burst` 个请求:全部必须通过(同一个共享桶)。
	for i := 0; i < burst; i++ {
		req := httptest.NewRequest(http.MethodPost, paths[i%2], nil)
		req.RemoteAddr = ip
		rec := httptest.NewRecorder()
		rl.middleware(stubOKHandler()).ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("alternating OAuth request %d/%d unexpectedly 429 within shared burst", i+1, burst)
		}
	}
	// 紧接着的下一个交替请求必须是 429——共享桶已被耗尽,
	// 这证明配额没有跨两条路径翻倍。
	req := httptest.NewRequest(http.MethodPost, paths[burst%2], nil)
	req.RemoteAddr = ip
	rec := httptest.NewRecorder()
	rl.middleware(stubOKHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("OAuth request past shared burst: want 429 got %d — class budget doubled across paths", rec.Code)
	}
}

// TestRateLimit_RetryAfterTracksConfiguredRate 证明 429 的 Retry-After 提示
// 来自已配置的速率,而非一个陈旧的硬编码默认值:把 register 调低到
// 1/min 会得到约 60s,而非默认值的那个较小值。
//
// 变异:把 retryAfterForRate 改回一个常量,针对调整后速率的断言
// (want 60)就会变红。
func TestRateLimit_RetryAfterTracksConfiguredRate(t *testing.T) {
	clearRateLimitEnv(t)
	t.Setenv("HUAKAI_RL_AUTH_REGISTER_PER_MIN", "1")
	rl := newRateLimiter(nil, nil)
	frozen := time.Unix(1_700_000_000, 0)
	rl.nowFn = func() time.Time { return frozen }

	const ip = "203.0.113.150:9200"
	// burst 被钳制到 >= 1;第一个请求通过,第二个为 429。
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

func TestRateLimit_InviteValidateSharesRegisterBucket(t *testing.T) {
	rl := fixedClockLimiter(t)
	registerTier := rl.authStrict["/v1/auth/register"]
	validateTier := rl.authStrict["/v1/auth/validate-invitation-code"]
	if registerTier == nil || validateTier == nil {
		t.Fatalf("register tier=%v validate tier=%v; validate-invitation-code must be auth-strict limited", registerTier, validateTier)
	}
	if registerTier != validateTier {
		t.Fatal("validate-invitation-code must share the register auth-strict bucket, not mint a separate registration budget")
	}
	burst := int(registerTier.registry.burst)
	const ip = "203.0.113.151:9201"
	paths := []string{"/v1/auth/register", "/v1/auth/validate-invitation-code"}
	for i := 0; i < burst; i++ {
		req := httptest.NewRequest(http.MethodPost, paths[i%len(paths)], nil)
		req.RemoteAddr = ip
		rec := httptest.NewRecorder()
		rl.middleware(stubOKHandler()).ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("alternating register/validate request %d/%d unexpectedly 429 within shared burst", i+1, burst)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/validate-invitation-code", nil)
	req.RemoteAddr = ip
	rec := httptest.NewRecorder()
	rl.middleware(stubOKHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("validate request past shared register burst: want 429 got %d", rec.Code)
	}
}

// TestRateLimit_GlobalRetryAfterTracksConfiguredRate 证明 global 级 429 的
// Retry-After 来自 HUAKAI_RL_GLOBAL_RATE,而非硬编码的 1s:在 0.1 req/s 时,
// 下一个令牌大约还有 10s。
//
// 变异:把 retryAfterS 硬编码为 1,want-10 断言就会变红。
func TestRateLimit_GlobalRetryAfterTracksConfiguredRate(t *testing.T) {
	clearRateLimitEnv(t)
	t.Setenv("HUAKAI_RL_GLOBAL_RATE", "0.1")
	t.Setenv("HUAKAI_RL_GLOBAL_BURST", "1")
	rl := newRateLimiter(nil, nil)
	frozen := time.Unix(1_700_000_000, 0)
	rl.nowFn = func() time.Time { return frozen }

	const ip = "203.0.113.160:9300"
	// 使用一个非 auth 路径,使得只有 global 级才能产生 429。
	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/v1/pricing/rate-table", nil)
		req.RemoteAddr = ip
		rec := httptest.NewRecorder()
		rl.middleware(stubOKHandler()).ServeHTTP(rec, req)
		return rec
	}
	_ = send() // 消耗掉单个 burst 令牌
	rec := send()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second global request at 0.1 req/s: want 429 got %d", rec.Code)
	}
	if ra := rec.Header().Get("Retry-After"); ra != "10" {
		t.Fatalf("global Retry-After at 0.1 req/s: want 10 got %q — hint not derived from configured rate", ra)
	}
}

func TestMediaPerIPLimit(t *testing.T) {
	clearRateLimitEnv(t)
	t.Setenv("HUAKAI_RL_MEDIA_PER_MIN", "2")
	rl := newRateLimiter(nil, nil)
	frozen := time.Unix(1_700_000_000, 0)
	rl.nowFn = func() time.Time { return frozen }

	const ip = "203.0.113.190:9500"
	send := func(path string) int {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.RemoteAddr = ip
		rec := httptest.NewRecorder()
		rl.middleware(stubOKHandler()).ServeHTTP(rec, req)
		return rec.Code
	}

	for i := 0; i < 2; i++ {
		if code := send("/v1/audio/speech"); code == http.StatusTooManyRequests {
			t.Fatalf("media request %d unexpectedly 429 within configured burst", i+1)
		}
	}
	// 变异:不把媒体路径接到媒体类上,会让第三个请求通过。
	if code := send("/v1/audio/speech"); code != http.StatusTooManyRequests {
		t.Fatalf("third media request: want 429 got %d", code)
	}

	// 防护:未设置/为零的环境变量会禁用附加的媒体级,保留旧有行为。
	clearRateLimitEnv(t)
	disabled := newRateLimiter(nil, nil)
	disabled.nowFn = func() time.Time { return frozen }
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", nil)
		req.RemoteAddr = "203.0.113.191:9501"
		rec := httptest.NewRecorder()
		disabled.middleware(stubOKHandler()).ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("media env unset but request %d got 429", i+1)
		}
	}
}

// TestRateLimit_DenialEmitsOperatorEvidence 证明一次限流拒绝会产出运维可见
// 的证据(AT-SEC-001):一条携带级别与客户端 IP 的结构化日志,使运维能够
// 复查突发流量并区分滥用与误报。变异:移除拒绝路径上的 rl.denied(...) 调用,
// 「期望一条拒绝日志」的断言就会变红。
func TestRateLimit_DenialEmitsOperatorEvidence(t *testing.T) {
	clearRateLimitEnv(t)
	core, logs := observer.New(zap.WarnLevel)
	rl := newRateLimiter(nil, zap.New(core))
	frozen := time.Unix(1_700_000_000, 0)
	rl.nowFn = func() time.Time { return frozen }

	tier := rl.authStrict["/v1/auth/login"]
	burst := int(tier.registry.burst)
	const ip = "203.0.113.170:9400"
	// 耗尽登录桶,然后触发一次拒绝。
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
