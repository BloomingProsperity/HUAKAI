// Package main is the HUAKAI gateway entry point.
//
// Phase 3 skeleton per docs/16_PHASED_DELIVERY_PLAN.md §Phase 3.
// DR-008 §1: skeleton ONLY — no business logic until per-feature spec implementation.
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

func run(logger *zap.Logger) error {
	cfgPath := os.Getenv("HUAKAI_CONFIG")
	if cfgPath == "" {
		cfgPath = "config.yaml"
	}

	// TODO: load + validate config per docs/specs/api-contract.md
	logger.Info("config path", zap.String("path", cfgPath))

	// TODO: open DB pool per DR-006 (PostgreSQL via pgx)
	// TODO: open Redis pool per DR-006 (cache + rate-limit + token-cache)
	// TODO: wire feature modules per docs/specs/*.md

	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Recoverer)
	router.Use(middleware.Timeout(60 * time.Second))

	mountRoutes(router, logger)

	addr := os.Getenv("HUAKAI_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go func() {
		logger.Info("listening", zap.String("addr", addr))
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
func mountRoutes(r chi.Router, logger *zap.Logger) {
	// Gateway endpoints (F-GW-002)
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
