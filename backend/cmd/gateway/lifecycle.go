package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	l2cache "github.com/BloomingProsperity/HUAKAI/internal/cache"
	runtimeconfig "github.com/BloomingProsperity/HUAKAI/internal/config"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker"
	legacydlq "github.com/BloomingProsperity/HUAKAI/internal/dlq"
	mailinfra "github.com/BloomingProsperity/HUAKAI/internal/email"
	obsoutbox "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

type gatewayRuntime struct {
	deps                *deps
	pgPool              *pgxpool.Pool
	selectorCleanup     func()
	closeReplica        func()
	credentialScheduler *credentialworker.Scheduler
	dlqWorker           *legacydlq.Worker
	outboxWorker        *obsoutbox.Worker
	obsDLQEnabled       bool
	outboxRuntime       obsoutbox.RuntimeConfig
}

func (rt *gatewayRuntime) close() {
	if rt == nil {
		return
	}
	if rt.closeReplica != nil {
		rt.closeReplica()
	}
	if rt.selectorCleanup != nil {
		rt.selectorCleanup()
	}
	if rt.pgPool != nil {
		rt.pgPool.Close()
	}
}

func newGatewayServer(listen string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

func serveGateway(ctx context.Context, srv *http.Server, rt *gatewayRuntime, cancel context.CancelFunc, logger *zap.Logger) error {
	go func() {
		logger.Info("listening", zap.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server error", zap.Error(err))
			cancel()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")
	return shutdownGateway(srv, rt)
}

func shutdownGateway(srv *http.Server, rt *gatewayRuntime) error {
	credentialStopCtx, credentialStopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer credentialStopCancel()

	var credentialStopErr error
	if rt.credentialScheduler != nil {
		credentialStopErr = rt.credentialScheduler.Stop(credentialStopCtx)
	}
	var eventBusStopErr error
	if rt.deps != nil && rt.deps.completionBus != nil {
		eventBusStopErr = rt.deps.completionBus.Stop(credentialStopCtx)
	}
	var dlqStopErr error
	if rt.obsDLQEnabled && rt.dlqWorker != nil {
		dlqStopErr = rt.dlqWorker.Stop(credentialStopCtx)
	}

	outboxStopCtx, outboxStopCancel := context.WithTimeout(context.Background(), rt.outboxRuntime.DrainTimeout)
	defer outboxStopCancel()
	var outboxStopErr error
	if rt.outboxWorker != nil {
		outboxStopErr = rt.outboxWorker.Stop(outboxStopCtx)
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
	if outboxStopErr != nil {
		return fmt.Errorf("stop async outbox worker: %w", outboxStopErr)
	}
	return nil
}

func loadRuntimeOptions(logger *zap.Logger) (*runtimeOptions, error) {
	outboxRuntime, err := obsoutbox.LoadRuntimeConfigFromEnv()
	if err != nil {
		return nil, fmt.Errorf("load async outbox config: %w", err)
	}
	logger.Info("async outbox config loaded",
		zap.Int("max_attempts", outboxRuntime.MaxAttempts),
		zap.Duration("max_backoff", outboxRuntime.MaxBackoff),
		zap.Duration("drain_timeout", outboxRuntime.DrainTimeout),
	)
	selectorCfg, err := runtimeconfig.LoadPoolSelector()
	if err != nil {
		return nil, fmt.Errorf("load pool selector config: %w", err)
	}
	logger.Info("pool selector config loaded",
		zap.String("mode", string(selectorCfg.Mode)),
		zap.Int("shadow_pct", selectorCfg.ShadowPercent),
		zap.Int("canary_pct", selectorCfg.CanaryPercent),
	)
	cacheCfg, err := runtimeconfig.LoadL2Cache()
	if err != nil {
		return nil, fmt.Errorf("load L2 cache config: %w", err)
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
	obsDLQCfg, err := runtimeconfig.LoadObsDLQ()
	if err != nil {
		return nil, fmt.Errorf("load obs DLQ config: %w", err)
	}
	logger.Info("observability DLQ config loaded",
		zap.Bool("enabled", obsDLQCfg.Enabled),
		zap.Bool("replica_configured", obsDLQCfg.ReplicaDSN != ""),
		zap.Int("high_workers", obsDLQCfg.HighWorkers),
		zap.Int("medium_workers", obsDLQCfg.MediumWorkers),
		zap.Int("low_workers", obsDLQCfg.LowWorkers),
	)
	eventBusCfg, err := runtimeconfig.LoadEventBus()
	if err != nil {
		return nil, fmt.Errorf("load eventbus config: %w", err)
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
	return &runtimeOptions{
		selector:      selectorCfg,
		obsDLQ:        obsDLQCfg,
		eventBus:      eventBusCfg,
		outboxRuntime: outboxRuntime,
		responseCache: responseCache,
	}, nil
}

func buildUserServices(pgPool *pgxpool.Pool, keys credentialstore.KeyProvider, emailSettings *mailinfra.PostgresSettingsStore, logger *zap.Logger) (*userauth.Service, *usersession.Service, error) {
	userAuthService := userauth.NewService(userauth.NewPostgresStoreWithKeys(pgPool, keys))
	userAuthService.OAuth = buildUserOAuthService(logger)
	userAuthService.VerificationTTL = mailinfra.DefaultVerificationTTL
	userAuthService.Verification = mailinfra.NewVerificationPolicy(emailSettings)
	sessionSigningKey, err := loadSessionSigningKey()
	if err != nil {
		return nil, nil, fmt.Errorf("load session signing key: %w", err)
	}
	userSessionService := usersession.NewService(usersession.NewPostgresStore(pgPool))
	userSessionService.SigningKey = sessionSigningKey
	return userAuthService, userSessionService, nil
}

func buildAuditServices(ctx context.Context, pgPool *pgxpool.Pool, logger *zap.Logger) (*sign.Signer, auditledger.Ledger, auditledger.PubkeyRegistry, error) {
	auditSigner, err := loadAuditSigner(logger)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load audit ledger signer: %w", err)
	}
	auditPubkeyRegistry, err := auditledger.NewPGXPubkeyRegistry(pgPool)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("build audit pubkey registry: %w", err)
	}
	if err := auditledger.EnsureSignerPubkey(ctx, auditPubkeyRegistry, auditSigner, time.Now().UTC()); err != nil {
		return nil, nil, nil, fmt.Errorf("register audit signer pubkey: %w", err)
	}
	auditLedger, err := buildAuditLedger(ctx, pgPool, auditSigner, logger)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("build audit ledger: %w", err)
	}
	return auditSigner, auditLedger, auditPubkeyRegistry, nil
}

func buildDLQRuntime(pgPool *pgxpool.Pool, cfg *runtimeconfig.ObsDLQConfig) (*legacydlq.Store, *legacydlq.Service, *legacydlq.Worker, string, func()) {
	dlqStore := legacydlq.NewStore(pgPool)
	dlqService := legacydlq.NewService(dlqStore, legacydlq.WithPolicy(legacydlq.RetryPolicy{
		BaseBackoff: cfg.BaseBackoff,
		CapBackoff:  cfg.CapBackoff,
		MaxAttempts: cfg.MaxAttempts,
		DLQAfter:    cfg.DLQAfter,
	}))
	dlqService.Register(legacydlq.EventKindUsageRecord, legacydlq.NewUsageRecordHandler(pgPool))
	replicaTarget := ""
	var closeReplica func()
	if cfg.ReplicaDSN != "" {
		replicaTarget = "postgres"
		replicaHandler, cleanup := legacydlq.NewLazyPostgresReplicaHandler(cfg.ReplicaDSN)
		closeReplica = cleanup
		dlqService.Register(legacydlq.EventKindBillingEventReplica, replicaHandler)
		dlqService.Register(legacydlq.EventKindAuditEventReplica, replicaHandler)
	}
	dlqWorker := legacydlq.NewWorker(dlqService, legacydlq.WorkerConfig{
		HighWorkers:   cfg.HighWorkers,
		MediumWorkers: cfg.MediumWorkers,
		LowWorkers:    cfg.LowWorkers,
		LeaseTTL:      cfg.LeaseTTL,
		IdleSleep:     time.Second,
	})
	return dlqStore, dlqService, dlqWorker, replicaTarget, closeReplica
}
