package main

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
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
	legacydlq "github.com/BloomingProsperity/HUAKAI/internal/dlq"
	mailinfra "github.com/BloomingProsperity/HUAKAI/internal/email"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp"
	obsoutbox "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
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

// deps is the live dependency tree handlers receive after run() boots.
type deps struct {
	cfg                   *Config
	adminQueries          *admindb.Queries
	billingQueries        *dbbilling.Queries
	billingPolicyStore    billing.PolicyStore
	billingPolicyResolver *billing.PolicyResolver
	selector              pool.Selector
	channelHealth         *channelhealth.Service
	claimGate             billing.ClaimGate
	settler               billing.Settler
	replayStore           billing.ReplayStore
	forwarder             *gateway.StreamForwarder
	credentialVault       provider.CredentialVault
	credentialStore       *credentialstore.Store
	credentialKeys        credentialstore.KeyProvider
	credentialAcqStore    *credentialacq.PostgresSessionStore
	emailSettings         *mailinfra.PostgresSettingsStore
	authEmailSender       gatewayhttp.AuthEmailSender
	userAuth              *userauth.Service
	userSessions          *usersession.Service
	voucherService        *voucher.Service
	invitationService     *communityinvitation.Service
	dispatcher            *gateway.UpstreamDispatcher
	responseCache         l2cache.Store
	dlqService            *legacydlq.Service
	completionBus         *eventbus.Bus
	inboundAuth           *auth.APIKeyResolver
	auditLedger           auditledger.Ledger
	auditSigner           *sign.Signer
	auditPubkeyRegistry   auditledger.PubkeyRegistry
	receiptStore          *auditreceipt.PGXReceiptStorage
	receiptFormatter      *auditreceipt.ReceiptFormatter
	refundQueue           *auditreceipt.MismatchRefundQueue
	rateTableSource       billing.RateTableSource
	modelRegistry         *registry.PostgresRegistry
	routePlanner          *router.DefaultRouter
	adminAuth             *admin.AdminResolver
	adminIssuer           *admin.KeyIssuer
	adminRevoker          *admin.KeyRevoker
	billingAuditUpdater   gatewayhttp.AdminBillingSettingsAuditUpdater
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
	outboxStore := obsoutbox.NewPostgresOutbox(pgPool)
	opts, err := loadRuntimeOptions(logger)
	if err != nil {
		return nil, err
	}
	rt.outboxRuntime = opts.outboxRuntime

	credentialKeys, err := loadCredentialKeyProvider()
	if err != nil {
		return nil, fmt.Errorf("load credential encryption key: %w", err)
	}
	credentialStore := credentialstore.NewStore(pgPool, credentialKeys, credentialstore.DefaultHandlerRegistry())
	credentialAcqStore := credentialacq.NewPostgresSessionStoreWithKeys(pgPool, credentialKeys)
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
	channelHealthStore := channelhealth.NewPostgresStoreWithAuditSigner(pgPool, auditSigner)
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
		ctx, pgPool, auditSigner, auditLedger, dlqStore, dlqService, replicaTarget, opts.eventBus, logger,
	)
	if err != nil {
		return nil, err
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
		forwarder:             buildStreamForwarder(auditLedger, auditSigner),
		credentialVault:       provider.NewPostgresCredentialVaultWithStore(pgPool, credentialStore),
		credentialStore:       credentialStore,
		credentialKeys:        credentialKeys,
		credentialAcqStore:    credentialAcqStore,
		emailSettings:         emailSettingsStore,
		authEmailSender:       authEmailSender,
		userAuth:              userAuthService,
		userSessions:          userSessionService,
		voucherService:        voucher.NewService(voucher.NewPostgresStore(pgPool)),
		invitationService:     communityinvitation.NewService(communityinvitation.NewPostgresStore(pgPool)),
		responseCache:         opts.responseCache,
		dlqService:            dlqService,
		completionBus:         completionBus,
		dispatcher: &gateway.UpstreamDispatcher{
			Adapters:         registrydefault.Build(),
			TransportFactory: transport.NewFactory(mimicryRegistry),
			ProxyResolver:    provider.NewPostgresProxyResolver(pgPool),
		},
		inboundAuth:         auth.NewAPIKeyResolver(authQueries),
		auditLedger:         auditLedger,
		auditSigner:         auditSigner,
		auditPubkeyRegistry: auditPubkeyRegistry,
		receiptStore:        receiptStore,
		receiptFormatter:    receiptFormatter,
		refundQueue:         refundQueue,
		rateTableSource:     rateTableSource,
		modelRegistry:       registry.NewPostgresRegistry(pgPool, nil),
		routePlanner:        router.NewDefaultRouter(),
		adminAuth:           admin.NewAdminResolver(adminQueries),
		adminIssuer:         admin.NewKeyIssuer(pgPool),
		adminRevoker:        admin.NewKeyRevoker(pgPool),
		billingAuditUpdater: gatewayhttp.NewAdminBillingSettingsAuditUpdater(pgPool),
	}
	if err := admin.MaybeBootstrap(ctx, pgPool, logger); err != nil {
		return nil, fmt.Errorf("admin bootstrap: %w", err)
	}
	credentialScheduler := credentialworker.NewScheduler(
		billingQueries,
		auth.NewStormController(authQueries),
		auditSigner,
		credentialworker.NewAccountCredentialRefresher(credentialStore, credentialworker.DefaultModeAdapterRegistry()),
		credentialworker.WithAuditQueries(authQueries),
		credentialworker.WithAuditLedger(auditLedger),
		credentialworker.WithRefreshQueries(credentialworker.NewAccountCredentialRefreshQueries(pgPool)),
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
