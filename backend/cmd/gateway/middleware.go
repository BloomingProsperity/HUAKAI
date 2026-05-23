package main

import (
	"context"
	"errors"
	"expvar"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	auditreceipt "github.com/BloomingProsperity/HUAKAI/internal/audit"
	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/clientid"
	runtimeconfig "github.com/BloomingProsperity/HUAKAI/internal/config"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker/adapters"
	legacydlq "github.com/BloomingProsperity/HUAKAI/internal/dlq"
	mailinfra "github.com/BloomingProsperity/HUAKAI/internal/email"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp"
	obsoutbox "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/observability"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
)

func newRouter(d *deps, logger *zap.Logger) chi.Router {
	router := chi.NewRouter()
	privacyRedactor := privacy.DefaultRedactor()
	privacyLogger := privacy.NewStdoutSystemLogger(privacyRedactor)
	router.Use(middleware.RequestID)
	router.Use(gatewayhttp.RequestIDLengthLimiter(gatewayhttp.MaxRequestIDLength))
	router.Use(middleware.RealIP)
	router.Use(privacy.Recoverer(privacyLogger))
	router.Use(middleware.Timeout(60 * time.Second))
	router.Use(privacy.Middleware(8 << 20))
	// U6-B: 把 client identity 写入 request ctx，必须早于后续 auth/quota/billing。
	router.Use(clientid.Middleware(logger))

	// /debug/vars 使用 admin auth gate 包住，避免 metrics 裸露。
	router.Handle("/debug/vars", adminGate(d.adminAuth, expvar.Handler()))
	mountRoutes(router, d, logger)
	return router
}

// adminGate 把任意 http.Handler 包到 admin auth 后面。
// resolver 为 nil 时 fail-closed 返 503，不允许 ops 暴露面裸奔。
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

func buildStreamForwarder(auditLedger auditledger.Ledger, auditSigner *sign.Signer, auditLedgerDLQ auditledger.DLQEnqueuer) *gateway.StreamForwarder {
	return &gateway.StreamForwarder{
		ProtocolAdapters: gateway.BuildDefaultProtocolAdapterRegistry(),
		Scanners:         gateway.BuildDefaultStreamScannerRegistry(),
		Timeouts: gateway.TimeoutConfig{
			FirstTokenTimeout:  5 * time.Second,
			InterEventTimeout:  10 * time.Second,
			TotalStreamTimeout: 60 * time.Second,
			DrainMaxSeconds:    1 * time.Second,
		},
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

func buildSettlementServices(_ context.Context, pgPool *pgxpool.Pool, auditSigner *sign.Signer, auditLedger auditledger.Ledger, dlqStore *legacydlq.Store, dlqService *legacydlq.Service, replicaTarget string, eventBusCfg *runtimeconfig.EventBusConfig, auditRefPolicy *eventbus.AuditRefPolicy, logger *zap.Logger) (billing.Settler, *auditreceipt.PGXReceiptStorage, *auditreceipt.ReceiptFormatter, *auditreceipt.MismatchRefundQueue, billing.RateTableSource, *eventbus.Bus, error) {
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
		auditreceipt.WithReceiptHookErrorHandler(func(_ context.Context, requestID string, err error) {
			logger.Warn("cost receipt write failed after settle",
				zap.String("request_id", requestID),
				zap.Error(err),
			)
		}))
	settler := auditreceipt.NewReceiptHookSettler(baseSettler, receiptHook)
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
