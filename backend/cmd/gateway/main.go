// Package main is the HUAKAI gateway entry point.
//
// Phase C wiring per docs/process/plans/2026-04-30-phase-c-gateway-wiring.md.
//
// Released specs governing this binary:
//   - docs/specs/pool-routing.md (F-POOL-001)
//   - docs/specs/observability-billing.md (F-OBS-001 + F-BILL-001 framing)
//   - docs/specs/streaming-forwarder.md (F-GW-002)
//   - docs/specs/rate-limiting.md (F-RATE-001)
//   - docs/specs/protocol-translation.md (F-PROTO-002)
//   - docs/specs/upstream-credential-management.md (F-AUTH-005)
//   - docs/specs/api-contract.md (Phase 2.2 contract lock)
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"expvar"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminhttp"
	auditreceipt "github.com/BloomingProsperity/HUAKAI/internal/audit"
	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	l2cache "github.com/BloomingProsperity/HUAKAI/internal/cache"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/clientid"
	communityinvitation "github.com/BloomingProsperity/HUAKAI/internal/community/invitation"
	"github.com/BloomingProsperity/HUAKAI/internal/config"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker/adapters"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	legacydlq "github.com/BloomingProsperity/HUAKAI/internal/dlq"
	mailinfra "github.com/BloomingProsperity/HUAKAI/internal/email"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp"
	obsoutbox "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/observability"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
	"github.com/BloomingProsperity/HUAKAI/internal/voucher"
)

// smokeBuildStamp is overridden via -ldflags during smoke test builds to
// produce a unique binary hash per run, dodging Smart App Control's
// per-hash block cache. Empty in normal/production builds.
var smokeBuildStamp string

func main() {
	_ = smokeBuildStamp // referenced only by the smoke build to defeat dead-code elimination
	logger, err := zap.NewProduction()
	if err != nil {
		fmt.Fprintf(os.Stderr, "logger init failed: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = logger.Sync() }()

	if err := run(logger); err != nil {
		logger.Fatal("gateway exited with error", zap.Error(err))
	}
}

// deps is the live dependency tree handlers receive after run() boots.
type deps struct {
	cfg                 *config.Config
	queries             *db.Queries
	selector            pool.Selector // M6: 接口化, 由 buildSelector 按 PoolSelectorConfig 装配 default 或 dispatcher
	channelHealth       *channelhealth.Service
	claimGate           billing.ClaimGate
	settler             billing.Settler
	forwarder           *gateway.StreamForwarder
	credentialVault     provider.CredentialVault
	credentialStore     *credentialstore.Store
	credentialKeys      credentialstore.KeyProvider
	credentialAcqStore  *credentialacq.PostgresSessionStore
	emailSettings       *mailinfra.PostgresSettingsStore
	authEmailSender     gatewayhttp.AuthEmailSender
	userAuth            *userauth.Service
	userSessions        *usersession.Service
	voucherService      *voucher.Service
	invitationService   *communityinvitation.Service
	dispatcher          *gateway.UpstreamDispatcher
	responseCache       l2cache.Store
	dlqService          *legacydlq.Service
	completionBus       *eventbus.Bus
	inboundAuth         *auth.APIKeyResolver
	auditLedger         auditledger.Ledger
	auditSigner         *sign.Signer
	auditPubkeyRegistry auditledger.PubkeyRegistry
	receiptStore        *auditreceipt.PGXReceiptStorage
	receiptFormatter    *auditreceipt.ReceiptFormatter
	refundQueue         *auditreceipt.MismatchRefundQueue
	rateTableSource     billing.RateTableSource
	// Slice 2 (N+5a): real Registry resolver.
	modelRegistry *registry.PostgresRegistry
	// Slice 2 (N+5b 2026-05-01): Router consumes Registry's PoolCandidates
	// and stamps the registry+router snapshot onto every plan.
	routePlanner *router.DefaultRouter
	// Slice 2 (N+4b2 2026-05-02): admin operator surface — auth resolver,
	// key issuer, key revoker. Wired into /admin/v1/api-keys.
	adminAuth    *admin.AdminResolver
	adminIssuer  *admin.KeyIssuer
	adminRevoker *admin.KeyRevoker
}

type refundReceiptAppender interface {
	AppendReceipt(context.Context, *auditreceipt.CostReceipt) error
}

type refundReceiptSequenceReader interface {
	GetReceiptBySequence(context.Context, string, int64, int32) (*auditreceipt.CostReceipt, error)
}

type refundReceiptSink struct {
	appender refundReceiptAppender
}

func (s refundReceiptSink) AppendRefundReceipt(ctx context.Context, receipt *auditreceipt.CostReceipt) error {
	if s.appender == nil {
		return auditreceipt.ErrReceiptStorageRequired
	}
	return s.appender.AppendReceipt(ctx, receipt)
}

func (s refundReceiptSink) GetReceiptBySequence(ctx context.Context, requestID string, tenantID int64, sequence int32) (*auditreceipt.CostReceipt, error) {
	reader, ok := s.appender.(refundReceiptSequenceReader)
	if !ok {
		return nil, auditreceipt.ErrReceiptStorageRequired
	}
	return reader.GetReceiptBySequence(ctx, requestID, tenantID, sequence)
}

func (d *deps) AdminObservabilityAuth() gatewayhttp.AdminObservabilityAuth {
	return d.adminAuth
}

func (d *deps) AdminObservabilityStore() gatewayhttp.AdminObservabilityStore {
	return d.queries
}

func (d *deps) AdminDLQAuth() gatewayhttp.AdminDLQAuth {
	return d.adminAuth
}

func (d *deps) AdminDLQStore() gatewayhttp.AdminDLQStore {
	return d.dlqService
}

func run(logger *zap.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger.Info("config loaded",
		zap.String("listen", cfg.Listen),
		zap.String("auth_mode", "api_keys_table"),
	)
	mimicryRegistry, err := loadMimicryTemplateRegistry(logger)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	pgPool, err := db.Open(ctx, db.PoolConfig{DSN: cfg.DatabaseURL})
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer pgPool.Close()

	q := db.New(pgPool)
	outboxStore := obsoutbox.NewPostgresOutbox(pgPool)
	outboxRuntime, err := obsoutbox.LoadRuntimeConfigFromEnv()
	if err != nil {
		return fmt.Errorf("load async outbox config: %w", err)
	}
	logger.Info("async outbox config loaded",
		zap.Int("max_attempts", outboxRuntime.MaxAttempts),
		zap.Duration("max_backoff", outboxRuntime.MaxBackoff),
		zap.Duration("drain_timeout", outboxRuntime.DrainTimeout),
	)
	credentialKeys, err := loadCredentialKeyProvider()
	if err != nil {
		return fmt.Errorf("load credential encryption key: %w", err)
	}
	credentialModeRegistry := credentialstore.DefaultHandlerRegistry()
	credentialStore := credentialstore.NewStore(pgPool, credentialKeys, credentialModeRegistry)
	credentialAcqStore := credentialacq.NewPostgresSessionStoreWithKeys(pgPool, credentialKeys)
	emailSettingsStore := mailinfra.NewPostgresSettingsStore(pgPool)
	if releaseModeProduction() {
		if err := mailinfra.ValidateProductionReleaseGate(ctx, emailSettingsStore, credentialKeys); err != nil {
			return fmt.Errorf("production email release gate: %w", err)
		}
	}
	authEmailSender, err := buildAuthEmailSender(cfg, emailSettingsStore, credentialKeys, logger, outboxStore)
	if err != nil {
		return fmt.Errorf("build auth email sender: %w", err)
	}
	userAuthStore := userauth.NewPostgresStoreWithKeys(pgPool, credentialKeys)
	userSessionStore := usersession.NewPostgresStore(pgPool)
	sessionSigningKey, err := loadSessionSigningKey()
	if err != nil {
		return fmt.Errorf("load session signing key: %w", err)
	}
	userAuthService := userauth.NewService(userAuthStore)
	userAuthService.OAuth = buildUserOAuthService(logger)
	userAuthService.VerificationTTL = mailinfra.DefaultVerificationTTL
	userAuthService.Verification = mailinfra.NewVerificationPolicy(emailSettingsStore)
	userSessionService := usersession.NewService(userSessionStore)
	userSessionService.SigningKey = sessionSigningKey

	// M6 main-wire: 按 PoolSelectorConfig 装配 selector。 default 模式等价现状
	// (零回归); shadow / canary / pasr-* 启动 PASR 基础设施 (SegmentTable +
	// AgingWorker + cache feedback observer + dispatcher)。
	selectorCfg, err := config.LoadPoolSelector()
	if err != nil {
		return fmt.Errorf("load pool selector config: %w", err)
	}
	logger.Info("pool selector config loaded",
		zap.String("mode", string(selectorCfg.Mode)),
		zap.Int("shadow_pct", selectorCfg.ShadowPercent),
		zap.Int("canary_pct", selectorCfg.CanaryPercent),
	)
	cacheCfg, err := config.LoadL2Cache()
	if err != nil {
		return fmt.Errorf("load L2 cache config: %w", err)
	}
	var responseCache l2cache.Store
	if cacheCfg.Enabled {
		responseCache = l2cache.NewMemoryStore(cacheCfg.SizeBytes, cacheCfg.TTL)
	}
	logger.Info("L2 response cache config loaded",
		zap.Bool("enabled", cacheCfg.Enabled),
		zap.Int64("size_bytes", cacheCfg.SizeBytes),
		zap.Duration("ttl", cacheCfg.TTL),
	)
	obsDLQCfg, err := config.LoadObsDLQ()
	if err != nil {
		return fmt.Errorf("load obs DLQ config: %w", err)
	}
	logger.Info("observability DLQ config loaded",
		zap.Bool("enabled", obsDLQCfg.Enabled),
		zap.Bool("replica_configured", obsDLQCfg.ReplicaDSN != ""),
		zap.Int("high_workers", obsDLQCfg.HighWorkers),
		zap.Int("medium_workers", obsDLQCfg.MediumWorkers),
		zap.Int("low_workers", obsDLQCfg.LowWorkers),
	)
	eventBusCfg, err := config.LoadEventBus()
	if err != nil {
		return fmt.Errorf("load eventbus config: %w", err)
	}
	logger.Info("async processor eventbus config loaded",
		zap.Bool("enabled", eventBusCfg.Enabled),
		zap.Int("high_workers", eventBusCfg.HighWorkers),
		zap.Int("medium_workers", eventBusCfg.MediumWorkers),
		zap.Int("low_workers", eventBusCfg.LowWorkers),
		zap.Int("high_buffer", eventBusCfg.HighBuffer),
		zap.Int("medium_buffer", eventBusCfg.MediumBuffer),
		zap.Int("low_buffer", eventBusCfg.LowBuffer),
		zap.Duration("handler_timeout", eventBusCfg.HandlerTimeout),
	)
	auditSigner, err := loadAuditSigner(logger)
	if err != nil {
		return fmt.Errorf("load audit ledger signer: %w", err)
	}
	auditPubkeyRegistry, err := auditledger.NewPGXPubkeyRegistry(pgPool)
	if err != nil {
		return fmt.Errorf("build audit pubkey registry: %w", err)
	}
	if err := auditledger.EnsureSignerPubkey(ctx, auditPubkeyRegistry, auditSigner, time.Now().UTC()); err != nil {
		return fmt.Errorf("register audit signer pubkey: %w", err)
	}
	auditLedger, err := buildAuditLedger(ctx, pgPool, auditSigner, logger)
	if err != nil {
		return fmt.Errorf("build audit ledger: %w", err)
	}
	receiptStore, err := auditreceipt.NewPGXReceiptStorage(pgPool)
	if err != nil {
		return fmt.Errorf("build receipt storage: %w", err)
	}
	receiptSource, err := auditreceipt.NewPGXReceiptSource(pgPool)
	if err != nil {
		return fmt.Errorf("build receipt source: %w", err)
	}
	rateTableSource := billing.NewPGXRateTableSource(pgPool)
	channelHealthStore := channelhealth.NewPostgresStoreWithAuditSigner(pgPool, auditSigner)
	channelHealthService := channelhealth.NewService(channelHealthStore, channelhealth.DefaultPolicy(), nil, channelhealth.WithAlertOutbox(outboxStore))
	voucherService := voucher.NewService(voucher.NewPostgresStore(pgPool))
	invitationService := communityinvitation.NewService(communityinvitation.NewPostgresStore(pgPool))
	selector, selectorCleanup, err := buildSelector(ctx, q, pgPool, selectorCfg, channelHealthService, logger)
	if err != nil {
		return fmt.Errorf("build selector: %w", err)
	}
	defer selectorCleanup()
	dlqStore := legacydlq.NewStore(pgPool)
	dlqService := legacydlq.NewService(dlqStore, legacydlq.WithPolicy(legacydlq.RetryPolicy{
		BaseBackoff: obsDLQCfg.BaseBackoff,
		CapBackoff:  obsDLQCfg.CapBackoff,
		MaxAttempts: obsDLQCfg.MaxAttempts,
		DLQAfter:    obsDLQCfg.DLQAfter,
	}))
	dlqService.Register(legacydlq.EventKindUsageRecord, legacydlq.NewUsageRecordHandler(pgPool))
	replicaTarget := ""
	var closeReplica func()
	if obsDLQCfg.ReplicaDSN != "" {
		replicaTarget = "postgres"
		replicaHandler, cleanup := legacydlq.NewLazyPostgresReplicaHandler(obsDLQCfg.ReplicaDSN)
		closeReplica = cleanup
		dlqService.Register(legacydlq.EventKindBillingEventReplica, replicaHandler)
		dlqService.Register(legacydlq.EventKindAuditEventReplica, replicaHandler)
	}
	if closeReplica != nil {
		defer closeReplica()
	}
	dlqWorker := legacydlq.NewWorker(dlqService, legacydlq.WorkerConfig{
		HighWorkers:   obsDLQCfg.HighWorkers,
		MediumWorkers: obsDLQCfg.MediumWorkers,
		LowWorkers:    obsDLQCfg.LowWorkers,
		LeaseTTL:      obsDLQCfg.LeaseTTL,
		IdleSleep:     time.Second,
	})
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

	baseSettler := billing.NewSettler(pgPool, billing.WithDLQStore(dlqStore), billing.WithReplicaTarget(replicaTarget))
	receiptFormatter, err := auditreceipt.NewReceiptFormatter(auditLedger, baseSettler, receiptSource, auditSigner)
	if err != nil {
		return fmt.Errorf("build receipt formatter: %w", err)
	}
	refundPendingStore, err := auditreceipt.NewPGXRefundPendingStore(pgPool)
	if err != nil {
		return fmt.Errorf("build audit refund pending store: %w", err)
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
		auditreceipt.WithReceiptHookErrorHandler(func(_ context.Context, requestID string, err error) {
			logger.Warn("cost receipt write failed after settle",
				zap.String("request_id", requestID),
				zap.Error(err),
			)
		}))
	settler := auditreceipt.NewReceiptHookSettler(baseSettler, receiptHook)
	completionBus, err := buildCompletionEventBus(eventBusCfg, settler, dlqService, logger)
	if err != nil {
		return fmt.Errorf("build completion eventbus: %w", err)
	}

	d := &deps{
		cfg:           cfg,
		queries:       q,
		selector:      selector,
		channelHealth: channelHealthService,
		claimGate:     billing.NewClaimGate(pgPool),
		settler:       settler,
		forwarder: &gateway.StreamForwarder{
			// 协议适配器注册表：按 ForwardRequest.ProtocolFamily 选语义层 adapter。
			// 当前注册：详见 gateway.BuildDefaultProtocolAdapterRegistry。
			ProtocolAdapters: gateway.BuildDefaultProtocolAdapterRegistry(),
			// Wire-format scanner 注册表（A1 atomic 引入）：按 ProtocolFamily
			// 选 wire 切帧实现。当前 19 个 family 全走 SSE；Bedrock binary
			// EventStream 在 A2+A3 atomic 加入专用 scanner。
			Scanners: gateway.BuildDefaultStreamScannerRegistry(),
			Timeouts: gateway.TimeoutConfig{
				FirstTokenTimeout:  5 * time.Second,
				InterEventTimeout:  10 * time.Second,
				TotalStreamTimeout: 60 * time.Second,
				DrainMaxSeconds:    1 * time.Second,
			},
			ScannerBufferCap: 1 << 20,
			AuditLedger:      auditLedger,
			Signer:           auditSigner,
		},
		// 生产 vault：优先读取 account_credentials v2；旧 JSONB 仅作迁移回退。
		credentialVault:    provider.NewPostgresCredentialVaultWithStore(pgPool, credentialStore),
		credentialStore:    credentialStore,
		credentialKeys:     credentialKeys,
		credentialAcqStore: credentialAcqStore,
		emailSettings:      emailSettingsStore,
		authEmailSender:    authEmailSender,
		userAuth:           userAuthService,
		userSessions:       userSessionService,
		voucherService:     voucherService,
		invitationService:  invitationService,
		responseCache:      responseCache,
		dlqService:         dlqService,
		completionBus:      completionBus,
		dispatcher: &gateway.UpstreamDispatcher{
			Adapters:         registrydefault.Build(),
			TransportFactory: transport.NewFactory(mimicryRegistry),
			// 生产 ProxyResolver：按 provider_accounts.proxy_url 做账号级出站代理。
			ProxyResolver: provider.NewPostgresProxyResolver(pgPool),
		},
		// Phase L0 minimum (N+4a): table-backed inbound auth via api_keys.
		// Replaces the SmokeAuthResolver env-injected single bearer pattern.
		// Rollback path is git revert; no SmokeAuthResolver is wired into the
		// default build.
		inboundAuth:         auth.NewAPIKeyResolver(q),
		auditLedger:         auditLedger,
		auditSigner:         auditSigner,
		auditPubkeyRegistry: auditPubkeyRegistry,
		receiptStore:        receiptStore,
		receiptFormatter:    receiptFormatter,
		refundQueue:         refundQueue,
		rateTableSource:     rateTableSource,
		// Slice 2 (N+5a) registry resolver. cache=nil → noopCache (D2: no
		// L0 cache; LRU lands in Slice 5 keyed on registry_version).
		// Takes pgxpool.Pool because Resolve runs all reads inside a
		// REPEATABLE READ + read-only TX (codex N+5a P2 fix).
		modelRegistry: registry.NewPostgresRegistry(pgPool, nil),
		// Slice 2 (N+5b) router. Stateless; SnapshotVersion is "v0.1-phase-c"
		// until Slice 5 introduces multi-attempt + cross-pool fallback.
		routePlanner: router.NewDefaultRouter(),
		// Slice 2 (N+4b2) admin trio. Resolver looks up admin_tokens by
		// prefix + bcrypt-verifies; issuer mints api_keys via admin path;
		// revoker soft-revokes. All three live entirely under
		// internal/admin (CMB-1: zero overlap with hot inbound resolver).
		adminAuth:    admin.NewAdminResolver(q),
		adminIssuer:  admin.NewKeyIssuer(pgPool),
		adminRevoker: admin.NewKeyRevoker(pgPool),
	}

	// Bootstrap-token issuance: env-var seeded first admin token. No-op
	// after the table has any active row (the env path is one-shot per
	// life of the database).
	if err := admin.MaybeBootstrap(ctx, pgPool, logger); err != nil {
		return fmt.Errorf("admin bootstrap: %w", err)
	}

	// F-AUTH-005 v2：按 (vendor, auth_mode) 扫 account_credentials，
	// 15 个 Owner 指定模式默认注册，刷新窗口按 OCAW-34 为 15 分钟。
	credentialScheduler := credentialworker.NewScheduler(
		q,
		auth.NewStormController(q),
		auditSigner,
		credentialworker.NewAccountCredentialRefresher(credentialStore, credentialworker.DefaultModeAdapterRegistry()),
		credentialworker.WithAuditLedger(auditLedger),
		credentialworker.WithRefreshQueries(credentialworker.NewAccountCredentialRefreshQueries(pgPool)),
	)
	if err := credentialScheduler.Start(ctx); err != nil {
		return fmt.Errorf("start credential refresh scheduler: %w", err)
	}
	if obsDLQCfg.Enabled {
		dlqWorker.Start(ctx)
	}
	outboxWorker.Start(ctx)

	router := chi.NewRouter()
	privacyRedactor := privacy.DefaultRedactor()
	privacyLogger := privacy.NewStdoutSystemLogger(privacyRedactor)
	router.Use(middleware.RequestID)
	router.Use(gatewayhttp.RequestIDLengthLimiter(gatewayhttp.MaxRequestIDLength))
	router.Use(middleware.RealIP)
	router.Use(privacy.Recoverer(privacyLogger))
	router.Use(middleware.Timeout(60 * time.Second))
	router.Use(privacy.Middleware(8 << 20))
	// U6-B: 把 client identity（Cursor / Claude Code / Cody / 等）写入
	// request ctx，让下游 handler / quota / metrics 通过 IdentityFromContext
	// 读取。无副作用 stateless middleware，加在 Recoverer 之后。
	//
	// **顺序 invariant**: 必须在任何 auth / quota / billing middleware **之前**——
	// 这样下游 auth/quota 才能读 IdentityFromContext 做 per-client 决策。
	// 当前没有 auth middleware（auth 在 ChatHandlerDeps.Auth 内做），将来若
	// 加 router.Use(authMiddleware) 需放在本行之后。
	router.Use(clientid.Middleware(logger))

	// /debug/vars 暴露 stdlib expvar metrics（含 clientid_request_count 等）。
	// 用 admin auth gate 包住：必须带 hk_admin_ bearer，否则 401。
	// Risk 闭环（Owner deep-review P2.1 / 2026-05-17）：之前无 auth 暴露
	// counter 给任何能 hit 的客户端，泄漏 metrics + 提供旁路侧信道。
	router.Handle("/debug/vars", adminGate(d.adminAuth, expvar.Handler()))

	mountRoutes(router, d, logger)

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		logger.Info("listening", zap.String("addr", cfg.Listen))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server error", zap.Error(err))
			cancel()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	credentialStopCtx, credentialStopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer credentialStopCancel()
	credentialStopErr := credentialScheduler.Stop(credentialStopCtx)
	var eventBusStopErr error
	if d.completionBus != nil {
		eventBusStopErr = d.completionBus.Stop(credentialStopCtx)
	}
	var dlqStopErr error
	if obsDLQCfg.Enabled {
		dlqStopErr = dlqWorker.Stop(credentialStopCtx)
	}
	outboxStopCtx, outboxStopCancel := context.WithTimeout(context.Background(), outboxRuntime.DrainTimeout)
	defer outboxStopCancel()
	outboxStopErr := outboxWorker.Stop(outboxStopCtx)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if credentialStopErr != nil {
		return fmt.Errorf("stop credential refresh scheduler: %w", credentialStopErr)
	}
	if eventBusStopErr != nil {
		return fmt.Errorf("stop completion eventbus: %w", eventBusStopErr)
	}
	if dlqStopErr != nil {
		return fmt.Errorf("stop observability DLQ worker: %w", dlqStopErr)
	}
	if outboxStopErr != nil {
		return fmt.Errorf("stop async outbox worker: %w", outboxStopErr)
	}
	return nil
}

func buildCompletionEventBus(cfg *config.EventBusConfig, settler billing.Settler, dlqService *legacydlq.Service, logger *zap.Logger) (*eventbus.Bus, error) {
	if cfg == nil || !cfg.Enabled {
		return nil, nil
	}
	bus := eventbus.New(eventbus.Config{
		Enabled:              cfg.Enabled,
		HighWorkers:          cfg.HighWorkers,
		MediumWorkers:        cfg.MediumWorkers,
		LowWorkers:           cfg.LowWorkers,
		HighBuffer:           cfg.HighBuffer,
		MediumBuffer:         cfg.MediumBuffer,
		LowBuffer:            cfg.LowBuffer,
		HandlerTimeout:       cfg.HandlerTimeout,
		ShutdownDrainTimeout: cfg.ShutdownDrainTimeout,
	}, eventbus.WithDLQ(dlqService), eventbus.WithDropHook(func(notice eventbus.DropNotice) {
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
		observability.NewAuditLoggerHandler(cfg.HandlerTimeout),
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

func registerCredentialRefreshAdapters(registry *credentialworker.AdapterRegistry) error {
	registrations := []struct {
		name    string
		adapter credentialworker.RefreshAdapter
	}{
		{name: "anthropic", adapter: adapters.AnthropicRefresh{}},
		{name: "openai", adapter: adapters.OpenAIRefresh{}},
		{name: "gemini", adapter: adapters.GeminiRefresh{}},
		{name: "codex", adapter: adapters.CodexRefresh{}},
		{name: "antigravity", adapter: adapters.AntigravityRefresh{}},
	}
	for _, item := range registrations {
		if err := registry.Register(item.name, item.adapter); err != nil {
			return err
		}
	}
	for _, name := range credentialworker.MockOnlyProviders {
		if err := registry.Register(name, credentialworker.MockOnlyAdapter{}); err != nil {
			return err
		}
	}
	return nil
}

func buildAuthEmailSender(_ *config.Config, store mailinfra.SettingsStore, keys credentialstore.KeyProvider, logger *zap.Logger, outbox obsoutbox.Outbox) (gatewayhttp.AuthEmailSender, error) {
	sender, err := mailinfra.BuildEmailSender(context.Background(), store, keys, mailinfra.WithOutbox(outbox))
	if err != nil {
		return nil, err
	}
	if logger != nil && strings.EqualFold(strings.TrimSpace(os.Getenv("HUAKAI_DEV_AUTH_RETURN_TOKEN")), "true") {
		logger.Warn("dev mode, do not enable in production", zap.String("env", "HUAKAI_DEV_AUTH_RETURN_TOKEN"))
	}
	return sender, nil
}

func releaseModeProduction() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("HUAKAI_RELEASE_MODE")), "production")
}

func loadCredentialKeyProvider() (credentialstore.KeyProvider, error) {
	keyID := strings.TrimSpace(os.Getenv("HUAKAI_CREDENTIAL_KEY_ID"))
	if keyID == "" {
		keyID = "local-v1"
	}
	raw := strings.TrimSpace(os.Getenv("HUAKAI_CREDENTIAL_KEY_B64"))
	if raw == "" {
		return nil, fmt.Errorf("%w: HUAKAI_CREDENTIAL_KEY_B64", credentialstore.ErrKeyUnavailable)
	}
	material, err := credentialstore.DecodeKeyMaterial(raw)
	if err != nil {
		return nil, err
	}
	return credentialstore.NewStaticKeyProvider(keyID, material)
}

func loadSessionSigningKey() ([]byte, error) {
	b64Names := []string{"HUAKAI_SESSION_SIGNING_KEY_B64", "HUAKAI_SESSION_HMAC_KEY_B64"}
	for _, name := range b64Names {
		raw := strings.TrimSpace(os.Getenv(name))
		if raw == "" {
			continue
		}
		for _, decode := range []func(string) ([]byte, error){
			base64.StdEncoding.DecodeString,
			base64.RawStdEncoding.DecodeString,
			base64.RawURLEncoding.DecodeString,
		} {
			key, err := decode(raw)
			if err == nil && len(key) >= 32 {
				return key, nil
			}
		}
		return nil, fmt.Errorf("%s must decode to at least 32 bytes", name)
	}
	if raw := strings.TrimSpace(os.Getenv("HUAKAI_SESSION_SIGNING_KEY_HEX")); raw != "" {
		key, err := hex.DecodeString(raw)
		if err != nil {
			return nil, fmt.Errorf("decode HUAKAI_SESSION_SIGNING_KEY_HEX: %w", err)
		}
		if len(key) < 32 {
			return nil, fmt.Errorf("HUAKAI_SESSION_SIGNING_KEY_HEX must decode to at least 32 bytes")
		}
		return key, nil
	}
	return nil, fmt.Errorf("HUAKAI_SESSION_SIGNING_KEY_B64 or HUAKAI_SESSION_SIGNING_KEY_HEX is required")
}

func buildUserOAuthService(logger *zap.Logger) *userauth.OAuthService {
	providers := make([]userauth.OAuthProvider, 0, 2)
	if p := buildOAuthProvider(logger, userauth.OAuthConfig{
		Provider:     userauth.SocialProviderGoogle,
		ClientID:     os.Getenv("HUAKAI_GOOGLE_OAUTH_CLIENT_ID"),
		ClientSecret: os.Getenv("HUAKAI_GOOGLE_OAUTH_CLIENT_SECRET"),
		RedirectURI:  os.Getenv("HUAKAI_GOOGLE_OAUTH_REDIRECT_URI"),
		AuthURL:      os.Getenv("HUAKAI_GOOGLE_OAUTH_AUTH_URL"),
		TokenURL:     envDefault("HUAKAI_GOOGLE_OAUTH_TOKEN_URL", "https://oauth2.googleapis.com/token"),
		JWKSURL:      envDefault("HUAKAI_GOOGLE_OAUTH_JWKS_URL", "https://www.googleapis.com/oauth2/v3/certs"),
		Issuer:       envDefault("HUAKAI_GOOGLE_OAUTH_ISSUER", "https://accounts.google.com"),
	}); p != nil {
		providers = append(providers, p)
	}
	if p := buildOAuthProvider(logger, userauth.OAuthConfig{
		Provider:     userauth.SocialProviderGitHub,
		ClientID:     os.Getenv("HUAKAI_GITHUB_OAUTH_CLIENT_ID"),
		ClientSecret: os.Getenv("HUAKAI_GITHUB_OAUTH_CLIENT_SECRET"),
		RedirectURI:  os.Getenv("HUAKAI_GITHUB_OAUTH_REDIRECT_URI"),
		AuthURL:      os.Getenv("HUAKAI_GITHUB_OAUTH_AUTH_URL"),
		TokenURL:     envDefault("HUAKAI_GITHUB_OAUTH_TOKEN_URL", "https://github.com/login/oauth/access_token"),
		UserURL:      envDefault("HUAKAI_GITHUB_OAUTH_USER_URL", "https://api.github.com/user"),
		EmailsURL:    envDefault("HUAKAI_GITHUB_OAUTH_EMAILS_URL", "https://api.github.com/user/emails"),
	}); p != nil {
		providers = append(providers, p)
	}
	return userauth.NewOAuthService(providers...)
}

func buildOAuthProvider(logger *zap.Logger, cfg userauth.OAuthConfig) userauth.OAuthProvider {
	if strings.TrimSpace(cfg.ClientID) == "" {
		if logger != nil {
			logger.Info("user oauth provider disabled", zap.String("provider", cfg.Provider), zap.String("reason", "client_id_missing"))
		}
		return nil
	}
	p, err := userauth.NewOAuthHTTPProvider(cfg, http.DefaultClient)
	if err != nil {
		if logger != nil {
			logger.Warn("user oauth provider disabled", zap.String("provider", cfg.Provider), zap.Error(err))
		}
		return nil
	}
	return p
}

func envDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func loadMimicryTemplateRegistry(logger *zap.Logger) (*mimicry.TemplateRegistry, error) {
	candidates := []string{
		"tools/fingerprint-collector/templates",
		"../tools/fingerprint-collector/templates",
	}
	for _, dir := range candidates {
		registry := mimicry.NewTemplateRegistry()
		err := registry.LoadFromDirectory(dir)
		if err == nil {
			logger.Info("mimicry template registry loaded",
				zap.String("dir", dir),
				zap.Int("mode_count", len(registry.Modes())),
			)
			return registry, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		return nil, fmt.Errorf("load mimicry template registry from %s: %w", dir, err)
	}
	logger.Warn("mimicry template registry not found; using Phase A default template fallback")
	return nil, nil
}

// buildAuditLedger 根据 HUAKAI_AUDIT_LEDGER_BACKEND 构建 audit ledger。
//   - memory (默认 / dev): 内存版，重启丢失，打 warn 日志
//   - postgres: PostgresLedger，持久化；production 模式强制使用此后端
func buildAuditLedger(_ context.Context, pgPool *pgxpool.Pool, signer *sign.Signer, logger *zap.Logger) (auditledger.Ledger, error) {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("HUAKAI_AUDIT_LEDGER_BACKEND")))
	isProd := releaseModeProduction()
	if isProd && backend != "postgres" {
		return nil, fmt.Errorf("production 模式要求 HUAKAI_AUDIT_LEDGER_BACKEND=postgres，当前值: %q", backend)
	}
	if backend == "postgres" {
		l, err := auditledger.NewPostgresLedger(pgPool, signer)
		if err != nil {
			return nil, fmt.Errorf("NewPostgresLedger: %w", err)
		}
		logger.Info("audit ledger backend: postgres（持久化）")
		return l, nil
	}
	// 默认 memory（dev 模式）
	logger.Warn("audit ledger backend: memory — 重启后 audit 链丢失，仅适用于开发环境")
	return auditledger.NewMemoryLedger(signer)
}

func loadAuditSigner(logger *zap.Logger) (*sign.Signer, error) {
	path := strings.TrimSpace(os.Getenv("HUAKAI_AUDIT_PRIVATE_KEY_PATH"))
	isProd := releaseModeProduction()
	if path == "" {
		if isProd {
			return nil, fmt.Errorf("production 模式要求持久私钥：请设置 HUAKAI_AUDIT_PRIVATE_KEY_PATH")
		}
		logger.Warn("using ephemeral key — restart loses chain")
		return sign.GenerateKey()
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read HUAKAI_AUDIT_PRIVATE_KEY_PATH: %w", err)
	}
	signer, err := parseAuditPrivateKey(raw)
	if err != nil {
		return nil, err
	}
	logger.Info("audit private key loaded", zap.String("path", path), zap.String("fingerprint", signer.Fingerprint()))
	return signer, nil
}

func parseAuditPrivateKey(raw []byte) (*sign.Signer, error) {
	if len(raw) == ed25519.PrivateKeySize {
		return sign.NewSignerFromKey(ed25519.PrivateKey(raw))
	}
	trimmed := strings.TrimSpace(string(raw))
	if block, _ := pem.Decode([]byte(trimmed)); block != nil {
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse audit PEM private key: %w", err)
		}
		priv, ok := key.(ed25519.PrivateKey)
		if !ok {
			return nil, sign.ErrInvalidPrivateKey
		}
		return sign.NewSignerFromKey(priv)
	}
	for _, decode := range []func(string) ([]byte, error){
		base64.StdEncoding.DecodeString,
		base64.RawStdEncoding.DecodeString,
		hex.DecodeString,
	} {
		decoded, err := decode(trimmed)
		if err == nil && len(decoded) == ed25519.PrivateKeySize {
			return sign.NewSignerFromKey(ed25519.PrivateKey(decoded))
		}
	}
	return nil, sign.ErrInvalidPrivateKey
}

// mountRoutes wires the HTTP routes per docs/openapi/openapi.yaml.
// All handlers return 501 Not Implemented except the Phase C / N+5b chat
// completions endpoint which runs the full Tx1 → forward → Tx2 pipeline.
func mountRoutes(r chi.Router, d *deps, logger *zap.Logger) {
	// Gateway endpoints (F-GW-002) — chat-completions is real (Phase C / N+5b).
	r.Post("/v1/chat/completions", gatewayhttp.NewChatCompletionsHandler(gatewayhttp.ChatHandlerDeps{
		Auth:                 d.inboundAuth,
		Registry:             d.modelRegistry,
		Router:               d.routePlanner,
		ClaimGate:            d.claimGate,
		Selector:             d.selector,
		CredentialVault:      d.credentialVault,
		Dispatcher:           d.dispatcher,
		Forwarder:            d.forwarder,
		ResponseCache:        d.responseCache,
		Settler:              d.settler,
		CompletionBus:        d.completionBus,
		AuditLedger:          d.auditLedger,
		Signer:               d.auditSigner,
		ChannelHealth:        d.channelHealth,
		BillingPolicyVersion: d.cfg.BillingPolicyVersion,
		RequestClass:         d.cfg.RequestClass,
	}))
	r.Post("/v1/responses", gatewayhttp.NewResponsesHandler(gatewayhttp.ChatHandlerDeps{
		Auth:                 d.inboundAuth,
		Registry:             d.modelRegistry,
		Router:               d.routePlanner,
		ClaimGate:            d.claimGate,
		Selector:             d.selector,
		CredentialVault:      d.credentialVault,
		Dispatcher:           d.dispatcher,
		Forwarder:            d.forwarder,
		ResponseCache:        d.responseCache,
		Settler:              d.settler,
		CompletionBus:        d.completionBus,
		AuditLedger:          d.auditLedger,
		Signer:               d.auditSigner,
		ChannelHealth:        d.channelHealth,
		BillingPolicyVersion: d.cfg.BillingPolicyVersion,
		RequestClass:         d.cfg.RequestClass,
	}))
	// /v1/messages = Anthropic Messages API endpoint。复用 chat-completions
	// pipeline (同 deps + EndpointFamily="messages")。registry 把 model alias
	// 解析到 bedrock_invoke + AutoTranslateAnthropicAPIBody=true 的 PassthroughAdapter
	// 即可让 Anthropic CLI / Claude Code 直连 AWS Bedrock。
	r.Post("/v1/messages", gatewayhttp.NewMessagesHandler(gatewayhttp.ChatHandlerDeps{
		Auth:                 d.inboundAuth,
		Registry:             d.modelRegistry,
		Router:               d.routePlanner,
		ClaimGate:            d.claimGate,
		Selector:             d.selector,
		CredentialVault:      d.credentialVault,
		Dispatcher:           d.dispatcher,
		Forwarder:            d.forwarder,
		ResponseCache:        d.responseCache,
		Settler:              d.settler,
		CompletionBus:        d.completionBus,
		AuditLedger:          d.auditLedger,
		Signer:               d.auditSigner,
		ChannelHealth:        d.channelHealth,
		BillingPolicyVersion: d.cfg.BillingPolicyVersion,
		RequestClass:         d.cfg.RequestClass,
	}))

	auditVerifyDeps := gatewayhttp.AuditVerifyStaticDeps{Ledger: d.auditLedger, Registry: d.auditPubkeyRegistry}
	auditPubkeyDeps := gatewayhttp.AuditPubkeyDeps{Signer: d.auditSigner, Registry: d.auditPubkeyRegistry}
	r.Route("/v1/audit", func(r chi.Router) {
		r.Get("/pubkey", gatewayhttp.NewAuditPubkeyHandler(auditPubkeyDeps))
		r.Get("/pubkeys", gatewayhttp.NewAuditPubkeysHandler(auditPubkeyDeps))
		r.Get("/pubkey/{fingerprint_hex}", gatewayhttp.NewAuditPubkeyByFingerprintHandler(auditPubkeyDeps))
		r.Get("/verify", gatewayhttp.NewAuditVerifyHandler(auditVerifyDeps))
		r.Post("/verify", gatewayhttp.NewAuditVerifyHandler(auditVerifyDeps))
		r.Get("/merkle-tree.json", gatewayhttp.NewAuditMerkleTreeHandler(auditVerifyDeps))
	})

	receiptDeps := gatewayhttp.CostReceiptHandlerDeps{
		Receipts:        d.receiptStore,
		DerivedReceipts: d.receiptFormatter,
		MismatchRefunds: d.refundQueue,
		RateTables:      d.rateTableSource,
		Signer:          d.auditSigner,
		PubkeyRegistry:  d.auditPubkeyRegistry,
	}
	r.Route("/v1/receipts", func(r chi.Router) {
		r.With(auth.SessionMiddleware(d.userSessions)).Get("/{request_id}", gatewayhttp.NewCostReceiptGetHandler(receiptDeps))
		r.Post("/{request_id}", http.NotFound)
		r.With(auth.SessionMiddleware(d.userSessions)).Post("/{request_id}/verify", gatewayhttp.NewCostReceiptVerifyHandler(receiptDeps))
		r.With(auth.SessionMiddleware(d.userSessions)).Get("/{request_id_host}/{request_id_tail}", gatewayhttp.NewCostReceiptGetHandler(receiptDeps))
		r.Post("/{request_id_host}/{request_id_tail}", http.NotFound)
		r.With(auth.SessionMiddleware(d.userSessions)).Post("/{request_id_host}/{request_id_tail}/verify", gatewayhttp.NewCostReceiptVerifyHandler(receiptDeps))
	})
	r.Get("/v1/pricing/rate-table", gatewayhttp.NewPricingRateTableHandler(receiptDeps))
	r.Get("/v1/pricing/snapshots", gatewayhttp.NewPricingSnapshotsHandler(receiptDeps))
	r.Get("/v1/pricing/snapshots/{snapshot_id}", gatewayhttp.NewPricingSnapshotHandler(receiptDeps))

	r.Route("/v1/auth", func(r chi.Router) {
		gatewayhttp.MountAuthRoutes(r, gatewayhttp.AuthHandlerDeps{
			Auth:        d.userAuth,
			Sessions:    d.userSessions,
			EmailSender: d.authEmailSender,
			AdminAuth:   d.adminAuth,
		})
	})

	r.Route("/v1/sessions", func(r chi.Router) {
		r.Use(auth.SessionMiddleware(d.userSessions))
		gatewayhttp.MountSessionRoutes(r, gatewayhttp.SessionHandlerDeps{Sessions: d.userSessions})
	})

	r.Route("/v1/users/me/vouchers", func(r chi.Router) {
		r.Use(auth.SessionMiddleware(d.userSessions))
		gatewayhttp.MountVoucherUserRoutes(r, gatewayhttp.VoucherUserDeps{Service: d.voucherService})
	})

	r.With(auth.SessionMiddleware(d.userSessions)).Post("/v1/invitations", gatewayhttp.NewInvitationCreateHandler(gatewayhttp.InvitationDeps{
		Service: d.invitationService,
	}))

	r.Route("/v1/admin/email", func(r chi.Router) {
		gatewayhttp.MountAdminEmailSettingsRoutes(r, gatewayhttp.AdminEmailSettingsDeps{
			Auth:  d.adminAuth,
			Store: d.emailSettings,
			Keys:  d.credentialKeys,
		})
	})

	// Admin: API Keys (Slice 2 N+4b2). Replaces hand-written SQL INSERT
	// for operator key issuance. POST=issue / GET=list / POST revoke.
	r.Route("/admin/v1/api-keys", func(r chi.Router) {
		adminhttp.MountAPIKeyRoutes(r, adminhttp.AdminAPIKeysDeps{
			Auth:    d.adminAuth,
			Issuer:  d.adminIssuer,
			Revoker: d.adminRevoker,
			Queries: d.queries,
		})
	})

	mountProviderAccountAdminRoutes := func(r chi.Router) {
		gatewayhttp.MountAdminPoolAccountRoutes(r, gatewayhttp.AdminPoolAccountDeps{
			Auth:          d.adminAuth,
			Store:         d.queries,
			Credentials:   d.credentialStore,
			ChannelHealth: d.channelHealth,
		})
		gatewayhttp.MountAdminCredentialRoutes(r, gatewayhttp.AdminCredentialDeps{
			Auth:        d.adminAuth,
			Credentials: d.credentialStore,
			AuditStore:  d.queries,
		})
		gatewayhttp.MountAdminCredentialAcquisitionRoutes(r, gatewayhttp.AdminCredentialAcquisitionDeps{
			Auth:            d.adminAuth,
			Sessions:        d.credentialAcqStore,
			Credentials:     d.credentialStore,
			CredentialAudit: d.credentialStore,
			AuditStore:      d.queries,
		})
		gatewayhttp.MountChannelHealthAdminRoutes(r, gatewayhttp.ChannelHealthAdminDeps{
			Auth:       d.adminAuth,
			Controller: d.channelHealth,
		})
	}

	// Admin: Provider Accounts canonical contract for the frontend/OpenAPI.
	r.Route("/admin/v1/provider-accounts", mountProviderAccountAdminRoutes)
	r.Route("/v1/admin/provider-accounts", mountProviderAccountAdminRoutes)
	r.Route("/v1/admin/channel-health", func(r chi.Router) {
		gatewayhttp.MountChannelHealthReadAdminRoutes(r, gatewayhttp.ChannelHealthAdminDeps{
			Auth:       d.adminAuth,
			Controller: d.channelHealth,
		})
	})
	// TODO(post-Phase-6): delete legacy pool-accounts alias after emergency rollback clients migrate.
	r.Route("/v1/admin/pool-accounts", mountProviderAccountAdminRoutes)

	r.Route("/admin/v1/credentials", func(r chi.Router) {
		gatewayhttp.MountAdminCredentialAcquisitionHelperRoutes(r, gatewayhttp.AdminCredentialAcquisitionDeps{
			Auth:            d.adminAuth,
			Sessions:        d.credentialAcqStore,
			Credentials:     d.credentialStore,
			CredentialAudit: d.credentialStore,
			AuditStore:      d.queries,
		})
	})

	// Admin: Pool Groups (F-POOL-001)
	r.Route("/admin/v1/pools", func(r chi.Router) {
		r.Mount("/", gatewayhttp.NewAdminPoolsHandler(gatewayhttp.AdminPoolsDeps{
			Auth:  d.adminAuth,
			Store: d.queries,
		}))
	})

	r.Route("/v1/admin/vouchers", func(r chi.Router) {
		gatewayhttp.MountVoucherAdminRoutes(r, gatewayhttp.VoucherAdminDeps{
			Auth:    d.adminAuth,
			Service: d.voucherService,
		})
	})

	// Admin: Usage / Billing / Audit / DLQ (F-OBS-001)
	r.Get("/admin/v1/usage", gatewayhttp.NewUsageHandler(d))
	r.Get("/admin/v1/billing/claims", gatewayhttp.NewClaimsHandler(d))
	r.Get("/admin/v1/audit-events", gatewayhttp.NewAuditEventsHandler(d))
	r.Get("/admin/v1/dlq/{handler}", gatewayhttp.NewAdminDLQListHandler(d))
	r.Post("/admin/v1/dlq/{id}/replay", gatewayhttp.NewAdminDLQReplayHandler(d))
	r.Post("/admin/v1/usage-record-dlq/{id}/replay", gatewayhttp.NewAdminDLQReplayHandler(d))
	r.Route("/admin/v1/cache/l2", func(r chi.Router) {
		gatewayhttp.MountAdminL2CacheRoutes(r, gatewayhttp.AdminL2CacheDeps{
			Auth:  d.adminAuth,
			Store: d.responseCache,
		})
	})

	logger.Info("routes mounted")
}

// adminGate 把任意 http.Handler 包到 admin auth 后面。
// 用于 /debug/vars 等 ops 暴露面：未带合法 hk_admin_ bearer 直接 401，
// 与 admin/v1/* 路由共用 internal/admin.AdminResolver 统一鉴权。
// resolver 为 nil 时 fail-closed 返 503 — 不允许悄无声息地裸奔。
func adminGate(resolver *admin.AdminResolver, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if resolver == nil {
			writeAdminGateError(w, http.StatusServiceUnavailable,
				"admin_gate_not_configured", "admin auth resolver unset")
			return
		}
		if _, err := resolver.Resolve(r.Context(), r); err != nil {
			if errors.Is(err, admin.ErrAdminBackend) {
				writeAdminGateError(w, http.StatusServiceUnavailable,
					"admin_backend_error", "admin auth backend transient failure")
				return
			}
			writeAdminGateError(w, http.StatusUnauthorized,
				"admin_unauthorized", "missing or invalid admin credential")
			return
		}
		h.ServeHTTP(w, r)
	})
}

func writeAdminGateError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"error":{"code":%q,"message":%q}}`, code, message)
}

func notImplemented(label string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = fmt.Fprintf(w, `{"error":{"code":"NOT_IMPLEMENTED","message":"%s — not implemented in the current Phase C / N+5b slice"}}`, label)
	}
}
