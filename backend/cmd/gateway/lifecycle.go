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
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/mediatask"
	obsoutbox "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/BloomingProsperity/HUAKAI/internal/subscription"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

type gatewayRuntime struct {
	deps                       *deps
	pgPool                     *pgxpool.Pool
	selectorCleanup            func()
	replayJanitorStop          func()
	hermesRetentionWorker      *hermes.MessageRetentionWorker
	leaseSweepStop             func()
	paymentExpireSweepStop     func()
	pendingReconcileStop       func()
	modelSyncStop              func()
	alertingEvalStop           func()
	closeReplica               func()
	credentialScheduler        *credentialworker.Scheduler
	dlqWorker                  *legacydlq.Worker
	outboxWorker               *obsoutbox.Worker
	subscriptionExpiryWorker   *subscription.ExpiryWorker
	subscriptionReminderWorker *subscription.ReminderWorker
	mediaTaskWorker            *mediatask.Worker
	obsDLQEnabled              bool
	outboxRuntime              obsoutbox.RuntimeConfig
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
	if rt.replayJanitorStop != nil {
		rt.replayJanitorStop()
	}
	if rt.hermesRetentionWorker != nil {
		rt.hermesRetentionWorker.Stop()
	}
	if rt.leaseSweepStop != nil {
		rt.leaseSweepStop()
	}
	if rt.paymentExpireSweepStop != nil {
		rt.paymentExpireSweepStop()
	}
	if rt.pendingReconcileStop != nil {
		rt.pendingReconcileStop()
	}
	if rt.mediaTaskWorker != nil {
		rt.mediaTaskWorker.Stop()
	}
	if rt.modelSyncStop != nil {
		rt.modelSyncStop()
	}
	if rt.alertingEvalStop != nil {
		rt.alertingEvalStop()
	}
	if rt.deps != nil && rt.deps.otelShutdown != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = rt.deps.otelShutdown(ctx)
		cancel()
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
		// P1-B 防 slowloris-style body 慢速攻击:headers 发完后慢慢滴 body
		// 会一直占住协程 + 连接,Go 默认无 ReadTimeout 拿不到 socket-level
		// deadline (chi middleware.Timeout 只设 ctx.Done,不是 read deadline)。
		// 60s 跟 chi Timeout 对齐,留余量给最大 1MiB body 在合理带宽下完成。
		ReadTimeout: 60 * time.Second,
		// 防 keep-alive 闲连接耗尽:Go 默认 IdleTimeout=0 = 等于 ReadTimeout,
		// 显式拉到 90s 跟 nginx/proxy 上游一致避免被对方先关。
		IdleTimeout: 90 * time.Second,
		// 注意:故意不设 WriteTimeout — SSE 流可以长达 stream budget (数分钟以上),
		// WriteTimeout 是连接级总写时长,会把合法长流响应砍断。
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
	// shutdown 顺序之前先停 scheduler / completionBus /
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
	if rt.modelSyncStop != nil {
		rt.modelSyncStop()
	}
	if rt.alertingEvalStop != nil {
		rt.alertingEvalStop()
	}
	if rt.hermesRetentionWorker != nil {
		rt.hermesRetentionWorker.Stop()
	}
	// 到期 worker 独立于 in-flight handler; Stop 在当前 tick 结束后立即返回 (非整周期等待)。
	if rt.subscriptionExpiryWorker != nil {
		rt.subscriptionExpiryWorker.Stop()
	}
	// 提醒 worker 同理独立, 优雅停止。
	if rt.subscriptionReminderWorker != nil {
		rt.subscriptionReminderWorker.Stop()
	}
	if rt.mediaTaskWorker != nil {
		rt.mediaTaskWorker.Stop()
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
	modelSyncCfg, err := runtimeconfig.LoadModelSync()
	if err != nil {
		return nil, fmt.Errorf("load model sync config: %w", err)
	}
	logger.Info("model sync config loaded",
		zap.Bool("enabled", modelSyncCfg.Enabled),
		zap.Duration("interval", modelSyncCfg.Interval),
		zap.Duration("timeout", modelSyncCfg.Timeout),
		zap.Bool("openai_configured", modelSyncCfg.OpenAI.Configured()),
		zap.Bool("anthropic_configured", modelSyncCfg.Anthropic.Configured()),
		zap.Bool("gemini_configured", modelSyncCfg.Gemini.Configured()),
	)
	return &runtimeOptions{
		selector:      selectorCfg,
		obsDLQ:        obsDLQCfg,
		eventBus:      eventBusCfg,
		modelSync:     modelSyncCfg,
		outboxRuntime: outboxRuntime,
		responseCache: responseCache,
		cacheScope:    cacheCfg.Scope,
	}, nil
}

func buildUserServices(pgPool *pgxpool.Pool, keys credentialstore.KeyProvider, emailSettings *mailinfra.PostgresSettingsStore, logger *zap.Logger) (*userauth.Service, *usersession.Service, error) {
	userAuthService := userauth.NewService(userauth.NewPostgresStoreWithKeys(pgPool, keys))
	registrationMode, err := loadUserRegistrationModeFromEnv()
	if err != nil {
		return nil, nil, fmt.Errorf("load user registration mode: %w", err)
	}
	userAuthService.RegistrationMode = registrationMode
	userAuthService.OAuth = buildUserOAuthService(logger)
	// caller 提供的 redirect_uri 必须在此白名单内,否则拒绝;空 = 只用 provider 服务端固定回调。
	userAuthService.AllowedRedirectURIs = loadUserOAuthRedirectAllowlistFromEnv()
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

func buildDLQRuntime(pgPool *pgxpool.Pool, cfg *runtimeconfig.ObsDLQConfig, auditLedger auditledger.Ledger) (*legacydlq.Store, *legacydlq.Service, *legacydlq.Worker, string, func()) {
	dlqStore := legacydlq.NewStore(pgPool)
	dlqService := legacydlq.NewService(dlqStore, legacydlq.WithPolicy(legacydlq.RetryPolicy{
		BaseBackoff: cfg.BaseBackoff,
		CapBackoff:  cfg.CapBackoff,
		MaxAttempts: cfg.MaxAttempts,
		DLQAfter:    cfg.DLQAfter,
	}))
	dlqService.Register(legacydlq.EventKindUsageRecord, legacydlq.NewUsageRecordHandler(pgPool))
	dlqService.Register(legacydlq.EventKindAuditLedgerEntry, auditledger.NewDLQHandler(auditLedger))
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
