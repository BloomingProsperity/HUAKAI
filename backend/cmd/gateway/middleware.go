package main

import (
	"context"
	"encoding/json"
	"errors"
	"expvar"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/accesslog"
	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	auditreceipt "github.com/BloomingProsperity/HUAKAI/internal/audit"
	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/browsersession"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/clientid"
	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
	communityinvitation "github.com/BloomingProsperity/HUAKAI/internal/community/invitation"
	runtimeconfig "github.com/BloomingProsperity/HUAKAI/internal/config"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	legacydlq "github.com/BloomingProsperity/HUAKAI/internal/dlq"
	mailinfra "github.com/BloomingProsperity/HUAKAI/internal/email"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp"
	obsoutbox "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/observability"
	"github.com/BloomingProsperity/HUAKAI/internal/observability/accounthealthprobe"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	"github.com/BloomingProsperity/HUAKAI/internal/reqdecompress"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
)

func newRouter(d *deps, logger *zap.Logger) chi.Router {
	router := chi.NewRouter()
	privacyRedactor := privacy.DefaultRedactor()
	privacyLogger := privacy.NewStdoutSystemLogger(privacyRedactor)
	// 安全响应头放在最前,这样即便是提前退出的响应(例如
	// RequestIDLengthLimiter 返回的 400)也携带浏览器安全响应头契约。
	router.Use(securityHeaders(d.clientIPResolver))
	router.Use(middleware.RequestID)
	router.Use(gatewayhttp.RequestIDLengthLimiter(gatewayhttp.MaxRequestIDLength))
	router.Use(accesslog.Middleware(logger))
	// 显式白名单 CORS,尽早执行(预检在 auth 之前应答)。
	// 白名单经由 HUAKAI_CORS_ALLOWED_ORIGINS(逗号分隔);为空 = 拒绝。
	// 它运行在限流器之前,这样对白名单内浏览器源返回的 429
	// 仍携带 Access-Control-Allow-Origin/Vary(前端看到的是 JSON
	// 429 + Retry-After,而非不透明的 CORS 失败),并且白名单内的预检
	// 在限流器尚未运行前就以 204 应答。同源判定复用可信代理解析器：
	// TLS 在可信边缘终止时才采信 X-Forwarded-Proto，公网直连伪造不会放行。
	router.Use(corsMiddleware(
		parseAllowedOrigins(os.Getenv("HUAKAI_CORS_ALLOWED_ORIGINS")),
		d.clientIPResolver,
	))
	// 始终开启的按 IP 入站限流。它使用 fail-closed 的可信代理解析器：
	// 只有 socket 对端位于运维白名单时才采信 X-Forwarded-For，其余请求
	// 一律以真实 socket 对端为键，攻击者不能靠轮换转发头铸造新桶。
	// 洪泛(包括开销很大的 argon2 auth 路径)会在任何路由 handler / 配额 / 上游账号
	// 使用之前以 429 卸掉。增量式:它不改变
	// securityHeaders/corsMiddleware 的行为,只是嵌在
	// CORS 之后。HUAKAI_RL_DISABLE 可将其关闭。
	if !rateLimitDisabled() {
		opts := []rateLimiterOption{}
		if d.inboundRateLimit != nil {
			opts = append(opts, withSharedRateLimitStore(d.inboundRateLimit))
		}
		router.Use(newRateLimiter(d.clientIPResolver, logger, opts...).middleware)
	}
	// /internal/*(Hermes runner 引导/密钥/刷新、只读的
	// tool-execute 回调、内部 OpenAI 出口)是内部控制
	// 面。把它限制到可信来源网络(默认 loopback + RFC1918 私网 +
	// link-local;通过 HUAKAI_HERMES_INTERNAL_EXTRA_ALLOW_CIDRS 追加 CIDR),
	// 使其无法从公网经由这个共享监听器被访问——
	// 应用层的 internal_token 不再是唯一屏障(审计 B2)。必须
	// 它只判定真实 socket 对端，不读取任何转发头，因此
	// `X-Forwarded-For: 127.0.0.1` 无法伪造内部来源。
	router.Use(internalSourceGate(parseInternalAllowCIDRs(os.Getenv(internalAllowCIDRsEnv)), logger))
	router.Use(privacy.Recoverer(privacyLogger))
	router.Use(aiAwareTimeout(60 * time.Second))
	// relay 入站请求体上限(env 可配,MB 单位,默认 32MiB 抬到中转站量级):gatewayhttp 内 relay 请求体
	// 上限共用 HUAKAI_MAX_REQUEST_BODY_MB。在 router 开始 serve 之前一次性设定 gatewayhttp 包级上限
	// (set-once,之后只读)。
	maxRequestBody := bodyLimitBytesFromEnv("HUAKAI_MAX_REQUEST_BODY_MB", 32<<20)
	gatewayhttp.ConfigureBodyLimits(maxRequestBody)
	// 入站请求体透明解码 Content-Encoding(Codex CLI 0.125+ 默认发 zstd,delta-mine #8)。
	// 解码后剥头+修正 ContentLength,下游各 handler 的 io.ReadAll 读到明文;带解压上限防炸弹。
	// 【位置 codex #4】刻意放在 newRateLimiter + internalSourceGate + aiAwareTimeout 之后:
	// 未认证洪泛/越权请求先被这些门 429/403 挡掉,避免在限流前就为攻击者的压缩体分配
	// 解压缓冲(解码先于限流=未认证内存放大面)。非 relay 路径的慢解压还受 aiAwareTimeout
	// 总超时约束;relay 路径被该超时刻意豁免(长跑推理),其解压由 DefaultMaxDecodedBytes
	// (64MiB→413)兜底。仍在 privacy 缓冲 body 之前,故下游 handler 读到的是解码后明文。
	// 解压必须位于认证和限流之后，避免未认证请求放大内存消耗。
	router.Use(reqdecompress.Middleware(reqdecompress.DefaultMaxDecodedBytes))
	// privacy.Middleware 在 auth 之前对所有路由全量缓冲 body 解析元数据。若给非 relay 的未认证端点
	// (login/register 等)也用 relay 的大上限,会无谓抬高它们的 pre-auth 内存放大面。故按路径区分:
	// 只有 relay 数据面(isAIRelayPath)才放宽到 maxRequestBody,其余维持 privacy 既有的小上限。
	router.Use(privacy.MiddlewareFunc(func(r *http.Request) int {
		return privacyBodyLimitForRequest(r, int(maxRequestBody), nonRelayPrivacyBodyLimitBytes)
	}))
	// U6-B: 把 client identity 写入 request ctx，必须早于后续 auth/quota/billing。
	router.Use(clientid.Middleware(logger))

	// /debug/vars 使用 admin auth gate + platform-admin RBAC 包住，避免 metrics 裸露。
	// 用局部接口变量承接:typed-nil *AdminResolver(未配置 deps)会塌成 nil 接口,
	// 让 adminGate 的 resolver==nil 仍命中 → 保持 admin_gate_not_configured(503)语义。
	var adminResolver adminIdentityResolver
	if d.adminAuth != nil {
		adminResolver = d.adminAuth
	}
	router.Handle("/debug/vars", adminGate(adminResolver, expvar.Handler()))
	if d.metricsHandler != nil {
		// Prometheus metrics 也是进程级观测面；启用后必须和 /debug/vars
		// 共用 admin gate，避免 billing/dispatch 指标被公网裸读。
		router.Handle("/metrics", adminGate(adminResolver, d.metricsHandler))
	}
	mountRoutes(router, d, logger)
	return router
}

// adminIdentityResolver 把一个 admin 凭据解析为其身份(或错误)。
// adminGate 依赖这个接口——而非具体的 *admin.AdminResolver——这样
// gate 的 RBAC 可以用注入的身份做单元测试。*admin.AdminResolver
// 满足该接口。
type adminIdentityResolver interface {
	Resolve(ctx context.Context, req *http.Request) (admin.AdminIdentity, error)
}

// adminGate 把任意 http.Handler 包到 admin auth + platform-admin RBAC 后面。
//
// /debug/vars 暴露的是 PROCESS-GLOBAL metrics(clientid/cache/dispatcher 计数器,
// 无租户过滤),所以 tenant_operator 即使持合法凭据也【不得】命中——只有
// platform_admin 可以。原实现只验 auth、丢弃 Resolve 返回的身份(role),任何
// 已认证 operator 都能读到全局进程指标 → 多租户信息泄漏。
// resolver 为 nil 时 fail-closed 返 503,不允许 ops 暴露面裸奔。
func adminGate(resolver adminIdentityResolver, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if resolver == nil {
			writeAdminGateError(w, http.StatusServiceUnavailable,
				"admin_gate_not_configured", "admin auth resolver unset")
			return
		}
		id, err := resolver.Resolve(r.Context(), r)
		if err != nil {
			if errors.Is(err, admin.ErrAdminBackend) {
				writeAdminGateError(w, http.StatusServiceUnavailable,
					"admin_backend_error", "admin auth backend transient failure")
				return
			}
			writeAdminGateError(w, http.StatusUnauthorized,
				"admin_unauthorized", "missing or invalid admin credential")
			return
		}
		// RBAC: 全局 ops 面是 platform-only。tenant_operator 已认证但未授权。
		if id.Role != admin.RolePlatformAdmin {
			writeAdminGateError(w, http.StatusForbidden,
				"admin_forbidden_scope", "platform_admin role required for global ops surface")
			return
		}
		// 把已认证身份注入 context, 供下游 handler 做审计/归属(取代信任请求体的 actor/admin_id 等可伪造字段)。
		h.ServeHTTP(w, r.WithContext(admin.IdentityToContext(r.Context(), id)))
	})
}

func writeAdminGateError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// 与 gateway/admin 其它错误写入器一致:用 encoding/json 编码而非 fmt %q 手拼,避免控制字节产出
	// 非法 JSON。本入口当前只传静态字面量,统一改法是纵深防御 + 防回归。
	body, err := json.Marshal(map[string]map[string]string{
		"error": {"code": code, "message": message},
	})
	if err != nil {
		body = []byte(`{"error":{"code":"internal_error","message":"internal error"}}`)
	}
	_, _ = w.Write(body)
}

// parseAllowedOrigins 解析逗号分隔的 CORS 白名单(HUAKAI_CORS_ALLOWED_ORIGINS)。
// 为空 => 不授予任何跨源浏览器访问(默认拒绝)。
func parseAllowedOrigins(raw string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, o := range strings.Split(raw, ",") {
		if normalized, ok := normalizeBrowserOrigin(o); ok {
			out[normalized] = struct{}{}
		}
	}
	return out
}

// normalizeBrowserOrigin 只接受浏览器 Origin 的标准形态。生产来源必须是 HTTPS；
// HTTP 仅允许 loopback 开发地址，避免运维误把明文远端站点加入携带凭据的白名单。
func normalizeBrowserOrigin(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "null") {
		return "", false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.Host == "" ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawPath != "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false
	}

	scheme := strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Hostname())
	if host == "" || strings.IndexFunc(host, func(r rune) bool { return r > 127 }) >= 0 {
		return "", false
	}
	if scheme != "https" && !(scheme == "http" && isLoopbackBrowserHost(host)) {
		return "", false
	}

	port := parsed.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", false
		}
		if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
			port = ""
		}
	}
	return scheme + "://" + browserAuthority(host, port), true
}

func isLoopbackBrowserHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func browserAuthority(host, port string) string {
	if port != "" {
		return net.JoinHostPort(host, port)
	}
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}

func requestHostMatchesOrigin(
	r *http.Request,
	normalizedOrigin string,
	proxyResolver *clientip.Resolver,
) bool {
	if r == nil {
		return false
	}
	scheme := requestExternalScheme(r, proxyResolver)
	target, ok := normalizeBrowserOrigin(scheme + "://" + strings.TrimSpace(r.Host))
	if !ok {
		return false
	}
	return target == normalizedOrigin
}

func requestExternalScheme(r *http.Request, proxyResolver *clientip.Resolver) string {
	if r == nil {
		return ""
	}
	if r.TLS != nil {
		return "https"
	}
	if proxyResolver == nil || !proxyResolver.TrustedPeer(r) {
		return "http"
	}
	values := r.Header.Values("X-Forwarded-Proto")
	if len(values) != 1 || strings.Contains(values[0], ",") {
		return "http"
	}
	switch strings.ToLower(strings.TrimSpace(values[0])) {
	case "https":
		return "https"
	case "http":
		return "http"
	default:
		return "http"
	}
}

// securityHeaders 在每个响应上安装浏览器安全响应头契约。
// 该网关是一个 JSON API,背后是面向浏览器的 /v1/auth、/v1/sessions、
// /v1/api-keys、/v1/admin 路由,这些此前一个安全响应头都没有。
// HSTS 仅在直连 TLS，或可信代理明确报告外部 HTTPS 时发出；公网直连请求
// 不能通过伪造 X-Forwarded-Proto 让明文来源收到错误声明。
func securityHeaders(proxyResolver *clientip.Resolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("X-Permitted-Cross-Domain-Policies", "none")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Permissions-Policy", "camera=(), geolocation=(), payment=(), serial=(), usb=(), microphone=(self)")
			// 当前进程只提供 API；所有路径统一使用不允许文档资源自举的严格 CSP。
			h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
			if requestExternalScheme(r, proxyResolver) == "https" {
				h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

var corsAllowedMethods = map[string]struct{}{
	http.MethodGet: {}, http.MethodHead: {}, http.MethodPost: {},
	http.MethodPut: {}, http.MethodPatch: {}, http.MethodDelete: {}, http.MethodOptions: {},
}

var corsAllowedRequestHeaders = map[string]struct{}{
	"accept": {}, "anthropic-beta": {}, "anthropic-dangerous-direct-browser-access": {},
	"anthropic-version": {}, "authorization": {},
	"content-type": {}, "idempotency-key": {}, "last-event-id": {}, "openai-beta": {},
	"openai-organization": {}, "openai-project": {}, "prefer": {}, "x-api-key": {},
	"x-chat-session-id": {}, "x-client-name": {}, "x-client-request-id": {},
	"x-client-version": {}, "x-correlation-id": {}, "x-huakai-csrf": {},
	"x-huakai-session-mode": {}, "x-huakai-setup-token": {}, "x-request-id": {},
	"x-stainless-arch": {}, "x-stainless-custom-poll-interval": {},
	"x-stainless-helper": {}, "x-stainless-helper-method": {}, "x-stainless-lang": {},
	"x-stainless-os": {}, "x-stainless-package-version": {}, "x-stainless-poll-helper": {},
	"x-stainless-retry-count": {}, "x-stainless-runtime": {},
	"x-stainless-runtime-version": {}, "x-stainless-timeout": {},
}

const corsAllowHeaders = "Accept, Anthropic-Beta, Anthropic-Dangerous-Direct-Browser-Access, Anthropic-Version, Authorization, Content-Type, Idempotency-Key, Last-Event-ID, OpenAI-Beta, OpenAI-Organization, OpenAI-Project, Prefer, X-API-Key, X-Chat-Session-ID, X-Client-Name, X-Client-Request-ID, X-Client-Version, X-Correlation-ID, X-HUAKAI-CSRF, X-HUAKAI-Session-Mode, X-HUAKAI-Setup-Token, X-Request-ID, X-Stainless-Arch, X-Stainless-Custom-Poll-Interval, X-Stainless-Helper, X-Stainless-Helper-Method, X-Stainless-Lang, X-Stainless-OS, X-Stainless-Package-Version, X-Stainless-Poll-Helper, X-Stainless-Retry-Count, X-Stainless-Runtime, X-Stainless-Runtime-Version, X-Stainless-Timeout"

const corsExposeHeaders = "Retry-After, X-Request-ID, X-HUAKAI-Request-ID, X-HUAKAI-Model-Requested, X-HUAKAI-Model-Delivered, X-HUAKAI-Model-Fallback, X-HUAKAI-Fallback-Attempts, X-HUAKAI-Trust-Status, X-HUAKAI-Upstream-Provider, X-HUAKAI-Upstream-Model, X-HUAKAI-Trust-Signature, X-HUAKAI-Trust-Pubkey-Fingerprint, X-HUAKAI-Trust-Schema, X-HUAKAI-Idempotency-Hit, X-HUAKAI-Cache-L2, X-HUAKAI-Ledger-ID, X-HUAKAI-Ledger-DLQ-Ref, X-HUAKAI-Verify, X-HUAKAI-Sig-Fingerprint, X-HUAKAI-Stream-State, X-HUAKAI-Delivered-Tokens, X-HUAKAI-Key-Display, X-Snapshot-Cache, X-Truncated"

// corsMiddleware 强制执行显式来源、方法和请求头白名单。没有 Origin 的 CLI、SDK
// 与服务器调用保持原样；浏览器同源请求自动允许，真正跨源请求必须命中配置白名单。
// 不可信来源不只“不给浏览器读响应”，而是在业务 handler 前直接拒绝，避免带 Cookie
// 的副作用请求已经执行。预检也不再回显调用方自报的任意请求头。
func corsMiddleware(
	allowed map[string]struct{},
	proxyResolver *clientip.Resolver,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 在此中间件经手的【每个】响应上加 Vary: Origin——共享
			// 缓存绝不能把一个无源/被拒绝的响应(不含 CORS
			// 响应头)复用给后续白名单内浏览器请求。
			h := w.Header()
			h.Add("Vary", "Origin")
			origins := r.Header.Values("Origin")
			if len(origins) == 0 {
				next.ServeHTTP(w, r)
				return
			}
			if len(origins) != 1 || strings.Contains(origins[0], ",") {
				writeCORSForbidden(w)
				return
			}
			origin, valid := normalizeBrowserOrigin(origins[0])
			if !valid {
				writeCORSForbidden(w)
				return
			}
			_, explicitlyAllowed := allowed[origin]
			sameOrigin := requestHostMatchesOrigin(r, origin, proxyResolver)
			if !explicitlyAllowed && !sameOrigin {
				writeCORSForbidden(w)
				return
			}
			if _, ok := corsAllowedMethods[r.Method]; !ok {
				writeCORSForbidden(w)
				return
			}

			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Expose-Headers", corsExposeHeaders)
			// 正式浏览器会话依赖 SameSite=Strict 的主机 Cookie，只允许同源页面。
			// 外置页面应把 /v1 反向代理到自身同源；CORS 白名单不负责放宽会话 Cookie。
			if r.Method != http.MethodOptions && browsersession.IsBrowser(r) && !sameOrigin {
				writeBrowserSessionOriginForbidden(w)
				return
			}
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h.Add("Vary", "Access-Control-Request-Method")
				h.Add("Vary", "Access-Control-Request-Headers")
				requestedMethod := strings.ToUpper(strings.TrimSpace(r.Header.Get("Access-Control-Request-Method")))
				if _, ok := corsAllowedMethods[requestedMethod]; !ok {
					clearCORSGrantHeaders(h)
					writeCORSForbidden(w)
					return
				}
				if !corsRequestHeadersAllowed(r.Header.Values("Access-Control-Request-Headers")) {
					clearCORSGrantHeaders(h)
					writeCORSForbidden(w)
					return
				}
				h.Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Allow-Headers", corsAllowHeaders)
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func corsRequestHeadersAllowed(values []string) bool {
	total := 0
	for _, value := range values {
		total += len(value)
		if total > 4096 {
			return false
		}
		for _, name := range strings.Split(value, ",") {
			name = strings.ToLower(strings.TrimSpace(name))
			if name == "" {
				continue
			}
			if _, ok := corsAllowedRequestHeaders[name]; !ok {
				return false
			}
		}
	}
	return true
}

func clearCORSGrantHeaders(h http.Header) {
	h.Del("Access-Control-Allow-Origin")
	h.Del("Access-Control-Allow-Credentials")
	h.Del("Access-Control-Expose-Headers")
}

func writeCORSForbidden(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(`{"error":{"code":"cors_forbidden","message":"browser origin, method, or headers are not allowed"}}`))
}

func writeBrowserSessionOriginForbidden(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(`{"error":{"code":"browser_session_cross_origin_forbidden","message":"browser session mode requires a same-origin API"}}`))
}

// streamDurationEnv 读取 time.ParseDuration 格式(如 "120s"、"10m")的流超时配置;空或非法回退默认。
// 允许运维按需调整,避免把长跑推理/agentic 请求写死掐断。
func streamDurationEnv(name string, fallback time.Duration) time.Duration {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 0 {
			return d
		}
	}
	return fallback
}

const defaultGatewayTotalStreamTimeout = 600 * time.Second

// platformCooldownSource 在每个新冷却决策上读取平台设置。platformsettings.Service
// 自带 30 秒缓存且 Upsert 会立即刷新缓存，因此运营保存后只影响后续事件，不改写已持久化的截止时间。
type platformCooldownSource struct {
	settings gatewayPlatformSettings
}

func (s platformCooldownSource) CooldownForStatus(ctx context.Context, statusCode int) (time.Duration, error) {
	key := platformsettings.KeyCooldown429Seconds
	if statusCode == 529 {
		key = platformsettings.KeyCooldown529Seconds
	}
	if s.settings == nil {
		return 0, platformsettings.ErrStoreNotConfigured
	}
	stored, err := s.settings.Get(ctx, key)
	if err != nil {
		return 0, err
	}
	duration, err := time.ParseDuration(strings.TrimSpace(stored.Value) + "s")
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("解析平台冷却设置 %s: %w", key, platformsettings.ErrInvalidValue)
	}
	return duration, nil
}

// totalStreamTimeoutFromSettings 保留接线前的 env 契约：没有 DB 覆盖时仍以
// HUAKAI_STREAM_TOTAL_TIMEOUT 为准；只有运营显式保存了设置才由平台值接管。
func totalStreamTimeoutFromSettings(ctx context.Context, settings gatewayPlatformSettings, fallback time.Duration) time.Duration {
	if settings == nil {
		return fallback
	}
	stored, err := settings.Get(ctx, platformsettings.KeyStreamTimeoutSeconds)
	if err != nil || stored.Source != platformsettings.SourceDB {
		return fallback
	}
	duration, err := time.ParseDuration(strings.TrimSpace(stored.Value) + "s")
	if err != nil || duration < 0 {
		return fallback
	}
	return duration
}

// bodyLimitBytesFromEnv 读取以 MB 为单位的容量上限 env(如 "32"),返回字节数;空或非法/非正回退默认。
// 用 MB 单位对运维更友好(对齐成熟中转站习惯),内部转字节。
func bodyLimitBytesFromEnv(name string, fallbackBytes int64) int64 {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		if mb, err := strconv.ParseInt(v, 10, 64); err == nil && mb > 0 {
			return mb << 20
		}
	}
	return fallbackBytes
}

// nonRelayPrivacyBodyLimitBytes 是非 relay 路由(login/register/admin 等)在 auth 前 privacy 缓冲的上限。
// 取 8MiB——即抬高 relay 上限之前 privacy 一直用的值:relay 数据面放宽到大上限时,不把这放大波及到
// 未认证控制面端点,使其 pre-auth 内存放大面维持原状(避免无谓扩大滥用面)。
const nonRelayPrivacyBodyLimitBytes = 8 << 20

// privacyBodyLimitForRequest 按路径选 privacy 缓冲上限:relay 数据面用 relayMax(随 HUAKAI_MAX_REQUEST_BODY_MB),
// 其余路由用 nonRelayMax。把 relay 的大上限与未认证控制面端点的小上限解耦。
func privacyBodyLimitForRequest(r *http.Request, relayMax, nonRelayMax int) int {
	if r != nil && isAIRelayPath(r.URL.Path) {
		return relayMax
	}
	return nonRelayMax
}

func buildGatewayTimeoutConfig(ctx context.Context, settings gatewayPlatformSettings) gateway.TimeoutConfig {
	totalFallback := streamDurationEnv("HUAKAI_STREAM_TOTAL_TIMEOUT", defaultGatewayTotalStreamTimeout)
	return gateway.TimeoutConfig{
		FirstTokenTimeout:   streamDurationEnv("HUAKAI_STREAM_FIRST_TOKEN_TIMEOUT", 120*time.Second),
		InterEventTimeout:   streamDurationEnv("HUAKAI_STREAM_INTER_EVENT_TIMEOUT", 60*time.Second),
		TotalStreamTimeout:  totalStreamTimeoutFromSettings(ctx, settings, totalFallback),
		DrainMax:            streamDurationEnv("HUAKAI_STREAM_DRAIN_MAX", 15*time.Second),
		KeepAliveInterval:   streamDurationEnv("HUAKAI_STREAM_KEEPALIVE_INTERVAL", 15*time.Second),
		HeaderToFirstByte:   streamDurationEnv("HUAKAI_UPSTREAM_HEADER_TIMEOUT", 15*time.Second),
		RequestTotalTimeout: streamDurationEnv("HUAKAI_UPSTREAM_REQUEST_TIMEOUT", 120*time.Second),
	}
}

func buildStreamForwarder(auditLedger auditledger.Ledger, auditSigner *sign.Signer, auditLedgerDLQ auditledger.DLQEnqueuer, timeouts gateway.TimeoutConfig) *gateway.StreamForwarder {
	return &gateway.StreamForwarder{
		ProtocolAdapters: gateway.BuildDefaultProtocolAdapterRegistry(),
		Scanners:         gateway.BuildDefaultStreamScannerRegistry(),
		// 流超时支持平台显式值覆盖原 env，并保留 600 秒现实默认，适配长跑推理请求：
		// 旧硬编码 First=5s/Inter=10s/Total=60s 会在上游还在思考时就中止。KeepAlive 避开反代空闲超时。
		Timeouts: timeouts,
		// 上游 SSE 单事件扫描缓冲上限(env 可配,默认 16MiB;normalizeScannerCap 钳到 ≤64MiB 防内存爆)。
		// 旧版硬写 1MiB 会把大单事件(大 tool-call / Gemini 大块)溢出砍流。
		ScannerBufferCap: int(bodyLimitBytesFromEnv("HUAKAI_MAX_SSE_EVENT_MB", 16<<20)),
		AuditLedger:      auditLedger,
		AuditLedgerDLQ:   auditLedgerDLQ,
		Signer:           auditSigner,
	}
}

func buildOutboxWorker(outboxStore obsoutbox.Outbox, outboxRuntime obsoutbox.RuntimeConfig, emailSettingsStore *mailinfra.PostgresSettingsStore, credentialKeys credentialstore.KeyProvider, channelHealthStore *channelhealth.PostgresStore) *obsoutbox.Worker {
	outboxWorker := obsoutbox.NewWorker(outboxStore, obsoutbox.WorkerConfig{
		IdleSleep:    time.Second,
		DrainTimeout: outboxRuntime.DrainTimeout,
		RetryPolicy: obsoutbox.RetryPolicy{
			MaxAttempts: outboxRuntime.MaxAttempts,
			MaxBackoff:  outboxRuntime.MaxBackoff,
		},
	})
	outboxWorker.Register(obsoutbox.EventTypeEmailRetry, mailinfra.NewDLQHandler(emailSettingsStore, credentialKeys, nil))
	outboxWorker.Register(obsoutbox.EventTypeChannelAlert, channelhealth.NewAlertDLQHandler(channelHealthStore))
	return outboxWorker
}

// buildSettlementServices 构造 base→receipt 结算 settler 与配套件。completion 事件总线【不在此处
// 构造】——它必须持 quota/budget/notify 全装饰完成后的最终 settler,由调用方在装饰链装配后再建
// (见 wiring.go buildCompletionEventBus 调用点),否则异步成功结算会绕过外层装饰器、漏释放配额/预算。
func buildSettlementServices(_ context.Context, pgPool *pgxpool.Pool, auditSigner *sign.Signer, auditLedger auditledger.Ledger, dlqStore *legacydlq.Store, dlqService *legacydlq.Service, replicaTarget string, logger *zap.Logger, referralRewardIssuer auditreceipt.ReferralRewardIssuer, referralRewardSettings auditreceipt.ReferralRewardSettings, quotaReverser auditreceipt.QuotaReverser) (billing.Settler, *auditreceipt.PGXReceiptStorage, *auditreceipt.ReceiptFormatter, *auditreceipt.MismatchRefundQueue, *billing.PGXRateTableSource, error) {
	receiptStore, err := auditreceipt.NewPGXReceiptStorage(pgPool)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("build receipt storage: %w", err)
	}
	receiptSource, err := auditreceipt.NewPGXReceiptSource(pgPool)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("build receipt source: %w", err)
	}
	baseSettler := billing.NewSettler(pgPool, billing.WithDLQStore(dlqStore), billing.WithReplicaTarget(replicaTarget))
	receiptFormatter, err := auditreceipt.NewReceiptFormatter(auditLedger, receiptSource, auditSigner)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("build receipt formatter: %w", err)
	}
	refundPendingStore, err := auditreceipt.NewPGXRefundPendingStore(pgPool)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("build audit refund pending store: %w", err)
	}
	refundWorkerOpts := []auditreceipt.RefundWorkerOption{
		auditreceipt.WithRefundLedger(auditLedger),
		auditreceipt.WithRefundReceiptSink(receiptStore),
	}
	if _, ok := auditLedger.(*auditledger.PostgresLedger); ok {
		refundWorkerOpts = append(refundWorkerOpts, auditreceipt.WithRefundTxPool(pgPool))
	}
	if quotaReverser != nil {
		refundWorkerOpts = append(refundWorkerOpts, auditreceipt.WithRefundQuotaReverser(quotaReverser))
	}
	refundWorker := auditreceipt.NewMismatchRefundWorker(refundPendingStore, baseSettler, receiptFormatter, refundWorkerOpts...)
	dlqService.Register(legacydlq.EventKindAuditMismatchRefund, refundWorker.Handler())
	refundQueue := auditreceipt.NewMismatchRefundQueue(dlqService,
		auditreceipt.WithRefundEligibilityVerifier(baseSettler))
	settlementAuditError := func(_ context.Context, requestID string, err error) {
		logger.Warn("settlement audit warning",
			zap.String("request_id", requestID),
			zap.Error(err),
		)
	}
	receiptHook := auditreceipt.NewReceiptHookHandler(receiptFormatter, receiptStore,
		auditreceipt.WithReceiptHookTrustSigner(auditSigner),
		auditreceipt.WithReceiptHookRecoveryEnqueuer(dlqService),
		auditreceipt.WithReceiptHookErrorHandler(settlementAuditError))
	dlqService.Register(legacydlq.EventKindCostReceiptAppend, receiptHook.HandleReceiptRecovery)
	referralQualifier := communityinvitation.NewService(communityinvitation.NewPostgresStore(pgPool))
	ledgerSettler := auditreceipt.NewSettlementLedgerSettler(baseSettler, auditLedger, dlqService, settlementAuditError)
	settler := auditreceipt.NewReceiptHookSettler(ledgerSettler, receiptHook,
		auditreceipt.WithReceiptHookReferralQualifier(referralQualifier),
		auditreceipt.WithReceiptHookReferralRewardIssuer(referralRewardIssuer),
		auditreceipt.WithReceiptHookReferralRewardSettings(referralRewardSettings))
	return settler, receiptStore, receiptFormatter, refundQueue, billing.NewPGXRateTableSource(pgPool), nil
}

func buildCompletionEventBus(cfg *runtimeconfig.EventBusConfig, settler billing.Settler, pgPool *pgxpool.Pool, dlqService *legacydlq.Service, auditRefPolicy *eventbus.AuditRefPolicy, logger *zap.Logger) (*eventbus.Bus, error) {
	logAuditRefEscapeFlag(auditRefPolicy, logger)
	if cfg == nil || !cfg.Enabled {
		return nil, nil
	}
	bus := eventbus.New(buildCompletionEventBusConfig(cfg, auditRefPolicy), eventbus.WithDLQ(dlqService), eventbus.WithDropHook(func(notice eventbus.DropNotice) {
		if logger != nil {
			logger.Warn("async processor dropped oldest queued event",
				zap.String("handler_id", string(notice.HandlerID)),
				zap.String("tier", string(notice.Tier)),
				zap.String("event_id", notice.EventID),
				zap.String("reason", notice.Reason),
			)
		}
	}))
	// 接通请求完成观测:内部 handler 保留历史 probe 命名,但这里只记录被动请求完成时间,
	// 不执行主动上游探测。写入独立 last_request_observed_at 存储列。该写入纯可观测、
	// 异步、单行 PK update,
	// 不在请求转发热路径上。pgPool 为 nil 时退回空转,保持旧行为不致启动失败。
	var healthProbe func(context.Context, observability.AccountHealthSignal) error
	if pgPool != nil {
		healthProbe = accounthealthprobe.NewPostgresProbe(admindb.New(pgPool))
	}
	handlers := []eventbus.Handler{
		observability.NewBillingPersisterHandler(settler, cfg.HandlerTimeout),
		observability.NewAuditLoggerHandler(cfg.HandlerTimeout,
			observability.WithRequiredAuditRef(),
			observability.WithAuditRefPolicy(auditRefPolicy)),
		observability.NewAccountHealthProbeHandler(cfg.HandlerTimeout, healthProbe),
	}
	for _, h := range handlers {
		if err := bus.Register(h); err != nil {
			return nil, err
		}
	}
	return bus, nil
}

func buildCompletionEventBusConfig(cfg *runtimeconfig.EventBusConfig, auditRefPolicy *eventbus.AuditRefPolicy) eventbus.Config {
	if cfg == nil {
		return eventbus.Config{AuditRefPolicy: auditRefPolicy}
	}
	return eventbus.Config{
		Enabled:              cfg.Enabled,
		HighWorkers:          cfg.HighWorkers,
		MediumWorkers:        cfg.MediumWorkers,
		LowWorkers:           cfg.LowWorkers,
		HighBuffer:           cfg.HighBuffer,
		MediumBuffer:         cfg.MediumBuffer,
		LowBuffer:            cfg.LowBuffer,
		HandlerTimeout:       cfg.HandlerTimeout,
		ShutdownDrainTimeout: cfg.ShutdownDrainTimeout,
		AuditRefPolicy:       auditRefPolicy,
		MaxStates:            cfg.MaxStates,
	}
}

func logAuditRefEscapeFlag(policy *eventbus.AuditRefPolicy, logger *zap.Logger) {
	if logger == nil || policy == nil || !policy.AllowMissingMoneyRef {
		return
	}
	logger.Warn(runtimeconfig.EnvTrustLedgerAllowMissingMoneyRef+" escape flag active",
		zap.String("env_var", runtimeconfig.EnvTrustLedgerAllowMissingMoneyRef),
		zap.String("release_mode", string(policy.ReleaseMode)),
		zap.Bool("allow_missing_money_ref", true),
	)
}

// aiAwareTimeout 套连接级总超时,但豁免 AI 数据面 relay 路径。chi middleware.Timeout 会给
// r.Context() 加一个总 deadline;对长流(SSE 推理/agent)与长非流推理,这会在 forwarder 自己
// 的 first-token/inter-event/total 预算之前把合法长响应砍断(并误判为 TotalStreamTimeout)。
// relay 路径只依赖 first-byte、inter-token 空闲超时和客户端断连，不套连接级总 deadline(仍保留
// r.Context() 的客户端断连取消),控制面/admin 路径保留 60s(无 AI relay,长挂死应被砍)。
func aiAwareTimeout(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isAIRelayPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// isAIRelayPath 标识上游 AI relay 数据面端点(流式/长跑),豁免连接级总超时。
func isAIRelayPath(p string) bool {
	switch p {
	case "/v1/chat/completions",
		"/v1/completions",
		"/v1/responses",
		"/v1/messages",
		"/v1/messages/count_tokens",
		"/v1/embeddings",
		"/v1/rerank",
		"/v1/images/generations",
		"/v1/images/edits",
		"/v1/images/variations",
		"/v1/audio/speech",
		"/v1/audio/transcriptions",
		"/v1/audio/translations":
		return true
	default:
		return false
	}
}
