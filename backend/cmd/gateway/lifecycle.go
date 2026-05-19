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
	serveErrCh := make(chan error, 1)
	go func() {
		logger.Info("listening", zap.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server error", zap.Error(err))
			serveErrCh <- err
			cancel()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")
	shutdownErr := shutdownGateway(srv, rt)
	select {
	case serveErr := <-serveErrCh:
		return errors.Join(serveErr, shutdownErr)
	default:
		return shutdownErr
	}
}

func shutdownGateway(srv *http.Server, rt *gatewayRuntime) error {
	// codex chunk6 P1 fix: shutdown 顺序之前先停 scheduler / completionBus /
	// DLQ / outbox 再 srv.Shutdown, 但 in-flight HTTP handler 仍引用这些 deps
	// (Settler 写账, CompletionBus 派 event, Outbox 投 DLQ); deps 已停 →
	// handler 出错 / 静默丢消息。正确顺序: 先 srv.Shutdown 等 in-flight handler
	// drain 完, 再依次停 dep workers, 最后 close DB pool (defer rt.close)。
	httpShutdownCtx, httpShutdownCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer httpShutdownCancel()
	httpShutdownErr := srv.Shutdown(httpShutdownCtx)

	// in-flight 已 drain, 现在停 worker 依次按 budget 独立计时, 不串行共享
	// 单一 5s ctx 让前面 worker 抢走后面 worker 的 drain 预算。
	var credentialStopErr error
	if rt.credentialScheduler != nil {
		schedCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		credentialStopErr = rt.credentialScheduler.Stop(schedCtx)
		cancel()
	}
	var eventBusStopErr error
	if rt.deps != nil && rt.deps.completionBus != nil {
		busCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		eventBusStopErr = rt.deps.completionBus.Stop(busCtx)
		cancel()
	}
	var dlqStopErr error
	if rt.obsDLQEnabled && rt.dlqWorker != nil {
		dlqCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		dlqStopErr = rt.dlqWorker.Stop(dlqCtx)
		cancel()
	}
	var outboxStopErr error
	if rt.outboxWorker != nil {
		outboxCtx, cancel := context.WithTimeout(context.Background(), rt.outboxRuntime.DrainTimeout)
		outboxStopErr = rt.outboxWorker.Stop(outboxCtx)
		cancel()
	}

	// 合并错误一并返回, 不让前面 step 的失败遮盖后面 step。
	return errors.Join(
		wrapIfErr("shutdown HTTP server", httpShutdownErr),
		wrapIfErr("stop credential refresh scheduler", credentialStopErr),
		wrapIfErr("stop completion eventbus", eventBusStopErr),
		wrapIfErr("stop observability DLQ worker", dlqStopErr),
		wrapIfErr("stop async outbox worker", outboxStopErr),
	)
}

func wrapIfErr(prefix string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", prefix, err)
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
