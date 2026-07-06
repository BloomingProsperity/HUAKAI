package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauth"
	"github.com/BloomingProsperity/HUAKAI/internal/alerting"
	"github.com/BloomingProsperity/HUAKAI/internal/alertmetrics"
	"github.com/BloomingProsperity/HUAKAI/internal/announcement"
	"github.com/BloomingProsperity/HUAKAI/internal/anthropicoauth"
	"github.com/BloomingProsperity/HUAKAI/internal/apikeyexpiry"
	auditreceipt "github.com/BloomingProsperity/HUAKAI/internal/audit"
	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/authcooldown"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/budget"
	"github.com/BloomingProsperity/HUAKAI/internal/budgetenforce"
	l2cache "github.com/BloomingProsperity/HUAKAI/internal/cache"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/checkin"
	"github.com/BloomingProsperity/HUAKAI/internal/circuitbreaker"
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
	hermestoolsdb "github.com/BloomingProsperity/HUAKAI/internal/db/hermestoolsdb"
	"github.com/BloomingProsperity/HUAKAI/internal/dbmigrate"
	legacydlq "github.com/BloomingProsperity/HUAKAI/internal/dlq"
	mailinfra "github.com/BloomingProsperity/HUAKAI/internal/email"
	"github.com/BloomingProsperity/HUAKAI/internal/emailsendlimit"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/hermeschat"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesconfirm"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops/mutateguard"
	"github.com/BloomingProsperity/HUAKAI/internal/loginthrottle"
	"github.com/BloomingProsperity/HUAKAI/internal/mediatask"
	"github.com/BloomingProsperity/HUAKAI/internal/modelsync"
	"github.com/BloomingProsperity/HUAKAI/internal/moduleregistry"
	"github.com/BloomingProsperity/HUAKAI/internal/notify"
	obsoutbox "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/obsconfig"
	"github.com/BloomingProsperity/HUAKAI/internal/otelbridge"
	"github.com/BloomingProsperity/HUAKAI/internal/panelauth"
	"github.com/BloomingProsperity/HUAKAI/internal/passkey"
	"github.com/BloomingProsperity/HUAKAI/internal/payment"
	"github.com/BloomingProsperity/HUAKAI/internal/paymenthttp"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/pool/queuewait"
	"github.com/BloomingProsperity/HUAKAI/internal/pricingcatalog"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	providercopilot "github.com/BloomingProsperity/HUAKAI/internal/provider/copilot"
	providercursor "github.com/BloomingProsperity/HUAKAI/internal/provider/cursor"
	providergemini "github.com/BloomingProsperity/HUAKAI/internal/provider/gemini"
	providerkiro "github.com/BloomingProsperity/HUAKAI/internal/provider/kiro"
	provideropenaicodex "github.com/BloomingProsperity/HUAKAI/internal/provider/openai_codex"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
	providerwindsurf "github.com/BloomingProsperity/HUAKAI/internal/provider/windsurf"
	"github.com/BloomingProsperity/HUAKAI/internal/proxyhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/quota"
	"github.com/BloomingProsperity/HUAKAI/internal/quotaenforce"
	ratelimit "github.com/BloomingProsperity/HUAKAI/internal/rate"
	"github.com/BloomingProsperity/HUAKAI/internal/recentreq"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/retrybudget"
	"github.com/BloomingProsperity/HUAKAI/internal/routeadmin"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/sessioncap"
	"github.com/BloomingProsperity/HUAKAI/internal/settingscipher"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementrecovery"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/BloomingProsperity/HUAKAI/internal/subscription"
	"github.com/BloomingProsperity/HUAKAI/internal/tenancy"
	"github.com/BloomingProsperity/HUAKAI/internal/tlsfphealth"
	"github.com/BloomingProsperity/HUAKAI/internal/tlsfpresolve"
	"github.com/BloomingProsperity/HUAKAI/internal/toolpricing"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
	"github.com/BloomingProsperity/HUAKAI/internal/twofa"
	"github.com/BloomingProsperity/HUAKAI/internal/usageretention"
	"github.com/BloomingProsperity/HUAKAI/internal/userauditlog"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
	"github.com/BloomingProsperity/HUAKAI/internal/userkey"
	"github.com/BloomingProsperity/HUAKAI/internal/usernotice"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
	"github.com/BloomingProsperity/HUAKAI/internal/voucher"
	"github.com/BloomingProsperity/HUAKAI/internal/windowcost"
	sqlmigrations "github.com/BloomingProsperity/HUAKAI/sql"
)

// deps 是 run() 启动后 handler 收到的 live 依赖树。
type deps struct {
	cfg                   *Config
	clientIPResolver      *clientip.Resolver
	pgPool                *pgxpool.Pool
	adminQueries          *admindb.Queries
	billingQueries        *dbbilling.Queries
	billingPolicyStore    billing.PolicyStore
	billingPolicyResolver *billing.PolicyResolver
	selector              pool.Selector
	queueWaiter           *queuewait.Executor
	channelHealth         *channelhealth.Service
	authCooldown          *authcooldown.Store
	modelCooldowns        *ratelimit.ModelCooldownService
	upstreamRate          ratelimit.Service
	retryBudget           *retrybudget.Budget
	claimGate             billing.ClaimGate
	settler               billing.Settler
	quotaReserver         quotaenforce.Reserver
	replayStore           billing.ReplayStore
	forwarder             *gateway.StreamForwarder
	credentialVault       provider.CredentialVault
	credentialStore       *credentialstore.Store
	credentialKeys        credentialstore.KeyProvider
	credentialAcqStore    *credentialacq.PostgresSessionStore
	credentialExchangers  *credentialacq.ExchangerRegistry
	credentialScheduler   *credentialworker.Scheduler
	emailSettings         *mailinfra.PostgresSettingsStore
	authEmailSender       gatewayhttp.AuthEmailSender
	emailSendLimit        *emailsendlimit.Limiter
	userAuth              *userauth.Service
	userSessions          *usersession.Service
	passkeys              *passkey.Service
	twoFactor             *twofa.Service
	loginThrottle         *loginthrottle.Limiter
	userKeyService        *userkey.Service
	userAuditStore        *userauditlog.PostgresStore
	paymentService        *payment.Service
	checkinService        *checkin.Service
	paymentProviders      map[string]paymenthttp.ProviderBinding
	paymentRefundRequests paymenthttp.RefundRequestRecorder
	voucherService        *voucher.Service
	subscriptionService   *subscription.Service
	subExpiryWorker       *subscription.ExpiryWorker
	subReminderWorker     *subscription.ReminderWorker
	subAutoRenewWorker    *subscription.AutoRenewWorker
	notificationSettings  *notify.Service
	announcementService   *announcement.Service
	userNoticeService     *usernotice.Service
	mediaTaskService      *mediatask.Service
	mediaTaskWorker       *mediatask.Worker
	// mediaTaskStore 既供 worker/service 用,也供孤儿对账 admin 面(orphanreconcilehttp)
	// 复用其只读列表 + 单一动钱入口 ReconcileOrphan(Manual-First,复用既有 billing settle)。
	mediaTaskStore           *mediatask.PostgresStore
	routeAdminService        *routeadmin.Service
	panelAuthResolver        *panelauth.Resolver
	invitationService        *communityinvitation.Service
	dispatcher               *gateway.UpstreamDispatcher
	responseCache            l2cache.Store
	cacheScope               string
	dlqService               *legacydlq.Service
	obsDLQAdminStore         *obsoutbox.PostgresOutbox
	completionBus            *eventbus.Bus
	auditRefPolicy           *eventbus.AuditRefPolicy
	inboundAuth              *auth.APIKeyResolver
	auditLedger              auditledger.Ledger
	auditSigner              *sign.Signer
	cacheOverrideStore       *billing.CacheOverrideStore
	auditPubkeyRegistry      auditledger.PubkeyRegistry
	receiptStore             *auditreceipt.PGXReceiptStorage
	receiptFormatter         *auditreceipt.ReceiptFormatter
	disputeStore             *auditreceipt.CostDisputeStore
	refundQueue              *auditreceipt.MismatchRefundQueue
	rateTableSource          *billing.PGXRateTableSource
	pricingRatioStore        pricingcatalog.Store
	pricingRatioResolver     *pricingcatalog.RatioResolver
	modelRegistry            *registry.PostgresRegistry
	modelSync                *modelsync.Service
	routePlanner             *router.DefaultRouter
	adminAuth                *adminsessionauth.Resolver
	adminIssuer              *admin.KeyIssuer
	adminRevoker             *admin.KeyRevoker
	adminTokenIssuer         *admin.AdminTokenIssuer
	billingAuditUpdater      gatewayhttp.AdminBillingSettingsAuditUpdater
	platformSettings         *platformsettings.Service
	hermesService            *hermes.Service
	hermesRunner             *hermes.RunnerClient
	hermesChatBridge         *hermeschat.Bridge
	hermesKeyStore           *hermes.KeyStore
	hermesBootstrapIssuer    *hermes.BootstrapIssuer
	hermesRunnerSharedSecret []byte
	// hermesAdminOnly 为 true(默认)时,把 Hermes 重新门控为仅
	// ADMIN/OPERATOR 鉴权。为 false 时,旧的终端用户 APIKeyMiddleware 路径被
	// 逐字保留以便干净回滚。经 envBoolDefault(..., true) 从
	// HUAKAI_HERMES_ADMIN_ONLY 取值。
	hermesAdminOnly      bool
	metricsHandler       http.Handler
	otelShutdown         func(context.Context) error
	usageRetentionWorker *usageretention.Worker
	sessionCapRegistry   *sessioncap.Registry
	recentReqRing        *recentreq.Ring
	// toolPriceSource 提供工具调用附加费价表来源(NAPI-BILLING-01)。按运维开关
	// HUAKAI_TOOL_SURCHARGE_ENABLED 构建:启用 → 带平台默认价的 platformSource;
	// 关闭 → nil(退回旧 $0 行为)。接入 chatHandlerDeps 的 ToolPricingTable。
	toolPriceSource toolpricing.Source
	// moduleRegistry 是 WAVE H2 运行时的模块知识脊柱,由 admin 的
	// /admin/v1/modules 端点(以及后续的 Hermes 运维助手)查询。它不在任何
	// 请求热路径上;在 buildGatewayRuntime 接近结尾、probe 引用的服务都接好后填充。
	moduleRegistry *moduleregistry.Registry
	// WAVE H3 只读运维脊柱:诊断工具 registry、hermes_tool_calls 审计写入器,
	// 以及模块上下文 source。三者都不在请求热路径上;挂在 H1 admin 门控的
	// /v1/hermes 之下。
	hermesToolRegistry *hermesops.Registry
	hermesToolCalls    *hermestoolsdb.Queries
	hermesModuleSource *moduleSource
	// hermesMutator 运行 WAVE H4 mutating-tool 的原子审计 + advisory-lock 事务。
	// pool 未设置时为 nil(此时 mutating 工具 fail closed)。
	// 用 S2 并发上限 + 事务 deadline 的 orchestrator 选项构建。
	hermesMutator *hermesops.MutateOrchestrator
	// hermesConfirmCache 是 Hermes mutating-tool 的 dry-run→confirm 进程内单次令牌 store 的
	// **共享单例**。构造一次,注入 hermeshttp 的 operator 确认侧(本 PR);后续 Phase B 会把同一实例
	// 注入 hermeschat 的 LLM 提议侧,使提议发的 correlation_id 能被 operator 的确认消费(同一进程内)。
	hermesConfirmCache *hermesconfirm.Cache
	// hermesMutateRateLimiter 是 S2 (c) 的按 operator-token 滑动窗口限流器,
	// 在 mutate handler 中强制。rate 旋钮为 0 时为 nil/禁用(旧的无界行为)。
	// 仅在 admin-only 的 mutator 路径生效时挂载。
	hermesMutateRateLimiter *mutateguard.RateLimiter
	// hermesMutatingEnabled 是所有 Hermes mutating 工具的运行时总开关(KNOB A)。
	// 默认 true(HUAKAI_HERMES_MUTATING_ENABLED)。为 false 时,tool-execute 的
	// mutating 分支被拒绝(403 hermes_mutating_disabled),且 mutator 也不会被挂入
	// router,而只读诊断 + chat 仍然在线。
	hermesMutatingEnabled bool
	// hermesToolLoopEnabled 是 LLM 对话式工具循环的运行时总开关(KNOB B)。
	// 默认 true(HUAKAI_HERMES_LLM_TOOLLOOP_ENABLED)。为 false 时,不向 chat
	// 请求体注入任何只读工具 catalog,且 runner 的 /internal/hermes/tool-execute
	// 回调被拒绝(403 llm_toolloop_disabled),而普通的 /v1/hermes/chat 仍持续流式。
	hermesToolLoopEnabled bool
	// hermesProposeEnabled 是 Phase B 提议 KNOB(默认 FALSE,HUAKAI_HERMES_LLM_PROPOSE_ENABLED)。
	// 为 false 时,internal handler 在任何 dry-run 解析之前拒绝每个 mode=propose 调用
	//(403 llm_propose_disabled),故 LLM 无法提议任何 mutating 工具。默认关意味着接入提议路径在
	// Owner 翻开它之前是零生产行为变。额外受 hermesToolLoopEnabled(KNOB B)门控。
	hermesProposeEnabled bool
	// WAVE H3b 对话式只读工具循环:共享的 session-binding store
	//(每个 chat 会话的 operator 身份,以 internal_token 的 request_id 为键),
	// 以及 runner 回调进来的 internal tool-execute handler。在 admin-only 重定位
	// 之外、或 chat bridge 未设置时,该 handler 为 nil。
	hermesSessionBindings     *hermeschat.SessionBindings
	hermesInternalToolHandler *hermeschat.InternalToolHandler
}

func quotaReconcilerEnabledFromEnv() bool {
	raw, ok := os.LookupEnv("HUAKAI_QUOTA_RECONCILER_ENABLED")
	if !ok || strings.TrimSpace(raw) == "" {
		return true
	}
	on, err := strconv.ParseBool(raw)
	if err != nil {
		return true
	}
	return on
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
	cacheScope    string
}

// newClaimGateWithLease 构造 claim gate 并按 env 设置孤儿回收租约窗口。
// HUAKAI_BILLING_CLAIM_LEASE 默认 billing.DefaultClaimLeaseWindow(30min),
// 必须 > HUAKAI_STREAM_TOTAL_TIMEOUT(默认 600s)+ 结算/DLQ 余量,否则跑得久的
// 合法流式请求会被 LeaseSweeper 在传输中误 Abort(亏钱 + 超并发)。留 env 开关可回退。
func newClaimGateWithLease(pool *pgxpool.Pool) billing.ClaimGate {
	cg := billing.NewClaimGate(pool)
	if cg.LeaseWindow > 0 {
		cg.LeaseWindow = streamDurationEnv("HUAKAI_BILLING_CLAIM_LEASE", billing.DefaultClaimLeaseWindow)
	}
	return cg
}

func buildTransportFactory(cfg *Config, mimicryRegistry *mimicry.TemplateRegistry) *transport.Factory {
	factory := transport.NewFactory(mimicryRegistry)
	if cfg != nil {
		factory.SidecarSocketPath = cfg.TransportSidecarSocket
		factory.SidecarFallbackEnabled = cfg.TransportSidecarFallback
		// nil 时保持 factory 默认(沿用 mimicry env 默认强制 H1);非 nil 时运维显式覆盖。
		factory.SidecarForceH1 = cfg.TransportSidecarForceH1
	}
	return factory
}

type alertingEvaluatorRunner interface {
	Run(context.Context) error
}

type apiKeyExpiryWorker interface {
	Start(context.Context)
	Stop()
}

func startAPIKeyExpiryWorker(ctx context.Context, cfg *Config, worker apiKeyExpiryWorker) func() {
	if cfg == nil || !cfg.APIKeyExpirySweepEnabled || worker == nil {
		return nil
	}
	worker.Start(ctx)
	return worker.Stop
}

type notifyFiringDeliverer struct {
	notifier *notify.Notifier
}

func (d notifyFiringDeliverer) DeliverFiring(ctx context.Context, tenantID int64, notice alerting.FiringNotice) error {
	if d.notifier == nil {
		return nil
	}
	return d.notifier.NotifyAlertFiring(ctx, tenantID, notify.AlertFiringInfo{
		RuleName:      notice.RuleName,
		Metric:        notice.Metric,
		MetricType:    string(notice.MetricType),
		Comparator:    string(notice.Comparator),
		Threshold:     notice.Threshold,
		Severity:      string(notice.Severity),
		ObservedValue: notice.ObservedValue,
		Dimensions:    notice.Dimensions,
		FiredAt:       notice.FiredAt,
	})
}

// providerAccountDownDeliverer 把一次触发了 operator-alert 标志(CRED-293)的
// credential-worker 健康状态切换,映射到 notify.NotifyProviderAccountDown,
// 复用与告警触发相同的多渠道广播管线。与 notifyFiringDeliverer 平行。
type providerAccountDownDeliverer struct {
	notifier *notify.Notifier
}

func (d providerAccountDownDeliverer) DeliverProviderAccountDown(ctx context.Context, change credentialworker.ProviderAccountHealthChange, outcome auth.Outcome) error {
	if d.notifier == nil {
		return nil
	}
	return d.notifier.NotifyProviderAccountDown(ctx, change.TenantID, notify.ProviderAccountDownInfo{
		ProviderAccountID: change.ProviderAccountID,
		VendorName:        change.VendorName,
		HealthState:       change.HealthState,
		Outcome:           string(outcome),
		Severity:          string(alerting.SeverityCritical),
	})
}

func startAlertingEvaluator(ctx context.Context, cfg *Config, runner alertingEvaluatorRunner, logger *zap.Logger) func() {
	if cfg == nil || !cfg.AlertingEvalEnabled || runner == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := runner.Run(runCtx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Warn("alerting evaluator stopped with error", zap.Error(err))
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
}

type vendorRefresherBinding struct {
	name      string
	refresher credentialworker.Refresher
}

const (
	geminiPublicCLIOAuthClientSecretEnv = "HUAKAI_GEMINI_OAUTH_CLIENT_SECRET"
	adminOAuthCallbackAllowlistEnv      = "HUAKAI_ADMIN_OAUTH_CALLBACK_ALLOWLIST"
	userOAuthRedirectAllowlistEnv       = "HUAKAI_USER_OAUTH_REDIRECT_ALLOWLIST"
	// trustedProxyCIDRsEnv 列出反向代理 / CDN 的 CIDR,这些来源的 X-Forwarded-For
	// 在提取 client-IP 时被信任。为空 = 直连暴露,只用 RemoteAddr。
	trustedProxyCIDRsEnv = "HUAKAI_TRUSTED_PROXY_CIDRS"
	// Refresh-storm 的端点/全局令牌预算。每个 scope 都需要同时有一个正的 rate
	//(令牌/秒)和一个 burst >= 1 才会生效;全部未设置 = 仅账号 scope。账号 scope
	// 始终强制(DB 持久化),与这些 env 无关。
	stormEndpointRateEnv  = "HUAKAI_STORM_ENDPOINT_RATE"
	stormEndpointBurstEnv = "HUAKAI_STORM_ENDPOINT_BURST"
	stormGlobalRateEnv    = "HUAKAI_STORM_GLOBAL_RATE"
	stormGlobalBurstEnv   = "HUAKAI_STORM_GLOBAL_BURST"
	tenantRetryBudgetEnv  = "HUAKAI_TENANT_RETRY_BUDGET"
	tenantRetryWindowEnv  = "HUAKAI_TENANT_RETRY_WINDOW"
	// CRED-288 凭据轮换扫描:credentialworker 每个 tick 在 refresh pass 之后
	// 把签发 (created_at) 超过 maxAge 的 active 凭据置为 needs_rotation。
	// 默认 OFF(maxAge 留空 => 0 => 关),因为 needs_rotation 不是 serving 状态
	// (resolveActiveQuery / LoadForRefresh 都排除它),且没有自动恢复路径——
	// CRED-288c 恢复闭环落地后默认【开启】(留空=90 天窗口):超期凭据不再被粗暴下线
	// ——可刷新凭据保持在线自愈、静态 key 仅告警,故默认开不造成可用性/计费回退。
	// 取值:Go duration("2160h"=90 天)或裸秒数;显式设 0 关闭;留空=默认 90 天。
	credentialRotationMaxAgeEnv = "HUAKAI_CREDENTIAL_ROTATION_MAX_AGE"
	// 每个 tick 最多置标的行数上限(<=0 => 走 store 默认 100),用于把"超期积压"
	// 分摊到多个 tick、避免一次性把大量账号打入 needs_rotation。
	credentialRotationLimitEnv = "HUAKAI_CREDENTIAL_ROTATION_LIMIT"
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

func paymentServiceOptions(cfg *Config) []payment.Option {
	if cfg == nil {
		return nil
	}
	var opts []payment.Option
	if cfg.PaymentEnableMock {
		opts = append(opts, payment.WithTestProvider())
	}
	if cfg.PaymentTaobaoEnabled {
		opts = append(opts, payment.WithTaobaoProvider(cfg.PaymentTaobaoCheckoutURL))
	}
	return opts
}

func buildQuotaEnforcement(cfg *Config, pgPool *pgxpool.Pool, plain billing.Settler, settings *platformsettings.Service) (billing.Settler, quotaenforce.Reserver) {
	if cfg == nil || !cfg.QuotaEnforce {
		if cfg == nil || !cfg.Budget.Enabled {
			return plain, nil
		}
	}
	var quotaService *quota.Service
	var reserver quotaenforce.Reserver
	settler := plain
	if cfg != nil && cfg.QuotaEnforce {
		quotaService = quota.NewService(quota.NewPostgresStore(pgPool))
		reserver = quotaService
		settler = quotaenforce.NewSettler(settler, quotaService)
	}
	budgetService := buildBudgetService(cfg, settings)
	if budgetService != nil {
		reserver = budgetenforce.NewReserver(budgetService, reserver)
		settler = budgetenforce.NewSettler(settler, budgetService)
	}
	return settler, reserver
}

type budgetSettingsProvider struct {
	settings *platformsettings.Service
}

func (p budgetSettingsProvider) BudgetLimitsJSON(ctx context.Context) (string, error) {
	if p.settings == nil {
		return "", nil
	}
	setting, err := p.settings.Get(ctx, platformsettings.KeyBudgetLimits)
	if err != nil {
		return "", err
	}
	return setting.Value, nil
}

func buildBudgetService(cfg *Config, settings *platformsettings.Service) budgetenforce.Budget {
	if cfg == nil || !cfg.Budget.Enabled {
		return nil
	}
	memory := budget.NewMemoryStore(nil)
	store, err := buildBudgetStore(cfg, memory)
	if err != nil {
		// 配置了 RedisURL 但解析失败:不静默退回单进程内存(多副本下每副本独立限额 =
		// 限额×副本数,等于变相放开预算)。响亮告警让运维发现配置错误。
		_ = privacy.LogSystem(context.Background(), privacy.SystemEvent{
			Severity:   privacy.SeverityError,
			Component:  "budget.redis_config",
			ErrorClass: privacy.ErrorClassFor(context.Background(), err),
			Attrs:      map[string]any{"event_class": "budget_redis_url_invalid_fallback_to_memory"},
		})
	}
	limits := budget.StaticLimitsProvider{
		Default: budget.LimitPair{RPM: cfg.Budget.DefaultRPM, TPM: cfg.Budget.DefaultTPM},
	}
	provider := budget.MergedLimitsProvider{Static: limits, Settings: budgetSettingsProvider{settings: settings}}
	return budget.NewService(store, provider,
		budget.WithFailMode(budget.FailMode(cfg.Budget.FailMode)),
		budget.WithMemoryFallback(memory),
	)
}

// buildBudgetStore 构造预算计数后端。配置了 RedisURL 但解析失败时返回 (memory, error),
// 让上层响亮告警而非静默退回单进程内存。空 RedisURL 直接用内存(无错误)。
func buildBudgetStore(cfg *Config, memory *budget.MemoryStore) (budget.Store, error) {
	if cfg == nil || strings.TrimSpace(cfg.Budget.RedisURL) == "" {
		return memory, nil
	}
	opts, err := redis.ParseURL(cfg.Budget.RedisURL)
	if err != nil {
		return memory, fmt.Errorf("budget redis url invalid: %w", err)
	}
	return budget.NewBreakerStore(
		budget.NewRedisStore(redis.NewClient(opts)),
		circuitbreaker.New(circuitbreaker.Config{OpenCooldown: 10 * time.Second}),
	), nil
}

func loadTenantRetryBudgetFromEnv() (*retrybudget.Budget, error) {
	limit, err := parseTenantRetryBudgetLimit(tenantRetryBudgetEnv)
	if err != nil {
		return nil, err
	}
	window, err := parseTenantRetryBudgetWindow(tenantRetryWindowEnv, time.Minute)
	if err != nil {
		return nil, err
	}
	return retrybudget.New(limit, window), nil
}

func parseTenantRetryBudgetLimit(name string) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid integer %q: %w", name, raw, err)
	}
	if v < 0 {
		return 0, fmt.Errorf("%s: must be non-negative, got %d", name, v)
	}
	return v, nil
}

func parseTenantRetryBudgetWindow(name string, def time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def, nil
	}
	window, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid duration %q: %w", name, raw, err)
	}
	if window <= 0 {
		return 0, fmt.Errorf("%s: must be positive, got %s", name, raw)
	}
	return window, nil
}

// loadStormScopeConfigFromEnv 解析可选的端点/全局 refresh-storm 预算。
// 半配置的 scope(只设了 rate 或只设了 burst,或 burst<1)是一个启动错误——
// 宁可 fail loud,也不能默默让限流关着、放任跨账号的踩踏冲过去。
// 四个全未设置 => 仅账号 scope。
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
		// ParseFloat 会无错误地接受 "Inf"/"NaN";无穷大的预算会启动一个
		// 实际上无界的限流,而 NaN 会默默禁用该 scope——两者都违背 fail-loud
		// 契约,所以在这里拒绝它们。
		return 0, fmt.Errorf("%s: must be a finite number, got %q", name, raw)
	}
	if v < 0 {
		return 0, fmt.Errorf("%s: must be non-negative, got %v", name, v)
	}
	return v, nil
}

// validateStormScopePair 拒绝半配置的 scope,这样一个笔误(设了 rate、
// 忘了 burst)就不能默默禁用限流。
func validateStormScopePair(scope string, rate, burst float64) error {
	switch {
	case rate == 0 && burst == 0:
		return nil // scope 刻意关闭
	case rate > 0 && burst >= 1:
		return nil // scope 完整配置
	default:
		return fmt.Errorf("storm %s scope half-configured: rate=%v burst=%v (need both rate>0 and burst>=1, or both unset)", scope, rate, burst)
	}
}

// credentialRotationScanConfig 是 CRED-288 凭据轮换扫描的运维配置。
// MaxAge<=0 表示关闭(默认);>0 才启用扫描。
type credentialRotationScanConfig struct {
	MaxAge time.Duration
	Limit  int
}

// Enabled 报告扫描是否被运维显式打开(必须配置一个正的 maxAge)。
func (c credentialRotationScanConfig) Enabled() bool { return c.MaxAge > 0 }

// defaultCredentialRotationMaxAge 是 CRED-288 凭据轮换扫描的默认 maxAge(90 天)。
// 之所以默认【开启】而非 opt-in:恢复闭环(CRED-288c)落地后,扫描对超期凭据不再
// 粗暴下线——可刷新(OAuth)凭据保持 active 在线、只被推进既有刷新流自愈,静态 key
// 仅告警不下线,故"默认开"不会再造成可用性/计费回退。配合 DueForRotation 按
// COALESCE(last_refresh_at, created_at) 判超期(幂等、刷过即掉出),默认开是安全的。
// 运维可显式设 HUAKAI_CREDENTIAL_ROTATION_MAX_AGE=0 关闭,或设其它正 duration 调节窗口。
const defaultCredentialRotationMaxAge = 90 * 24 * time.Hour

// loadCredentialRotationScanFromEnv 解析 CRED-288 凭据轮换扫描的开关。
// HUAKAI_CREDENTIAL_ROTATION_MAX_AGE 留空 => 默认 90 天(启用);显式设 0 => 关闭;
// 配其它正 duration(如 "2160h")=> 自定义窗口。malformed => fail-loud。
// HUAKAI_CREDENTIAL_ROTATION_LIMIT 控制每个 tick 处理上限,<=0 => 走 store 默认。
func loadCredentialRotationScanFromEnv() (credentialRotationScanConfig, error) {
	maxAge, err := envDurationDisable0Default(credentialRotationMaxAgeEnv, defaultCredentialRotationMaxAge)
	if err != nil {
		return credentialRotationScanConfig{}, err
	}
	limit, err := envIntDisable0Default(credentialRotationLimitEnv, 0)
	if err != nil {
		return credentialRotationScanConfig{}, err
	}
	return credentialRotationScanConfig{MaxAge: maxAge, Limit: limit}, nil
}

// buildRotationScanOptions 把轮换扫描配置转成 scheduler option。disabled(MaxAge<=0)
// 时返回空切片——即不注入 WithRotationScan,保持现有行为不翻转;enabled 时返回恰好
// 一个 WithRotationScan,装上传入的 store 与 maxAge/limit。把这条 gating 决策抽成独立
// 函数,既给生产 wiring 用,也让测试能直接断言"死开关是否被救活"。
func buildRotationScanOptions(cfg credentialRotationScanConfig, store credentialworker.RotationStore) []credentialworker.Option {
	if !cfg.Enabled() {
		return nil
	}
	return []credentialworker.Option{
		credentialworker.WithRotationScan(
			store,
			cfg.MaxAge,
			cfg.Limit,
			nil, // alert 可选;运维通过 needs_rotation 状态 + 既有 health 告警感知
		),
	}
}

// loadClientIPResolverFromEnv 从 trustedProxyCIDRsEnv(逗号分隔的 CIDR/IP)
// 构建感知可信代理的 client IP resolver。格式错误的条目是一个硬性启动错误
// (fail loud),而非默默降级 burst-limit / anomaly / voucher 的来源。
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

// buildAuthCooldownStore 按 HUAKAI_AUTH_COOLDOWN_ENABLED 决定是否接线 auth 降级车道(缺口① S1)。
// 默认关(未设/false)→ 返回 nil → 车道未接线,auth 失败对选号仍是 no-op(逐字节保持既有行为);
// 翻默认开 = 默认行为翻转(§2 硬门 A1,Owner-gated):auth 失败后临时把坏号移出选号、短 TTL 自愈。
// 退避参数(base=30s/cap=30min/硬禁 strike K=3)用 Config 默认;进一步调参为 Owner-gated 后续项(F2)。
func buildAuthCooldownStore() *authcooldown.Store {
	if on, _ := strconv.ParseBool(os.Getenv("HUAKAI_AUTH_COOLDOWN_ENABLED")); !on {
		return nil
	}
	return authcooldown.NewStore(authcooldown.Config{})
}

func buildGatewayRuntime(ctx context.Context, cfg *Config, mimicryRegistry *mimicry.TemplateRegistry, logger *zap.Logger) (*gatewayRuntime, error) {
	pgPool, err := db.Open(ctx, dbPoolConfig(cfg))
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	// 可选进程内自迁移(HUAKAI_AUTO_MIGRATE=true,默认关):在任何代码用表之前把 schema 升到最新,
	// 供裸二进制单实例部署省去"先手动跑迁移再起 gateway"那一步。默认关时迁移仍外置(compose
	// 的 migrate one-shot 受控跑、多副本防竞态);两路共用同一张 schema_migrations、互相幂等不撞表。
	if autoMigrateEnabled() {
		logger.Info("HUAKAI_AUTO_MIGRATE=true:启动时进程内自迁移到最新")
		if err := dbmigrate.Up(sqlmigrations.Files, cfg.DatabaseURL); err != nil {
			return nil, fmt.Errorf("auto-migrate: %w", err)
		}
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
	// 提前加载凭据加密主密钥,给 secret 平台设置做 at-rest 加密(secret settings 复用同一把主密钥)。
	credentialKeys, err := loadCredentialKeyProvider()
	if err != nil {
		return nil, fmt.Errorf("load credential encryption key: %w", err)
	}
	settingsOpts := []platformsettings.Option{}
	if settingsSecretCipher := settingscipher.New(credentialstore.NewCipher(credentialKeys)); settingsSecretCipher != nil {
		settingsOpts = append(settingsOpts, platformsettings.WithSecretCipher(settingsSecretCipher))
	}
	platformSettingsService := platformsettings.NewService(platformsettings.NewPostgresStore(pgPool), nil, settingsOpts...)
	if err := platformSettingsService.RefreshAll(ctx); err != nil {
		logger.Warn("platform settings prewarm failed", zap.Error(err))
	}
	// captcha secret 现为 settings-first(后台设置 captcha_secret 优先、空回退 env),
	// boot 期日志/生产门也据此解析,避免密钥已在管理台配置却误报"未配置"。
	// platformSettingsService 此处必非 nil(刚构造),可安全 Get。
	captchaSecret := captchaTurnstileSecret()
	if s, err := platformSettingsService.Get(ctx, platformsettings.KeyCaptchaSecret); err == nil {
		if v := strings.TrimSpace(s.Value); v != "" {
			captchaSecret = v
		}
	}
	if err := validateProductionCaptchaConfig(ctx, platformSettingsService, captchaSecret); err != nil {
		return nil, err
	}
	logCaptchaConfig(ctx, logger, platformSettingsService, captchaSecret)
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
	hermesAdminOnly, err := hermesAdminOnlyFromEnv(logger)
	if err != nil {
		return nil, err
	}
	// 运行时总开关(两者默认 ENABLED => 不设置时零行为变更)。
	hermesMutatingEnabled, err := hermesBoolEnabledDefaultTrue(hermesMutatingEnabledEnv)
	if err != nil {
		return nil, err
	}
	hermesToolLoopEnabled, err := hermesBoolEnabledDefaultTrue(hermesLLMToolLoopEnabledEnv)
	if err != nil {
		return nil, err
	}
	// Phase B 提议 KNOB(默认禁用 => 未设置即零行为变:在 Owner 显式打开它之前,LLM 无法提议任何
	// mutating 工具)。
	hermesProposeEnabled, err := hermesBoolEnabledDefaultFalse(hermesLLMProposeEnabledEnv)
	if err != nil {
		return nil, err
	}
	hermesProposeEnabled = effectiveHermesProposeEnabled(hermesMutatingEnabled, hermesProposeEnabled, logger)
	if !hermesMutatingEnabled {
		logger.Warn("Hermes MUTATING tools disabled at runtime — account_pause/account_resume/dlq_replay/renew_trigger are refused; read-only diagnostics + chat remain live",
			zap.String("knob", hermesMutatingEnabledEnv+"=false"))
	}
	if !hermesToolLoopEnabled {
		logger.Warn("Hermes LLM conversational tool loop disabled at runtime — no tool catalog is injected and the internal tool-execute callback is refused; plain chat keeps streaming",
			zap.String("knob", hermesLLMToolLoopEnabledEnv+"=false"))
	}
	if hermesProposeEnabled {
		// 默认关的特权面:LLM-提议路径被激活时大声记日志,使激活可审计。LLM 现在可以提议可逆的
		// B 级 mutating 工具(仅 dry-run);执行仍需一个独立的 OPERATOR 确认——提议本身从不 mutate。
		logger.Warn("Hermes LLM-propose path ENABLED at runtime — the assistant may now propose Proposable mutating tools (dry-run + operator confirm required to execute)",
			zap.String("knob", hermesLLMProposeEnabledEnv+"=true"))
	}
	// S2:给 Hermes 的 mutating 路径设界(并发上限 + 事务 deadline +
	// 按 operator-token 限流)。默认值偏保守;每个旋钮都带一个禁用哨兵值,
	// 这样一个未设置的部署就是逐字节等同于旧行为。
	hermesMutateGuard, err := hermesMutateGuardConfigFromEnv()
	if err != nil {
		return nil, err
	}
	logger.Info("Hermes mutating-path guards (S2)",
		zap.Int("max_concurrency", hermesMutateGuard.maxConcurrency),
		zap.Duration("acquire_wait", hermesMutateGuard.acquireWait),
		zap.Duration("tx_deadline", hermesMutateGuard.txDeadline),
		zap.Int("rate_per_token", hermesMutateGuard.ratePerToken),
		zap.Duration("rate_window", hermesMutateGuard.rateWindow))
	outboxStore := obsoutbox.NewPostgresOutbox(pgPool)
	opts, err := loadRuntimeOptions(logger)
	if err != nil {
		return nil, err
	}
	rt.outboxRuntime = opts.outboxRuntime
	auditRefPolicy := buildAuditRefPolicy(opts.eventBus)

	// credentialKeys 已在前面(platformSettings 构造处)加载,此处直接复用。
	if hermesService != nil {
		hermesService.WithMessageContentKeys(credentialKeys)
	}
	credentialStore := credentialstore.NewStore(pgPool, credentialKeys, credentialstore.DefaultHandlerRegistry())
	// 启动期凭证密钥自检(fail-closed):用当前 KEK 解一条既有 active 凭证,解不开则拒绝启动,
	// 避免 operator 轮换密钥后在运行时全 relay 静默解密瘫痪而无启动信号。详见 VerifyKeySelfCheck。
	if err := credentialStore.VerifyKeySelfCheck(ctx); err != nil {
		return nil, err
	}
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
	if err := installCodexWebOAuthExchanger(credentialExchangers, auth.NewSSRFProtectedOAuthClient(http.DefaultClient), adminOAuthCallbackAllowlist); err != nil {
		return nil, fmt.Errorf("register openai codex_web_oauth exchanger: %w", err)
	}
	// Fail-loud: wiring 启动时立即自检 install 真把 default registry
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
	if err := assertCodexWebOAuthExchangerHasHTTPClient(credentialExchangers); err != nil {
		return nil, fmt.Errorf("openai codex_web_oauth wiring self-check: %w", err)
	}
	// OAuth ModePlan 自一致性闸。在全部 install 之后,对运行时真实使用的 registry 校验:
	// 每个 Kind==FlowKindOAuth 的 ModePlan 必须映射到一个非-fake、非-缺失的 exchanger(显式 fail-closed
	// 的显式不可用态视为合规)。若某 OAuth mode 仍是 fake(会把攻击者可影响的回调码当 JSON 凭据接受)或根本没
	// 注册 exchanger(只在回调期才暴露 ErrOAuthExchangerMissing),进程在此拒启动 —— 把配置漂移在 boot
	// 时变成 fatal,杜绝"暴露为可完成的 OAuth mode 实则映射 fake/缺失"这类信任边界回归。
	if err := credentialacq.ValidateOAuthModeConsistency(credentialacq.DefaultModePlans(), credentialExchangers); err != nil {
		return nil, fmt.Errorf("oauth mode-plan consistency self-check: %w", err)
	}
	emailSettingsStore := mailinfra.NewPostgresSettingsStore(pgPool)
	if releaseModeProduction() {
		// production 邮箱门:默认软化为不拦启动(对齐成熟中转站的请求时惰性做法),未配齐 SMTP/邮箱验证的
		// 租户其验证邮件功能在请求时惰性失败;设 HUAKAI_REQUIRE_EMAIL_GATE=true 恢复"必须配齐才放行"的旧行为。
		gateErr := mailinfra.ValidateProductionReleaseGate(ctx, emailSettingsStore, credentialKeys)
		if err := emailGateStartupError(gateErr, requireEmailReleaseGate()); err != nil {
			return nil, fmt.Errorf("production email release gate: %w", err)
		}
		if gateErr != nil {
			logger.Warn("production 邮箱门未满足:未配齐 SMTP/邮箱验证的租户,其验证邮件相关功能将在请求时惰性返回错误"+
				"(注册按\"验证关闭\"放行,用户直接 active);如需恢复\"必须配齐才放行\"的旧行为,设 HUAKAI_REQUIRE_EMAIL_GATE=true",
				zap.Error(gateErr))
		}
	}
	// 前端 base URL 解析器:从 platformsettings 读 site_frontend_base_url(运营在设置里配),
	// 使鉴权邮件能拼出完整可点链接;未配则返回空,邮件回退裸 token(前端粘贴框兜底)。
	frontendBaseURLResolver := func(ctx context.Context) string {
		setting, err := platformSettingsService.Get(ctx, platformsettings.KeySiteFrontendBaseURL)
		if err != nil {
			return ""
		}
		return setting.Value
	}
	authEmailSender, err := buildAuthEmailSender(cfg, emailSettingsStore, credentialKeys, logger, outboxStore, frontendBaseURLResolver)
	if err != nil {
		return nil, fmt.Errorf("build auth email sender: %w", err)
	}
	userAuthService, userSessionService, err := buildUserServices(pgPool, credentialKeys, emailSettingsStore, logger)
	if err != nil {
		return nil, err
	}
	// 注入 OAuth provider 请求期解析器(settings-first 覆盖 env)。必须用 cipher-enabled 的
	// platformSettingsService(line ~815 带 WithSecretCipher):oauth_providers_secrets 在库里是密文,
	// 读时须解密成明文 client_secret;buildUserServices 内部那个是 cipher-less 的,不能用于读 secret。
	if userAuthService.OAuth != nil {
		userAuthService.OAuth.SetProviderResolver(oauthProviderSettingsResolver(platformSettingsService, logger))
	}
	twoFactorService := twofa.NewService(twofa.NewPostgresStore(pgPool), credentialKeys)
	passkeyService := passkey.NewService(
		passkey.NewPostgresStore(pgPool),
		userAuthService.Store,
		passkey.NewPlatformSettingsConfigSource(platformSettingsService),
	)

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
	// auth 降级车道(缺口① S1):默认关(HUAKAI_AUTH_COOLDOWN_ENABLED 未设=nil→车道未接线,行为逐字节不变)。
	// 翻默认开=默认行为翻转(§2 硬门,Owner-gated A1):auth 失败从对选号 no-op 变临时排除坏号。
	authCooldownStore := buildAuthCooldownStore()
	// 车道绑定的分类规则(R-024/R-025 + xai→grok 归一化)与 knob 同源生效:它们改变 grok/xai
	// 400 坏 key 的客户端契约(400 透传→401 换号)与健康记账,knob 关时必须保持基底行为。
	gateway.SetAuthLaneRulesEnabled(authCooldownStore != nil)
	// 渠道健康状态转换的 stdout 结构化运维日志走进程默认 slog 实例(与 billing lease sweeper /
	// quota reconciler 同源;片D slog 门面合并后自动升级 JSON)。显式注入 = 等价 nil 兜底,零行为变化。
	channelHealthOptions := []channelhealth.ServiceOption{
		channelhealth.WithAlertOutbox(outboxStore),
		channelhealth.WithLogger(slog.Default()),
	}
	if authCooldownStore != nil {
		channelHealthOptions = append(channelHealthOptions, channelhealth.WithAuthCooldownLane(authCooldownStore))
	}
	channelHealthService := channelhealth.NewService(channelHealthStore, channelhealth.DefaultPolicy(), nil, channelHealthOptions...)
	// SUB2-EGRESS-03:在 selector 之前构建 window cost cache,这样 gate
	// 在启动时就能引用它。worker 在 selector 就绪后再启动。
	windowCostCache := windowcost.NewCache()
	// SUB2-EGRESS-02:按账号的最大并发会话封顶 registry。
	sessionCapRegistry := sessioncap.NewRegistry(0)
	recentReqRing := recentreq.NewRing()
	selector, selectorCleanup, err := buildSelector(ctx, billingQueries, pgPool, opts.selector, channelHealthService, windowCostCache, sessionCapRegistry, logger)
	if err != nil {
		return nil, fmt.Errorf("build selector: %w", err)
	}
	rt.selectorCleanup = selectorCleanup

	dlqStore, dlqService, dlqWorker, replicaTarget, closeReplica := buildDLQRuntime(pgPool, opts.obsDLQ, auditLedger, outboxStore)
	rt.closeReplica = closeReplica
	outboxWorker := buildOutboxWorker(outboxStore, opts.outboxRuntime, emailSettingsStore, credentialKeys, channelHealthStore)
	notificationStore := notify.NewPostgresStore(pgPool, credentialKeys)
	notificationSettings := notify.NewService(notificationStore)
	announcementService := announcement.NewService(announcement.NewPostgresStore(pgPool))
	userNoticeService := usernotice.NewService(usernotice.NewPostgresStore(pgPool))
	notificationEmailSender, err := mailinfra.BuildEmailSender(ctx, emailSettingsStore, credentialKeys, mailinfra.WithOutbox(outboxStore))
	if err != nil {
		return nil, fmt.Errorf("build notification email sender: %w", err)
	}
	notifier := notify.NewNotifier(notify.Config{
		Store:       notificationStore,
		EmailSender: notificationEmailSender,
	})
	paymentStore := payment.NewPostgresStore(pgPool)
	paymentService := payment.NewService(paymentStore, paymentServiceOptions(cfg)...)
	// NAPI-DIST-SIGNUP-03 + INVITEE-04:接线注册时的钱包入账签发器
	//(默认关闭;金额从 env 读取,为 0 => IssueSignupBonus/Reward 在任何 insert
	// 之前短路)。userauth 会吞掉返回的 error,因此一次入账失败绝不会回滚注册。
	signupInviteeCfg := payment.SignupInviteeConfigFromEnv()
	userAuthService.SignupBonusFn = func(ctx context.Context, tenantID, userID int64) error {
		_, err := paymentService.IssueSignupBonus(ctx, signupInviteeCfg, tenantID, userID)
		return err
	}
	userAuthService.InviteeRewardFn = func(ctx context.Context, tenantID, userID int64) error {
		_, err := paymentService.IssueInviteeReward(ctx, signupInviteeCfg, tenantID, userID)
		return err
	}
	// 退款配额冲减器:启用配额强制时,退款落库后把退款的实际金额从配额 settled_value 负向冲减
	// (修补"退款只退钱包、配额计数没退→用户提前撞成本上限")。quota.Service 无状态,此处独立实例
	// 与配额强制层用同一套 quota 表,功能等价;未启用配额强制则传 nil,退款不触发冲减(零行为变化)。
	var refundQuotaReverser auditreceipt.QuotaReverser
	if cfg != nil && cfg.QuotaEnforce {
		refundQuotaReverser = quotaCostReverser{svc: quota.NewService(quota.NewPostgresStore(pgPool))}
	}
	settler, receiptStore, receiptFormatter, refundQueue, rateTableSource, err := buildSettlementServices(
		ctx, pgPool, auditSigner, auditLedger, dlqStore, dlqService, replicaTarget, logger, paymentService, platformSettingsService, refundQuotaReverser,
	)
	if err != nil {
		return nil, err
	}
	disputeStore, err := auditreceipt.NewPGXDisputeStore(pgPool)
	if err != nil {
		return nil, fmt.Errorf("build dispute storage: %w", err)
	}
	settler, quotaReserver := buildQuotaEnforcement(cfg, pgPool, settler, platformSettingsService)
	settler = notify.NewSettler(settler, notifier, notify.WithSettlerDeliveryErrorRecorder(func(err error) {
		logger.Warn("low balance notification delivery failed", zap.Error(err))
	}))
	// completion 事件总线在 settler 全装饰(quota/budget/notify)完成后再构造,使异步成功结算走完整的
	// billing→quota→budget→notify 结算链——否则总线持中间层 settler,成功请求漏释放配额预留/并发槽与
	// 预算预留(默认 eventbus+quota 均开即触发)。abort 走同步装饰后 settler 本就正确,唯成功异步路径漏。
	completionBus, err := buildCompletionEventBus(opts.eventBus, settler, pgPool, dlqService, auditRefPolicy, logger)
	if err != nil {
		return nil, fmt.Errorf("build completion eventbus: %w", err)
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
	// WAVE H3 只读诊断工具脊柱 + 它的 hermes_tool_calls 审计写入器,在这里构建
	//(早于 chat bridge),这样 WAVE H3b 对话式工具循环就能把 registry(catalog
	// provider)+ session-binding store 与 bridge 以及 internal tool-execute handler
	// 共享。mutating orchestrator 也一并返回,供 H4 路径用。每个工具都包裹它已有的
	// 读函数——没有新的查询逻辑。这里先暂存在局部变量中(deps 结构体稍后才构建),
	// 然后在下方赋给 d。
	hermesToolRegistry, hermesToolCalls, hermesMutator := buildHermesToolRegistry(hermesToolDeps{
		pool:           pgPool,
		adminQueries:   adminQueries,
		billingQueries: billingQueries,
		credentialStr:  credentialStore,
		channelHealth:  channelHealthService,
		dlqStore:       dlqStore,
		dlqService:     dlqService,
		// model_resolve_diagnose 的只读解析依赖。此 hermes 工具注册早于下方 1150 行的 canonical
		// modelRegistry 构造(它给 model registry admin/router 用),故这里就地构造一份专用实例:
		// PostgresRegistry 是 pgPool 的无状态包装(noopCache),ResolveModel 每次自开只读 TX,
		// 两份实例语义等价且彼此隔离,避免为接线而重排这个 god-file 的 100+ 行依赖顺序。
		modelRegistry: registry.NewPostgresRegistry(pgPool, nil),
	}, hermesMutateGuard.orchestratorOptions()...)
	// S2 (c):按 operator-token 的限流器,在 mutate handler 中强制。
	hermesMutateRateLimiter := hermesMutateGuard.newRateLimiter()
	// WAVE H3b:进程内的 session-binding store,由 bridge(在 chat 开始时写入
	// operator 绑定)和 internal tool handler(读取它以授权每次对话式工具调用)共享。
	hermesSessionBindings := hermeschat.NewSessionBindings(nil)
	var hermesChatBridge *hermeschat.Bridge
	if hermesRunner != nil {
		hermesChatBridge, err = buildHermesChatBridge(hermesService, dlqService, platformSettingsService, credentialKeys, hermesSessionBindings, hermesToolRegistry, hermesToolLoopEnabled, hermesProposeEnabled)
		if err != nil {
			return nil, err
		}
	}
	if hermesService != nil {
		retentionDays, err := hermes.MessageRetentionDaysFromEnv()
		if err != nil {
			return nil, err
		}
		// retention_days=0 是默认安全值：永久保留，运维显式配置正整数后才启动硬删 worker。
		if retentionDays > 0 {
			hermesRetentionWorker := hermes.NewMessageRetentionWorker(hermes.MessageRetentionWorkerConfig{
				Store:         hermes.NewPostgresMessagePurgeStore(hermesQueries),
				RetentionDays: retentionDays,
			})
			hermesRetentionWorker.Start(ctx)
			rt.hermesRetentionWorker = hermesRetentionWorker
		}
	}
	usageRetentionDays, err := usageretention.UsageRetentionDaysFromEnv()
	if err != nil {
		return nil, err
	}
	// retention_days=0 是默认的安全值:除非运维显式配置一个正的 TTL,
	// 否则永久保留用量分析历史。
	if usageRetentionDays > 0 {
		usageRetentionWorker := usageretention.NewUsageRetentionWorker(usageretention.Config{
			Store:         usageretention.NewPostgresUsagePurgeStore(billingQueries),
			RetentionDays: usageRetentionDays,
		})
		usageRetentionWorker.Start(ctx)
		rt.usageRetentionWorker = usageRetentionWorker
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

	// quota 补偿器 worker:每轮两段——①重放结算/释放失败后入队的补偿 job;②清扫 lease 已过期、
	// billing claim 已终态、但补偿 job 从未入队(进程死于 billing 终态与 quota 补偿之间的崩溃窗口)
	// 的孤儿预留,按 claim 终态定向 Settle/Release。两段缺一,预留都会永久卡 reserved 冻结窗口
	// headroom(手动/累计窗无滚动自愈)。默认启动;显式 false/0 才跳过。
	if quotaReconcilerEnabledFromEnv() {
		quotaReconciler := quota.NewReconciler(nil, quota.NewPostgresStore(pgPool), quota.ReconcilerOptions{})
		quotaWorker := quota.NewReconciliationWorker(quotaReconciler, 0)
		quotaWorker.Start(ctx)
		rt.quotaReconcileStop = quotaWorker.Stop
	}

	// PROXY-04: 代理池健康探测 worker, 带迟滞维护 status (active<->dead)。无此
	// worker 时一个 flap 的代理会被永久 dead, 绑定它的账号 fail-closed 永不自愈;
	// TCP 探活恢复后账号自动回来。随 ctx 取消停。
	proxyHealthWorker := proxyhealth.NewWorker(
		proxyhealth.NewPostgresLister(pgPool),
		proxyhealth.NewTCPProber(5*time.Second),
		proxyhealth.NewPostgresStatusStore(pgPool),
		proxyhealth.DefaultInterval,
		nil,
	)
	proxyHealthWorker.Start(ctx)

	// UTLS-06: TLS 指纹 profile 漂移/健康 worker。周期校验 active 自定义 profile
	// 还能否构建可用 uTLS ClientHello, 坏的标 drift_detected (无 JA3 计算, 无误杀)。
	tlsProfileHealthWorker := tlsfphealth.NewWorker(
		tlsfphealth.NewPostgresLister(pgPool),
		tlsfphealth.NewPostgresDriftMarker(pgPool),
		tlsfphealth.DefaultInterval,
		nil,
	)
	tlsProfileHealthWorker.Start(ctx)

	// SUB2-EGRESS-03:启动按账号的 5 小时窗口消费封顶聚合器。
	windowCostWorker := windowcost.NewWorker(
		windowcost.NewPostgresLister(pgPool),
		windowcost.NewPostgresAggregator(pgPool),
		windowCostCache,
		windowcost.DefaultInterval,
		nil,
	)
	windowCostWorker.Start(ctx)

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
	pricingRatioStore := pricingcatalog.NewPostgresStoreWithAuditSigner(pgPool, auditSigner)
	pricingRatioResolver := pricingcatalog.NewRatioResolver(pricingRatioStore, 0)
	checkinService := checkin.NewService(checkin.Deps{
		Store:    checkin.NewPostgresStore(pgPool),
		Payment:  paymentService,
		Settings: platformSettingsService,
	})
	if err := applyStoredPaymentProviderConfig(ctx, platformSettingsService, paymentService); err != nil {
		logger.Warn("payment provider runtime config prewarm failed", zap.Error(err))
	}

	loginThrottle, err := loadLoginThrottleFromEnv()
	if err != nil {
		return nil, fmt.Errorf("load login throttle config: %w", err)
	}
	emailSendLimit, err := loadEmailSendLimitFromEnv()
	if err != nil {
		return nil, fmt.Errorf("load email send limit config: %w", err)
	}
	tenantRetryBudget, err := loadTenantRetryBudgetFromEnv()
	if err != nil {
		return nil, fmt.Errorf("load tenant retry budget: %w", err)
	}

	billingPolicyStore := billing.NewPolicyStore(pgPool)
	billingPolicyResolver := billing.NewPolicyResolver(billingPolicyStore, 0)
	mediaTaskConfig := mediatask.NewPlatformConfigSource(platformSettingsService, cfg.BillingPolicyVersion, cfg.RequestClass)
	mediaTaskStore := mediatask.NewPostgresStore(pgPool, mediatask.PostgresStoreConfig{
		BillingPolicyVersion: cfg.BillingPolicyVersion,
		RequestClass:         cfg.RequestClass,
		BalanceModeResolver:  billingPolicyResolver,
	})
	// 媒体 provider 出站客户端必须带 Timeout(与同文件 modelsync fetcher 一致):裸 http.DefaultClient
	// 无超时,慢上游/半开连接会让串行媒体 worker 永久挂起。worker 侧已对每次 Submit/Poll 加 context 硬超时
	// 为主防线,此处 client.Timeout 作外层兜底(防 provider 实现忽略 ctx)。
	mediaTaskProviders := mediatask.NewHTTPProviderRegistry(mediaTaskConfig, &http.Client{Timeout: 60 * time.Second})
	mediaTaskService := mediatask.NewService(mediaTaskStore, mediaTaskConfig, mediaTaskProviders)
	// OrphanReporter 把"租约丢失致 providerTaskID 未落库"的孤儿上游任务持久化到 media_task_orphans,
	// 供对账消费者/运维查处(此前仅日志,易随轮转丢失)。logger 传 nil → 与 worker 同走 slog 默认实例。
	mediaTaskWorker := mediatask.NewWorker(mediaTaskStore, mediaTaskConfig, mediaTaskProviders, mediatask.WorkerOptions{
		OrphanReporter: mediatask.NewPersistingOrphanReporter(mediaTaskStore, nil),
	})
	mediaTaskWorker.Start(ctx)
	rt.mediaTaskWorker = mediaTaskWorker
	userAuditStore := userauditlog.NewPostgresStore(pgPool)

	d := &deps{
		cfg:                   cfg,
		clientIPResolver:      clientIPResolver,
		adminQueries:          adminQueries,
		billingQueries:        billingQueries,
		billingPolicyStore:    billingPolicyStore,
		billingPolicyResolver: billingPolicyResolver,
		selector:              selector,
		queueWaiter:           queuewait.NewExecutor(),
		sessionCapRegistry:    sessionCapRegistry,
		recentReqRing:         recentReqRing,
		toolPriceSource:       buildToolPriceSource(),
		channelHealth:         channelHealthService,
		authCooldown:          authCooldownStore,
		modelCooldowns:        ratelimit.NewModelCooldownService(billingQueries),
		upstreamRate:          ratelimit.NewUpstreamRateServiceWithSessionWindowStore(nil, channelHealthService.Policy().DefaultRateLimitCooldown, ratelimit.NewPostgresSessionWindowStore(pgPool), ratelimit.WithAccountErrorRulesProvider(ratelimit.NewPostgresAccountErrorRulesProvider(pgPool)), ratelimit.WithCooldownStateStore(ratelimit.NewPostgresCooldownStateStore(pgPool))),
		retryBudget:           tenantRetryBudget,
		claimGate:             newClaimGateWithLease(pgPool),
		settler:               settler,
		quotaReserver:         quotaReserver,
		replayStore:           replayStore,
		forwarder:             buildStreamForwarder(auditLedger, auditSigner, dlqService),
		credentialVault:       provider.NewPostgresCredentialVaultWithStore(pgPool, credentialStore),
		credentialStore:       credentialStore,
		credentialKeys:        credentialKeys,
		credentialAcqStore:    credentialAcqStore,
		credentialExchangers:  credentialExchangers,
		emailSettings:         emailSettingsStore,
		authEmailSender:       authEmailSender,
		emailSendLimit:        emailSendLimit,
		userAuth:              userAuthService,
		pgPool:                pgPool,
		userSessions:          userSessionService,
		passkeys:              passkeyService,
		twoFactor:             twoFactorService,
		loginThrottle:         loginThrottle,
		userKeyService:        userkey.NewService(pgPool, nil, userkey.WithAuditSink(userAuditStore), userkey.WithDefaultKeyQuota(runtimeconfig.LoadDefaultKeyQuota())),
		userAuditStore:        userAuditStore,
		paymentService:        paymentService,
		checkinService:        checkinService,
		paymentProviders:      paymentProviders,
		paymentRefundRequests: buildPaymentRefundRequestRecorder(pgPool, paymentService),
		voucherService:        voucher.NewService(voucher.NewPostgresStore(pgPool), buildVoucherServiceOptions(cfg)...),
		subscriptionService:   subscription.NewService(subscription.NewPostgresStore(pgPool)),
		notificationSettings:  notificationSettings,
		announcementService:   announcementService,
		userNoticeService:     userNoticeService,
		mediaTaskService:      mediaTaskService,
		mediaTaskWorker:       mediaTaskWorker,
		mediaTaskStore:        mediaTaskStore,
		routeAdminService:     routeadmin.NewService(routeadmin.NewPostgresStore(pgPool), nil),
		panelAuthResolver:     panelauth.NewResolver(panelauth.NewPostgresRoleStore(pgPool)),
		invitationService:     communityinvitation.NewService(communityinvitation.NewPostgresStore(pgPool)),
		responseCache:         opts.responseCache,
		cacheScope:            opts.cacheScope,
		dlqService:            dlqService,
		obsDLQAdminStore:      outboxStore,
		completionBus:         completionBus,
		auditRefPolicy:        auditRefPolicy,
		dispatcher: &gateway.UpstreamDispatcher{
			Adapters:                 registrydefault.Build(),
			TransportFactory:         buildTransportFactory(cfg, mimicryRegistry),
			ProxyResolver:            provider.NewPostgresProxyResolverWithKeys(pgPool, credentialKeys),
			TLSProfileResolver:       tlsfpresolve.NewPostgresResolver(pgPool),
			Timeouts:                 buildGatewayTimeoutConfig(),
			AnthropicAutoBreakpoints: cfg.CacheAnthropicAutoBreakpoints,
			// 仅 dev/demo:当设置了 HUAKAI_DEV_MOCK_UPSTREAM 且网关不在
			// production 模式时,一个伪造的 doer 会捏造上游 SSE,使本地 MVP
			// 循环无需真实 provider 即可运行。在 production / 未设置时为 nil →
			// 真实的 transport 路径不受影响。
			HTTPClient: devMockUpstreamDoer(),
		},
		inboundAuth:          auth.NewAPIKeyResolverWithClientIPResolver(authQueries, clientIPResolver),
		auditLedger:          auditLedger,
		auditSigner:          auditSigner,
		cacheOverrideStore:   billing.NewCacheOverrideStore(auditSigner, nil),
		auditPubkeyRegistry:  auditPubkeyRegistry,
		receiptStore:         receiptStore,
		receiptFormatter:     receiptFormatter,
		disputeStore:         disputeStore,
		refundQueue:          refundQueue,
		rateTableSource:      rateTableSource,
		pricingRatioStore:    pricingRatioStore,
		pricingRatioResolver: pricingRatioResolver,
		modelRegistry:        modelRegistry,
		modelSync:            modelSyncService,
		routePlanner:         router.NewDefaultRouter(),
		adminAuth: adminsessionauth.New(
			admin.NewAdminResolver(adminQueries),   // 令牌通道(hk_admin_,行为不变)
			userSessionService,                     // session 校验器
			panelauth.NewPostgresRoleStore(pgPool), // users.role 只读查询
			clientIPResolver,
		),
		adminIssuer:              admin.NewKeyIssuer(pgPool),
		adminRevoker:             admin.NewKeyRevoker(pgPool),
		adminTokenIssuer:         admin.NewAdminTokenIssuer(pgPool),
		billingAuditUpdater:      gatewayhttp.NewAdminBillingSettingsAuditUpdater(pgPool),
		platformSettings:         platformSettingsService,
		hermesService:            hermesService,
		hermesRunner:             hermesRunner,
		hermesChatBridge:         hermesChatBridge,
		hermesKeyStore:           hermesKeyStore,
		hermesBootstrapIssuer:    hermesBootstrapIssuer,
		hermesRunnerSharedSecret: hermesRunnerSharedSecret,
		hermesAdminOnly:          hermesAdminOnly,
		hermesToolRegistry:       hermesToolRegistry,
		hermesToolCalls:          hermesToolCalls,
		hermesMutator:            hermesMutator,
		hermesConfirmCache:       hermesconfirm.NewCache(),
		hermesMutateRateLimiter:  hermesMutateRateLimiter,
		hermesMutatingEnabled:    hermesMutatingEnabled,
		hermesToolLoopEnabled:    hermesToolLoopEnabled,
		hermesProposeEnabled:     hermesProposeEnabled,
		hermesSessionBindings:    hermesSessionBindings,
	}
	rt.deps = d
	apiKeyExpiryService := apikeyexpiry.NewService(
		authQueries,
		apikeyexpiry.WithBatchLimit(int32(cfg.APIKeyExpirySweepBatchLimit)),
	)
	apiKeyExpiryWorker := apikeyexpiry.NewWorker(apikeyexpiry.WorkerConfig{
		Service:  apiKeyExpiryService,
		Interval: cfg.APIKeyExpirySweepInterval,
		Logger:   logger,
	})
	rt.apiKeyExpirySweepStop = startAPIKeyExpiryWorker(ctx, cfg, apiKeyExpiryWorker)
	if cfg.PaymentExpireSweepInterval > 0 {
		paymentExpireSweeper := payment.NewExpireSweeper(payment.ExpireSweeperConfig{
			Store:      paymentStore,
			Interval:   cfg.PaymentExpireSweepInterval,
			BatchLimit: cfg.PaymentExpireSweepBatchLimit,
			Logger:     logger,
		})
		paymentExpireSweeper.Start(ctx)
		rt.paymentExpireSweepStop = paymentExpireSweeper.Stop
	}
	metricsProvider, metricsHandler, otelShutdown, err := otelbridge.Setup(ctx)
	if err != nil {
		return nil, fmt.Errorf("setup otel metrics bridge: %w", err)
	}
	if err := otelbridge.RegisterBridge(ctx, metricsProvider); err != nil {
		_ = otelShutdown(ctx)
		return nil, fmt.Errorf("register otel expvar bridge: %w", err)
	}
	d.metricsHandler = metricsHandler
	d.otelShutdown = otelShutdown
	alertingStore := alerting.NewPostgresStore(pgPool)
	alertingService := alerting.NewService(alertingStore,
		alerting.WithFiringDeliverer(notifyFiringDeliverer{notifier: notifier}),
		alerting.WithFiringDeliveryErrorRecorder(func(_ context.Context, tenantID int64, notice alerting.FiringNotice, err error) {
			logger.Warn("alert firing notification delivery failed",
				zap.Int64("tenant_id", tenantID),
				zap.Int64("rule_id", notice.RuleID),
				zap.String("rule_name", notice.RuleName),
				zap.Error(err),
			)
		}),
	)
	alertingScheduler := alerting.NewScheduler(alerting.SchedulerConfig{
		Evaluator: alertingService,
		Store:     alertingStore,
		MetricSource: alertmetrics.NewCompositeMetricSource(alertmetrics.CompositeMetricSourceConfig{
			GlobalSource:  otelbridge.NewExpvarMetricSource(),
			UsageRolluper: alertmetrics.NewBillingRecentUsageRolluper(billingQueries),
			UsageStats:    obsconfig.NewUsageStatsProvider(platformsettings.NewPostgresStore(pgPool)),
			AccountHealth: alertmetrics.NewPoolAccountHealthCounter(billingQueries),
		}),
		Interval: cfg.AlertingEvalInterval,
		// OBS-193:对于某个 tick,只有抢到 advisory lock 的副本才触发告警,
		// 这样多副本部署就不会发出重复告警。
		LeaderLock: alerting.NewPostgresLeaderLock(pgPool),
	})
	rt.alertingEvalStop = startAlertingEvaluator(ctx, cfg, alertingScheduler, logger)

	if err := admin.MaybeBootstrap(ctx, pgPool, logger); err != nil {
		return nil, fmt.Errorf("admin bootstrap: %w", err)
	}
	// 单租户开箱即用:空库自动种默认工作租户(id=0 哨兵不算工作租户),否则
	// 从零部署必须手写 psql 才能得到第一个可用租户(users/api_keys 全链 FK
	// 断头)。已有任意工作租户时本钩子零写入。
	if err := tenancy.EnsureDefaultTenant(ctx, pgPool, logger); err != nil {
		return nil, fmt.Errorf("ensure default tenant: %w", err)
	}
	// role 制单登录 bootstrap:把 HUAKAI_ADMIN_BOOTSTRAP_EMAIL 指定的已注册账号提升为
	// role=admin(env 未设 = no-op)。租户走与种默认租户同一真相源,尊重 env 覆盖。
	if workingTenantID, err := tenancy.WorkingTenantIDFromEnv(); err != nil {
		return nil, fmt.Errorf("resolve working tenant: %w", err)
	} else if err := panelauth.MaybeBootstrapAdminUser(ctx, pgPool, workingTenantID, logger); err != nil {
		return nil, fmt.Errorf("admin user bootstrap: %w", err)
	}
	// Production-required gate: credentialScheduler 必须装
	// authQueries + auditLedger + pgPool + auditSigner,否则 audit/ledger 链
	// 在 OAuth refresh 时静默失败。Startup fail-fast 比 runtime
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
		// CRED-293:当一次 refresh 把某账号推入终态/不健康状态(Alert 标志)时,
		// 经 notify 管线触发一次运维告警。
		credentialworker.WithProviderAccountDownDeliverer(providerAccountDownDeliverer{notifier: notifier}),
	}
	// CRED-288:把凭据轮换扫描接进 scheduler。运维 opt-in(配一个正的 maxAge 才启用);
	// 默认关闭以避免把超期凭据集体打入 needs_rotation 这一非 serving 状态(可用性回退)。
	rotationScanCfg, err := loadCredentialRotationScanFromEnv()
	if err != nil {
		return nil, fmt.Errorf("load credential rotation scan config: %w", err)
	}
	if rotationScanCfg.Enabled() {
		credentialSchedulerOptions = append(
			credentialSchedulerOptions,
			buildRotationScanOptions(rotationScanCfg, credentialworker.NewPostgresRotationStore(pgPool))...,
		)
		logger.Info("credential rotation-due scan enabled",
			zap.Duration("max_age", rotationScanCfg.MaxAge),
			zap.Int("limit", rotationScanCfg.Limit),
		)
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
	d.credentialScheduler = credentialScheduler
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

	subscriptionExpiryWorker := subscription.NewExpiryWorker(subscription.ExpiryWorkerConfig{Service: d.subscriptionService})
	subscriptionExpiryWorker.Start(ctx)

	subscriptionReminderWorker := subscription.NewReminderWorker(subscription.ReminderWorkerConfig{
		Service: subscription.NewReminderService(
			subscription.NewPostgresStore(pgPool),
			subscription.NewEmailReminderMailer(notificationEmailSender),
		),
	})
	subscriptionReminderWorker.Start(ctx)
	d.subExpiryWorker = subscriptionExpiryWorker
	d.subReminderWorker = subscriptionReminderWorker

	// 订阅自动续费 worker: money 动作 (扫到期 → 扣钱包余额 → 续期), 默认 KNOB 关 (零生产行为变),
	// 仅 HUAKAI_SUBSCRIPTION_AUTO_RENEW_ENABLED=true 时构造并启动。非法值 fail-loud。
	autoRenewEnabled, err := subscriptionAutoRenewEnabledFromEnv(subscriptionAutoRenewEnabledEnv)
	if err != nil {
		return nil, err
	}
	var subscriptionAutoRenewWorker *subscription.AutoRenewWorker
	if autoRenewEnabled {
		subscriptionAutoRenewWorker = subscription.NewAutoRenewWorker(subscription.AutoRenewWorkerConfig{Service: d.subscriptionService})
		subscriptionAutoRenewWorker.Start(ctx)
		if logger != nil {
			logger.Info("订阅自动续费 worker 已启用 (将自动扣减钱包余额续期到期订阅)",
				zap.String("enabled_by", subscriptionAutoRenewEnabledEnv+"=true"))
		}
	}
	d.subAutoRenewWorker = subscriptionAutoRenewWorker

	rt.credentialScheduler = credentialScheduler
	rt.dlqWorker = dlqWorker
	rt.outboxWorker = outboxWorker
	rt.subscriptionExpiryWorker = subscriptionExpiryWorker
	rt.subscriptionReminderWorker = subscriptionReminderWorker
	rt.subscriptionAutoRenewWorker = subscriptionAutoRenewWorker
	rt.obsDLQEnabled = opts.obsDLQ.Enabled
	// WAVE H2:最后再构建模块知识脊柱,等所有 probe 引用的服务
	//(settler、selector、credential store + scheduler)都接好之后,这样 seed
	// probe 捕获到的是 live(非 nil)引用。不在请求热路径上。
	d.moduleRegistry = buildModuleRegistry(d)
	// WAVE H3 只读诊断工具脊柱在更早处构建(早于 chat bridge),这样 H3b
	// 对话式工具循环能共享它。这里只构建模块上下文 source。
	d.hermesModuleSource = newModuleSource(d.moduleRegistry)
	// WAVE H3b:runner 在对话中途回调进来的 internal 只读 tool-execute handler。
	// 它校验 session 的 internal_token、解析出绑定的 operator,并且只派发只读工具
	//(Run 拒绝任何 mutation),带上 operator 的角色下限 + 租户作用域 + 审计。
	// 仅在 admin-only 重定位 + chat bridge 都生效时才构建。
	if d.hermesAdminOnly && d.hermesChatBridge != nil {
		if internalSecret := strings.TrimSpace(os.Getenv(hermeschat.InternalTokenSecretEnv)); internalSecret != "" {
			d.hermesInternalToolHandler = buildHermesInternalToolHandler(
				[]byte(internalSecret), d.hermesSessionBindings, d.hermesToolRegistry, d.hermesToolCalls, d.hermesToolLoopEnabled,
				d.hermesConfirmCache, d.hermesProposeEnabled,
			)
		}
	}
	// WAVE H5:每日运维巡检 worker。默认关闭(opt-in 启用标志);仅在启用
	// 且能解析出一个 admin 收件人(platform setting 或 env 回退)时才启动。
	// 它复用已有的只读诊断 + 通知邮件发送器 + 模块脊柱——纯增量,不在请求热路径上。
	if inspectionWorker := buildHermesInspectionWorker(ctx, hermesAdminDeps{
		settings:       platformSettingsService,
		emailSender:    notificationEmailSender,
		moduleRegistry: d.moduleRegistry,
		credentialStr:  credentialStore,
		channelHealth:  channelHealthService,
		dlqStore:       dlqStore,
		obsDLQStore:    outboxStore,
		billingQueries: billingQueries,
		logger:         logger,
	}); inspectionWorker != nil {
		inspectionWorker.Start(ctx)
		rt.hermesInspectionWorker = inspectionWorker
	}
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
			Vendor:         modelsync.VendorOpenAI,
			URL:            cfg.OpenAI.URL,
			APIKey:         cfg.OpenAI.APIKey,
			Timeout:        cfg.Timeout,
			AllowedHosts:   cfg.AllowedHosts,
			AllowUnsafeURL: cfg.AllowUnsafeURLs,
		}))
	}
	if cfg.Anthropic.Configured() {
		fetchers = append(fetchers, modelsync.NewHTTPFetcher(modelsync.HTTPFetcherConfig{
			Vendor:         modelsync.VendorAnthropic,
			URL:            cfg.Anthropic.URL,
			APIKey:         cfg.Anthropic.APIKey,
			Timeout:        cfg.Timeout,
			AllowedHosts:   cfg.AllowedHosts,
			AllowUnsafeURL: cfg.AllowUnsafeURLs,
		}))
	}
	if cfg.Gemini.Configured() {
		fetchers = append(fetchers, modelsync.NewHTTPFetcher(modelsync.HTTPFetcherConfig{
			Vendor:         modelsync.VendorGemini,
			URL:            cfg.Gemini.URL,
			APIKey:         cfg.Gemini.APIKey,
			Timeout:        cfg.Timeout,
			AllowedHosts:   cfg.AllowedHosts,
			AllowUnsafeURL: cfg.AllowUnsafeURLs,
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

func buildHermesChatBridge(hermesService *hermes.Service, dlqService *legacydlq.Service, settings *platformsettings.Service, keys credentialstore.KeyProvider, bindings *hermeschat.SessionBindings, toolRegistry *hermesops.Registry, toolLoopEnabled bool, proposeEnabled bool) (*hermeschat.Bridge, error) {
	if hermesService == nil {
		return nil, nil
	}
	if keys == nil {
		return nil, fmt.Errorf("%w: Hermes message content encryption key provider is required", hermes.ErrMisconfigured)
	}
	internalSecret := strings.TrimSpace(os.Getenv(hermeschat.InternalTokenSecretEnv))
	if internalSecret == "" {
		return nil, fmt.Errorf("%w: %s is required for Hermes chat bridge", hermes.ErrMisconfigured, hermeschat.InternalTokenSecretEnv)
	}
	opts := []hermeschat.Option{
		hermeschat.WithInternalTokenSecret([]byte(internalSecret)),
		hermeschat.WithInternalBaseURL(envDefault(hermeschat.InternalBaseURLEnv, hermeschat.DefaultInternalBaseURL)),
		hermeschat.WithAuditDLQ(dlqService),
		hermeschat.WithResponseHeaderSettings(settings),
		hermeschat.WithMessageContentKeys(keys),
	}
	// WAVE H3b:挂上 session-binding store + 只读工具 catalog,这样 chat 负载
	// 就携带该 catalog,且每个会话的 operator 都被绑定。两者均可选——
	// bindings store 为 nil 时,chat 路径保持不变。
	if bindings != nil {
		opts = append(opts, hermeschat.WithSessionBindings(bindings))
	}
	if toolRegistry != nil {
		// Phase B:proposeEnabled 打开时注入 ProposableCatalog(含可提议的 mutating 工具 + 标志),
		// 否则注入 ReadOnlyCatalog(默认,零行为变)。与 internal_tool_handler 的 propose 分支共用
		// 同一个 KNOB。
		opts = append(opts, hermeschat.WithToolCatalog(hermesToolCatalogProvider{reg: toolRegistry, proposeEnabled: proposeEnabled}))
	}
	// KNOB B:当 LLM 对话式工具循环在运行时被禁用时,bridge 不注入任何
	// tool_catalog(上面的 WithToolCatalog provider 仍接着,但 gate 抑制注入),
	// 因此 LLM 被告知没有任何工具。
	opts = append(opts, hermeschat.WithToolLoopEnabled(toolLoopEnabled))
	bridge, err := hermeschat.NewBridge(hermesService, opts...)
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

// hermesAdminOnlyEnv 把 Hermes 路由重新门控为仅 ADMIN/OPERATOR 鉴权。
const hermesAdminOnlyEnv = "HUAKAI_HERMES_ADMIN_ONLY"

// hermesAllowLegacyUserAuthEnv 是把 Hermes 挂到旧 customer-key 路径背后所需的
// 显式第二个 opt-in。没有它,HUAKAI_HERMES_ADMIN_ONLY=false 会被忽略,Hermes
// 保持 admin-only——单次 env 翻转再也无法默默把 LLM 运维助手暴露给
// 普通客户 API key。
const hermesAllowLegacyUserAuthEnv = "HUAKAI_HERMES_ALLOW_LEGACY_USER_AUTH"

// hermesAdminOnlyFromEnv 解析 Hermes 是否为 admin/operator-only。它是
// FAIL-CLOSED 的:默认(未设置)、任何真值、乃至显式的
// HUAKAI_HERMES_ADMIN_ONLY=false 但缺少第二个 opt-in
// HUAKAI_HERMES_ALLOW_LEGACY_USER_AUTH=true,都解析为 admin-only。只有当
// 两个 opt-in 都存在时,才进入旧的终端用户(customer-key)鉴权;并且在
// production(HUAKAI_RELEASE_MODE=production)下直接拒绝,镜像
// validateProductionCaptchaConfig 的启动门。格式错误的
// HUAKAI_HERMES_ADMIN_ONLY 仍是一个 fail-loud 启动错误。logger 为 nil 时
// 可容忍(跳过警告),与 logCaptchaConfig 一致。
func hermesAdminOnlyFromEnv(logger *zap.Logger) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(hermesAdminOnlyEnv))
	if raw == "" {
		return true, nil
	}
	wantAdminOnly, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean, got %q: %w", hermesAdminOnlyEnv, raw, err)
	}
	if wantAdminOnly {
		return true, nil
	}
	// 显式 false:请求使用旧的 customer-key 鉴权。要求第二个 opt-in;
	// 否则保持 admin-only(fail-closed)并大声告警。
	if !strings.EqualFold(strings.TrimSpace(os.Getenv(hermesAllowLegacyUserAuthEnv)), "true") {
		if logger != nil {
			logger.Warn("HUAKAI_HERMES_ADMIN_ONLY=false ignored — legacy end-user auth for Hermes requires an explicit second opt-in; staying admin-only",
				zap.String("required_optin", hermesAllowLegacyUserAuthEnv+"=true"))
		}
		return true, nil
	}
	// 两个 opt-in 都存在 → 旧模式。production 下绝不允许。
	if releaseModeProduction() {
		return false, fmt.Errorf("refusing to start: Hermes legacy end-user auth (%s=false + %s=true) exposes the LLM ops-assistant to customer API keys and is forbidden when HUAKAI_RELEASE_MODE=production",
			hermesAdminOnlyEnv, hermesAllowLegacyUserAuthEnv)
	}
	if logger != nil {
		logger.Warn("Hermes mounted behind LEGACY end-user (customer API key) auth — the LLM ops-assistant is reachable by customer keys; admin-only is the safe default",
			zap.String("enabled_by", hermesAdminOnlyEnv+"=false + "+hermesAllowLegacyUserAuthEnv+"=true"))
	}
	return false, nil
}

// hermesMutatingEnabledEnv 是所有 Hermes mutating 工具
// (account_pause/account_resume/dlq_replay/renew_trigger)的运行时总开关。
// 它默认 ENABLED,因此未设置 env 即零行为变更;翻为 false 可在运行时
// 禁用每个 mutating 工具,同时保持只读诊断 + 对话式 chat 完全在线。
// 与 HUAKAI_HERMES_ADMIN_ONLY 正交。
const hermesMutatingEnabledEnv = "HUAKAI_HERMES_MUTATING_ENABLED"

// hermesLLMToolLoopEnabledEnv 是 LLM 对话式工具循环的运行时总开关
// (向 chat 请求体注入只读工具 catalog + runner 在对话中途的
// /internal/hermes/tool-execute 回调)。它默认 ENABLED;翻为 false 可禁用
// 工具循环,同时普通的 /v1/hermes/chat 仍持续流式。与 HUAKAI_HERMES_ADMIN_ONLY
// 以及上面的 mutating 总开关均正交。
const hermesLLMToolLoopEnabledEnv = "HUAKAI_HERMES_LLM_TOOLLOOP_ENABLED"

// hermesLLMProposeEnabledEnv 是 Phase B 提议 KNOB(默认禁用)。未设置/false 时,internal handler
// 在任何 dry-run 解析之前拒绝每个 mode=propose 调用(403),故 LLM 无法提议任何 mutating 工具——
// 接入提议路径是零行为变。设为 true(Owner-gated 激活)可让助手提议可逆的 B 级 mutating 工具;
// 执行仍需一个独立的 OPERATOR 确认。额外受 tool-loop kill-switch(HUAKAI_HERMES_LLM_TOOLLOOP_ENABLED)
// 与 per-tool 的 Proposable 标志门控。
const hermesLLMProposeEnabledEnv = "HUAKAI_HERMES_LLM_PROPOSE_ENABLED"

// subscriptionAutoRenewEnabledEnv 是订阅自动续费 worker 的启用开关。默认 FALSE:
// 不设/空都解析为 false → worker 不启动 → 现有 auto_renew=true 订阅行为零变化
// (合并即零生产行为变)。Owner 显式翻 true 才激活"扫到期 → 扣钱包余额 → 续期"的自动扣费。
const subscriptionAutoRenewEnabledEnv = "HUAKAI_SUBSCRIPTION_AUTO_RENEW_ENABLED"

// subscriptionAutoRenewEnabledFromEnv 解析 DEFAULT-FALSE 运行时布尔旋钮: 不设/空 => false
// (默认关, money 安全); 任意可解析布尔被尊重; 非法值 fail-loud 启动报错 (绝不静默退回, 以免
// 把本该关闭的自动扣费悄悄打开)。形态对称 hermesBoolEnabledDefaultFalse, 语义专属订阅自动续费。
func subscriptionAutoRenewEnabledFromEnv(envName string) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(envName))
	if raw == "" {
		return false, nil
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean, got %q: %w", envName, raw, err)
	}
	return enabled, nil
}

// hermesBoolEnabledDefaultFalse 解析一个默认 FALSE 的运行时布尔 knob(hermesBoolEnabledDefaultTrue
// 的镜像):unset/空 => 默认(false、禁用);任何可解析的 bool 被采纳;格式错误的值是 fail-loud
// boot error(绝不静默回退而悄悄启用 operator 未选择开启的特权面)。用于 Phase B 提议 KNOB,其
// 安全默认是关。
func hermesBoolEnabledDefaultFalse(envName string) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(envName))
	if raw == "" {
		return false, nil
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean, got %q: %w", envName, raw, err)
	}
	return enabled, nil
}

// hermesBoolEnabledDefaultTrue 解析一个默认 TRUE 的运行时布尔 knob,
// 镜像 hermesAdminOnlyFromEnv 的解析风格:unset/空 => 默认(true、启用);
// 任何可解析的 bool 被采纳;格式错误的值是一个 fail-loud 启动错误
// (绝不静默回退而禁用强制,或更糟地悄悄重新启用一个 operator 本想关掉的
// 特权面)。用于 Hermes 的两个运行时总开关。
func hermesBoolEnabledDefaultTrue(envName string) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(envName))
	if raw == "" {
		return true, nil
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean, got %q: %w", envName, raw, err)
	}
	return enabled, nil
}

// installAnthropicClaudeAIOAuthMimicryExchanger 把 default ExchangerRegistry
// 中 anthropic/claude_ai_oauth 条目替换成带显式 HTTP client 的版本。生产
// wiring 传 anthropicoauth.DefaultHTTPClient() 接 mimicry uTLS sidecar
// (profile anthropic_cli_mimicry_v1); 测试可注入 mock client 验证替换
// 真正生效, 防止"忘调用该函数"的回归。
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

// loadUserOAuthRedirectAllowlistFromEnv 加载 social OAuth init 允许的 caller redirect_uri 白名单;
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

// installCodexWebOAuthExchanger 把默认 registry 中 openai/codex_web_oauth 条目
// 替换为带显式 OAuth-grade HTTP client 的版本。codex_web_oauth 与 chatgpt_oauth 同
// 走 OpenAI 公开 CLI PKCE-only profile(无 client_secret);生产 wiring 传
// SSRF-protected client + operator env 的 admin callback allowlist,避免 callback
// token exchange silent 退化到默认 transport。
func installCodexWebOAuthExchanger(registry *credentialacq.ExchangerRegistry, client *http.Client, allowlist []string) error {
	if registry == nil {
		return fmt.Errorf("nil exchanger registry")
	}
	if client == nil {
		return fmt.Errorf("nil http client (codex_web oauth transport missing)")
	}
	return registry.RegisterOrReplaceExchanger(
		credentialstore.ModeKey(credentialstore.VendorOpenAI, credentialstore.AuthModeCodexWebOAuth),
		credentialacq.NewCodexWebOAuthExchangerWithClientAndAdminCallbackAllowlist(client, allowlist),
	)
}

// assertCodexWebOAuthExchangerHasHTTPClient 在 wiring 完成 install 后立即自检
// codex_web_oauth exchanger 已注入 HTTP client。删除 install 调用或 helper 退化
// 时进程拒启动,防止 callback 交换链路 silent 退化。
func assertCodexWebOAuthExchangerHasHTTPClient(registry *credentialacq.ExchangerRegistry) error {
	if registry == nil {
		return fmt.Errorf("nil exchanger registry")
	}
	exc, ok := registry.Lookup(credentialstore.ModeKey(credentialstore.VendorOpenAI, credentialstore.AuthModeCodexWebOAuth))
	if !ok {
		return fmt.Errorf("openai/codex_web_oauth exchanger missing from registry after install")
	}
	if !credentialacq.IsCodexWebOAuthExchangerWithExplicitClient(exc) {
		return fmt.Errorf("openai/codex_web_oauth exchanger has nil httpClient — install 未生效")
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

// dbPoolConfig 把 Config 中运维可调的连接池 override 映射进 db.PoolConfig。
// 未设置的 override 保持为零,这样 db 包沿用它的默认值(16/2/30m/5m),
// 在允许规模化调优的同时保留现有部署行为。
func dbPoolConfig(cfg *Config) db.PoolConfig {
	return db.PoolConfig{
		DSN:             cfg.DatabaseURL,
		MaxConns:        cfg.DBMaxConns,
		MinConns:        cfg.DBMinConns,
		MaxConnLifetime: cfg.DBMaxConnLifetime,
		MaxConnIdleTime: cfg.DBMaxConnIdleTime,
	}
}
