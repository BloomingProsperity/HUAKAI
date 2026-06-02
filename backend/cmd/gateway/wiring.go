package main

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"os"
	"strconv"
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
	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
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
	"github.com/BloomingProsperity/HUAKAI/internal/modelsync"
	obsoutbox "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/payment"
	"github.com/BloomingProsperity/HUAKAI/internal/paymenthttp"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	providercopilot "github.com/BloomingProsperity/HUAKAI/internal/provider/copilot"
	providercursor "github.com/BloomingProsperity/HUAKAI/internal/provider/cursor"
	providergemini "github.com/BloomingProsperity/HUAKAI/internal/provider/gemini"
	providerkiro "github.com/BloomingProsperity/HUAKAI/internal/provider/kiro"
	provideropenaicodex "github.com/BloomingProsperity/HUAKAI/internal/provider/openai_codex"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
	providerwindsurf "github.com/BloomingProsperity/HUAKAI/internal/provider/windsurf"
	ratelimit "github.com/BloomingProsperity/HUAKAI/internal/rate"
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
	clientIPResolver         *clientip.Resolver
	pgPool                   *pgxpool.Pool
	adminQueries             *admindb.Queries
	billingQueries           *dbbilling.Queries
	billingPolicyStore       billing.PolicyStore
	billingPolicyResolver    *billing.PolicyResolver
	selector                 pool.Selector
	channelHealth            *channelhealth.Service
	modelCooldowns           *ratelimit.ModelCooldownService
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
	paymentService           *payment.Service
	paymentProviders         map[string]paymenthttp.ProviderBinding
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
	modelSync                *modelsync.Service
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
	modelSync     *runtimeconfig.ModelSyncConfig
	outboxRuntime obsoutbox.RuntimeConfig
	responseCache l2cache.Store
}

func buildTransportFactory(cfg *Config, mimicryRegistry *mimicry.TemplateRegistry) *transport.Factory {
	factory := transport.NewFactory(mimicryRegistry)
	if cfg != nil {
		factory.SidecarSocketPath = cfg.TransportSidecarSocket
		factory.SidecarFallbackEnabled = cfg.TransportSidecarFallback
	}
	return factory
}

type vendorRefresherBinding struct {
	name      string
	refresher credentialworker.Refresher
}

const (
	geminiPublicCLIOAuthClientSecretEnv = "HUAKAI_GEMINI_OAUTH_CLIENT_SECRET"
	adminOAuthCallbackAllowlistEnv      = "HUAKAI_ADMIN_OAUTH_CALLBACK_ALLOWLIST"
	userOAuthRedirectAllowlistEnv       = "HUAKAI_USER_OAUTH_REDIRECT_ALLOWLIST"
	// trustedProxyCIDRsEnv lists the reverse-proxy / CDN CIDRs whose X-Forwarded-For
	// is trusted for client-IP extraction (S2-109). Empty = direct exposure, RemoteAddr only.
	trustedProxyCIDRsEnv = "HUAKAI_TRUSTED_PROXY_CIDRS"
	// Refresh-storm endpoint/global token budgets (S2-045). Each scope needs BOTH
	// a positive rate (tokens/sec) and a burst >= 1 to engage; all unset = account
	// scope only. The account scope is always enforced (DB-durable), independent of these.
	stormEndpointRateEnv  = "HUAKAI_STORM_ENDPOINT_RATE"
	stormEndpointBurstEnv = "HUAKAI_STORM_ENDPOINT_BURST"
	stormGlobalRateEnv    = "HUAKAI_STORM_GLOBAL_RATE"
	stormGlobalBurstEnv   = "HUAKAI_STORM_GLOBAL_BURST"
)

func buildPaymentProviderBindings(cfg *Config) (map[string]paymenthttp.ProviderBinding, error) {
	if cfg == nil {
		return map[string]paymenthttp.ProviderBinding{}, nil
	}
	return paymenthttp.BuildProviderBindings(paymenthttp.ProviderRegistryConfig{
		HMACSecrets: cfg.PaymentHMACSecrets,
		EnableMock:  cfg.PaymentEnableMock,
		ReleaseMode: string(releaseMode()),
	})
}

// loadStormScopeConfigFromEnv parses the optional endpoint/global refresh-storm
// budgets (S2-045). A scope half-configured (only rate or only burst, or burst<1)
// is a boot error — fail loud rather than silently leaving the throttle off and
// letting a cross-account stampede through. All four unset => account scope only.
func loadStormScopeConfigFromEnv() (auth.StormScopeConfig, error) {
	endpointRate, err := parseStormFloatEnv(stormEndpointRateEnv)
	if err != nil {
		return auth.StormScopeConfig{}, err
	}
	endpointBurst, err := parseStormFloatEnv(stormEndpointBurstEnv)
	if err != nil {
		return auth.StormScopeConfig{}, err
	}
	globalRate, err := parseStormFloatEnv(stormGlobalRateEnv)
	if err != nil {
		return auth.StormScopeConfig{}, err
	}
	globalBurst, err := parseStormFloatEnv(stormGlobalBurstEnv)
	if err != nil {
		return auth.StormScopeConfig{}, err
	}
	if err := validateStormScopePair("endpoint", endpointRate, endpointBurst); err != nil {
		return auth.StormScopeConfig{}, err
	}
	if err := validateStormScopePair("global", globalRate, globalBurst); err != nil {
		return auth.StormScopeConfig{}, err
	}
	return auth.StormScopeConfig{
		PerEndpointRate:  endpointRate,
		PerEndpointBurst: endpointBurst,
		GlobalRate:       globalRate,
		GlobalBurst:      globalBurst,
	}, nil
}

func parseStormFloatEnv(name string) (float64, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid float %q: %w", name, raw, err)
	}
	if math.IsInf(v, 0) || math.IsNaN(v) {
		// ParseFloat accepts "Inf"/"NaN" without error; an infinite budget would
		// boot an effectively unbounded throttle and a NaN would silently disable
		// the scope — both defeat the fail-loud contract, so reject them here.
		return 0, fmt.Errorf("%s: must be a finite number, got %q", name, raw)
	}
	if v < 0 {
		return 0, fmt.Errorf("%s: must be non-negative, got %v", name, v)
	}
	return v, nil
}

// validateStormScopePair rejects a half-configured scope so a typo (rate set,
// burst forgotten) cannot silently disable the throttle.
func validateStormScopePair(scope string, rate, burst float64) error {
	switch {
	case rate == 0 && burst == 0:
		return nil // scope intentionally off
	case rate > 0 && burst >= 1:
		return nil // scope fully configured
	default:
		return fmt.Errorf("storm %s scope half-configured: rate=%v burst=%v (need both rate>0 and burst>=1, or both unset)", scope, rate, burst)
	}
}

// loadClientIPResolverFromEnv builds the trusted-proxy-aware client IP resolver from
// trustedProxyCIDRsEnv (comma-separated CIDR/IP). A malformed entry is a hard boot error
// (fail loud) rather than silently degrading the burst-limit / anomaly / voucher source.
func loadClientIPResolverFromEnv() (*clientip.Resolver, error) {
	return clientip.NewResolver(parseCSVAllowlistEnv(trustedProxyCIDRsEnv))
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
	if err := installAnthropicClaudeAIOAuthMimicryExchanger(credentialExchangers, anthropicoauth.DefaultHTTPClient()); err != nil {
		return nil, fmt.Errorf("register anthropic claude_ai_oauth exchanger with mimicry: %w", err)
	}
	geminiOAuthClientSecret, err := loadGeminiPublicCLIOAuthClientSecretFromEnv()
	if err != nil {
		return nil, err
	}
	adminOAuthCallbackAllowlist := loadAdminOAuthCallbackAllowlistFromEnv()
	if err := installGeminiPublicCLIOAuthExchangers(credentialExchangers, auth.NewSSRFProtectedOAuthClient(http.DefaultClient), geminiOAuthClientSecret, adminOAuthCallbackAllowlist); err != nil {
		return nil, fmt.Errorf("register gemini public CLI oauth exchangers: %w", err)
	}
	if err := installChatGPTOAuthExchanger(credentialExchangers, auth.NewSSRFProtectedOAuthClient(http.DefaultClient), adminOAuthCallbackAllowlist); err != nil {
		return nil, fmt.Errorf("register openai chatgpt_oauth exchanger: %w", err)
	}
	// ANT-4 fail-loud: wiring 启动时立即自检 install 真把 default registry
	// 中的 nil-client exchanger 替换为带显式 HTTP client 的版本。删除 install
	// 调用或 helper 实现退化时这里直接 return error, 进程拒启动 (生产 fingerprint
	// 失效是 S0 级事故, 不能 silently 退化)。
	if err := assertAnthropicClaudeAIOAuthExchangerHasHTTPClient(credentialExchangers); err != nil {
		return nil, fmt.Errorf("anthropic claude_ai_oauth mimicry wiring self-check: %w", err)
	}
	if err := assertGeminiPublicCLIOAuthExchangersHaveHTTPClient(credentialExchangers); err != nil {
		return nil, fmt.Errorf("gemini public CLI oauth wiring self-check: %w", err)
	}
	if err := assertChatGPTOAuthExchangerHasHTTPClient(credentialExchangers); err != nil {
		return nil, fmt.Errorf("openai chatgpt_oauth wiring self-check: %w", err)
	}
	// S1-008: OAuth ModePlan 自一致性闸。在全部 install 之后,对运行时真实使用的 registry 校验:
	// 每个 Kind==FlowKindOAuth 的 ModePlan 必须映射到一个非-fake、非-缺失的 exchanger(显式 fail-closed
	// 的暂停态视为合规)。若某 OAuth mode 仍是 fake(会把攻击者可影响的回调码当 JSON 凭据接受)或根本没
	// 注册 exchanger(只在回调期才暴露 ErrOAuthExchangerMissing),进程在此拒启动 —— 把配置漂移在 boot
	// 时变成 fatal,杜绝"暴露为可完成的 OAuth mode 实则映射 fake/缺失"这类信任边界回归。
	if err := credentialacq.ValidateOAuthModeConsistency(credentialacq.DefaultModePlans(), credentialExchangers); err != nil {
		return nil, fmt.Errorf("oauth mode-plan consistency self-check: %w", err)
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
	leaseSweeper := billing.NewLeaseSweeper(pgPool, settler, 0)
	leaseSweeper.Start(ctx)
	rt.leaseSweepStop = leaseSweeper.Stop
	pendingReconciler := billing.NewPendingReconciliationWorker(
		billing.NewPostgresPendingReconciliationFinalizer(pgPool),
		0,
		0,
		0,
	)
	pendingReconciler.Start(ctx)
	rt.pendingReconcileStop = pendingReconciler.Stop

	clientIPResolver, err := loadClientIPResolverFromEnv()
	if err != nil {
		return nil, fmt.Errorf("load trusted proxy client IP resolver: %w", err)
	}

	stormScopeCfg, err := loadStormScopeConfigFromEnv()
	if err != nil {
		return nil, fmt.Errorf("load refresh-storm scope budgets: %w", err)
	}
	paymentProviders, err := buildPaymentProviderBindings(cfg)
	if err != nil {
		return nil, fmt.Errorf("build payment provider bindings: %w", err)
	}
	modelRegistry := registry.NewPostgresRegistry(pgPool, nil)
	modelSyncService := buildModelSyncService(opts.modelSync, modelRegistry)

	d := &deps{
		cfg:                   cfg,
		clientIPResolver:      clientIPResolver,
		adminQueries:          adminQueries,
		billingQueries:        billingQueries,
		billingPolicyStore:    billing.NewPolicyStore(pgPool),
		billingPolicyResolver: billing.NewPolicyResolver(billing.NewPolicyStore(pgPool), 0),
		selector:              selector,
		channelHealth:         channelHealthService,
		modelCooldowns:        ratelimit.NewModelCooldownService(billingQueries),
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
		paymentService:        payment.NewService(payment.NewPostgresStore(pgPool)),
		paymentProviders:      paymentProviders,
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
		modelRegistry:            modelRegistry,
		modelSync:                modelSyncService,
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
		auth.NewStormControllerWithScopeBudget(authQueries, stormScopeCfg),
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
	if opts.modelSync != nil && opts.modelSync.Enabled && modelSyncService != nil {
		scheduler := modelsync.NewScheduler(modelSyncService, modelsync.SchedulerConfig{
			Interval:   opts.modelSync.Interval,
			RunOnStart: true,
		})
		rt.modelSyncStop = scheduler.Start(ctx)
	}

	rt.deps = d
	rt.credentialScheduler = credentialScheduler
	rt.dlqWorker = dlqWorker
	rt.outboxWorker = outboxWorker
	rt.obsDLQEnabled = opts.obsDLQ.Enabled
	ready = true
	return rt, nil
}

func buildModelSyncService(cfg *runtimeconfig.ModelSyncConfig, store *registry.PostgresRegistry) *modelsync.Service {
	if cfg == nil || store == nil {
		return nil
	}
	fetchers := make([]modelsync.Fetcher, 0, 3)
	if cfg.OpenAI.Configured() {
		fetchers = append(fetchers, modelsync.NewHTTPFetcher(modelsync.HTTPFetcherConfig{
			Vendor:  modelsync.VendorOpenAI,
			URL:     cfg.OpenAI.URL,
			APIKey:  cfg.OpenAI.APIKey,
			Timeout: cfg.Timeout,
		}))
	}
	if cfg.Anthropic.Configured() {
		fetchers = append(fetchers, modelsync.NewHTTPFetcher(modelsync.HTTPFetcherConfig{
			Vendor:  modelsync.VendorAnthropic,
			URL:     cfg.Anthropic.URL,
			APIKey:  cfg.Anthropic.APIKey,
			Timeout: cfg.Timeout,
		}))
	}
	if cfg.Gemini.Configured() {
		fetchers = append(fetchers, modelsync.NewHTTPFetcher(modelsync.HTTPFetcherConfig{
			Vendor:  modelsync.VendorGemini,
			URL:     cfg.Gemini.URL,
			APIKey:  cfg.Gemini.APIKey,
			Timeout: cfg.Timeout,
		}))
	}
	if len(fetchers) == 0 {
		return nil
	}
	return modelsync.NewService(modelsync.ServiceConfig{
		Fetchers: fetchers,
		Store:    store,
	})
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

// installAnthropicClaudeAIOAuthMimicryExchanger 把 default ExchangerRegistry
// 中 anthropic/claude_ai_oauth 条目替换成带显式 HTTP client 的版本。生产
// wiring 传 anthropicoauth.DefaultHTTPClient() 接 mimicry uTLS sidecar
// (profile anthropic_cli_mimicry_v1); 测试可注入 mock client 验证替换
// 真正生效 — 这是 ANT-4 的判别 fixture, 防止"忘调用该函数"的回归 (codex
// R1 抓出 wiring 直接 inline 时 wiring_test 杀不掉 mutation 的问题)。
func installAnthropicClaudeAIOAuthMimicryExchanger(registry *credentialacq.ExchangerRegistry, client *http.Client) error {
	if registry == nil {
		return fmt.Errorf("nil exchanger registry")
	}
	if client == nil {
		return fmt.Errorf("nil http client (mimicry transport missing — 不允许 silently 退化到 http.DefaultClient)")
	}
	return registry.RegisterOrReplaceExchanger(
		credentialstore.ModeKey(credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeAIOAuth),
		credentialacq.NewClaudeAIOAuthExchangerWithClient(client),
	)
}

// assertAnthropicClaudeAIOAuthExchangerHasHTTPClient 在 wiring 完成 install
// 后立即自检 default registry 中 anthropic/claude_ai_oauth 真被替换成带显式
// HTTP client 的版本。删除 install 调用或 helper 退化时进程拒启动 — 比单元
// test 更稳, 因为 unit test 隔离调 helper, 看不到 wiring 是否真调用 helper。
func assertAnthropicClaudeAIOAuthExchangerHasHTTPClient(registry *credentialacq.ExchangerRegistry) error {
	if registry == nil {
		return fmt.Errorf("nil exchanger registry")
	}
	exc, ok := registry.Lookup(credentialstore.ModeKey(credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeAIOAuth))
	if !ok {
		return fmt.Errorf("anthropic/claude_ai_oauth exchanger missing from registry after install")
	}
	if !credentialacq.IsClaudeAIOAuthExchangerWithExplicitClient(exc) {
		return fmt.Errorf("anthropic/claude_ai_oauth exchanger has nil httpClient — install 未生效, 生产将退化为 http.DefaultClient 失去 mimicry uTLS")
	}
	return nil
}

func loadGeminiPublicCLIOAuthClientSecretFromEnv() (string, error) {
	return strings.TrimSpace(os.Getenv(geminiPublicCLIOAuthClientSecretEnv)), nil
}

func loadAdminOAuthCallbackAllowlistFromEnv() []string {
	return parseCSVAllowlistEnv(adminOAuthCallbackAllowlistEnv)
}

// loadUserOAuthRedirectAllowlistFromEnv 加载 social OAuth init 允许的 caller redirect_uri 白名单(S2-009);
// 空 = 不接受任何 caller redirect_uri,只用 provider 服务端固定 RedirectURI(fail-closed)。
func loadUserOAuthRedirectAllowlistFromEnv() []string {
	return parseCSVAllowlistEnv(userOAuthRedirectAllowlistEnv)
}

// parseCSVAllowlistEnv 解析逗号分隔的 env 白名单,trim 空项;全空/未设返回 nil。
func parseCSVAllowlistEnv(name string) []string {
	raw := os.Getenv(name)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	allowlist := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		allowlist = append(allowlist, item)
	}
	if len(allowlist) == 0 {
		return nil
	}
	return allowlist
}

// installGeminiPublicCLIOAuthExchangers 把默认 registry 中 Gemini code_assist /
// google_one 条目替换为带显式 OAuth-grade HTTP client 的版本。client 由调用方
// 构造，生产 wiring 传 auth.NewSSRFProtectedOAuthClient，secret 与 admin callback
// allowlist 必须来自 operator env。secret 缺失时仍安装受控 HTTP client；Gemini
// public CLI OAuth 在 StartOAuthFlow 边界因缺 client_secret fail-closed，避免
// 非 Gemini 部署被全局启动依赖阻断。
func installGeminiPublicCLIOAuthExchangers(registry *credentialacq.ExchangerRegistry, client *http.Client, clientSecret string, allowlist []string) error {
	if registry == nil {
		return fmt.Errorf("nil exchanger registry")
	}
	if client == nil {
		return fmt.Errorf("nil http client (gemini OAuth transport missing)")
	}
	for _, mode := range []string{credentialstore.AuthModeCodeAssist, credentialstore.AuthModeGoogleOne} {
		if err := registry.RegisterOrReplaceExchanger(
			credentialstore.ModeKey(credentialstore.VendorGemini, mode),
			credentialacq.NewGeminiPublicCLIOAuthExchangerWithClientSecretAndAdminCallbackAllowlist(mode, client, clientSecret, allowlist),
		); err != nil {
			return err
		}
	}
	return nil
}

// assertGeminiPublicCLIOAuthExchangersHaveHTTPClient 在 wiring 完成 install 后
// 立即自检 Gemini OAuth exchanger 已注入 HTTP client。删除 install 调用或漏
// 任一 auth mode 时进程拒启动，防止 callback 交换链路 silent 退化。
func assertGeminiPublicCLIOAuthExchangersHaveHTTPClient(registry *credentialacq.ExchangerRegistry) error {
	if registry == nil {
		return fmt.Errorf("nil exchanger registry")
	}
	for _, mode := range []string{credentialstore.AuthModeCodeAssist, credentialstore.AuthModeGoogleOne} {
		exc, ok := registry.Lookup(credentialstore.ModeKey(credentialstore.VendorGemini, mode))
		if !ok {
			return fmt.Errorf("gemini/%s exchanger missing from registry after install", mode)
		}
		if !credentialacq.IsGeminiPublicCLIOAuthExchangerWithExplicitClient(exc) {
			return fmt.Errorf("gemini/%s exchanger has nil httpClient — install 未生效", mode)
		}
	}
	return nil
}

// installChatGPTOAuthExchanger 把默认 registry 中 openai/chatgpt_oauth 条目
// 替换为带显式 OAuth-grade HTTP client 的版本。ChatGPT OAuth 是 PKCE-only，
// 不需要 client_secret；client 由调用方构造，生产 wiring 传 SSRF-protected
// client，admin callback allowlist 来自 operator env，避免 callback token exchange
// silent 退化到默认 transport。
func installChatGPTOAuthExchanger(registry *credentialacq.ExchangerRegistry, client *http.Client, allowlist []string) error {
	if registry == nil {
		return fmt.Errorf("nil exchanger registry")
	}
	if client == nil {
		return fmt.Errorf("nil http client (chatgpt OAuth transport missing)")
	}
	return registry.RegisterOrReplaceExchanger(
		credentialstore.ModeKey(credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth),
		credentialacq.NewChatGPTOAuthExchangerWithClientAndAdminCallbackAllowlist(client, allowlist),
	)
}

// assertChatGPTOAuthExchangerHasHTTPClient 在 wiring 完成 install 后立即自检
// ChatGPT OAuth exchanger 已注入 HTTP client。删除 install 调用或 helper 退化
// 时进程拒启动，防止 callback 交换链路 silent 退化。
func assertChatGPTOAuthExchangerHasHTTPClient(registry *credentialacq.ExchangerRegistry) error {
	if registry == nil {
		return fmt.Errorf("nil exchanger registry")
	}
	exc, ok := registry.Lookup(credentialstore.ModeKey(credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth))
	if !ok {
		return fmt.Errorf("openai/chatgpt_oauth exchanger missing from registry after install")
	}
	if !credentialacq.IsChatGPTOAuthExchangerWithExplicitClient(exc) {
		return fmt.Errorf("openai/chatgpt_oauth exchanger has nil httpClient — install 未生效")
	}
	return nil
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
