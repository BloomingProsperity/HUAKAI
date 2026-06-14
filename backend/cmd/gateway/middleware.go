package main

import (
	"context"
	"encoding/json"
	"errors"
	"expvar"
	"fmt"
	"net/http"
	"os"
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
	legacydlq "github.com/BloomingProsperity/HUAKAI/internal/dlq"
	mailinfra "github.com/BloomingProsperity/HUAKAI/internal/email"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp"
	obsoutbox "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/observability"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	"github.com/BloomingProsperity/HUAKAI/internal/reqdecompress"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
)

func newRouter(d *deps, logger *zap.Logger) chi.Router {
	router := chi.NewRouter()
	privacyRedactor := privacy.DefaultRedactor()
	privacyLogger := privacy.NewStdoutSystemLogger(privacyRedactor)
	// Security headers go FIRST so even early-exit responses (e.g. the
	// RequestIDLengthLimiter 400) carry the browser security-header contract.
	router.Use(securityHeaders)
	router.Use(middleware.RequestID)
	router.Use(gatewayhttp.RequestIDLengthLimiter(gatewayhttp.MaxRequestIDLength))
	router.Use(accesslog.Middleware(logger))
	// 入站请求体透明解码 Content-Encoding(Codex CLI 0.125+ 默认发 zstd,delta-mine #8)。
	// 解码后剥头+修正 ContentLength,下游各 handler 的 io.ReadAll 读到明文;带解压上限防炸弹。
	router.Use(reqdecompress.Middleware(reqdecompress.DefaultMaxDecodedBytes))
	// Explicit allowlist CORS, early (preflight answered before auth).
	// Allowlist via HUAKAI_CORS_ALLOWED_ORIGINS (comma-separated); empty = deny.
	// It runs before the limiter so a 429 to an allowlisted browser origin
	// still carries Access-Control-Allow-Origin/Vary (the frontend sees the JSON
	// 429 + Retry-After, not an opaque CORS failure), and an allowlisted preflight
	// is answered 204 before the limiter even runs. corsMiddleware reads only
	// Origin/Method, never RemoteAddr, so this is independent of RealIP ordering.
	router.Use(corsMiddleware(parseAllowedOrigins(os.Getenv("HUAKAI_CORS_ALLOWED_ORIGINS"))))
	// Always-on per-IP inbound rate limit. It runs BEFORE middleware.RealIP
	// on purpose: chi's RealIP overwrites RemoteAddr from client-supplied
	// True-Client-IP/X-Real-IP/X-Forwarded-For with NO trusted-proxy check, so a
	// limiter keyed off the post-RealIP RemoteAddr would let an attacker mint fresh
	// per-IP buckets by rotating those headers. Running before RealIP lets the
	// gateway's fail-closed trusted-proxy resolver derive the key from the genuine
	// socket peer (honoring forwarded hops only when the peer is an allowlisted
	// proxy). Floods (including the expensive argon2 auth path) are shed with
	// 429 before any route handler / quota / provider-account use. Additive: it does
	// not change the securityHeaders/corsMiddleware behavior, only slots in
	// after CORS. HUAKAI_RL_DISABLE turns it off.
	if !rateLimitDisabled() {
		router.Use(newRateLimiter(d.clientIPResolver, logger).middleware)
	}
	// /internal/* (Hermes runner bootstrap/keys/refresh, the read-only
	// tool-execute callback, the internal OpenAI egress) is the internal control
	// plane. Gate it to trusted source networks (loopback + RFC1918 private +
	// link-local by default; add CIDRs via HUAKAI_HERMES_INTERNAL_EXTRA_ALLOW_CIDRS)
	// so it cannot be reached from the public internet on this shared listener —
	// the app-layer internal_token is no longer the sole barrier (audit B2). MUST
	// run BEFORE RealIP (same X-Forwarded-For spoof reason as the rate limiter): it
	// judges the genuine socket peer, which a `X-Forwarded-For: 127.0.0.1` cannot
	// forge.
	router.Use(internalSourceGate(parseInternalAllowCIDRs(os.Getenv(internalAllowCIDRsEnv)), logger))
	router.Use(middleware.RealIP)
	router.Use(privacy.Recoverer(privacyLogger))
	router.Use(aiAwareTimeout(60 * time.Second))
	router.Use(privacy.Middleware(8 << 20))
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

// adminIdentityResolver resolves an admin credential to its identity (or error).
// adminGate depends on this interface — not the concrete *admin.AdminResolver — so
// the gate's RBAC is unit-testable with injected identities. *admin.AdminResolver
// satisfies it.
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
		h.ServeHTTP(w, r)
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

// parseAllowedOrigins parses a comma-separated CORS allowlist (HUAKAI_CORS_ALLOWED_ORIGINS).
// Empty => no cross-origin browser access is granted (default-deny).
func parseAllowedOrigins(raw string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, o := range strings.Split(raw, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out[o] = struct{}{}
		}
	}
	return out
}

// securityHeaders installs the browser security-header contract on every response
// The gateway is a JSON API behind browser-facing /v1/auth, /v1/sessions,
// /v1/api-keys, /v1/admin routes that previously shipped zero security headers.
// HSTS is only emitted when the edge is TLS (r.TLS or X-Forwarded-Proto=https) so
// it is never wrongly asserted over plaintext.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		// API responses are JSON, never a document context: lock the page down hard.
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// corsMiddleware enforces an explicit, allowlist-based CORS policy. It echoes ONLY
// an allowlisted Origin back (never "*"), so credentialed cross-origin requests are
// scoped to vetted front-ends — the wildcard-with-credentials anti-pattern is
// structurally impossible. A disallowed/absent Origin gets no CORS headers
// (browser blocks). Preflight (OPTIONS) is answered 204 here, before auth.
func corsMiddleware(allowed map[string]struct{}) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Vary: Origin on EVERY response this middleware touches — a shared
			// cache must never reuse a no-origin/disallowed response (without CORS
			// headers) for a later allowlisted browser request.
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
					// Disallowed origin preflight: deny with no CORS headers.
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
		Timeouts:         buildGatewayTimeoutConfig(),
		ScannerBufferCap: 1 << 20,
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

func buildSettlementServices(_ context.Context, pgPool *pgxpool.Pool, auditSigner *sign.Signer, auditLedger auditledger.Ledger, dlqStore *legacydlq.Store, dlqService *legacydlq.Service, replicaTarget string, eventBusCfg *runtimeconfig.EventBusConfig, auditRefPolicy *eventbus.AuditRefPolicy, logger *zap.Logger, referralRewardIssuer auditreceipt.ReferralRewardIssuer, referralRewardSettings auditreceipt.ReferralRewardSettings) (billing.Settler, *auditreceipt.PGXReceiptStorage, *auditreceipt.ReceiptFormatter, *auditreceipt.MismatchRefundQueue, *billing.PGXRateTableSource, *eventbus.Bus, error) {
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
	completionBus, err := buildCompletionEventBus(eventBusCfg, settler, dlqService, auditRefPolicy, logger)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("build completion eventbus: %w", err)
	}
	return settler, receiptStore, receiptFormatter, refundQueue, billing.NewPGXRateTableSource(pgPool), completionBus, nil
}

func buildCompletionEventBus(cfg *runtimeconfig.EventBusConfig, settler billing.Settler, dlqService *legacydlq.Service, auditRefPolicy *eventbus.AuditRefPolicy, logger *zap.Logger) (*eventbus.Bus, error) {
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
	handlers := []eventbus.Handler{
		observability.NewBillingPersisterHandler(settler, cfg.HandlerTimeout,
			observability.WithBillingPersisterReconciler(reconciler)),
		observability.NewAuditLoggerHandler(cfg.HandlerTimeout,
			observability.WithRequiredAuditRef(),
			observability.WithAuditRefPolicy(auditRefPolicy)),
		observability.NewReconciliationHandler(cfg.HandlerTimeout, reconciler),
		observability.NewAccountHealthProbeHandler(cfg.HandlerTimeout, nil),
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
