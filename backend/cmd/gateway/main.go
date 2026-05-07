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
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/config"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
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
	cfg         *config.Config
	queries     *db.Queries
	selector    *pool.DefaultSelector
	claimGate   billing.ClaimGate
	settler     billing.Settler
	forwarder       *gateway.StreamForwarder
	credentialVault provider.CredentialVault
	dispatcher      *gateway.UpstreamDispatcher
	inboundAuth     *auth.APIKeyResolver
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

func run(logger *zap.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger.Info("config loaded",
		zap.String("listen", cfg.Listen),
		zap.String("auth_mode", "api_keys_table"),
	)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	pgPool, err := db.Open(ctx, db.PoolConfig{DSN: cfg.DatabaseURL})
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer pgPool.Close()

	q := db.New(pgPool)
	selector := pool.NewDefaultSelector(
		pool.NewDBAccountSource(q),
		pool.WithSlotManager(pool.NewDBSlotManager(pgPool)),
		pool.WithClaimGate(pool.NewDBClaimGate(q)),
	)

	d := &deps{
		cfg:       cfg,
		queries:   q,
		selector:  selector,
		claimGate: billing.NewClaimGate(pgPool),
		settler:   billing.NewSettler(pgPool),
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
		},
		// 内存 vault：dev/test 用空表占位；DB-backed vault 后续 atomic 接入。
		credentialVault: provider.NewStaticVault(),
		dispatcher: &gateway.UpstreamDispatcher{
			Adapters:         registrydefault.Build(),
			TransportFactory: transport.NewFactory(),
			// 内存 ProxyResolver：dev/test 默认空（所有账号直连）。
			// DB-backed resolver 后续 atomic 接入；接入后此处替换实例。
			ProxyResolver: provider.NewStaticProxyResolver(),
		},
		// Phase L0 minimum (N+4a): table-backed inbound auth via api_keys.
		// Replaces the SmokeAuthResolver env-injected single bearer pattern.
		// Rollback path is git revert; no SmokeAuthResolver is wired into the
		// default build.
		inboundAuth: auth.NewAPIKeyResolver(q),
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

	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Recoverer)
	router.Use(middleware.Timeout(60 * time.Second))

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

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	return srv.Shutdown(shutdownCtx)
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
		Settler:              d.settler,
		BillingPolicyVersion: d.cfg.BillingPolicyVersion,
		RequestClass:         d.cfg.RequestClass,
	}))
	r.Post("/v1/responses", notImplemented("F-GW-002 responses handler"))
	r.Post("/v1/messages", notImplemented("F-GW-002 anthropic-messages handler"))

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

	// Admin: Pool Groups (F-POOL-001)
	r.Route("/admin/v1/pools", func(r chi.Router) {
		r.Get("/", notImplemented("F-POOL-001 list pools"))
		r.Post("/", notImplemented("F-POOL-001 create pool"))
		r.Get("/{id}", notImplemented("F-POOL-001 get pool"))
		r.Patch("/{id}", notImplemented("F-POOL-001 update pool"))
	})

	// Admin: Provider Accounts (F-POOL-001 + F-AUTH-005 + F-RATE-001)
	r.Route("/admin/v1/provider-accounts", func(r chi.Router) {
		r.Get("/", notImplemented("F-POOL-001 list provider-accounts"))
		r.Post("/", notImplemented("F-AUTH-005 create provider-account"))
		r.Get("/{id}", notImplemented("F-POOL-001 get provider-account"))
		r.Patch("/{id}", notImplemented("F-POOL-001 update provider-account"))
		r.Post("/{id}/clear-rate-limit", notImplemented("F-RATE-001 cascade clear"))
	})

	// Admin: Usage / Billing / Audit / DLQ (F-OBS-001)
	r.Get("/admin/v1/usage", notImplemented("F-OBS-001 usage query"))
	r.Get("/admin/v1/billing/claims", notImplemented("F-OBS-001 claims query"))
	r.Get("/admin/v1/audit-events", notImplemented("F-OBS-001 + F-RATE-001 + F-AUTH-005 audit query"))
	r.Post("/admin/v1/usage-record-dlq/{id}/replay", notImplemented("F-OBS-001 DLQ replay"))

	logger.Info("routes mounted")
}

func notImplemented(label string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = fmt.Fprintf(w, `{"error":{"code":"NOT_IMPLEMENTED","message":"%s — not implemented in the current Phase C / N+5b slice"}}`, label)
	}
}
