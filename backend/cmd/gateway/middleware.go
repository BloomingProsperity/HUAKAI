package main

import (
	"context"
	"encoding/json"
	"errors"
	"expvar"
	"fmt"
	"net/http"
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
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/clientid"
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
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	"github.com/BloomingProsperity/HUAKAI/internal/reqdecompress"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/BloomingProsperity/HUAKAI/internal/webui"
)

func newRouter(d *deps, logger *zap.Logger) chi.Router {
	router := chi.NewRouter()
	privacyRedactor := privacy.DefaultRedactor()
	privacyLogger := privacy.NewStdoutSystemLogger(privacyRedactor)
	// 安全响应头放在最前,这样即便是提前退出的响应(例如
	// RequestIDLengthLimiter 返回的 400)也携带浏览器安全响应头契约。
	router.Use(securityHeaders)
	router.Use(middleware.RequestID)
	router.Use(gatewayhttp.RequestIDLengthLimiter(gatewayhttp.MaxRequestIDLength))
	router.Use(accesslog.Middleware(logger))
	// 显式白名单 CORS,尽早执行(预检在 auth 之前应答)。
	// 白名单经由 HUAKAI_CORS_ALLOWED_ORIGINS(逗号分隔);为空 = 拒绝。
	// 它运行在限流器之前,这样对白名单内浏览器源返回的 429
	// 仍携带 Access-Control-Allow-Origin/Vary(前端看到的是 JSON
	// 429 + Retry-After,而非不透明的 CORS 失败),并且白名单内的预检
	// 在限流器尚未运行前就以 204 应答。corsMiddleware 只读
	// Origin/Method,从不读 RemoteAddr,故与 RealIP 的顺序无关。
	router.Use(corsMiddleware(parseAllowedOrigins(os.Getenv("HUAKAI_CORS_ALLOWED_ORIGINS"))))
	// 始终开启的按 IP 入站限流。它刻意运行在 middleware.RealIP 之前:
	// chi 的 RealIP 会用客户端提供的
	// True-Client-IP/X-Real-IP/X-Forwarded-For 覆盖 RemoteAddr,且不做可信代理校验,
	// 所以一个以 RealIP 处理之后的 RemoteAddr 为键的限流器,会让攻击者通过轮换这些请求头
	// 凭空铸造出新的按 IP 桶。运行在 RealIP 之前可让
	// 网关的 fail-closed 可信代理解析器从真实的
	// socket 对端推导出键(仅在对端是白名单内代理时才采信转发跳数)。
	// 洪泛(包括开销很大的 argon2 auth 路径)会在任何路由 handler / 配额 / 上游账号
	// 使用之前以 429 卸掉。增量式:它不改变
	// securityHeaders/corsMiddleware 的行为,只是嵌在
	// CORS 之后。HUAKAI_RL_DISABLE 可将其关闭。
	if !rateLimitDisabled() {
		router.Use(newRateLimiter(d.clientIPResolver, logger).middleware)
	}
	// /internal/*(Hermes runner 引导/密钥/刷新、只读的
	// tool-execute 回调、内部 OpenAI 出口)是内部控制
	// 面。把它限制到可信来源网络(默认 loopback + RFC1918 私网 +
	// link-local;通过 HUAKAI_HERMES_INTERNAL_EXTRA_ALLOW_CIDRS 追加 CIDR),
	// 使其无法从公网经由这个共享监听器被访问——
	// 应用层的 internal_token 不再是唯一屏障(审计 B2)。必须
	// 运行在 RealIP 之前(与限流器同样的 X-Forwarded-For 伪造原因):它
	// 判定真实的 socket 对端,这是 `X-Forwarded-For: 127.0.0.1` 无法
	// 伪造的。
	router.Use(internalSourceGate(parseInternalAllowCIDRs(os.Getenv(internalAllowCIDRsEnv)), logger))
	router.Use(middleware.RealIP)
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
	// 对齐 sub2api:其请求体解码也在各 handler 内、限流之后(非全局 pre-限流)。
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
	// 对任何未命中 API 路由的路径,提供内嵌的单页前端。
	// 仅在以 `-tags embed` 编译的构建(包含真实 dist)中启用;
	// 默认构建在此返回 nil handler,保留 chi 的原始 404。
	if spa := webui.Handler(webui.Dist()); spa != nil {
		router.NotFound(spa.ServeHTTP)
	}
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
		if o = strings.TrimSpace(o); o != "" {
			out[o] = struct{}{}
		}
	}
	return out
}

// securityHeaders 在每个响应上安装浏览器安全响应头契约。
// 该网关是一个 JSON API,背后是面向浏览器的 /v1/auth、/v1/sessions、
// /v1/api-keys、/v1/admin 路由,这些此前一个安全响应头都没有。
// HSTS 仅在边缘为 TLS 时(r.TLS 或 X-Forwarded-Proto=https)才发出,
// 因此绝不会在明文上被错误声明。
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		// API 响应是 JSON,绝非文档上下文:把页面彻底锁死。
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// corsMiddleware 强制执行一套显式的、基于白名单的 CORS 策略。它【只】回显
// 白名单内的 Origin(绝不回 "*"),因此携带凭据的跨源请求被
// 限定到经过审核的前端——「通配符 + 携带凭据」这一反模式在
// 结构上不可能出现。被拒绝/缺失的 Origin 拿不到任何 CORS 响应头
// (浏览器拦截)。预检(OPTIONS)在此处以 204 应答,先于 auth。
func corsMiddleware(allowed map[string]struct{}) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 在此中间件经手的【每个】响应上加 Vary: Origin——共享
			// 缓存绝不能把一个无源/被拒绝的响应(不含 CORS
			// 响应头)复用给后续白名单内浏览器请求。
			w.Header().Add("Vary", "Origin")
			origin := r.Header.Get("Origin")
			if origin != "" {
				if _, ok := allowed[origin]; ok {
					h := w.Header()
					h.Set("Access-Control-Allow-Origin", origin)
					h.Set("Access-Control-Allow-Credentials", "true")
					if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
						h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
						reqHeaders := r.Header.Get("Access-Control-Request-Headers")
						if reqHeaders == "" {
							reqHeaders = "Authorization, Content-Type"
						}
						h.Set("Access-Control-Allow-Headers", reqHeaders)
						h.Set("Access-Control-Max-Age", "600")
						w.WriteHeader(http.StatusNoContent)
						return
					}
				} else if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
					// 被拒绝来源的预检:不带任何 CORS 响应头直接拒绝。
					w.WriteHeader(http.StatusForbidden)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
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

func buildGatewayTimeoutConfig() gateway.TimeoutConfig {
	return gateway.TimeoutConfig{
		FirstTokenTimeout:   streamDurationEnv("HUAKAI_STREAM_FIRST_TOKEN_TIMEOUT", 120*time.Second),
		InterEventTimeout:   streamDurationEnv("HUAKAI_STREAM_INTER_EVENT_TIMEOUT", 60*time.Second),
		TotalStreamTimeout:  streamDurationEnv("HUAKAI_STREAM_TOTAL_TIMEOUT", 600*time.Second),
		DrainMaxSeconds:     streamDurationEnv("HUAKAI_STREAM_DRAIN_MAX", 15*time.Second),
		KeepAliveInterval:   streamDurationEnv("HUAKAI_STREAM_KEEPALIVE_INTERVAL", 15*time.Second),
		HeaderToFirstByte:   streamDurationEnv("HUAKAI_UPSTREAM_HEADER_TIMEOUT", 15*time.Second),
		RequestTotalTimeout: streamDurationEnv("HUAKAI_UPSTREAM_REQUEST_TIMEOUT", 120*time.Second),
	}
}

func buildStreamForwarder(auditLedger auditledger.Ledger, auditSigner *sign.Signer, auditLedgerDLQ auditledger.DLQEnqueuer) *gateway.StreamForwarder {
	return &gateway.StreamForwarder{
		ProtocolAdapters: gateway.BuildDefaultProtocolAdapterRegistry(),
		Scanners:         gateway.BuildDefaultStreamScannerRegistry(),
		// 流超时改为 env 可配 + 调大默认,适配长跑推理请求:旧硬编码 First=5s/Inter=10s/
		// Total=60s 会在上游还在思考时就被 HUAKAI 自己掐断。配合 KeepAlive 心跳避开反代空闲超时。
		Timeouts: buildGatewayTimeoutConfig(),
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

func buildSettlementServices(_ context.Context, pgPool *pgxpool.Pool, auditSigner *sign.Signer, auditLedger auditledger.Ledger, dlqStore *legacydlq.Store, dlqService *legacydlq.Service, replicaTarget string, eventBusCfg *runtimeconfig.EventBusConfig, auditRefPolicy *eventbus.AuditRefPolicy, logger *zap.Logger, referralRewardIssuer auditreceipt.ReferralRewardIssuer, referralRewardSettings auditreceipt.ReferralRewardSettings, quotaReverser auditreceipt.QuotaReverser) (billing.Settler, *auditreceipt.PGXReceiptStorage, *auditreceipt.ReceiptFormatter, *auditreceipt.MismatchRefundQueue, *billing.PGXRateTableSource, *eventbus.Bus, error) {
	receiptStore, err := auditreceipt.NewPGXReceiptStorage(pgPool)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("build receipt storage: %w", err)
	}
	receiptSource, err := auditreceipt.NewPGXReceiptSource(pgPool)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("build receipt source: %w", err)
	}
	baseSettler := billing.NewSettler(pgPool, billing.WithDLQStore(dlqStore), billing.WithReplicaTarget(replicaTarget))
	receiptFormatter, err := auditreceipt.NewReceiptFormatter(auditLedger, baseSettler, receiptSource, auditSigner)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("build receipt formatter: %w", err)
	}
	refundPendingStore, err := auditreceipt.NewPGXRefundPendingStore(pgPool)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("build audit refund pending store: %w", err)
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
	refundQueue := auditreceipt.NewMismatchRefundQueue(dlqService)
	receiptHook := auditreceipt.NewReceiptHookHandler(receiptFormatter, receiptStore,
		auditreceipt.WithReceiptHookTrustSigner(auditSigner),
		auditreceipt.WithReceiptHookRecoveryEnqueuer(dlqService),
		auditreceipt.WithReceiptHookErrorHandler(func(_ context.Context, requestID string, err error) {
			logger.Warn("cost receipt hook warning after settle",
				zap.String("request_id", requestID),
				zap.Error(err),
			)
		}))
	dlqService.Register(legacydlq.EventKindCostReceiptAppend, receiptHook.HandleReceiptRecovery)
	referralQualifier := communityinvitation.NewService(communityinvitation.NewPostgresStore(pgPool))
	settler := auditreceipt.NewReceiptHookSettler(baseSettler, receiptHook,
		auditreceipt.WithReceiptHookReferralQualifier(referralQualifier),
		auditreceipt.WithReceiptHookReferralRewardIssuer(referralRewardIssuer),
		auditreceipt.WithReceiptHookReferralRewardSettings(referralRewardSettings))
	completionBus, err := buildCompletionEventBus(eventBusCfg, settler, pgPool, dlqService, auditRefPolicy, logger)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("build completion eventbus: %w", err)
	}
	return settler, receiptStore, receiptFormatter, refundQueue, billing.NewPGXRateTableSource(pgPool), completionBus, nil
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
	reconciler := observability.NewDualRunReconciler(observability.DefaultDualRunWindow)
	// 接线 account health probe 死开关:此前 probe 传 nil → handler 每次请求完成都被触发
	// 但空转,provider_accounts.last_probe_at 永远为 NULL、健康面板恒空。这里注入一个真实
	// 的 pgxpool 支撑写,盖 last_probe_at 戳点亮面板。纯可观测、异步、单行 PK update,
	// 不在请求转发热路径上。pgPool 为 nil 时(理论上 eventbus 已 Enabled 不应发生)退回
	// 空转,保持旧行为不致启动失败。
	var healthProbe func(context.Context, observability.AccountHealthSignal) error
	if pgPool != nil {
		healthProbe = accounthealthprobe.NewPostgresProbe(admindb.New(pgPool))
	}
	handlers := []eventbus.Handler{
		observability.NewBillingPersisterHandler(settler, cfg.HandlerTimeout,
			observability.WithBillingPersisterReconciler(reconciler)),
		observability.NewAuditLoggerHandler(cfg.HandlerTimeout,
			observability.WithRequiredAuditRef(),
			observability.WithAuditRefPolicy(auditRefPolicy)),
		observability.NewReconciliationHandler(cfg.HandlerTimeout, reconciler),
		observability.NewAccountHealthProbeHandler(cfg.HandlerTimeout, healthProbe),
		observability.NewMetricsAggregatorHandler(cfg.HandlerTimeout),
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
// new-api / sub2api / CLIProxyAPI 与 OpenAI 官方在数据面都不设这种连接级总超时——只靠
// first-byte + inter-token 空闲超时 + 客户端断连。故 relay 路径不套总 deadline(仍保留
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
		"/v1/responses",
		"/v1/messages",
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
