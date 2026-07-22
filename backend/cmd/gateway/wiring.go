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

	"github.com/BloomingProsperity/HUAKAI/internal/accountmodeldiscovery"
	"github.com/BloomingProsperity/HUAKAI/internal/accountquota"
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
	"github.com/BloomingProsperity/HUAKAI/internal/balanceledger"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/billingadminhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/billingmaint"
	"github.com/BloomingProsperity/HUAKAI/internal/budget"
	"github.com/BloomingProsperity/HUAKAI/internal/budgetenforce"
	l2cache "github.com/BloomingProsperity/HUAKAI/internal/cache"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/channelprobe"
	"github.com/BloomingProsperity/HUAKAI/internal/checkin"
	"github.com/BloomingProsperity/HUAKAI/internal/circuitbreaker"
	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
	communityinvitation "github.com/BloomingProsperity/HUAKAI/internal/community/invitation"
	runtimeconfig "github.com/BloomingProsperity/HUAKAI/internal/config"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/projectenrich"
	"github.com/BloomingProsperity/HUAKAI/internal/credentiallegacy"
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
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
	"github.com/BloomingProsperity/HUAKAI/internal/healthhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/hermeschat"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesconfirm"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops/mutateguard"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesrecovery"
	"github.com/BloomingProsperity/HUAKAI/internal/inboundlimit"
	"github.com/BloomingProsperity/HUAKAI/internal/logcontract"
	"github.com/BloomingProsperity/HUAKAI/internal/loginthrottle"
	"github.com/BloomingProsperity/HUAKAI/internal/logretention"
	"github.com/BloomingProsperity/HUAKAI/internal/logsink"
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
	providerantigravity "github.com/BloomingProsperity/HUAKAI/internal/provider/antigravity"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
	"github.com/BloomingProsperity/HUAKAI/internal/proxyhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/quota"
	"github.com/BloomingProsperity/HUAKAI/internal/quotaenforce"
	"github.com/BloomingProsperity/HUAKAI/internal/quotaprobe"
	ratelimit "github.com/BloomingProsperity/HUAKAI/internal/rate"
	"github.com/BloomingProsperity/HUAKAI/internal/recentreq"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/retrybudget"
	"github.com/BloomingProsperity/HUAKAI/internal/routeadmin"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/routingsignal"
	"github.com/BloomingProsperity/HUAKAI/internal/servermonitor"
	"github.com/BloomingProsperity/HUAKAI/internal/sessioncap"
	"github.com/BloomingProsperity/HUAKAI/internal/settingscipher"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementintent"
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
	"github.com/BloomingProsperity/HUAKAI/internal/upstreamfeedback"
	"github.com/BloomingProsperity/HUAKAI/internal/usageretention"
	"github.com/BloomingProsperity/HUAKAI/internal/userauditlog"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
	"github.com/BloomingProsperity/HUAKAI/internal/userkey"
	"github.com/BloomingProsperity/HUAKAI/internal/usernotice"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
	"github.com/BloomingProsperity/HUAKAI/internal/voucher"
	"github.com/BloomingProsperity/HUAKAI/internal/windowcost"
	"github.com/BloomingProsperity/HUAKAI/internal/workerlease"
	sqlmigrations "github.com/BloomingProsperity/HUAKAI/sql"
)

const (
	channelRecoveryLeaderLockKey  int64 = 0x48554B434852414D
	quotaProbeLeaderLockKey       int64 = 0x48554B51554F5441
	modelSyncLeaderLockKey        int64 = 0x48554B4D4F44454C
	proxyHealthLeaderLockKey      int64 = 0x48554B505258484C
	tlsProfileHealthLeaderLockKey int64 = 0x48554B544C534650
	opsInspectionLeaderLockKey    int64 = 0x48554B4845524D49
	stagedCleanupLeaderLockKey    int64 = 0x48554B535447434C
)

// deps 是 run() 启动后 handler 收到的 live 依赖树。
type deps struct {
	cfg *Config
	// platformTenantID 是平台自有租户(tenancy 工作租户),与 adminsessionauth /
	// balanceledger / panelauth bootstrap 同一真相源,供按租户归属裁决的 handler 注入。
	platformTenantID          int64
	readiness                 *healthhttp.Readiness
	clientIPResolver          *clientip.Resolver
	inboundRateLimit          inboundlimit.Store
	pgPool                    *pgxpool.Pool
	logSink                   *logsink.Sink
	runtimeLogStore           *logsink.PostgresStore
	logRetention              *logretention.Manager
	adminQueries              *admindb.Queries
	billingQueries            *dbbilling.Queries
	billingPolicyStore        billing.PolicyStore
	billingPolicyResolver     *billing.PolicyResolver
	selector                  pool.Selector
	selectorConfig            *runtimeconfig.PoolSelectorConfig
	queueWaiter               *queuewait.Executor
	channelHealth             *channelhealth.Service
	authCooldown              *authcooldown.Store
	modelCooldowns            *ratelimit.ModelCooldownService
	upstreamRate              ratelimit.Service
	upstreamFeedback          *upstreamfeedback.Observer
	retryBudget               *retrybudget.Budget
	claimGate                 billing.ClaimGate
	settler                   billing.Settler
	settlementIntents         settlementintent.Store
	quotaReserver             quotaenforce.Reserver
	replayStore               billing.ReplayStore
	forwarder                 *gateway.StreamForwarder
	credentialVault           provider.CredentialVault
	credentialStore           *credentialstore.Store
	credentialKeys            credentialstore.KeyProvider
	credentialAcqStore        *credentialacq.PostgresSessionStore
	credentialExchangers      *credentialacq.ExchangerRegistry
	anthropicOAuthClient      *http.Client
	projectEnricher           projectenrich.Enricher
	importCredentialRefresher accountintake.ImportCredentialRefresher
	quotaProbeWorker          *quotaprobe.Worker
	credentialScheduler       *credentialworker.Scheduler
	emailSettings             *mailinfra.PostgresSettingsStore
	authEmailSender           gatewayhttp.AuthEmailSender
	emailSendLimit            *emailsendlimit.Limiter
	userAuth                  *userauth.Service
	userSessions              *usersession.Service
	passkeys                  *passkey.Service
	twoFactor                 *twofa.Service
	loginThrottle             *loginthrottle.Limiter
	userKeyService            *userkey.Service
	userAuditStore            *userauditlog.PostgresStore
	balanceService            *balanceledger.Service
	paymentService            *payment.Service
	checkinService            *checkin.Service
	paymentProviders          map[string]paymenthttp.ProviderBinding
	paymentRefundRequests     paymenthttp.RefundRequestRecorder
	voucherService            *voucher.Service
	subscriptionService       *subscription.Service
	subExpiryWorker           *subscription.ExpiryWorker
	subReminderWorker         *subscription.ReminderWorker
	subAutoRenewWorker        *subscription.AutoRenewWorker
	notificationSettings      *notify.Service
	announcementService       *announcement.Service
	userNoticeService         *usernotice.Service
	mediaTaskService          *mediatask.Service
	mediaTaskWorker           *mediatask.Worker
	// mediaTaskStore 既供 worker/service 用,也供孤儿对账 admin 面(orphanreconcilehttp)
	// 复用其只读列表 + 单一动钱入口 ReconcileOrphan(Manual-First,复用既有 billing settle)。
	mediaTaskStore            *mediatask.PostgresStore
	routeAdminService         *routeadmin.Service
	panelAuthResolver         *panelauth.Resolver
	invitationService         *communityinvitation.Service
	dispatcher                *gateway.UpstreamDispatcher
	accountModelDiscovery     *accountmodeldiscovery.Service
	responseCache             l2cache.Store
	cacheScope                string
	dlqService                *legacydlq.Service
	obsDLQAdminStore          *obsoutbox.PostgresOutbox
	completionBus             *eventbus.Bus
	auditRefPolicy            *eventbus.AuditRefPolicy
	inboundAuth               *auth.APIKeyResolver
	auditLedger               auditledger.Ledger
	auditSigner               *sign.Signer
	cacheOverrideStore        *billing.CacheOverrideStore
	auditPubkeyRegistry       auditledger.PubkeyRegistry
	receiptStore              *auditreceipt.PGXReceiptStorage
	receiptFormatter          *auditreceipt.ReceiptFormatter
	disputeStore              *auditreceipt.CostDisputeStore
	disputeResolver           *auditreceipt.CostDisputeResolver
	refundQueue               *auditreceipt.MismatchRefundQueue
	rateTableSource           *billing.PGXRateTableSource
	pricingRatioStore         pricingcatalog.Store
	pricingRatioResolver      *pricingcatalog.RatioResolver
	modelRegistry             *registry.PostgresRegistry
	modelSync                 *modelsync.Service
	modelSyncScheduler        *modelsync.Scheduler
	routePlanner              *router.DefaultRouter
	adminAuth                 *adminsessionauth.Resolver
	adminIssuer               *admin.KeyIssuer
	adminRevoker              *admin.KeyRevoker
	adminTokenIssuer          *admin.AdminTokenIssuer
	billingAuditUpdater       billingadminhttp.AdminBillingSettingsAuditUpdater
	platformSettings          *platformsettings.Service
	hermesService             *hermes.Service
	hermesRunner              *hermes.RunnerClient
	hermesChatBridge          *hermeschat.Bridge
	hermesInternalTokenSecret []byte
	metricsHandler            http.Handler
	otelShutdown              func(context.Context) error
	sessionCapRegistry        *sessioncap.Registry
	recentReqRing             *recentreq.Ring
	alertingService           *alerting.Service
	serverMonitorStore        *servermonitor.PostgresStore
	serverMonitorEnabled      bool
	serverMonitorOffline      time.Duration
	// toolPriceSource 提供工具调用附加费价表来源(NAPI-BILLING-01)。按运维开关
	// HUAKAI_TOOL_SURCHARGE_ENABLED 构建:启用 → 带平台默认价的 platformSource;
	// 关闭 → nil(退回旧 $0 行为)。接入 chatHandlerDeps 的 ToolPricingTable。
	toolPriceSource toolpricing.Source
	// moduleRegistry 是模块知识的运行时真相源，由管理端点和 Hermes 查询。它不在任何
	// 请求热路径上;在 buildGatewayRuntime 接近结尾、probe 引用的服务都接好后填充。
	moduleRegistry *moduleregistry.Registry
	// Hermes 只读运维主干包含工具注册表、工具日志写入器和模块上下文来源。
	hermesToolRegistry *hermesops.Registry
	hermesToolCalls    *hermestoolsdb.Queries
	hermesModuleSource *moduleSource
	// hermesMutator 在原子日志和 advisory lock 事务中运行改动型工具。
	// pool 未设置时为 nil(此时 mutating 工具 fail closed)。
	// 编排器同时受并发上限和事务期限保护。
	hermesMutator *hermesops.MutateOrchestrator
	// hermesConfirmStore 由 PostgreSQL 提供跨副本原子单次消费，提议和人工确认共用。
	hermesConfirmStore hermesconfirm.Store
	// hermesMutateRateLimiter 是按管理员身份执行的滑动窗口限流器，
	// 在 mutate handler 中强制。rate 旋钮为 0 时为 nil/禁用(旧的无界行为)。
	// 仅在 admin-only 的 mutator 路径生效时挂载。
	hermesMutateRateLimiter *mutateguard.RateLimiter
	// hermesMutatingEnabled 是所有 Hermes 改动型工具的运行时总开关。
	// 默认 true(HUAKAI_HERMES_MUTATING_ENABLED)。为 false 时,tool-execute 的
	// mutating 分支被拒绝(403 hermes_mutating_disabled),且 mutator 也不会被挂入
	// router,而只读诊断 + chat 仍然在线。
	hermesMutatingEnabled bool
	// hermesToolLoopEnabled 是官方 Hermes MCP 工具面的运行时总开关。
	// 关闭后模型对话仍可用，但 MCP 目录和工具调用全部拒绝。
	hermesToolLoopEnabled bool
	// hermesProposeEnabled 控制 MCP 是否向模型暴露可逆且需人工确认的提议工具。
	hermesProposeEnabled bool
	hermesMCPHandler     *hermeschat.MCPHandler
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

func buildTransportFactory(cfg *Config) *transport.Factory {
	factory := transport.NewFactory()
	if cfg != nil {
		factory.SidecarSocketPath = cfg.TransportSidecarSocket
		// nil 时按 profile 的 ALPN 工作；非 nil 时由部署者显式覆盖。
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

const (
	geminiPublicCLIOAuthClientSecretEnv = credentialacq.GeminiPublicCLISecretEnv
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

func buildSettlementIntentStore(queries *dbbilling.Queries, enabled bool) settlementintent.Store {
	return settlementintent.NewConfiguredPostgresStore(queries, enabled)
}

func buildSettlementIntentSweeper(queries *dbbilling.Queries, enabled bool) *settlementintent.SettlementIntentSweeper {
	if !enabled {
		return nil
	}
	store := settlementintent.NewPostgresStore(queries)
	return settlementintent.NewSettlementIntentSweeper(
		store,
		settlementintent.NewPostgresClaimAuthority(queries),
		settlementintent.SweeperOptions{},
	)
}

func buildGatewayRuntime(ctx context.Context, cfg *Config, logger *zap.Logger, sink *logsink.Sink) (*gatewayRuntime, error) {
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
			pgPool.Close()
			return nil, fmt.Errorf("auto-migrate: %w", err)
		}
	}
	platformTenantID, err := tenancy.WorkingTenantIDFromEnv()
	if err != nil {
		pgPool.Close()
		return nil, fmt.Errorf("resolve working tenant: %w", err)
	}
	serverMonitorConfig, err := servermonitor.LoadConfigFromEnv()
	if err != nil {
		pgPool.Close()
		return nil, fmt.Errorf("load server monitor config: %w", err)
	}
	serverMonitorStore := servermonitor.NewPostgresStore(pgPool)
	sharedRateLimits, err := buildSharedRateLimits(ctx, cfg)
	if err != nil {
		pgPool.Close()
		return nil, err
	}

	runtimeLogStore := logsink.NewPostgresStore(pgPool)
	rt := &gatewayRuntime{pgPool: pgPool, closeInboundRateLimit: sharedRateLimits.close}
	workerCtx, cancelWorkers := context.WithCancel(context.Background())
	rt.cancelWorkers = cancelWorkers
	ready := false
	defer func() {
		if !ready {
			rt.close()
		}
	}()
	// 迁移完成后再启动日志落库，避免新表尚不存在时把启动期日志整批丢弃。sink 独立于
	// 信号 ctx，必须等其他 worker 全停后排空，最后才关闭数据库。
	if sink != nil {
		sinkCtx, sinkCancel := context.WithCancel(context.Background())
		sink.Start(sinkCtx, runtimeLogStore)
		rt.logSinkStop = func() {
			sinkCancel()
			sink.WaitDone(3 * time.Second)
		}
	}
	logRetention := logretention.New(pgPool)
	logRetention.Start(workerCtx)
	rt.logRetention = logRetention

	adminQueries := admindb.New(pgPool)
	authQueries := dbauth.New(pgPool)
	billingQueries := dbbilling.New(pgPool)
	// 提前加载凭据加密主密钥,给 secret 平台设置做 at-rest 加密(secret settings 复用同一把主密钥)。
	credentialKeys, err := loadCredentialKeyProvider()
	if err != nil {
		return nil, fmt.Errorf("load credential encryption key: %w", err)
	}
	stagedCleanupWorker := accountintake.NewStagedCleanupWorker(accountintake.StagedCleanupWorkerConfig{
		Store: accountintake.NewStagedStore(pgPool, credentialKeys),
		Lease: workerlease.NewPostgres(pgPool, stagedCleanupLeaderLockKey, "account_intake_staged_cleanup"),
	})
	stagedCleanupWorker.Start(workerCtx)
	rt.contextWorkerWaiters = append(rt.contextWorkerWaiters, contextWorkerWaiter{
		name: "account intake staged cleanup worker",
		wait: stagedCleanupWorker.Wait,
	})
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
	hermesRunner, err := hermes.NewRunnerClientFromEnv()
	if err != nil {
		return nil, fmt.Errorf("build hermes runner client: %w", err)
	}
	var hermesInternalTokenSecret []byte
	var hermesService *hermes.Service
	if hermesRunner != nil {
		hermesInternalTokenSecret, err = loadHermesInternalTokenSecret()
		if err != nil {
			return nil, err
		}
		hermesService = hermes.NewServiceWithTx(hermesQueries, pgPool)
		logger.Info("Hermes runner: configured")
	} else {
		logger.Info("Hermes runner: disabled (env missing)")
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
	// 模型提议开关默认关闭；部署者显式启用前，模型不能提议任何改动型工具。
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
		hermesService.WithMessageContentKeys(credentialKeys).WithProfileCredentialKeys(credentialKeys)
	}
	credentialStore := credentialstore.NewStore(pgPool, credentialKeys, credentialstore.DefaultHandlerRegistry())
	credentialVault := provider.NewPostgresCredentialVaultWithStore(pgPool, credentialStore)
	accountProxyResolver := provider.NewPostgresProxyResolverWithKeys(pgPool, credentialKeys)
	antigravityProjectResolver := &providerantigravity.ProjectResolver{}
	credentialProjectEnricher := projectenrich.New(antigravityProjectResolver)
	// 启动期凭证密钥自检(fail-closed):用当前 KEK 解一条既有 active 凭证,解不开则拒绝启动,
	// 避免 operator 轮换密钥后在运行时全 relay 静默解密瘫痪而无启动信号。详见 VerifyKeySelfCheck。
	if err := credentialStore.VerifyKeySelfCheck(ctx); err != nil {
		return nil, err
	}
	legacyCredentialCount, err := credentiallegacy.Count(ctx, pgPool)
	if err != nil {
		return nil, err
	}
	if legacyCredentialCount > 0 {
		logger.Warn("检测到仍使用旧内联凭据的账号；新建入口已禁止该形态，请逐账号重新导入到加密凭据存储",
			zap.Int64("account_count", legacyCredentialCount),
			zap.String(logcontract.FieldCategory, string(logcontract.CategorySecurity)),
			zap.String(logcontract.FieldEventType, "credential.legacy_inline_detected"),
			zap.String(logcontract.FieldResult, string(logcontract.ResultPartial)),
			zap.String(logcontract.FieldErrorClass, string(logcontract.ErrorManualRecovery)),
			zap.Bool(logcontract.FieldRetryable, false),
			zap.String(logcontract.FieldRecoveryState, string(logcontract.RecoveryOperatorRequired)))
	}
	credentialAcqStore := credentialacq.NewPostgresSessionStoreWithKeys(pgPool, credentialKeys)
	transportFactory := buildTransportFactory(cfg)
	if err := mimicry.ProbeSidecarReadiness(ctx, cfg.TransportSidecarSocket); err != nil {
		return nil, fmt.Errorf("probe Rust sidecar readiness: %w", err)
	}
	readinessChecks := []healthhttp.ReadinessCheck{
		healthhttp.ReadinessCheck{Name: "database", Run: pgPool.Ping},
		healthhttp.ReadinessCheck{Name: "tls_sidecar", Run: func(checkCtx context.Context) error {
			return mimicry.ProbeSidecarReadiness(checkCtx, cfg.TransportSidecarSocket)
		}},
	}
	if sharedRateLimits.ping != nil {
		readinessChecks = append(readinessChecks, healthhttp.ReadinessCheck{Name: "rate_limit_store", Run: sharedRateLimits.ping})
	}
	readiness := healthhttp.NewReadiness(readinessChecks...)
	rt.readiness = readiness
	credentialExchangers := credentialacq.DefaultExchangerRegistry()
	anthropicOAuthHTTPClient, err := anthropicoauth.NewHTTPClient(transportFactory)
	if err != nil {
		return nil, fmt.Errorf("build anthropic Rust transport: %w", err)
	}
	if err := installAnthropicClaudeAIOAuthMimicryExchanger(credentialExchangers, anthropicOAuthHTTPClient); err != nil {
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
		// 未保存设置时，两个默认值均来自 channelhealth.DefaultPolicy 的 5 分钟现实值；
		// 保存后由 platformsettings 的现有缓存按新事件读取，旧 CooldownUntil 不回写。
		channelhealth.WithCooldownSource(platformCooldownSource{settings: platformSettingsService}),
	}
	if authCooldownStore != nil {
		channelHealthOptions = append(channelHealthOptions, channelhealth.WithAuthCooldownLane(authCooldownStore))
	}
	channelHealthService := channelhealth.NewService(channelHealthStore, channelhealth.DefaultPolicy(), nil, channelHealthOptions...)
	// ACCOUNT-WINDOW-COST：在 selector 之前构建 window cost cache，这样 gate
	// 在启动时就能引用它。worker 在 selector 就绪后再启动。
	windowCostCache := windowcost.NewCache()
	// ACCOUNT-SESSION-CAP：按账号的最大并发会话封顶 registry。
	sessionCapRegistry := sessioncap.NewRegistry(0)
	recentReqRing := recentreq.NewRing()
	selector, accountRateCounter, selectorCleanup, err := buildSelector(workerCtx, billingQueries, pgPool, opts.selector, channelHealthService, windowCostCache, sessionCapRegistry, logger)
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
	balanceService := balanceledger.NewService(balanceledger.NewPostgresStore(pgPool, platformTenantID))
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
	userAuthService.SignupRewardRecoveryFn = func(ctx context.Context, tenantID, userID int64, rewardKind string) error {
		return payment.EnqueueSignupRewardRecovery(ctx, outboxStore, tenantID, userID, rewardKind)
	}
	outboxWorker.Register(obsoutbox.EventTypeSignupReward,
		payment.NewSignupRewardRecoveryHandler(paymentService, signupInviteeCfg))
	// 启用配额强制时，把 mismatch 与成本争议退款的配额冲减加入同一 PostgreSQL 事务。
	// 未启用配额强制时不创建冲减器，退款不需要触碰 quota 表。
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
	// 争议裁决必须拿到底层可加入现有事务的退款执行器；外层 Settler 接口的
	// Refund 会自行开事务，无法与状态更新形成同提交/同回滚。
	disputeRefundSettler := billing.NewSettler(pgPool, billing.WithDLQStore(dlqStore), billing.WithReplicaTarget(replicaTarget))
	disputeResolverOpts := []auditreceipt.CostDisputeResolverOption{}
	if quotaReverser, ok := refundQuotaReverser.(auditreceipt.QuotaReverserInTx); ok {
		disputeResolverOpts = append(disputeResolverOpts, auditreceipt.WithDisputeQuotaReverser(quotaReverser))
	}
	disputeResolver, err := auditreceipt.NewCostDisputeResolver(pgPool, disputeRefundSettler, disputeResolverOpts...)
	if err != nil {
		return nil, fmt.Errorf("build dispute refund resolver: %w", err)
	}
	settler, quotaReserver := buildQuotaEnforcement(cfg, pgPool, settler, platformSettingsService)
	settler = notify.NewSettler(settler, notifier, notify.WithSettlerDeliveryErrorRecorder(func(err error) {
		logger.Warn("low balance notification delivery failed", zap.Error(err))
	}))
	claimGate := newClaimGateWithLease(pgPool)
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
	settlementHandler := newSettlementRecoveryHandler(settler, settlementProof, auditRefPolicy)
	dlqService.Register(legacydlq.EventKindPostDeliverySettlement, settlementHandler.Handle)
	modelRegistry := registry.NewPostgresRegistry(pgPool, nil)
	// 工具注册表、工具日志写入器和改动编排器在聊天桥之前构建，使管理端点与 MCP
	// 共用同一目录和持久化合同。每个工具复用既有查询或管理写路径。
	hermesToolRegistry, hermesToolCalls, hermesMutator, err := buildHermesToolRegistry(hermesToolDeps{
		pool:           pgPool,
		adminQueries:   adminQueries,
		billingQueries: billingQueries,
		credentialStr:  credentialStore,
		channelHealth:  channelHealthService,
		dlqStore:       dlqStore,
		dlqService:     dlqService,
		modelRegistry:  modelRegistry,
		vendorOAuth:    cfg.VendorOAuth,
	}, hermesMutateGuard.orchestratorOptions()...)
	if err != nil {
		return nil, err
	}
	hermesRecoveryWorker, err := hermesrecovery.NewWorker(hermesrecovery.NewStore(pgPool), dlqService.Replay)
	if err != nil {
		return nil, fmt.Errorf("构造 Hermes 变更恢复任务：%w", err)
	}
	hermesRecoveryWorker.Start(workerCtx)
	rt.contextWorkerWaiters = append(rt.contextWorkerWaiters, contextWorkerWaiter{
		name: "Hermes 变更恢复任务",
		wait: hermesRecoveryWorker.Wait,
	})
	// 改动确认按运营者令牌限流，由改动处理器统一强制执行。
	hermesMutateRateLimiter := hermesMutateGuard.newRateLimiter()
	var hermesChatBridge *hermeschat.Bridge
	if hermesRunner != nil {
		hermesChatBridge, err = buildHermesChatBridge(hermesService, dlqService, platformSettingsService, credentialKeys, hermesInternalTokenSecret)
		if err != nil {
			return nil, err
		}
	}
	if hermesService != nil {
		retentionDays, err := hermes.MessageRetentionDaysFromEnv()
		if err != nil {
			return nil, err
		}
		hermesRetentionWorker := hermes.NewMessageRetentionWorker(hermes.MessageRetentionWorkerConfig{
			Store:         hermes.NewPostgresMessagePurgeStore(hermesQueries),
			RetentionDays: retentionDays,
		})
		hermesRetentionWorker.Start(workerCtx)
		rt.hermesRetentionWorker = hermesRetentionWorker
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
		usageRetentionWorker.Start(workerCtx)
		rt.usageRetentionWorker = usageRetentionWorker
	}

	// 持久幂等重放存储 + 过期清理 janitor, 防表无界增长。
	replayStore := billing.NewReplayStore(pgPool)
	replayJanitor := billing.NewReplayJanitor(replayStore, 0)
	replayJanitor.Start(workerCtx)
	rt.replayJanitorStop = replayJanitor.Stop
	// scheduler_outbox 修剪 janitor:每笔成功结算写一行 outbox,无修剪会无界增长。
	outboxJanitor := billingmaint.NewSchedulerOutboxJanitor(billingmaint.NewOutboxPruneStore(pgPool), 0, 0)
	outboxJanitor.Start(workerCtx)
	rt.outboxJanitorStop = outboxJanitor.Stop
	leaseSweeper := billing.NewLeaseSweeper(pgPool, settler, 0)
	leaseSweeper.Start(workerCtx)
	rt.leaseSweepStop = leaseSweeper.Stop
	// sweeper 与正向意图共用默认关闭的总开关：关闭时不构造、不启动，也不扫描数据库。
	// 多副本无需选主来保证正确性，终态写由意图行的 version 与悬挂状态守卫裁决单胜者。
	settlementIntentSweeper := buildSettlementIntentSweeper(billingQueries, cfg.SettlementIntentEnabled)
	if settlementIntentSweeper != nil {
		settlementIntentSweeper.Start(workerCtx)
		rt.settlementIntentSweepStop = settlementIntentSweeper.Stop
	}
	pendingReconciler := billing.NewPendingReconciliationWorker(
		billing.NewPostgresPendingReconciliationFinalizer(pgPool),
		0,
		0,
		0,
	)
	pendingReconciler.Start(workerCtx)
	rt.pendingReconcileStop = pendingReconciler.Stop

	// quota 补偿器 worker:每轮两段——①重放结算/释放失败后入队的补偿 job;②清扫 lease 已过期、
	// billing claim 已终态、但补偿 job 从未入队(进程死于 billing 终态与 quota 补偿之间的崩溃窗口)
	// 的孤儿预留,按 claim 终态定向 Settle/Release。两段缺一,预留都会永久卡 reserved 冻结窗口
	// headroom(手动/累计窗无滚动自愈)。默认启动;显式 false/0 才跳过。
	if quotaReconcilerEnabledFromEnv() {
		quotaReconciler := quota.NewReconciler(nil, quota.NewPostgresStore(pgPool), quota.ReconcilerOptions{})
		quotaWorker := quota.NewReconciliationWorker(quotaReconciler, 0)
		quotaWorker.Start(workerCtx)
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
		proxyhealth.WithLeaderLease(workerlease.NewPostgres(pgPool, proxyHealthLeaderLockKey, "proxy_health")),
	)
	proxyHealthWorker.Start(workerCtx)
	rt.contextWorkerWaiters = append(rt.contextWorkerWaiters, contextWorkerWaiter{
		name: "proxy health worker",
		wait: proxyHealthWorker.Wait,
	})

	// TLS 指纹 profile 漂移/健康 worker。周期校验 active 自定义 profile
	// 能否转换为 Rust IPC 合同，坏的标 drift_detected（不伪造线上握手结果）。
	tlsProfileHealthWorker := tlsfphealth.NewWorker(
		tlsfphealth.NewPostgresLister(pgPool),
		tlsfphealth.NewPostgresDriftMarker(pgPool),
		tlsfphealth.DefaultInterval,
		nil,
		tlsfphealth.WithLeaderLease(workerlease.NewPostgres(pgPool, tlsProfileHealthLeaderLockKey, "tls_profile_health")),
	)
	tlsProfileHealthWorker.Start(workerCtx)
	rt.contextWorkerWaiters = append(rt.contextWorkerWaiters, contextWorkerWaiter{
		name: "TLS profile health worker",
		wait: tlsProfileHealthWorker.Wait,
	})

	// ACCOUNT-WINDOW-COST：启动按账号的 5 小时窗口消费封顶聚合器。
	windowCostWorker := windowcost.NewWorker(
		windowcost.NewPostgresLister(pgPool),
		windowcost.NewPostgresAggregator(pgPool),
		windowCostCache,
		windowcost.DefaultInterval,
		nil,
	)
	windowCostWorker.Start(workerCtx)
	rt.contextWorkerWaiters = append(rt.contextWorkerWaiters, contextWorkerWaiter{
		name: "window cost worker",
		wait: windowCostWorker.Wait,
	})

	// 渠道恢复协调器只扫描已到期冷却与渐进放量记录；请求热路径不再改健康状态。
	// PostgreSQL 会话级租约保证多副本同一轮只有一个执行者。当前不发付费探测，
	// 仅按真实流量样本或最长观察期推进恢复阶段。
	channelRampScheduler := channelprobe.NewChannelHealthScheduler(channelprobe.SchedulerConfig{
		Channels:    channelprobe.NewPostgresRampingChannelLister(pgPool, 0),
		Ramp:        channelHealthService,
		LeaderLease: workerlease.NewPostgres(pgPool, channelRecoveryLeaderLockKey, "channel_recovery"),
		Interval:    channelprobe.DefaultSchedulerInterval,
	})
	channelRampDone := make(chan struct{})
	go func() {
		defer close(channelRampDone)
		if err := channelRampScheduler.Run(workerCtx); err != nil {
			slog.WarnContext(workerCtx, "channel ramp scheduler exited with error", "error", err)
		}
	}()
	rt.contextWorkerWaiters = append(rt.contextWorkerWaiters, contextWorkerWaiter{
		name: "channel ramp scheduler",
		wait: func(ctx context.Context) error {
			select {
			case <-channelRampDone:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})

	// 厂商额度统一写入规范化投影；只有新鲜且明确耗尽的事实参与硬 gate。
	quotaVendorHTTPClient := auth.NewSSRFProtectedOAuthClient(http.DefaultClient)
	quotaProbeWorker := quotaprobe.NewWorker(quotaprobe.WorkerConfig{
		Accounts:      quotaprobe.NewPostgresAccountLister(pgPool),
		Vault:         credentialVault,
		Fetcher:       quotaprobe.NewHTTPUsageFetcher(anthropicOAuthHTTPClient, accountProxyResolver),
		Store:         ratelimit.NewPostgresSessionWindowStore(pgPool),
		FactStore:     accountquota.NewPostgresStore(pgPool),
		Subscriptions: credentialStore,
		Adapters: []quotaprobe.VendorAdapter{
			quotaprobe.NewCodexUsageAdapter(quotaVendorHTTPClient, accountProxyResolver),
			quotaprobe.NewAntigravityAdapter(quotaVendorHTTPClient, accountProxyResolver),
			quotaprobe.GeminiUnknownAdapter{},
			quotaprobe.NewGrokBillingAdapter(quotaVendorHTTPClient, accountProxyResolver),
		},
		Settings:    platformSettingsService,
		LeaderLease: workerlease.NewPostgres(pgPool, quotaProbeLeaderLockKey, "quota_probe"),
	})
	quotaProbeWorker.Start(workerCtx)
	rt.contextWorkerWaiters = append(rt.contextWorkerWaiters, contextWorkerWaiter{
		name: "quota probe worker",
		wait: quotaProbeWorker.Wait,
	})

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

	loginThrottle, err := loadLoginThrottleFromEnv(sharedRateLimits.login)
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
	// TotalStreamTimeout 的现实默认来自 defaultGatewayTotalStreamTimeout（600 秒）。
	// 仅 source=db 的显式平台设置覆盖原有 env；source=default 时保留接线前 env 行为。
	gatewayTimeouts := buildGatewayTimeoutConfig(ctx, platformSettingsService)
	gatewayDispatcher := &gateway.UpstreamDispatcher{
		Adapters:                 registrydefault.Build(),
		TransportFactory:         transportFactory,
		ProxyResolver:            accountProxyResolver,
		TLSProfileResolver:       tlsfpresolve.NewPostgresResolver(pgPool),
		Timeouts:                 gatewayTimeouts,
		AnthropicAutoBreakpoints: cfg.CacheAnthropicAutoBreakpoints,
		AnthropicTTLSettings:     platformSettingsService,
		HTTPClient:               devMockUpstreamDoer(),
	}
	accountModelDiscovery := accountmodeldiscovery.NewService(credentialVault, gatewayDispatcher, pgPool)
	mediaTaskConfig := mediatask.NewPlatformConfigSource(platformSettingsService, cfg.BillingPolicyVersion, cfg.RequestClass)
	mediaTaskStore := mediatask.NewPostgresStore(pgPool, mediatask.PostgresStoreConfig{
		BillingPolicyVersion: cfg.BillingPolicyVersion,
		RequestClass:         cfg.RequestClass,
		BalanceModeResolver:  billingPolicyResolver,
		ClaimGate:            claimGate,
		QuotaReserver:        quotaReserver,
		Settler:              settler,
	})
	// 媒体 provider 出站客户端必须带 Timeout(与同文件 modelsync fetcher 一致):裸 http.DefaultClient
	// 无超时,慢上游/半开连接会让串行媒体 worker 永久挂起。worker 侧已对每次 Submit/Poll 加 context 硬超时
	// 为主防线,此处 client.Timeout 作外层兜底(防 provider 实现忽略 ctx)。
	legacyMediaProviders := mediatask.NewHTTPProviderRegistry(mediaTaskConfig, &http.Client{Timeout: 60 * time.Second})
	mediaAccountAdmitter := mediatask.NewPostgresAccountRequestAdmitter(pgPool, accountRateCounter)
	grokVideoProvider := mediatask.NewGrokVideoProvider(mediatask.GrokVideoProviderDeps{
		Selector: selector, AccountAdmitter: mediaAccountAdmitter,
		CredentialVault: credentialVault, Dispatcher: gatewayDispatcher,
	})
	geminiVideoProvider := mediatask.NewGeminiVideoProvider(mediatask.GrokVideoProviderDeps{
		Selector: selector, AccountAdmitter: mediaAccountAdmitter,
		CredentialVault: credentialVault, Dispatcher: gatewayDispatcher,
	})
	mediaTaskProviders := mediatask.NewOverlayProviderRegistry(legacyMediaProviders, map[string]mediatask.AsyncMediaProvider{
		"grok_video": grokVideoProvider, "gemini_video": geminiVideoProvider,
	})
	mediaTaskService := mediatask.NewService(mediaTaskStore, mediaTaskConfig, mediaTaskProviders)
	// OrphanReporter 把"租约丢失致 providerTaskID 未落库"的孤儿上游任务持久化到 media_task_orphans,
	// 供对账消费者/运维查处(此前仅日志,易随轮转丢失)。logger 传 nil → 与 worker 同走 slog 默认实例。
	mediaTaskWorker := mediatask.NewWorker(mediaTaskStore, mediaTaskConfig, mediaTaskProviders, mediatask.WorkerOptions{
		OrphanReporter: mediatask.NewPersistingOrphanReporter(mediaTaskStore, nil),
	})
	rt.mediaTaskWorker = mediaTaskWorker
	userAuditStore := userauditlog.NewPostgresStore(pgPool)

	d := &deps{
		cfg:                   cfg,
		platformTenantID:      platformTenantID,
		readiness:             readiness,
		clientIPResolver:      clientIPResolver,
		inboundRateLimit:      sharedRateLimits.inbound,
		logSink:               sink,
		runtimeLogStore:       runtimeLogStore,
		logRetention:          logRetention,
		adminQueries:          adminQueries,
		billingQueries:        billingQueries,
		billingPolicyStore:    billingPolicyStore,
		billingPolicyResolver: billingPolicyResolver,
		selector:              selector,
		selectorConfig:        opts.selector,
		queueWaiter:           queuewait.NewExecutor(),
		sessionCapRegistry:    sessionCapRegistry,
		recentReqRing:         recentReqRing,
		toolPriceSource:       buildToolPriceSource(),
		channelHealth:         channelHealthService,
		authCooldown:          authCooldownStore,
		modelCooldowns:        ratelimit.NewModelCooldownService(billingQueries),
		upstreamRate:          ratelimit.NewUpstreamRateServiceWithSessionWindowStore(nil, channelHealthService.Policy().DefaultRateLimitCooldown, ratelimit.NewPostgresSessionWindowStore(pgPool), ratelimit.WithAccountErrorRulesProvider(ratelimit.NewPostgresAccountErrorRulesProvider(pgPool)), ratelimit.WithCooldownStateStore(ratelimit.NewPostgresCooldownStateStore(pgPool)), ratelimit.WithCooldownSource(platformCooldownSource{settings: platformSettingsService})),
		retryBudget:           tenantRetryBudget,
		claimGate:             claimGate,
		settler:               settler,
		settlementIntents:     buildSettlementIntentStore(billingQueries, cfg.SettlementIntentEnabled),
		quotaReserver:         quotaReserver,
		replayStore:           replayStore,
		forwarder:             buildStreamForwarder(auditLedger, auditSigner, dlqService, gatewayTimeouts),
		credentialVault:       credentialVault,
		credentialStore:       credentialStore,
		credentialKeys:        credentialKeys,
		credentialAcqStore:    credentialAcqStore,
		credentialExchangers:  credentialExchangers,
		anthropicOAuthClient:  anthropicOAuthHTTPClient,
		projectEnricher:       credentialProjectEnricher,
		quotaProbeWorker:      quotaProbeWorker,
		emailSettings:         emailSettingsStore,
		authEmailSender:       authEmailSender,
		emailSendLimit:        emailSendLimit,
		userAuth:              userAuthService,
		pgPool:                pgPool,
		serverMonitorStore:    serverMonitorStore,
		serverMonitorEnabled:  serverMonitorConfig.Enabled,
		serverMonitorOffline:  serverMonitorConfig.OfflineAfter,
		userSessions:          userSessionService,
		passkeys:              passkeyService,
		twoFactor:             twoFactorService,
		loginThrottle:         loginThrottle,
		userKeyService:        userkey.NewService(pgPool, nil, userkey.WithAuditSink(userAuditStore), userkey.WithDefaultKeyQuota(runtimeconfig.LoadDefaultKeyQuota())),
		userAuditStore:        userAuditStore,
		balanceService:        balanceService,
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
		dispatcher:            gatewayDispatcher,
		accountModelDiscovery: accountModelDiscovery,
		inboundAuth:           auth.NewAPIKeyResolverWithClientIPResolver(authQueries, clientIPResolver),
		auditLedger:           auditLedger,
		auditSigner:           auditSigner,
		cacheOverrideStore:    billing.NewCacheOverrideStore(auditSigner, nil),
		auditPubkeyRegistry:   auditPubkeyRegistry,
		receiptStore:          receiptStore,
		receiptFormatter:      receiptFormatter,
		disputeStore:          disputeStore,
		disputeResolver:       disputeResolver,
		refundQueue:           refundQueue,
		rateTableSource:       rateTableSource,
		pricingRatioStore:     pricingRatioStore,
		pricingRatioResolver:  pricingRatioResolver,
		modelRegistry:         modelRegistry,
		modelSync:             modelSyncService,
		routePlanner:          router.NewDefaultRouter(),
		adminAuth: adminsessionauth.New(
			admin.NewAdminResolver(adminQueries),   // 令牌通道(hk_admin_,行为不变)
			userSessionService,                     // session 校验器
			panelauth.NewPostgresRoleStore(pgPool), // users.role 只读查询
			clientIPResolver,
			platformTenantID,
		),
		adminIssuer:               admin.NewKeyIssuer(pgPool),
		adminRevoker:              admin.NewKeyRevoker(pgPool),
		adminTokenIssuer:          admin.NewAdminTokenIssuer(pgPool),
		billingAuditUpdater:       billingadminhttp.NewAdminBillingSettingsAuditUpdater(pgPool),
		platformSettings:          platformSettingsService,
		hermesService:             hermesService,
		hermesRunner:              hermesRunner,
		hermesChatBridge:          hermesChatBridge,
		hermesInternalTokenSecret: hermesInternalTokenSecret,
		hermesToolRegistry:        hermesToolRegistry,
		hermesToolCalls:           hermesToolCalls,
		hermesMutator:             hermesMutator,
		hermesConfirmStore:        hermesconfirm.NewPostgresStore(pgPool),
		hermesMutateRateLimiter:   hermesMutateRateLimiter,
		hermesMutatingEnabled:     hermesMutatingEnabled,
		hermesToolLoopEnabled:     hermesToolLoopEnabled,
		hermesProposeEnabled:      hermesProposeEnabled,
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
	rt.apiKeyExpirySweepStop = startAPIKeyExpiryWorker(workerCtx, cfg, apiKeyExpiryWorker)
	if cfg.PaymentExpireSweepInterval > 0 {
		paymentExpireSweeper := payment.NewExpireSweeper(payment.ExpireSweeperConfig{
			Store:      paymentStore,
			Interval:   cfg.PaymentExpireSweepInterval,
			BatchLimit: cfg.PaymentExpireSweepBatchLimit,
			Logger:     logger,
		})
		paymentExpireSweeper.Start(workerCtx)
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
		// NotifyEmail 的现实默认来自 bool 零值 false，收件人现实默认来自
		// platformsettings.KeyAdminNotificationEmail 的空串；二者都未配置时不新增任何发送。
		alerting.WithFiringEmailDeliverer(alerting.NewAdminEmailDeliverer(platformSettingsService, notificationEmailSender, slog.Default())),
		alerting.WithFiringDeliveryErrorRecorder(func(_ context.Context, tenantID int64, notice alerting.FiringNotice, err error) {
			logger.Warn("alert firing notification delivery failed",
				zap.Int64("tenant_id", tenantID),
				zap.Int64("rule_id", notice.RuleID),
				zap.String("rule_name", notice.RuleName),
				zap.Error(err),
			)
		}),
	)
	d.alertingService = alertingService
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
	if serverMonitorConfig.Enabled {
		identity, err := servermonitor.ResolveIdentity(serverMonitorConfig)
		if err != nil {
			return nil, fmt.Errorf("resolve server monitor identity: %w", err)
		}
		session, err := servermonitor.NewSession(time.Now())
		if err != nil {
			return nil, errors.New("create server monitor session failed")
		}
		monitorWorker, err := servermonitor.NewWorker(servermonitor.WorkerConfig{
			Identity:        identity,
			Session:         session,
			Collector:       servermonitor.NewCollector(),
			Store:           serverMonitorStore,
			NodeLease:       workerlease.NewPostgres(pgPool, servermonitor.NodeLeaseKey(identity.NodeID), "server_monitor_node"),
			CleanupLease:    workerlease.NewPostgres(pgPool, servermonitor.CleanupLeaseKey(), "server_monitor_cleanup"),
			Interval:        serverMonitorConfig.Interval,
			Retention:       serverMonitorConfig.Retention,
			CleanupInterval: serverMonitorConfig.CleanupInterval,
			CleanupBatch:    serverMonitorConfig.CleanupBatch,
		})
		if err != nil {
			return nil, fmt.Errorf("build server monitor worker: %w", err)
		}
		if err := monitorWorker.Start(workerCtx); err != nil {
			return nil, fmt.Errorf("start server monitor worker: %w", err)
		}
		rt.serverMonitorWorker = monitorWorker
		logger.Info("服务器实例监测已启动",
			zap.String("node_id", identity.NodeID),
			zap.String("identity_source", string(identity.Source)),
			zap.Bool("identity_stable", identity.Stable),
			zap.Duration("interval", serverMonitorConfig.Interval),
			zap.Duration("offline_after", serverMonitorConfig.OfflineAfter),
		)
	}

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
	if err := panelauth.MaybeBootstrapAdminUser(ctx, pgPool, platformTenantID, logger); err != nil {
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
	modeAdapterRegistry := credentialworker.DefaultModeAdapterRegistryWithRuntimeDependencies(
		antigravityProjectResolver, cfg.VendorOAuth, anthropicOAuthHTTPClient,
	)
	d.importCredentialRefresher = accountintake.NewModeRegistryImportRefresher(modeAdapterRegistry)
	credentialRefresher := credentialworker.NewAccountCredentialRefresher(credentialStore, modeAdapterRegistry)
	credentialSchedulerOptions := []credentialworker.Option{
		credentialworker.WithAuditQueries(authQueries),
		credentialworker.WithAuditLedger(auditLedger),
		credentialworker.WithRefreshQueries(credentialworker.NewAccountCredentialRefreshQueries(pgPool)),
		// 启用同事务路径 (RR-W5-002 步骤 1):audit insert + ledger append 同 tx。
		credentialworker.WithTxPool(pgPool),
		credentialworker.WithAuditLedgerSigner(auditSigner),
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
	credentialScheduler := credentialworker.NewScheduler(
		billingQueries,
		auth.NewStormControllerWithSharedScopeBudget(authQueries, stormScopeCfg, sharedRateLimits.storm,
			auth.WithRefreshAccountLockPool(pgPool)),
		auditSigner,
		credentialRefresher,
		credentialSchedulerOptions...,
	)
	if err := credentialScheduler.Start(workerCtx); err != nil {
		return nil, fmt.Errorf("start credential refresh scheduler: %w", err)
	}
	d.credentialScheduler = credentialScheduler
	d.upstreamFeedback = upstreamfeedback.NewObserver(upstreamfeedback.Dependencies{
		ChannelHealth:          d.channelHealth,
		ModelCooldowns:         d.modelCooldowns,
		RateService:            d.upstreamRate,
		CredentialHotRefresher: credentialScheduler,
		AuthCooldown:           d.authCooldown,
		RecentRequests:         d.recentReqRing,
		RoutingSignals:         routingsignal.NewPostgresRecorder(pgPool),
	})
	grokVideoProvider.SetFeedback(d.upstreamFeedback)
	geminiVideoProvider.SetFeedback(d.upstreamFeedback)
	mediaTaskWorker.Start(workerCtx)
	if opts.obsDLQ.Enabled {
		dlqWorker.Start(workerCtx)
	}
	outboxWorker.Start(workerCtx)
	if opts.modelSync != nil && opts.modelSync.Enabled && modelSyncService != nil {
		scheduler := modelsync.NewScheduler(modelSyncService, modelsync.SchedulerConfig{
			Interval:    opts.modelSync.Interval,
			RunOnStart:  true,
			LeaderLease: workerlease.NewPostgres(pgPool, modelSyncLeaderLockKey, "model_sync"),
		})
		d.modelSyncScheduler = scheduler
		rt.modelSyncStop = scheduler.Start(workerCtx)
	}

	subscriptionExpiryWorker := subscription.NewExpiryWorker(subscription.ExpiryWorkerConfig{Service: d.subscriptionService})
	subscriptionExpiryWorker.Start(workerCtx)

	subscriptionReminderWorker := subscription.NewReminderWorker(subscription.ReminderWorkerConfig{
		Service: subscription.NewReminderService(
			subscription.NewPostgresStore(pgPool),
			subscription.NewEmailReminderMailer(notificationEmailSender),
		),
	})
	subscriptionReminderWorker.Start(workerCtx)
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
		subscriptionAutoRenewWorker.Start(workerCtx)
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
	// 最后构建模块知识真相源，等待所有探针依赖的服务
	//(settler、selector、credential store + scheduler)都接好之后,这样 seed
	// probe 捕获到的是 live(非 nil)引用。不在请求热路径上。
	d.moduleRegistry = buildModuleRegistry(d)
	// 工具主干已在聊天桥之前构建；这里只构建共用的模块上下文来源。
	d.hermesModuleSource = newModuleSource(d.moduleRegistry)
	// 官方 Hermes 通过标准 MCP 读取工具目录并调用工具。短时内部令牌携带固定租户和真实管理员，
	// 不再向 runner 注入自定义工具目录或私有执行器。
	if d.hermesChatBridge != nil {
		if internalSecret := strings.TrimSpace(os.Getenv(hermeschat.InternalTokenSecretEnv)); internalSecret != "" {
			d.hermesMCPHandler = buildHermesMCPHandler(
				[]byte(internalSecret), d.hermesToolRegistry, d.hermesToolCalls, d.hermesToolLoopEnabled,
				d.hermesConfirmStore, d.hermesProposeEnabled,
			)
		}
	}
	// 每日运维巡检默认关闭，只在显式开启并解析出管理员收件人时启动。
	// 所有读取固定在部署者平台租户，不接受环境变量覆盖租户。
	if inspectionWorker := buildOpsInspectionWorker(ctx, opsInspectionDeps{
		platformTenantID: platformTenantID,
		settings:         platformSettingsService,
		emailSender:      notificationEmailSender,
		moduleRegistry:   d.moduleRegistry,
		credentialStr:    credentialStore,
		channelHealth:    channelHealthService,
		dlqStore:         dlqStore,
		obsDLQStore:      outboxStore,
		billingQueries:   billingQueries,
		logger:           logger,
		leaderLease:      workerlease.NewPostgres(pgPool, opsInspectionLeaderLockKey, "ops_inspection"),
		windowClaims:     sharedRateLimits.windows,
	}); inspectionWorker != nil {
		inspectionWorker.Start(workerCtx)
		rt.opsInspectionWorker = inspectionWorker
	}
	readiness.MarkReady()
	ready = true
	return rt, nil
}

func newSettlementRecoveryHandler(settler billing.Settler, proof settlementrecovery.CommittedProof, auditRefPolicy *eventbus.AuditRefPolicy) *settlementrecovery.Handler {
	return &settlementrecovery.Handler{
		Settler:        settler,
		Proof:          proof,
		AuditRefPolicy: auditRefPolicy,
	}
}

func buildModelSyncService(cfg *runtimeconfig.ModelSyncConfig, store *registry.PostgresRegistry) *modelsync.Service {
	if cfg == nil || store == nil {
		return nil
	}
	fetchers := make([]modelsync.Fetcher, 0, 4)
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
	if cfg.Grok.Configured() {
		fetchers = append(fetchers, modelsync.NewHTTPFetcher(modelsync.HTTPFetcherConfig{
			Vendor:         modelsync.VendorGrok,
			URL:            cfg.Grok.URL,
			APIKey:         cfg.Grok.APIKey,
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

func buildHermesChatBridge(hermesService *hermes.Service, dlqService *legacydlq.Service, settings *platformsettings.Service, keys credentialstore.KeyProvider, internalTokenSecret []byte) (*hermeschat.Bridge, error) {
	if hermesService == nil {
		return nil, nil
	}
	if keys == nil {
		return nil, fmt.Errorf("%w: Hermes message content encryption key provider is required", hermes.ErrMisconfigured)
	}
	if len(internalTokenSecret) == 0 {
		return nil, fmt.Errorf("%w: %s is required for Hermes chat bridge", hermes.ErrMisconfigured, hermeschat.InternalTokenSecretEnv)
	}
	opts := []hermeschat.Option{
		hermeschat.WithInternalTokenSecret(internalTokenSecret),
		hermeschat.WithAuditDLQ(dlqService),
		hermeschat.WithResponseHeaderSettings(settings),
		hermeschat.WithMessageContentKeys(keys),
	}
	bridge, err := hermeschat.NewBridge(hermesService, opts...)
	if err != nil {
		return nil, fmt.Errorf("build hermes chat bridge: %w", err)
	}
	return bridge, nil
}

func loadHermesInternalTokenSecret() ([]byte, error) {
	secret := strings.TrimSpace(os.Getenv(hermeschat.InternalTokenSecretEnv))
	if secret == "" {
		return nil, fmt.Errorf("%w: %s is required for Hermes internal routes", hermes.ErrMisconfigured, hermeschat.InternalTokenSecretEnv)
	}
	return []byte(secret), nil
}

// hermesMutatingEnabledEnv 是所有 Hermes mutating 工具
// (account_pause/account_resume/dlq_replay/renew_trigger)的运行时总开关。
// 它默认 ENABLED,因此未设置 env 即零行为变更;翻为 false 可在运行时
// 禁用每个 mutating 工具,同时保持只读诊断 + 对话式 chat 完全在线。
const hermesMutatingEnabledEnv = "HUAKAI_HERMES_MUTATING_ENABLED"

// hermesLLMToolLoopEnabledEnv 是 LLM 对话式工具循环的运行时总开关
// (向 chat 请求体注入只读工具 catalog + runner 在对话中途的
// /internal/hermes/tool-execute 回调)。它默认 ENABLED;翻为 false 可禁用
// 工具循环,同时普通的 /v1/hermes/chat 仍持续流式。
const hermesLLMToolLoopEnabledEnv = "HUAKAI_HERMES_LLM_TOOLLOOP_ENABLED"

// hermesLLMProposeEnabledEnv 是默认关闭的模型提议开关。未设置或为 false 时，内部处理器
// 在空跑解析前拒绝 mode=propose 请求，模型不能提议改动型工具。设为 true 后，助手只能提议
// 标记为 Proposable 的可逆工具，真正执行仍需运营者独立确认，并继续受工具循环总开关约束。
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

// hermesBoolEnabledDefaultFalse 解析默认关闭的运行时布尔开关。未设置或为空时返回 false，
// 合法布尔值按配置生效；格式错误会阻止启动，避免静默启用部署者未授权的特权能力。
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
// wiring 传经过启动探测的 Rust sidecar client；测试可注入 mock client验证替换
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
		return fmt.Errorf("anthropic/claude_ai_oauth exchanger has nil httpClient，安装未生效，生产启动必须拒绝")
	}
	return nil
}

func loadGeminiPublicCLIOAuthClientSecretFromEnv() (string, error) {
	return strings.TrimSpace(credentialacq.GeminiPublicCLIConfig().ClientSecret), nil
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
