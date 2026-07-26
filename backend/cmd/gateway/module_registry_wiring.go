package main

import (
	"context"

	runtimeconfig "github.com/BloomingProsperity/HUAKAI/internal/config"
	legacydlq "github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/modulecatalog"
	"github.com/BloomingProsperity/HUAKAI/internal/moduleregistry"
)

type moduleEndpointState struct {
	name     string
	injected bool
	active   bool
}

func moduleBool(value bool) *bool { return &value }

func moduleInt(value int) *int { return &value }

func moduleEndpoint(name string, available bool) moduleEndpointState {
	return moduleEndpointState{name: name, injected: available, active: available}
}

func moduleActivation(constructed bool, backend string, sharedSafe bool, endpoints ...moduleEndpointState) *moduleregistry.ActivationSnapshot {
	injected := false
	active := false
	projected := make([]moduleregistry.ActivationEndpoint, 0, len(endpoints))
	for _, endpoint := range endpoints {
		injected = injected || endpoint.injected
		active = active || endpoint.active
		projected = append(projected, moduleregistry.ActivationEndpoint{
			Name: endpoint.name, Injected: moduleBool(endpoint.injected), Active: moduleBool(endpoint.active),
		})
	}
	return &moduleregistry.ActivationSnapshot{
		Declared: moduleBool(true), Constructed: moduleBool(constructed), Injected: moduleBool(injected),
		Active: moduleBool(active), SharedSafe: moduleBool(sharedSafe), Observable: moduleBool(true),
		Backend: backend, Endpoints: projected,
	}
}

func sharedGatewayEndpoints(active bool) []moduleEndpointState {
	return []moduleEndpointState{
		moduleEndpoint("chat", active), moduleEndpoint("completions", active),
		moduleEndpoint("embeddings", active), moduleEndpoint("rerank", active),
		moduleEndpoint("images", active), moduleEndpoint("audio", active),
		moduleEndpoint("gemini", active), moduleEndpoint("video", active),
	}
}

func selectorActivation(d *deps) *moduleregistry.ActivationSnapshot {
	available := d.selector != nil
	snapshot := moduleActivation(available, "postgresql", true, sharedGatewayEndpoints(available)...)
	if d.selectorConfig == nil {
		return snapshot
	}
	snapshot.Mode = string(d.selectorConfig.Mode)
	switch d.selectorConfig.Mode {
	case runtimeconfig.PoolSelectorModeCanary:
		snapshot.TrafficPercent = moduleInt(d.selectorConfig.CanaryPercent)
	case runtimeconfig.PoolSelectorModeShadow:
		snapshot.TrafficPercent = moduleInt(d.selectorConfig.ShadowPercent)
	}
	return snapshot
}

// buildModuleRegistry 构建运行时模块知识总表，并接入计费、路由选号和凭据子系统。
//
// 注册约定：
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
		Activation: moduleActivation(settler != nil && claimGate != nil && reserver != nil, "postgresql", true,
			sharedGatewayEndpoints(settler != nil && claimGate != nil && reserver != nil)...),
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
		Activation: selectorActivation(d),
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
		Activation: func() *moduleregistry.ActivationSnapshot {
			available := channelHealth != nil && selector != nil
			backend := "postgresql"
			sharedSafe := true
			if d.authCooldown != nil {
				backend = "mixed"
				sharedSafe = false
			}
			return moduleActivation(channelHealth != nil, backend, sharedSafe, sharedGatewayEndpoints(available)...)
		}(),
		HealthProbe: func(ctx context.Context) moduleregistry.ProbeResult {
			if channelHealth == nil {
				return moduleregistry.ProbeResult{Status: moduleregistry.StatusDegraded, Detail: "channel-health service unwired"}
			}
			return moduleregistry.ProbeResult{Status: moduleregistry.StatusOK, Detail: "wired"}
		},
	})

	queueWaiter := d.queueWaiter
	_ = reg.Register(moduleregistry.ModuleDescriptor{
		ID: "queue.wait", Category: "routing", Title: "Queue wait admission",
		Capabilities: []string{"request queue waiting before dispatch", "chat admission smoothing"},
		Activation: moduleActivation(true, "local", false,
			moduleEndpointState{name: "chat", injected: queueWaiter != nil, active: true},
			moduleEndpoint("completions", false), moduleEndpoint("embeddings", false),
			moduleEndpoint("rerank", false), moduleEndpoint("images", false),
			moduleEndpoint("audio", false), moduleEndpoint("gemini", false),
			moduleEndpoint("video", false)),
		HealthProbe: func(context.Context) moduleregistry.ProbeResult {
			if queueWaiter == nil {
				return moduleregistry.ProbeResult{Status: moduleregistry.StatusOK, Detail: "handler default"}
			}
			return moduleregistry.ProbeResult{Status: moduleregistry.StatusOK, Detail: "wired"}
		},
	})

	// ── reliability: 死信队列 / 重放 ──────────────────────────────────────────
	// Probe: dlq 重放服务接线即 wired。只报 wired/degraded,不含任何队列内容。
	dlqService := d.dlqService
	settlementRecoveryReady := dlqService != nil && dlqService.HasHandler(legacydlq.EventKindPostDeliverySettlement)
	settlementIntentEnabled := d.cfg != nil && d.cfg.SettlementIntentEnabled
	settlementIntentReady := settlementIntentEnabled && d.settlementIntents != nil
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

	_ = reg.Register(moduleregistry.ModuleDescriptor{
		ID: "settlement.recovery", Category: "reliability", Title: "Post-delivery settlement recovery",
		Capabilities: []string{"post-delivery failure capture", "idempotent operator-visible replay"},
		Activation: moduleActivation(settlementRecoveryReady, "postgresql", true,
			moduleEndpoint("chat", settlementRecoveryReady),
			moduleEndpoint("completions", settlementRecoveryReady),
			moduleEndpoint("embeddings", settlementRecoveryReady),
			moduleEndpoint("rerank", settlementRecoveryReady),
			moduleEndpoint("images", settlementRecoveryReady),
			moduleEndpoint("audio", settlementRecoveryReady),
			moduleEndpoint("gemini", settlementRecoveryReady)),
		HealthProbe: func(context.Context) moduleregistry.ProbeResult {
			if !settlementRecoveryReady {
				return moduleregistry.ProbeResult{Status: moduleregistry.StatusDegraded, Detail: "settlement recovery handler unwired"}
			}
			return moduleregistry.ProbeResult{Status: moduleregistry.StatusOK, Detail: "wired"}
		},
	})

	_ = reg.Register(moduleregistry.ModuleDescriptor{
		ID: "settlement.intent", Category: "reliability", Title: "Pre-delivery settlement intent",
		Capabilities: []string{"pre-delivery crash-gap tracking", "stale intent reconciliation"},
		Activation: moduleActivation(d.settlementIntents != nil, "postgresql", true,
			moduleEndpoint("chat", settlementIntentReady),
			moduleEndpoint("completions", settlementIntentReady),
			moduleEndpoint("embeddings", settlementIntentReady),
			moduleEndpoint("rerank", settlementIntentReady),
			moduleEndpoint("images", settlementIntentReady),
			moduleEndpoint("audio", settlementIntentReady),
			moduleEndpoint("gemini", settlementIntentReady)),
		HealthProbe: func(context.Context) moduleregistry.ProbeResult {
			if !settlementIntentEnabled {
				return moduleregistry.ProbeResult{Status: moduleregistry.StatusDegraded, Detail: "disabled by configuration"}
			}
			if d.settlementIntents == nil {
				return moduleregistry.ProbeResult{Status: moduleregistry.StatusDegraded, Detail: "enabled but store unwired"}
			}
			return moduleregistry.ProbeResult{Status: moduleregistry.StatusOK, Detail: "all synchronous relay families wired"}
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
		Activation: moduleActivation(modelRegistry != nil, "postgresql", true,
			sharedGatewayEndpoints(modelRegistry != nil)...),
		HealthProbe: func(ctx context.Context) moduleregistry.ProbeResult {
			if modelRegistry == nil {
				return moduleregistry.ProbeResult{Status: moduleregistry.StatusDegraded, Detail: "model registry unwired"}
			}
			return moduleregistry.ProbeResult{Status: moduleregistry.StatusOK, Detail: "wired"}
		},
	})

	responseCache := d.responseCache
	_ = reg.Register(moduleregistry.ModuleDescriptor{
		ID: "response.cache", Category: "performance", Title: "Non-stream response cache",
		Capabilities: []string{"non-stream response reuse", "tenant-scoped cache control"},
		Activation: moduleActivation(responseCache != nil, "local", false,
			moduleEndpoint("chat", responseCache != nil),
			moduleEndpoint("completions", false), moduleEndpoint("embeddings", false),
			moduleEndpoint("rerank", false), moduleEndpoint("images", false),
			moduleEndpoint("audio", false), moduleEndpoint("gemini", false),
			moduleEndpoint("video", false)),
		HealthProbe: func(context.Context) moduleregistry.ProbeResult {
			if responseCache == nil {
				return moduleregistry.ProbeResult{Status: moduleregistry.StatusDegraded, Detail: "response cache disabled"}
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
// (无静态叠加层),因此脊柱绝不会为未映射的模块凭空捏造一条 catalog 记录。
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
