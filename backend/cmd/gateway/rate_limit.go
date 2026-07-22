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
	"github.com/BloomingProsperity/HUAKAI/internal/inboundlimit"
)

// 入站限流。
//
// 令牌桶分层,以 RealIP 推导出的客户端地址为键,在任何路由 handler /
// provider-account / 配额被使用之前拦下请求洪泛。
// 反枚举的 argon2 路径足够昂贵,以至于一个无界的 /v1/auth/* 洪泛
// 本身就是自我放大的 DoS;严格的 auth 层把它封顶。
// 可选的 media 层在显式启用时给高成本媒体端点封顶。
//
// 第 1 层(全局前门):每个客户端 IP 一个桶,对每个请求都生效。
// 第 2 层(auth-strict):每个客户端 IP 额外多一个更紧的桶,只对
// 敏感的 auth/session 变更端点生效。打到这些端点的请求必须同时通过两层。
// 第 3 层(media-strict):默认禁用;启用后,媒体路径共用一个
// 独立的按 IP 桶,不改变 auth/global 的预算。
//
// 这些桶复用既有的令牌桶原语;本包只拥有按 IP 的 registry、
// env 可调阈值,以及接线。registry 是有界的,这样 IP 伪造洪泛
//(大量不同源地址)无法让这个 map 无限增长——一旦达到上限,registry
// 即被重置,用一次小小的公平性抖动换来一个硬性的内存上限。

const (
	// envRateLimitDisable 设为真值时彻底禁用两层限流。否则按既定策略,
	// 限流器始终开启(无需显式 opt-in)。
	envRateLimitDisable = "HUAKAI_RL_DISABLE"

	// 全局前门默认值:每 IP 180 请求 / 180s => 持续 1 req/s,
	// 外加 180 请求的突发缓冲。
	defaultGlobalRate  = 1.0
	defaultGlobalBurst = 180.0

	// 各 class 的 auth-strict 默认值,以「每分钟请求数」表示。Burst 取
	// 同样的整数计数(一分钟的桶)。
	defaultAuthLoginPerMin    = 20.0
	defaultAuthRegisterPerMin = 5.0
	defaultAuthVerifyPerMin   = 5.0
	defaultAuthResetPerMin    = 5.0
	defaultAuthOAuthPerMin    = 20.0
	defaultRefreshPerMin      = 30.0
	defaultMediaPerMin        = 0.0

	// maxBucketsPerTier 给每层的按 IP registry 设上界,这样伪造源洪泛
	// 无法让它无限增长。达到上限会清空该 registry。
	maxBucketsPerTier = 50000
)

// ipBucketRegistry 是单层限流用的、并发安全且大小有界的
// 「客户端 IP -> TokenBucket」映射。它刻意保持简单:当条目数将要
// 超过 maxEntries 时,整个 map 被丢弃(一种粗放但 O(1) 的淘汰,
// 在 IP 伪造洪泛下保证硬性内存上限)。
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

// bucketFor 返回 key 对应的 TokenBucket,首次使用时创建。如果新增一个
// 条目会突破上限,则先重置 registry;界限作用于驻留条目数,而非历史上
// 见过的不同 IP 总数。
func (r *ipBucketRegistry) bucketFor(key string) *gateway.TokenBucket {
	r.mu.Lock()
	defer r.mu.Unlock()
	if b, ok := r.buckets[key]; ok {
		return b
	}
	if len(r.buckets) >= r.maxEntries {
		// 达到硬上限:丢弃整张表。新来者(包括当前正在被服务的这一个)
		// 拿到一个全新的满桶,对单个请求而言是 fail-open,但保住了
		// 抵御 IP 伪造的内存上界。
		r.buckets = make(map[string]*gateway.TokenBucket)
	}
	b := gateway.NewTokenBucket(r.rate, r.burst)
	r.buckets[key] = b
	return b
}

// allow 报告来自 key 的请求是否可以放行(消耗一个令牌)。
func (r *ipBucketRegistry) allow(key string, now time.Time) bool {
	return r.bucketFor(key).TryAcquire(now)
}

// rateLimiter 持有全局层,以及 auth-strict 层的「路径 -> class」查找表。
// nowFn 可注入,便于测试驱动一个确定性时钟。resolver 是网关的
// fail-closed 可信代理 IP 解析器(nil-safe):它从 socket 对端加上仅可信的
// 转发跳数推导出按 IP 的键,因此客户端无法通过伪造转发头来铸造新桶。
type rateLimiter struct {
	global      *ipBucketRegistry
	authStrict  map[string]*authStrictTier // 请求路径 -> 层
	mediaStrict map[string]*authStrictTier // 请求路径 -> 层
	resolver    *clientip.Resolver
	logger      *zap.Logger
	nowFn       func() time.Time
	retryAfterS int
	shared      inboundlimit.Store
	globalLimit inboundlimit.Limit
}

// authStrictTier 把一个按 IP registry 与它的 Retry-After 提示耦合在一起。
// auth 与 media 的 strict class 复用同一个层原语。该提示由配置的 rate 推导
// (并非写死),所以一个调过的 override 会给出准确的重试延迟。
type authStrictTier struct {
	registry   *ipBucketRegistry
	retryAfter int
	name       string
	limit      inboundlimit.Limit
}

// authClass 描述一个 strict 路径 class:一套桶策略,被一条或多条请求路径共享。
// 共用同一个 env 旋钮的路径(例如两条 OAuth 路由)属于同一个 class,
// 因此它们共享同一个 registry——否则在这些路径之间交替会让该 class
// 的有效预算成倍放大。
type authClass struct {
	name      string
	paths     []string // 共享本 class 桶的请求路径
	envPerMin string   // 持有「每分钟请求数」override 的 env 变量
	defPerMin float64  // 默认每分钟请求数
}

// authClasses 是静态策略表。login + 2fa 共用当前存在的那唯一一个 login
// 端点;verify-email 是「发送验证码」class;reset-password 是「忘记密码」
// class;社交登录路由共用一个 class(一份预算);sessions/refresh 是
// 「刷新令牌」class。
var authClasses = []authClass{
	{name: "auth_login", paths: []string{"/v1/auth/login", "/v1/auth/login/2fa", "/v1/auth/passkey/login/begin", "/v1/auth/passkey/login/finish"}, envPerMin: "HUAKAI_RL_AUTH_LOGIN_PER_MIN", defPerMin: defaultAuthLoginPerMin},
	{name: "auth_register", paths: []string{"/v1/auth/register", "/v1/auth/validate-invitation-code"}, envPerMin: "HUAKAI_RL_AUTH_REGISTER_PER_MIN", defPerMin: defaultAuthRegisterPerMin},
	{name: "auth_verify", paths: []string{"/v1/auth/verify-email", "/v1/auth/resend-verification"}, envPerMin: "HUAKAI_RL_AUTH_VERIFY_PER_MIN", defPerMin: defaultAuthVerifyPerMin},
	{name: "auth_reset", paths: []string{"/v1/auth/reset-password"}, envPerMin: "HUAKAI_RL_AUTH_RESET_PER_MIN", defPerMin: defaultAuthResetPerMin},
	{name: "auth_oauth", paths: []string{"/v1/auth/oauth-init", "/v1/auth/oauth-callback", "/v1/auth/telegram-login", "/v1/auth/oauth-pending/send-code", "/v1/auth/oauth-pending/complete"}, envPerMin: "HUAKAI_RL_AUTH_OAUTH_PER_MIN", defPerMin: defaultAuthOAuthPerMin},
	{name: "auth_refresh", paths: []string{"/v1/sessions/refresh"}, envPerMin: "HUAKAI_RL_AUTH_REFRESH_PER_MIN", defPerMin: defaultRefreshPerMin},
}

var mediaClasses = []authClass{
	{name: "media", paths: []string{
		"/v1/audio/speech",
		"/v1/audio/transcriptions",
		"/v1/audio/translations",
		"/v1/images/generations",
		"/v1/images/edits",
	}, envPerMin: "HUAKAI_RL_MEDIA_PER_MIN", defPerMin: defaultMediaPerMin},
}

// retryAfterForRatePerSec 把「每秒令牌补充速率」换算成一个整秒的
// Retry-After 提示:大致是到下一个令牌补充为止的时间(1/ratePerSec),
// 向上取整并以 1s 为下限。它跟随 env override,因此一个调低的 rate
// (例如 0.1 req/s)会报告一个真实的 ~10s 延迟,而非陈旧的 1s。
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

// retryAfterForRate 把「每分钟请求数」换算成一个整秒的 Retry-After 提示。
// 一个调低的 rate(例如 1/min)会报告一个真实的 ~60s 延迟。
func retryAfterForRate(perMin float64) int {
	return retryAfterForRatePerSec(perMin / 60.0)
}

// newRateLimiter 从 env 构建限流器(回退到既定默认值)。除非设置了
// HUAKAI_RL_DISABLE,返回的限流器始终开启。resolver 是网关用于桶键的
// 可信代理 IP 解析器(nil 也安全:此时键回退到 socket 对端)。logger 用于
// 在每次拒绝时发出运维可见的证据(nil-safe:此时使用一个 no-op logger)。
type rateLimiterOption func(*rateLimiter)

func withSharedRateLimitStore(store inboundlimit.Store) rateLimiterOption {
	return func(rl *rateLimiter) { rl.shared = store }
}

func newRateLimiter(resolver *clientip.Resolver, logger *zap.Logger, opts ...rateLimiterOption) *rateLimiter {
	if logger == nil {
		logger = zap.NewNop()
	}
	gRate := envFloat("HUAKAI_RL_GLOBAL_RATE", defaultGlobalRate)
	// 低于一个令牌的全局 burst 会拒绝每一个请求(限流器对每个请求恰好
	// 请求一个令牌),所以过小的 override 会回退到安全默认值,而不是把
	// 前门彻底锁死。
	gBurst := envBurst("HUAKAI_RL_GLOBAL_BURST", defaultGlobalBurst)

	authStrict := make(map[string]*authStrictTier, len(authClasses))
	for _, c := range authClasses {
		perMin := envFloat(c.envPerMin, c.defPerMin)
		// rate = 每秒请求数;burst = 每分钟计数(钳到 >= 1)。
		burst := perMin
		if burst < 1 {
			burst = 1
		}
		// 每个 class 一个 registry,被该 class 中的每条路径共享。
		tier := &authStrictTier{
			registry:   newIPBucketRegistry(perMin/60.0, burst),
			retryAfter: retryAfterForRate(perMin),
			name:       c.name,
			limit:      inboundlimit.Limit{RatePerSecond: perMin / 60.0, Burst: burst},
		}
		for _, p := range c.paths {
			authStrict[p] = tier
		}
	}
	mediaStrict := make(map[string]*authStrictTier, len(mediaClasses))
	for _, c := range mediaClasses {
		perMin := envFloat(c.envPerMin, c.defPerMin)
		if perMin <= 0 {
			continue
		}
		burst := perMin
		if burst < 1 {
			burst = 1
		}
		tier := &authStrictTier{
			registry:   newIPBucketRegistry(perMin/60.0, burst),
			retryAfter: retryAfterForRate(perMin),
			name:       c.name,
			limit:      inboundlimit.Limit{RatePerSecond: perMin / 60.0, Burst: burst},
		}
		for _, p := range c.paths {
			mediaStrict[p] = tier
		}
	}

	rl := &rateLimiter{
		global:      newIPBucketRegistry(gRate, gBurst),
		authStrict:  authStrict,
		mediaStrict: mediaStrict,
		resolver:    resolver,
		logger:      logger,
		nowFn:       time.Now,
		// 全局层的 Retry-After 由配置的 rate 推导,因此一个调低的
		// HUAKAI_RL_GLOBAL_RATE(例如 0.1 req/s)会报告真实的延迟,而非
		// 陈旧的 1s——后者会让守规矩的客户端过早重试。
		retryAfterS: retryAfterForRatePerSec(gRate),
		globalLimit: inboundlimit.Limit{RatePerSecond: gRate, Burst: gBurst},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(rl)
		}
	}
	return rl
}

// middleware 返回执行全局层加各已配置 strict 路径层的 chi 中间件。
// 全局层对每个请求先检查;auth/media strict 层只对其已配置的 class 检查。
// 任意一层耗尽都会产出 429,请求绝不会到达被包装的 handler(不消耗
// provider-account / 配额)。
func (rl *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := rl.clientKey(r)
		now := rl.nowFn()

		if !rl.global.allow(key, now) {
			rl.denied(r, "global", key, rl.retryAfterS)
			rl.reject(w, rl.retryAfterS)
			return
		}
		if !sharedRateLimitProbeExempt(r) && !rl.allowShared(w, r, "global", key, rl.globalLimit, rl.retryAfterS) {
			return
		}

		if tier := rl.authStrictFor(r); tier != nil {
			if !tier.registry.allow(key, now) {
				rl.denied(r, "auth_strict", key, tier.retryAfter)
				rl.reject(w, tier.retryAfter)
				return
			}
			if !rl.allowShared(w, r, tier.name, key, tier.limit, tier.retryAfter) {
				return
			}
		}

		if tier := rl.mediaStrictFor(r); tier != nil {
			if !tier.registry.allow(key, now) {
				rl.denied(r, "media_strict", key, tier.retryAfter)
				rl.reject(w, tier.retryAfter)
				return
			}
			if !rl.allowShared(w, r, tier.name, key, tier.limit, tier.retryAfter) {
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// sharedRateLimitProbeExempt 仅让存活/就绪探针绕过 Redis 共享桶。
// 每实例内存桶仍在它之前执行，因此探针没有失去防洪保护；同时 Redis
// 故障不会把 /healthz 误变成进程死亡，也不会盖住 /readyz 的依赖矩阵。
func sharedRateLimitProbeExempt(r *http.Request) bool {
	if r == nil || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return false
	}
	switch r.URL.Path {
	case "/healthz", "/readyz":
		return true
	default:
		return false
	}
}

func (rl *rateLimiter) allowShared(w http.ResponseWriter, r *http.Request, tier, key string, limit inboundlimit.Limit, fallbackRetry int) bool {
	if rl.shared == nil {
		return true
	}
	decision, err := rl.shared.Allow(r.Context(), tier, key, limit)
	if err != nil {
		rl.logger.Error("shared inbound rate limit unavailable",
			zap.String("tier", tier),
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.Error(err),
		)
		rl.rejectUnavailable(w)
		return false
	}
	if decision.Allowed {
		return true
	}
	retryAfter := int(math.Ceil(decision.RetryAfter.Seconds()))
	if retryAfter <= 0 {
		retryAfter = fallbackRetry
	}
	rl.denied(r, tier+"_shared", key, retryAfter)
	rl.reject(w, retryAfter)
	return false
}

// denied 为一次限流拒绝发出运维可见的证据(AT-SEC-001):哪一层触发、
// 按 IP 的键,以及请求的 method/path,这样运维就能复盘该事件并把滥用
// 与误报区分开。按 IP 的键是解析出的客户端 IP——而非凭据——所以可安全记录。
func (rl *rateLimiter) denied(r *http.Request, tier, key string, retryAfterS int) {
	rl.logger.Warn("inbound rate limit triggered",
		zap.String("tier", tier),
		zap.String("client_ip", key),
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
		zap.Int("retry_after_seconds", retryAfterS),
	)
}

// authStrictFor 为该请求解析出 auth-strict 层(若有)。限流器运行在 chi
// 路由匹配之前,所以它按请求路径匹配(若已解析出路由模式则回退到该模式)。
// 当请求不属于 auth-strict class 时返回 nil。
//
// auth-strict 层只对 POST 记账——这是这些 auth/session 路由接受的唯一变更方法。
// 任何其它方法(CORS OPTIONS 预检,或一个本就会被路由 405 的廉价跨站
// GET/HEAD)都不得耗尽那个紧得多的 auth 桶;若给它记账,一个浏览器预检
// 或被诱导的跨站 GET 就能让某个 IP 真正的 login/register/refresh POST 吃到 429。
// 更宽的全局层仍然给这些其它方法的洪泛设界。
func (rl *rateLimiter) authStrictFor(r *http.Request) *authStrictTier {
	return rl.strictTierFor(r, rl.authStrict)
}

func (rl *rateLimiter) mediaStrictFor(r *http.Request) *authStrictTier {
	return rl.strictTierFor(r, rl.mediaStrict)
}

func (rl *rateLimiter) strictTierFor(r *http.Request, tiers map[string]*authStrictTier) *authStrictTier {
	if r.Method != http.MethodPost {
		return nil
	}
	if tier, ok := tiers[r.URL.Path]; ok {
		return tier
	}
	if rctx := chi.RouteContext(r.Context()); rctx != nil {
		if tier, ok := tiers[rctx.RoutePattern()]; ok {
			return tier
		}
	}
	return nil
}

// reject 以 HTTP 429 写出网关标准的 JSON 错误响应体。
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

func (rl *rateLimiter) rejectUnavailable(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", "1")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`{"error":{"code":"rate_limit_unavailable","message":"request admission is temporarily unavailable"}}`))
}

// clientKey 推导按 IP 的桶键。它使用网关的可信代理 resolver,因此键是
// 穿过仅可信转发跳数之后看到的真实客户端——客户端无法通过伪造
// X-Forwarded-For/X-Real-IP 来铸造新桶。在未配置任何可信代理时,
// resolver 返回 socket 对端(直连暴露下的安全默认)。端口已由 resolver
// 剥除;一个裸 RemoteAddr 回退覆盖 resolver 为 nil 的路径。
// clientid.Identity 刻意不使用:它是一个粗粒度的客户端类型枚举,会把所有
// 调用方塌缩进同一个桶,破坏按 IP 的隔离。
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

// envFloat 读取一个正的、有限的 float env override,当变量未设置/为空/非法、
// 非正、或非有限(NaN/Inf)时回退。一个 NaN/Inf 的 burst 永远不会耗尽桶
// (从而悄悄禁用始终开启的限流器),所以这类值被拒绝、改用安全默认值。
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

// envBurst 读取一个 burst override,但额外拒绝低于一个令牌的值:
// 限流器对每个请求恰好消耗一个令牌,所以位于 (0,1) 的 burst 会拒绝
// 每一个请求、把前门彻底锁死。这类值回退到安全默认值。
func envBurst(name string, fallback float64) float64 {
	v := envFloat(name, fallback)
	if v < 1 {
		return fallback
	}
	return v
}

// rateLimitDisabled 报告限流器是否经由 env 被关闭。
func rateLimitDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(envRateLimitDisable))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
