// Package main is the HUAKAI gateway entry point.
//
// Phase C wiring per docs/plans/2026-04-30-phase-c-gateway-wiring.md.
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
	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	l2cache "github.com/BloomingProsperity/HUAKAI/internal/cache"
	"github.com/BloomingProsperity/HUAKAI/internal/clientid"
	"github.com/BloomingProsperity/HUAKAI/internal/config"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker/adapters"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	obsdlq "github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/observability"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
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
	cfg             *config.Config
	queries         *db.Queries
	selector        pool.Selector // M6: 接口化, 由 buildSelector 按 PoolSelectorConfig 装配 default 或 dispatcher
	claimGate       billing.ClaimGate
	settler         billing.Settler
	forwarder       *gateway.StreamForwarder
	credentialVault provider.CredentialVault
	credentialStore *credentialstore.Store
	dispatcher      *gateway.UpstreamDispatcher
	responseCache   l2cache.Store
	dlqService      *obsdlq.Service
	completionBus   *eventbus.Bus
	inboundAuth     *auth.APIKeyResolver
	auditLedger     auditledger.Ledger
	auditSigner     *sign.Signer
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
	credentialKeys, err := loadCredentialKeyProvider()
	if err != nil {
		return fmt.Errorf("load credential encryption key: %w", err)
	}
	credentialModeRegistry := credentialstore.DefaultHandlerRegistry()
	credentialStore := credentialstore.NewStore(pgPool, credentialKeys, credentialModeRegistry)

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
	selector, selectorCleanup, err := buildSelector(ctx, q, pgPool, selectorCfg, logger)
	if err != nil {
		return fmt.Errorf("build selector: %w", err)
	}
	defer selectorCleanup()

	auditSigner, err := loadAuditSigner(logger)
	if err != nil {
		return fmt.Errorf("load audit ledger signer: %w", err)
	}
	auditLedger, err := auditledger.NewMemoryLedger(auditSigner)
	if err != nil {
		return fmt.Errorf("build audit ledger: %w", err)
	}
	dlqStore := obsdlq.NewStore(pgPool)
	dlqService := obsdlq.NewService(dlqStore, obsdlq.WithPolicy(obsdlq.RetryPolicy{
		BaseBackoff: obsDLQCfg.BaseBackoff,
		CapBackoff:  obsDLQCfg.CapBackoff,
		MaxAttempts: obsDLQCfg.MaxAttempts,
		DLQAfter:    obsDLQCfg.DLQAfter,
	}))
	dlqService.Register(obsdlq.EventKindUsageRecord, obsdlq.NewUsageRecordHandler(pgPool))
	replicaTarget := ""
	var closeReplica func()
	if obsDLQCfg.ReplicaDSN != "" {
		replicaTarget = "postgres"
		replicaHandler, cleanup := obsdlq.NewLazyPostgresReplicaHandler(obsDLQCfg.ReplicaDSN)
		closeReplica = cleanup
		dlqService.Register(obsdlq.EventKindBillingEventReplica, replicaHandler)
		dlqService.Register(obsdlq.EventKindAuditEventReplica, replicaHandler)
	}
	if closeReplica != nil {
		defer closeReplica()
	}
	dlqWorker := obsdlq.NewWorker(dlqService, obsdlq.WorkerConfig{
		HighWorkers:   obsDLQCfg.HighWorkers,
		MediumWorkers: obsDLQCfg.MediumWorkers,
		LowWorkers:    obsDLQCfg.LowWorkers,
		LeaseTTL:      obsDLQCfg.LeaseTTL,
		IdleSleep:     time.Second,
	})

	settler := billing.NewSettler(pgPool, billing.WithDLQStore(dlqStore), billing.WithReplicaTarget(replicaTarget))
	completionBus, err := buildCompletionEventBus(eventBusCfg, settler, dlqService, logger)
	if err != nil {
		return fmt.Errorf("build completion eventbus: %w", err)
	}

	d := &deps{
		cfg:       cfg,
		queries:   q,
		selector:  selector,
		claimGate: billing.NewClaimGate(pgPool),
		settler:   settler,
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
		credentialVault: provider.NewPostgresCredentialVaultWithStore(pgPool, credentialStore),
		credentialStore: credentialStore,
		responseCache:   responseCache,
		dlqService:      dlqService,
		completionBus:   completionBus,
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
		inboundAuth: auth.NewAPIKeyResolver(q),
		auditLedger: auditLedger,
		auditSigner: auditSigner,
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

	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Recoverer)
	router.Use(middleware.Timeout(60 * time.Second))
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
	// 当前未加 auth；admin 暴露面有限期间放在主路由 root 上方便 ops curl。
	// TODO: 接入 admin auth middleware 后挪到 admin 路径下。
	router.Handle("/debug/vars", expvar.Handler())

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
	return nil
}

func buildCompletionEventBus(cfg *config.EventBusConfig, settler billing.Settler, dlqService *obsdlq.Service, logger *zap.Logger) (*eventbus.Bus, error) {
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

func loadAuditSigner(logger *zap.Logger) (*sign.Signer, error) {
	path := strings.TrimSpace(os.Getenv("HUAKAI_AUDIT_PRIVATE_KEY_PATH"))
	if path == "" {
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
		BillingPolicyVersion: d.cfg.BillingPolicyVersion,
		RequestClass:         d.cfg.RequestClass,
	}))

	gatewayhttp.MountAuditVerifyRoutes(r, gatewayhttp.AuditVerifyStaticDeps{Ledger: d.auditLedger})

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

	// Admin: Provider Account writes for the reverse-proxy main path Q1.
	r.Route("/v1/admin/pool-accounts", func(r chi.Router) {
		gatewayhttp.MountAdminPoolAccountRoutes(r, gatewayhttp.AdminPoolAccountDeps{
			Auth:        d.adminAuth,
			Store:       d.queries,
			Credentials: d.credentialStore,
		})
		gatewayhttp.MountAdminCredentialRoutes(r, gatewayhttp.AdminCredentialDeps{
			Auth:        d.adminAuth,
			Credentials: d.credentialStore,
			AuditStore:  d.queries,
		})
	})

	// Admin: Pool Groups (F-POOL-001)
	r.Route("/admin/v1/pools", func(r chi.Router) {
		r.Mount("/", gatewayhttp.NewAdminPoolsHandler(gatewayhttp.AdminPoolsDeps{
			Auth:  d.adminAuth,
			Store: d.queries,
		}))
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

func notImplemented(label string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = fmt.Fprintf(w, `{"error":{"code":"NOT_IMPLEMENTED","message":"%s — not implemented in the current Phase C / N+5b slice"}}`, label)
	}
}
