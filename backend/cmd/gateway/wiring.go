package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/anthropicoauth"
	auditreceipt "github.com/BloomingProsperity/HUAKAI/internal/audit"
	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	l2cache "github.com/BloomingProsperity/HUAKAI/internal/cache"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	communityinvitation "github.com/BloomingProsperity/HUAKAI/internal/community/invitation"
	runtimeconfig "github.com/BloomingProsperity/HUAKAI/internal/config"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	dbauth "github.com/BloomingProsperity/HUAKAI/internal/db/auth"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	dbhermes "github.com/BloomingProsperity/HUAKAI/internal/db/hermes"
	legacydlq "github.com/BloomingProsperity/HUAKAI/internal/dlq"
	mailinfra "github.com/BloomingProsperity/HUAKAI/internal/email"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/hermeschat"
	obsoutbox "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	providercopilot "github.com/BloomingProsperity/HUAKAI/internal/provider/copilot"
	providercursor "github.com/BloomingProsperity/HUAKAI/internal/provider/cursor"
	providergemini "github.com/BloomingProsperity/HUAKAI/internal/provider/gemini"
	providerkiro "github.com/BloomingProsperity/HUAKAI/internal/provider/kiro"
	provideropenaicodex "github.com/BloomingProsperity/HUAKAI/internal/provider/openai_codex"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
	providerwindsurf "github.com/BloomingProsperity/HUAKAI/internal/provider/windsurf"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementrecovery"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
	"github.com/BloomingProsperity/HUAKAI/internal/userkey"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
	"github.com/BloomingProsperity/HUAKAI/internal/voucher"
)

// deps is the live dependency tree handlers receive after run() boots.
type deps struct {
	cfg                      *Config
	pgPool                   *pgxpool.Pool
	adminQueries             *admindb.Queries
	billingQueries           *dbbilling.Queries
	billingPolicyStore       billing.PolicyStore
	billingPolicyResolver    *billing.PolicyResolver
	selector                 pool.Selector
	channelHealth            *channelhealth.Service
	claimGate                billing.ClaimGate
	settler                  billing.Settler
	replayStore              billing.ReplayStore
	forwarder                *gateway.StreamForwarder
	credentialVault          provider.CredentialVault
	credentialStore          *credentialstore.Store
	credentialKeys           credentialstore.KeyProvider
	credentialAcqStore       *credentialacq.PostgresSessionStore
	credentialExchangers     *credentialacq.ExchangerRegistry
	emailSettings            *mailinfra.PostgresSettingsStore
	authEmailSender          gatewayhttp.AuthEmailSender
	userAuth                 *userauth.Service
	userSessions             *usersession.Service
	userKeyService           *userkey.Service
	voucherService           *voucher.Service
	invitationService        *communityinvitation.Service
	dispatcher               *gateway.UpstreamDispatcher
	responseCache            l2cache.Store
	dlqService               *legacydlq.Service
	completionBus            *eventbus.Bus
	auditRefPolicy           *eventbus.AuditRefPolicy
	inboundAuth              *auth.APIKeyResolver
	auditLedger              auditledger.Ledger
	auditSigner              *sign.Signer
	auditPubkeyRegistry      auditledger.PubkeyRegistry
	receiptStore             *auditreceipt.PGXReceiptStorage
	receiptFormatter         *auditreceipt.ReceiptFormatter
	refundQueue              *auditreceipt.MismatchRefundQueue
	rateTableSource          billing.RateTableSource
	modelRegistry            *registry.PostgresRegistry
	routePlanner             *router.DefaultRouter
	adminAuth                *admin.AdminResolver
	adminIssuer              *admin.KeyIssuer
	adminRevoker             *admin.KeyRevoker
	billingAuditUpdater      gatewayhttp.AdminBillingSettingsAuditUpdater
	hermesService            *hermes.Service
	hermesRunner             *hermes.RunnerClient
	hermesChatBridge         *hermeschat.Bridge
	hermesKeyStore           *hermes.KeyStore
	hermesBootstrapIssuer    *hermes.BootstrapIssuer
	hermesRunnerSharedSecret []byte
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

type runtimeOptions struct {
	selector      *runtimeconfig.PoolSelectorConfig
	obsDLQ        *runtimeconfig.ObsDLQConfig
	eventBus      *runtimeconfig.EventBusConfig
	outboxRuntime obsoutbox.RuntimeConfig
	responseCache l2cache.Store
}

func buildTransportFactory(cfg *Config, mimicryRegistry *mimicry.TemplateRegistry) *transport.Factory {
	factory := transport.NewFactory(mimicryRegistry)
	if cfg != nil {
		factory.SidecarSocketPath = cfg.TransportSidecarSocket
	}
	return factory
}

type vendorRefresherBinding struct {
	name      string
	refresher credentialworker.Refresher
}

func buildVendorRefresherBindings(configs runtimeconfig.VendorOAuthConfigs, store *credentialstore.Store) []vendorRefresherBinding {
	configured := configs.Configured()
	bindings := make([]vendorRefresherBinding, 0, 5)
	if cfg, ok := configured[runtimeconfig.VendorOAuthCursor]; ok {
		bindings = append(bindings, vendorRefresherBinding{
			name: runtimeconfig.VendorOAuthCursor,
			refresher: providercursor.NewRefresher(store, providercursor.WithRefreshAdapter(providercursor.RefreshAdapter{
				TokenURL: cfg.TokenURL,
				ClientID: cfg.ClientID,
				Scope:    cfg.Scope,
			})),
		})
	}
	if cfg, ok := configured[runtimeconfig.VendorOAuthWindsurf]; ok {
		bindings = append(bindings, vendorRefresherBinding{
			name: runtimeconfig.VendorOAuthWindsurf,
			refresher: providerwindsurf.NewRefresher(store, providerwindsurf.WithRefreshAdapter(providerwindsurf.RefreshAdapter{
				TokenURL: cfg.TokenURL,
				ClientID: cfg.ClientID,
				Scope:    cfg.Scope,
			})),
		})
	}
	if cfg, ok := configured[runtimeconfig.VendorOAuthOpenAICodex]; ok {
		bindings = append(bindings, vendorRefresherBinding{
			name: runtimeconfig.VendorOAuthOpenAICodex,
			refresher: provideropenaicodex.NewRefresher(store, provideropenaicodex.WithRefreshAdapter(provideropenaicodex.RefreshAdapter{
				TokenURL: cfg.TokenURL,
				ClientID: cfg.ClientID,
				Scope:    cfg.Scope,
			})),
		})
	}
	if cfg, ok := configured[runtimeconfig.VendorOAuthKiro]; ok {
		bindings = append(bindings, vendorRefresherBinding{
			name: runtimeconfig.VendorOAuthKiro,
			refresher: providerkiro.NewRefresher(store, providerkiro.WithRefreshAdapter(providerkiro.RefreshAdapter{
				TokenURL:     cfg.TokenURL,
				ClientID:     cfg.ClientID,
				ClientSecret: cfg.ClientSecret,
			})),
		})
	}
	if cfg, ok := configured[runtimeconfig.VendorOAuthGemini]; ok {
		bindings = append(bindings, vendorRefresherBinding{
			name: runtimeconfig.VendorOAuthGemini,
			refresher: providergemini.NewRefresher(store, providergemini.WithRefreshAdapter(providergemini.RefreshAdapter{
				TokenURL:     cfg.TokenURL,
				ClientID:     cfg.ClientID,
				ClientSecret: cfg.ClientSecret,
				Scope:        cfg.Scope,
			})),
		})
	}
	return bindings
}

func buildVendorRefresherOptions(bindings []vendorRefresherBinding) []credentialworker.Option {
	opts := make([]credentialworker.Option, 0, len(bindings))
	for _, binding := range bindings {
		opts = append(opts, credentialworker.WithVendorRefresher(binding.name, binding.refresher))
	}
	return opts
}

func buildGatewayRuntime(ctx context.Context, cfg *Config, mimicryRegistry *mimicry.TemplateRegistry, logger *zap.Logger) (*gatewayRuntime, error) {
	pgPool, err := db.Open(ctx, db.PoolConfig{DSN: cfg.DatabaseURL})
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	rt := &gatewayRuntime{pgPool: pgPool}
	ready := false
	defer func() {
		if !ready {
			rt.close()
		}
	}()

	adminQueries := admindb.New(pgPool)
	authQueries := dbauth.New(pgPool)
	billingQueries := dbbilling.New(pgPool)
	hermesQueries := dbhermes.New(pgPool)
	hermesKeyStore := hermes.NewKeyStore(hermesQueries)
	hermesBootstrapIssuer, err := hermes.NewBootstrapIssuerFromEnv(hermesKeyStore)
	if err != nil {
		return nil, fmt.Errorf("build hermes bootstrap issuer: %w", err)
	}
	hermesRunner, err := hermes.NewRunnerClientFromEnv()
	if err != nil {
		return nil, fmt.Errorf("build hermes runner client: %w", err)
	}
	var hermesRunnerSharedSecret []byte
	var hermesService *hermes.Service
	if hermesRunner != nil || hermesBootstrapIssuer != nil {
		hermesRunnerSharedSecret, err = loadHermesInternalSharedSecret()
		if err != nil {
			return nil, err
		}
		hermesService = hermes.NewServiceWithTx(hermesQueries, pgPool)
		logger.Info("Hermes runner: configured")
	} else {
		logger.Info("Hermes runner: disabled (env missing)")
	}
	outboxStore := obsoutbox.NewPostgresOutbox(pgPool)
	opts, err := loadRuntimeOptions(logger)
	if err != nil {
		return nil, err
	}
	rt.outboxRuntime = opts.outboxRuntime
	auditRefPolicy := buildAuditRefPolicy(opts.eventBus)

	credentialKeys, err := loadCredentialKeyProvider()
	if err != nil {
		return nil, fmt.Errorf("load credential encryption key: %w", err)
	}
	credentialStore := credentialstore.NewStore(pgPool, credentialKeys, credentialstore.DefaultHandlerRegistry())
	credentialAcqStore := credentialacq.NewPostgresSessionStoreWithKeys(pgPool, credentialKeys)
	credentialExchangers := credentialacq.DefaultExchangerRegistry()
	if err := anthropicoauth.RegisterInto(credentialExchangers, anthropicoauth.NewExchanger()); err != nil {
		return nil, fmt.Errorf("register anthropic oauth exchanger: %w", err)
	}
	emailSettingsStore := mailinfra.NewPostgresSettingsStore(pgPool)
	if releaseModeProduction() {
		if err := mailinfra.ValidateProductionReleaseGate(ctx, emailSettingsStore, credentialKeys); err != nil {
			return nil, fmt.Errorf("production email release gate: %w", err)
		}
	}
	authEmailSender, err := buildAuthEmailSender(cfg, emailSettingsStore, credentialKeys, logger, outboxStore)
	if err != nil {
		return nil, fmt.Errorf("build auth email sender: %w", err)
	}
	userAuthService, userSessionService, err := buildUserServices(pgPool, credentialKeys, emailSettingsStore, logger)
	if err != nil {
		return nil, err
	}

	auditSigner, auditLedger, auditPubkeyRegistry, err := buildAuditServices(ctx, pgPool, logger)
	if err != nil {
		return nil, err
	}
	if err := requireProductionChannelHealthSigner(auditSigner); err != nil {
		return nil, err
	}
	channelHealthStoreOptions := []channelhealth.PostgresStoreOption{}
	if releaseModeProduction() {
		channelHealthStoreOptions = append(channelHealthStoreOptions, channelhealth.WithProductionRequired())
	}
	channelHealthStore := channelhealth.NewPostgresStoreWithAuditSigner(pgPool, auditSigner, channelHealthStoreOptions...)
	channelHealthService := channelhealth.NewService(channelHealthStore, channelhealth.DefaultPolicy(), nil, channelhealth.WithAlertOutbox(outboxStore))
	selector, selectorCleanup, err := buildSelector(ctx, billingQueries, pgPool, opts.selector, channelHealthService, logger)
	if err != nil {
		return nil, fmt.Errorf("build selector: %w", err)
	}
	rt.selectorCleanup = selectorCleanup

	dlqStore, dlqService, dlqWorker, replicaTarget, closeReplica := buildDLQRuntime(pgPool, opts.obsDLQ, auditLedger)
	rt.closeReplica = closeReplica
	outboxWorker := buildOutboxWorker(outboxStore, opts.outboxRuntime, emailSettingsStore, credentialKeys, channelHealthStore)
	settler, receiptStore, receiptFormatter, refundQueue, rateTableSource, completionBus, err := buildSettlementServices(
		ctx, pgPool, auditSigner, auditLedger, dlqStore, dlqService, replicaTarget, opts.eventBus, auditRefPolicy, logger,
	)
	if err != nil {
		return nil, err
	}

	// P2/P3 post-delivery settle 恢复:把 settler 注入 settlementrecovery
	// handler,注册到 dlqService 让 worker 拿到 post_delivery_settlement
	// 行后重调 Settler.Settle。三证 proof 用 PG 直接查 claim/usage/billing_events。
	settlementProof := settlementrecovery.NewPostgresCommittedProof(pgPool)
	settlementHandler := &settlementrecovery.Handler{
		Settler: settler,
		Proof:   settlementProof,
	}
	dlqService.Register(legacydlq.EventKindPostDeliverySettlement, settlementHandler.Handle)
	var hermesChatBridge *hermeschat.Bridge
	if hermesRunner != nil {
		hermesChatBridge, err = buildHermesChatBridge(hermesService, dlqService)
		if err != nil {
			return nil, err
		}
	}

	// 持久幂等重放存储 + 过期清理 janitor, 防表无界增长。
	replayStore := billing.NewReplayStore(pgPool)
	replayJanitor := billing.NewReplayJanitor(replayStore, 0)
	replayJanitor.Start(ctx)
	rt.replayJanitorStop = replayJanitor.Stop

	d := &deps{
		cfg:                   cfg,
		adminQueries:          adminQueries,
		billingQueries:        billingQueries,
		billingPolicyStore:    billing.NewPolicyStore(pgPool),
		billingPolicyResolver: billing.NewPolicyResolver(billing.NewPolicyStore(pgPool), 0),
		selector:              selector,
		channelHealth:         channelHealthService,
		claimGate:             billing.NewClaimGate(pgPool),
		settler:               settler,
		replayStore:           replayStore,
		forwarder:             buildStreamForwarder(auditLedger, auditSigner, dlqService),
		credentialVault:       provider.NewPostgresCredentialVaultWithStore(pgPool, credentialStore),
		credentialStore:       credentialStore,
		credentialKeys:        credentialKeys,
		credentialAcqStore:    credentialAcqStore,
		credentialExchangers:  credentialExchangers,
		emailSettings:         emailSettingsStore,
		authEmailSender:       authEmailSender,
		userAuth:              userAuthService,
		pgPool:                pgPool,
		userSessions:          userSessionService,
		userKeyService:        userkey.NewService(pgPool, nil),
		voucherService:        voucher.NewService(voucher.NewPostgresStore(pgPool)),
		invitationService:     communityinvitation.NewService(communityinvitation.NewPostgresStore(pgPool)),
		responseCache:         opts.responseCache,
		dlqService:            dlqService,
		completionBus:         completionBus,
		auditRefPolicy:        auditRefPolicy,
		dispatcher: &gateway.UpstreamDispatcher{
			Adapters:         registrydefault.Build(),
			TransportFactory: buildTransportFactory(cfg, mimicryRegistry),
			ProxyResolver:    provider.NewPostgresProxyResolver(pgPool),
		},
		inboundAuth:              auth.NewAPIKeyResolver(authQueries),
		auditLedger:              auditLedger,
		auditSigner:              auditSigner,
		auditPubkeyRegistry:      auditPubkeyRegistry,
		receiptStore:             receiptStore,
		receiptFormatter:         receiptFormatter,
		refundQueue:              refundQueue,
		rateTableSource:          rateTableSource,
		modelRegistry:            registry.NewPostgresRegistry(pgPool, nil),
		routePlanner:             router.NewDefaultRouter(),
		adminAuth:                admin.NewAdminResolver(adminQueries),
		adminIssuer:              admin.NewKeyIssuer(pgPool),
		adminRevoker:             admin.NewKeyRevoker(pgPool),
		billingAuditUpdater:      gatewayhttp.NewAdminBillingSettingsAuditUpdater(pgPool),
		hermesService:            hermesService,
		hermesRunner:             hermesRunner,
		hermesChatBridge:         hermesChatBridge,
		hermesKeyStore:           hermesKeyStore,
		hermesBootstrapIssuer:    hermesBootstrapIssuer,
		hermesRunnerSharedSecret: hermesRunnerSharedSecret,
	}
	if err := admin.MaybeBootstrap(ctx, pgPool, logger); err != nil {
		return nil, fmt.Errorf("admin bootstrap: %w", err)
	}
	// Production-required gate (RR-W5-002 步骤 3):credentialScheduler 必须装
	// authQueries + auditLedger + pgPool + auditSigner,否则 audit/ledger 链
	// 在 OAuth refresh 时静默失败,违 W5 D1/D4。Startup fail-fast 比 runtime
	// fail-closed 更早抓出 wiring 错配。
	if authQueries == nil {
		return nil, fmt.Errorf("credentialworker: production authQueries unset (audit fail-closed gate)")
	}
	if auditLedger == nil {
		return nil, fmt.Errorf("credentialworker: production auditLedger unset (audit fail-closed gate)")
	}
	if auditSigner == nil {
		return nil, fmt.Errorf("credentialworker: production auditSigner unset (audit fail-closed gate)")
	}
	credentialRefresher := credentialworker.NewAccountCredentialRefresher(credentialStore, credentialworker.DefaultModeAdapterRegistry())
	credentialSchedulerOptions := []credentialworker.Option{
		credentialworker.WithAuditQueries(authQueries),
		credentialworker.WithAuditLedger(auditLedger),
		credentialworker.WithRefreshQueries(credentialworker.NewAccountCredentialRefreshQueries(pgPool)),
		// 启用同事务路径 (RR-W5-002 步骤 1):audit insert + ledger append 同 tx。
		credentialworker.WithTxPool(pgPool),
		credentialworker.WithAuditLedgerSigner(auditSigner),
		credentialworker.WithVendorRefresher("anthropic", anthropicoauth.NewRefresher(
			credentialStore,
			anthropicoauth.WithFallbackRefresher(credentialRefresher),
		)),
		credentialworker.WithVendorRefresher("copilot", &providercopilot.CopilotRefresher{
			Store: providercopilot.NewCredentialStoreAdapter(credentialStore),
		}),
	}
	credentialSchedulerOptions = append(
		credentialSchedulerOptions,
		buildVendorRefresherOptions(buildVendorRefresherBindings(cfg.VendorOAuth, credentialStore))...,
	)
	credentialScheduler := credentialworker.NewScheduler(
		billingQueries,
		auth.NewStormController(authQueries),
		auditSigner,
		credentialRefresher,
		credentialSchedulerOptions...,
	)
	if err := credentialScheduler.Start(ctx); err != nil {
		return nil, fmt.Errorf("start credential refresh scheduler: %w", err)
	}
	if opts.obsDLQ.Enabled {
		dlqWorker.Start(ctx)
	}
	outboxWorker.Start(ctx)

	rt.deps = d
	rt.credentialScheduler = credentialScheduler
	rt.dlqWorker = dlqWorker
	rt.outboxWorker = outboxWorker
	rt.obsDLQEnabled = opts.obsDLQ.Enabled
	ready = true
	return rt, nil
}

func buildHermesChatBridge(hermesService *hermes.Service, dlqService *legacydlq.Service) (*hermeschat.Bridge, error) {
	if hermesService == nil {
		return nil, nil
	}
	internalSecret := strings.TrimSpace(os.Getenv(hermeschat.InternalTokenSecretEnv))
	if internalSecret == "" {
		return nil, fmt.Errorf("%w: %s is required for Hermes chat bridge", hermes.ErrMisconfigured, hermeschat.InternalTokenSecretEnv)
	}
	bridge, err := hermeschat.NewBridge(
		hermesService,
		hermeschat.WithInternalTokenSecret([]byte(internalSecret)),
		hermeschat.WithInternalBaseURL(envDefault(hermeschat.InternalBaseURLEnv, hermeschat.DefaultInternalBaseURL)),
		hermeschat.WithAuditDLQ(dlqService),
	)
	if err != nil {
		return nil, fmt.Errorf("build hermes chat bridge: %w", err)
	}
	return bridge, nil
}

func loadHermesInternalSharedSecret() ([]byte, error) {
	secret := strings.TrimSpace(os.Getenv(hermes.RunnerInternalSharedSecretEnv))
	if secret == "" {
		return nil, fmt.Errorf("%w: %s is required for Hermes internal routes", hermes.ErrMisconfigured, hermes.RunnerInternalSharedSecretEnv)
	}
	return []byte(secret), nil
}

func buildAuditRefPolicy(cfg *runtimeconfig.EventBusConfig) *eventbus.AuditRefPolicy {
	allowMissing := false
	if cfg != nil {
		allowMissing = cfg.AllowMissingMoneyRef
	}
	return &eventbus.AuditRefPolicy{
		ReleaseMode:          releaseMode(),
		AllowMissingMoneyRef: allowMissing,
	}
}
