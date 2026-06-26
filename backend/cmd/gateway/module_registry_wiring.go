package main

import (
	"context"

	"github.com/BloomingProsperity/HUAKAI/internal/modulecatalog"
	"github.com/BloomingProsperity/HUAKAI/internal/moduleregistry"
)

// buildModuleRegistry 构建运行时的模块知识脊柱,并用 WAVE H2 的三个高价值域
// 给它播种:billing/money-path 服务、routing selector,以及
// credentials/credentialworker 子系统。
//
// 注册约定(churn 最小,为 H2 选定):
// 模块在这里集中注册,源自 buildGatewayRuntime 中已组装好的 live deps——
// 每个模块一次 Register 调用,各自带一个稳定的点分 ID、一个 category、一个
// title、capabilities,以及一个可选的、廉价的只读 HealthProbe。这避免了
// 各包的 init() 钩子(会增加 import 边并在不可预测的时刻运行),并把接线
// 集中在一处以便审计。新模块加入脊柱只需在下方添加一次 Register 调用(或者,
// 对于自包含的子系统,加一个返回其 descriptor 的单行 helper)——无需改 schema,
// 不与热路径耦合。
//
// 各 PROBE 都是只读且隐私安全的:它们只报告「该子系统是否已接线」,
// 至多再带一个粗粒度计数,绝不含密钥或用户数据。它们仅在运维触发 Snapshot 时运行,
// 绝不在启动或任何请求路径上运行。
func buildModuleRegistry(d *deps) *moduleregistry.Registry {
	reg := moduleregistry.New()
	if d == nil {
		return reg
	}

	// ── money-path: 计费结算服务 ───────────────────────────────
	// Probe: 当且仅当 settler + claim gate + reserver 全部存在时,money path 才算已接线。
	// 我们只报 wired/degraded——不含余额,不含用户数据。
	settler := d.settler
	claimGate := d.claimGate
	reserver := d.quotaReserver
	_ = reg.Register(moduleregistry.ModuleDescriptor{
		ID:       "billing.service",
		Category: "money-path",
		Title:    "Billing settlement & claim gate",
		Capabilities: []string{
			"usage settlement (Tx2)",
			"pre-flight claim gate (Tx1)",
			"quota reservation",
		},
		HealthProbe: func(ctx context.Context) moduleregistry.ProbeResult {
			if settler == nil || claimGate == nil || reserver == nil {
				return moduleregistry.ProbeResult{
					Status: moduleregistry.StatusDegraded,
					Detail: "money-path partially wired",
				}
			}
			return moduleregistry.ProbeResult{Status: moduleregistry.StatusOK, Detail: "wired"}
		},
	})

	// ── routing: 上游账号 selector ───────────────────────────────────
	selector := d.selector
	_ = reg.Register(moduleregistry.ModuleDescriptor{
		ID:       "routing.selector",
		Category: "routing",
		Title:    "Upstream account selector",
		Capabilities: []string{
			"score-based account selection",
			"locality + headroom blending",
		},
		HealthProbe: func(ctx context.Context) moduleregistry.ProbeResult {
			if selector == nil {
				return moduleregistry.ProbeResult{Status: moduleregistry.StatusDegraded, Detail: "selector unwired"}
			}
			return moduleregistry.ProbeResult{Status: moduleregistry.StatusOK, Detail: "wired"}
		},
	})

	// ── credentials: 凭据 worker / 获取子系统 ───────────────
	credStore := d.credentialStore
	credScheduler := d.credentialScheduler // 可能在 buildGatewayRuntime 中稍后才被设置
	_ = reg.Register(moduleregistry.ModuleDescriptor{
		ID:       "credentials.worker",
		Category: "credentials",
		Title:    "Credential store, refresh worker & acquisition",
		Capabilities: []string{
			"credential storage (encrypted at rest)",
			"scheduled credential refresh",
			"credential acquisition exchange",
		},
		HealthProbe: func(ctx context.Context) moduleregistry.ProbeResult {
			if credStore == nil {
				return moduleregistry.ProbeResult{Status: moduleregistry.StatusDegraded, Detail: "credential store unwired"}
			}
			detail := "store wired"
			if credScheduler == nil {
				detail = "store wired, refresh scheduler off"
			}
			return moduleregistry.ProbeResult{Status: moduleregistry.StatusOK, Detail: detail}
		},
	})

	// ── channel-health: 健康状态机 / 熔断 / ramp 恢复 ──────────────────────────
	// Probe: 状态机服务接线即 wired。只报 wired/degraded,不含任何 channel 健康明细。
	channelHealth := d.channelHealth
	_ = reg.Register(moduleregistry.ModuleDescriptor{
		ID:       "channelhealth.service",
		Category: "channel-health",
		Title:    "Channel health state machine & failover",
		Capabilities: []string{
			"health state machine (active/degraded/cooling_down/ramping/disabled)",
			"adaptive cooldown + lazy ramp recovery",
			"pool gate flow admission",
		},
		HealthProbe: func(ctx context.Context) moduleregistry.ProbeResult {
			if channelHealth == nil {
				return moduleregistry.ProbeResult{Status: moduleregistry.StatusDegraded, Detail: "channel-health service unwired"}
			}
			return moduleregistry.ProbeResult{Status: moduleregistry.StatusOK, Detail: "wired"}
		},
	})

	// ── reliability: 死信队列 / 重放 ──────────────────────────────────────────
	// Probe: dlq 重放服务接线即 wired。只报 wired/degraded,不含任何队列内容。
	dlqService := d.dlqService
	_ = reg.Register(moduleregistry.ModuleDescriptor{
		ID:       "dlq.service",
		Category: "reliability",
		Title:    "Dead-letter queue & replay",
		Capabilities: []string{
			"failed async-effect capture",
			"idempotency-keyed replay (re-claim + re-deliver)",
			"priority lanes + bounded drain",
		},
		HealthProbe: func(ctx context.Context) moduleregistry.ProbeResult {
			if dlqService == nil {
				return moduleregistry.ProbeResult{Status: moduleregistry.StatusDegraded, Detail: "dlq service unwired"}
			}
			return moduleregistry.ProbeResult{Status: moduleregistry.StatusOK, Detail: "wired"}
		},
	})

	// ── registry: 模型注册表 / 模型→池绑定解析(路由栈第 1 层)──────────────────
	// Probe: 注册表接线即 wired。只报 wired/degraded,不含任何模型/租户配置明细。
	modelRegistry := d.modelRegistry
	_ = reg.Register(moduleregistry.ModuleDescriptor{
		ID:       "registry.model",
		Category: "registry",
		Title:    "Model registry & model→pool binding resolution",
		Capabilities: []string{
			"model alias → canonical resolution",
			"model → pool-group binding resolution",
			"capability graph projection",
		},
		HealthProbe: func(ctx context.Context) moduleregistry.ProbeResult {
			if modelRegistry == nil {
				return moduleregistry.ProbeResult{Status: moduleregistry.StatusDegraded, Detail: "model registry unwired"}
			}
			return moduleregistry.ProbeResult{Status: moduleregistry.StatusOK, Detail: "wired"}
		},
	})

	// ── routing: 跨池路由规划(路由栈第 2 层,在 selector 选号之上)──────────────
	// Probe: 路由规划器接线即 wired。只报 wired/degraded,不含任何路由决策明细。
	routePlanner := d.routePlanner
	_ = reg.Register(moduleregistry.ModuleDescriptor{
		ID:       "router.planner",
		Category: "routing",
		Title:    "Cross-pool route planner",
		Capabilities: []string{
			"multi-attempt route plan (bounded attempt budget ≤3)",
			"cross-pool candidate sequencing into attempt order",
			"delegates account selection + health gating to pool/executor layer",
		},
		HealthProbe: func(ctx context.Context) moduleregistry.ProbeResult {
			if routePlanner == nil {
				return moduleregistry.ProbeResult{Status: moduleregistry.StatusDegraded, Detail: "route planner unwired"}
			}
			return moduleregistry.ProbeResult{Status: moduleregistry.StatusOK, Detail: "wired"}
		},
	})

	// ── payments: 充值订单生命周期(中转站 SaaS 变现入口)────────────────────────
	// Probe: 支付服务接线即 wired。只报 wired/degraded,不含任何订单/余额明细。
	paymentService := d.paymentService
	_ = reg.Register(moduleregistry.ModuleDescriptor{
		ID:       "payment.service",
		Category: "payments",
		Title:    "Topup order lifecycle & balance",
		Capabilities: []string{
			"topup order lifecycle (create/cancel/refund/fulfill)",
			"admin manual confirm-paid crediting",
			"user balance + order history + per-order audit",
		},
		HealthProbe: func(ctx context.Context) moduleregistry.ProbeResult {
			if paymentService == nil {
				return moduleregistry.ProbeResult{Status: moduleregistry.StatusDegraded, Detail: "payment service unwired"}
			}
			return moduleregistry.ProbeResult{Status: moduleregistry.StatusOK, Detail: "wired"}
		},
	})

	// ── subscription: 订阅档管理 + 用户订阅生命周期 ───────────────────────────
	// Probe: 订阅服务接线即 wired。只报 wired/degraded,不含任何订阅明细。
	subscriptionService := d.subscriptionService
	_ = reg.Register(moduleregistry.ModuleDescriptor{
		ID:       "subscription.service",
		Category: "subscription",
		Title:    "Subscription plans & user subscriptions",
		Capabilities: []string{
			"plan management (create/update/disable)",
			"user subscription assign/cancel/extend",
			"windowed quota reset",
		},
		HealthProbe: func(ctx context.Context) moduleregistry.ProbeResult {
			if subscriptionService == nil {
				return moduleregistry.ProbeResult{Status: moduleregistry.StatusDegraded, Detail: "subscription service unwired"}
			}
			return moduleregistry.ProbeResult{Status: moduleregistry.StatusOK, Detail: "wired"}
		},
	})

	// ── promo: 兑换码(voucher)铸造与兑付 ────────────────────────────────────
	// Probe: 兑换码服务接线即 wired。只报 wired/degraded,不含任何码/兑付明细。
	voucherService := d.voucherService
	_ = reg.Register(moduleregistry.ModuleDescriptor{
		ID:       "voucher.service",
		Category: "promo",
		Title:    "Redemption codes (voucher)",
		Capabilities: []string{
			"redemption code mint (single + batch)",
			"code redemption → balance / subscription grant",
		},
		HealthProbe: func(ctx context.Context) moduleregistry.ProbeResult {
			if voucherService == nil {
				return moduleregistry.ProbeResult{Status: moduleregistry.StatusDegraded, Detail: "voucher service unwired"}
			}
			return moduleregistry.ProbeResult{Status: moduleregistry.StatusOK, Detail: "wired"}
		},
	})

	// ── auth: 用户登录/注册(密码 + 社交)──────────────────────────────────────
	// Probe: 用户鉴权服务接线即 wired。只报 wired/degraded,不含任何用户/凭据明细。
	userAuth := d.userAuth
	_ = reg.Register(moduleregistry.ModuleDescriptor{
		ID:       "auth.userauth",
		Category: "auth",
		Title:    "User authentication (password + social)",
		Capabilities: []string{
			"password login + registration",
			"social login (10 providers: google/github/qq/…)",
			"profile management + account unlock",
		},
		HealthProbe: func(ctx context.Context) moduleregistry.ProbeResult {
			if userAuth == nil {
				return moduleregistry.ProbeResult{Status: moduleregistry.StatusDegraded, Detail: "userauth service unwired"}
			}
			return moduleregistry.ProbeResult{Status: moduleregistry.StatusOK, Detail: "wired"}
		},
	})

	// ── auth: WebAuthn passkey 通行密钥 ────────────────────────────────────────────────
	// Probe: passkey 服务接线即 wired。只报 wired/degraded,不含任何凭据明细。
	passkeys := d.passkeys
	_ = reg.Register(moduleregistry.ModuleDescriptor{
		ID:       "auth.passkey",
		Category: "auth",
		Title:    "WebAuthn passkeys",
		Capabilities: []string{
			"passkey registration + assertion login",
			"credential listing / deletion",
			"origin verification",
		},
		HealthProbe: func(ctx context.Context) moduleregistry.ProbeResult {
			if passkeys == nil {
				return moduleregistry.ProbeResult{Status: moduleregistry.StatusDegraded, Detail: "passkey service unwired"}
			}
			return moduleregistry.ProbeResult{Status: moduleregistry.StatusOK, Detail: "wired"}
		},
	})

	// ── auth: TOTP 两步验证 ───────────────────────────────────────────────────
	// Probe: 2FA 服务接线即 wired。只报 wired/degraded,不含任何密钥/备份码。
	twoFactor := d.twoFactor
	_ = reg.Register(moduleregistry.ModuleDescriptor{
		ID:       "auth.twofa",
		Category: "auth",
		Title:    "Two-factor authentication (TOTP)",
		Capabilities: []string{
			"TOTP 2FA setup/enable/disable",
			"login challenge + verify",
			"backup codes",
		},
		HealthProbe: func(ctx context.Context) moduleregistry.ProbeResult {
			if twoFactor == nil {
				return moduleregistry.ProbeResult{Status: moduleregistry.StatusDegraded, Detail: "twofa service unwired"}
			}
			return moduleregistry.ProbeResult{Status: moduleregistry.StatusOK, Detail: "wired"}
		},
	})

	// ── registry: 上游模型目录同步 ────────────────────────────────────────────
	// Probe: 同步服务接线即 wired。只报 wired/degraded,不含任何模型明细。
	modelSync := d.modelSync
	_ = reg.Register(moduleregistry.ModuleDescriptor{
		ID:       "registry.modelsync",
		Category: "registry",
		Title:    "Upstream model catalog sync",
		Capabilities: []string{
			"upstream model catalog sync",
			"actor-attributed manual sync",
		},
		HealthProbe: func(ctx context.Context) moduleregistry.ProbeResult {
			if modelSync == nil {
				return moduleregistry.ProbeResult{Status: moduleregistry.StatusDegraded, Detail: "model sync service unwired"}
			}
			return moduleregistry.ProbeResult{Status: moduleregistry.StatusOK, Detail: "wired"}
		},
	})

	// ── media: 异步媒体任务(图像等)────────────────────────────────────────────
	// Probe: 媒体任务服务接线即 wired。只报 wired/degraded,不含任何任务/用户明细。
	mediaTaskService := d.mediaTaskService
	_ = reg.Register(moduleregistry.ModuleDescriptor{
		ID:       "media.task",
		Category: "media",
		Title:    "Async media tasks",
		Capabilities: []string{
			"async media task submit/status/list",
			"pluggable async media providers",
		},
		HealthProbe: func(ctx context.Context) moduleregistry.ProbeResult {
			if mediaTaskService == nil {
				return moduleregistry.ProbeResult{Status: moduleregistry.StatusDegraded, Detail: "media task service unwired"}
			}
			return moduleregistry.ProbeResult{Status: moduleregistry.StatusOK, Detail: "wired"}
		},
	})

	return reg
}

// seedCatalogJoin 把每个已播种的 live-module ID 映射到它应被补充的
// feature-tree catalog 包短名。不在此 map 中的 ID 被视为 live-only
//(无静态叠加层),因此脊柱绝不会为未映射的模块凭空捏造一条 catalog 记录。
var seedCatalogJoin = map[string]string{
	"billing.service":       "billing",
	"routing.selector":      "pool",
	"credentials.worker":    "credentialworker",
	"channelhealth.service": "channelhealth",
	"dlq.service":           "dlq",
	"registry.model":        "registry",
	"router.planner":        "router",
	"voucher.service":       "voucher",
	"auth.userauth":         "userauth",
	// payment.service / subscription.service / auth.passkey / auth.twofa:catalog 无对应 pkg → live-only。
}

// moduleSource 把 live registry + 内嵌静态 catalog 适配为 modulehttp.Source,
// 供 admin 端点与 Hermes 上下文馈送使用。
type moduleSource struct {
	reg     *moduleregistry.Registry
	catalog modulecatalog.Catalog
}

func newModuleSource(reg *moduleregistry.Registry) *moduleSource {
	return &moduleSource{reg: reg, catalog: modulecatalog.MustLoad()}
}

func (s *moduleSource) Snapshot(ctx context.Context) []moduleregistry.ModuleSnapshot {
	if s.reg == nil {
		return nil
	}
	return s.reg.Snapshot(ctx)
}

func (s *moduleSource) CatalogLookup(pkg string) (modulecatalog.CatalogModule, bool) {
	return s.catalog.Lookup(pkg)
}

func (s *moduleSource) CatalogPkgFor(moduleID string) (string, bool) {
	pkg, ok := seedCatalogJoin[moduleID]
	return pkg, ok
}
