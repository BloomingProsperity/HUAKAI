package main

import (
	"context"

	"github.com/BloomingProsperity/HUAKAI/internal/modulecatalog"
	"github.com/BloomingProsperity/HUAKAI/internal/moduleregistry"
)

// buildModuleRegistry constructs the runtime module-knowledge spine and seeds it
// with the three high-value domains for WAVE H2: the billing/money-path service,
// the routing selector, and the credentials/credentialworker subsystem.
//
// REGISTRATION CONVENTION (lowest-churn, chosen for H2):
// modules are registered HERE, centrally, from the live deps already assembled
// in buildGatewayRuntime — one Register call per module, each carrying a stable
// dotted ID, a category, a title, capabilities, and an optional cheap read-only
// HealthProbe. This avoids per-package init() hooks (which would add import
// edges and run at unpredictable times) and keeps the wiring auditable in one
// place. A new module joins the spine by adding ONE Register call below (or, for
// a self-contained subsystem, a one-line helper that returns its descriptor) —
// no schema change, no hot-path coupling.
//
// PROBES are READ-ONLY and privacy-safe: they report "is the subsystem wired"
// and at most a coarse count, never secrets or user data. They run only on an
// operator Snapshot, never on startup or any request path.
func buildModuleRegistry(d *deps) *moduleregistry.Registry {
	reg := moduleregistry.New()
	if d == nil {
		return reg
	}

	// ── money-path: billing settlement service ───────────────────────────────
	// Probe: the money path is wired iff the settler + claim gate + reserver are
	// all present. We report wired/degraded only — no balances, no user data.
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

	// ── routing: upstream account selector ───────────────────────────────────
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

	// ── credentials: credential worker / acquisition subsystem ───────────────
	credStore := d.credentialStore
	credScheduler := d.credentialScheduler // may be set later in buildGatewayRuntime
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

	return reg
}

// seedCatalogJoin maps each seeded live-module ID to the feature-tree catalog
// package short-name it should be enriched with. An ID absent from this map is
// treated as live-only (no static overlay), so the spine never fabricates a
// catalog entry for an unmapped module.
var seedCatalogJoin = map[string]string{
	"billing.service":       "billing",
	"routing.selector":      "pool",
	"credentials.worker":    "credentialworker",
	"channelhealth.service": "channelhealth",
	"dlq.service":           "dlq",
	"registry.model":        "registry",
	"router.planner":        "router",
}

// moduleSource adapts the live registry + embedded static catalog to
// modulehttp.Source for the admin endpoint and the Hermes context feed.
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
