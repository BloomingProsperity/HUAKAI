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

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/config"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
)

func main() {
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
//
// Phase C.1: pool selector is intentionally absent — its production
// AccountSource + SlotManager + ClaimGate adapters land in C.2. The chat
// handler in C.3 will fail-closed if Pool.Select is unreachable, instead of
// silently routing to a no-op selector.
type deps struct {
	cfg       *config.Config
	queries   *db.Queries
	claimGate billing.ClaimGate
	settler   billing.Settler
	forwarder *gateway.StreamForwarder
}

func run(logger *zap.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger.Info("config loaded",
		zap.String("listen", cfg.Listen),
		zap.Bool("smoke_auth_configured", cfg.SmokeAuthConfigured()),
	)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	pgPool, err := db.Open(ctx, db.PoolConfig{DSN: cfg.DatabaseURL})
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer pgPool.Close()

	q := db.New(pgPool)

	d := &deps{
		cfg:       cfg,
		queries:   q,
		claimGate: billing.NewClaimGate(pgPool),
		settler:   billing.NewSettler(pgPool),
		forwarder: &gateway.StreamForwarder{},
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
// All handlers return 501 Not Implemented in skeleton; per-feature implementation
// happens in Phase 4+ vertical slices.
func mountRoutes(r chi.Router, _ *deps, logger *zap.Logger) {
	// Gateway endpoints (F-GW-002)
	// Phase C.3 will replace this with the real chat handler that uses deps.
	r.Post("/v1/chat/completions", notImplemented("F-GW-002 chat-completions handler"))
	r.Post("/v1/responses", notImplemented("F-GW-002 responses handler"))
	r.Post("/v1/messages", notImplemented("F-GW-002 anthropic-messages handler"))

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

	logger.Info("routes mounted (skeleton)")
}

func notImplemented(label string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = fmt.Fprintf(w, `{"error":{"code":"NOT_IMPLEMENTED","message":"%s — Phase 3 skeleton; awaits Phase 4 implementation"}}`, label)
	}
}
