// selector_wiring.go — PASR-lite main-wire M6 atomic: 启动期根据 PoolSelectorConfig
// 装配 default / shadow / canary / pasr-primary / pasr-strict 5 mode 的具体
// Selector 实现, 暴露给 deps.selector 用 (handler 看到的统一 pool.Selector 接口)。
//
// 关键不变量:
//   - default mode 完全等价现状 — 不构造 PASR / SegmentTable / AgingWorker /
//     不注册 cache feedback observer; 主线零回归
//   - 启动期失败 → fail-fast 让 main 退出 (LoadPoolSelector 已守门, 这里再一次
//     Validate; misconfigure 不允许 silent 退化)
//   - shadow / canary / pasr-* 模式才启动 PASR 基础设施: SegmentTable +
//     AgingWorker (1 min ticker) + RegisterPASRCacheFeedback (cachemetrics
//     全局 observer)
//   - shadow 实例 Slots=nil + Claims=nil + ReadOnlySegments=true (段表只读
//   - 三层防御之一)
//   - canary / pasr-* 实例: Slots = DBSlotManager + Claims = DBClaimGate
//     (强制 slot parity)
//   - cleanup 函数包 dispatcher.Stop + agingWorker.Stop, 由 caller defer
//     在 srv.Shutdown 之前执行
package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/config"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/sessioncap"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionenforce"
	"github.com/BloomingProsperity/HUAKAI/internal/windowcost"
)

// buildSelector 装配 selector 链, 返 (Selector 接口, cleanup 闭包, error)。
// caller 应 defer cleanup() 在 server shutdown 之前调用。
//
// 装配策略按 cfg.Mode 分:
//   - default: 仅 DefaultSelector, 不启动任何 PASR 基础设施 (零回归)
//   - shadow:  DefaultSelector + shadow PASR 实例 (Slots=nil, Claims=nil,
//     ReadOnlySegments=true) + SegmentTable + AgingWorker + 注册
//     cache feedback observer; dispatcher 异步比对
//   - canary / pasr-primary / pasr-strict:
//     DefaultSelector + actual PASR 实例 (真 Slots + Claims) +
//     SegmentTable + AgingWorker + 注册 cache feedback observer
func buildSelector(
	ctx context.Context,
	q *dbbilling.Queries,
	pgPool *pgxpool.Pool,
	selectorCfg *config.PoolSelectorConfig,
	healthService *channelhealth.Service,
	windowCostReader windowcost.CostReader,
	sessionCapRegistry *sessioncap.Registry,
	logger *zap.Logger,
) (pool.Selector, func(), error) {
	if selectorCfg == nil {
		return nil, nil, errors.New("buildSelector: PoolSelectorConfig 必填")
	}
	if err := selectorCfg.Validate(); err != nil {
		return nil, nil, fmt.Errorf("buildSelector: invalid config: %w", err)
	}

	// 1. default selector 总是构造 — 即使 PASR 模式也作为 fallback 实例。
	// gate 链构造抽到 buildGroupRoutingGates 便于直接单测生产激活接线 (否则漏接订阅
	// gate 静默退回 AllowAll 无测可抓)。同一 gates 值流入 default selector + actual/shadow PASR,
	// 一处接线覆盖全 5 mode。
	gates := buildGroupRoutingGates(subscriptionenforce.NewPostgresRoutesRepo(pgPool), healthService, windowCostReader, sessionCapRegistry, logger)
	defaultSel := pool.NewDefaultSelector(
		pool.NewDBAccountSource(q),
		pool.WithGateChain(gates),
		pool.WithSlotManager(pool.NewDBSlotManager(pgPool)),
		pool.WithClaimGate(pool.NewDBClaimGate(q)),
		pool.WithStickyStore(pool.NewDBStickyStore(q)),
	)

	// 2. default mode: 直接返, 不启动 PASR 基础设施
	if !selectorCfg.IsPASR() {
		logger.Info("selector mode=default — PASR 基础设施未启动")
		return defaultSel, func() {}, nil
	}

	// 3. PASR 基础设施: SegmentTable + AgingWorker + cache feedback observer
	segments := pool.NewSegmentTable(pool.SegmentTableConfig{})
	agingWorker := pool.NewPASRAgingWorker(pool.PASRAgingWorkerConfig{
		Segments: segments,
	})
	agingWorker.Start(ctx)
	pool.RegisterPASRCacheFeedback(segments)
	logger.Info("PASR 基础设施已启动",
		zap.String("mode", string(selectorCfg.Mode)),
		zap.Int("shadow_pct", selectorCfg.ShadowPercent),
		zap.Int("canary_pct", selectorCfg.CanaryPercent),
	)

	// 4. 按 mode 构造 actual / shadow PASR 实例
	dispatcherCfg := pool.SelectorDispatcherConfig{
		Mode:          string(selectorCfg.Mode),
		ShadowPercent: selectorCfg.ShadowPercent,
		CanaryPercent: selectorCfg.CanaryPercent,
		SamplingSalt:  selectorCfg.SamplingSalt,
		Default:       defaultSel,
	}

	if selectorCfg.NeedsActualPASR() {
		actual, err := pool.NewPASRSelector(pool.PASRSelectorConfig{
			Accounts: pool.NewDBAccountSource(q),
			Claims:   pool.NewDBClaimGate(q),
			Slots:    pool.NewDBSlotManager(pgPool),
			Segments: segments,
			Gates:    gates,
			// RingProvider 不注入 — 走 request-scoped ring。
		})
		if err != nil {
			agingWorker.Stop()
			return nil, nil, fmt.Errorf("buildSelector: actual PASR: %w", err)
		}
		dispatcherCfg.PASR = actual
	}

	if selectorCfg.NeedsShadowInstance() {
		shadow, err := pool.NewPASRSelector(pool.PASRSelectorConfig{
			Accounts: pool.NewDBAccountSource(q),
			// Claims 显式 nil — 三层防御第一层 (shadow 不写 billing_claims)
			// Slots 显式 nil — shadow 不持 slot
			Segments:         segments,
			ReadOnlySegments: true, // D2: shadow 段表只读, 不污染 actual 学习数据
			Gates:            gates,
		})
		if err != nil {
			agingWorker.Stop()
			return nil, nil, fmt.Errorf("buildSelector: shadow PASR: %w", err)
		}
		dispatcherCfg.Shadow = shadow
	}

	dispatcher, err := pool.NewSelectorDispatcher(dispatcherCfg)
	if err != nil {
		agingWorker.Stop()
		return nil, nil, fmt.Errorf("buildSelector: dispatcher: %w", err)
	}

	cleanup := func() {
		dispatcher.Stop()
		agingWorker.Stop()
	}
	return dispatcher, cleanup, nil
}

// buildGroupRoutingGates 构造 selector 用的 gate 链 (R-SUB-WIRE-1 生产激活点)。在
// DefaultGateChain 基础上把 GroupPolicy 槽换成接 routes 的真订阅 gate, 并按需替换 Health。
// 抽成独立函数便于单测直接验证激活接线: 漏接订阅 gate 时 GroupPolicy 退回 AllowAll, 单测
// 必红 (TestBuildGroupRoutingGates_WiresRealGroupPolicyGate)。
// observer: routes repo 不可用 / 查询失败时 fail-open 保可用性并累计 metric + WARN;
// 明确硬拒路径保留 fail-closed observer 接口, transient 控制面问题不误拒付费用户。
func buildGroupRoutingGates(routesRepo subscriptionenforce.RoutesRepo, healthService *channelhealth.Service, windowCostReader windowcost.CostReader, sessionCapRegistry *sessioncap.Registry, logger *zap.Logger) pool.GateChain {
	gates := pool.DefaultGateChain()
	if healthService != nil {
		gates.Health = channelhealth.NewServicePoolGate(healthService, nil)
	}
	gates.GroupPolicy = subscriptionenforce.NewGroupPolicyGate(
		routesRepo,
		subscriptionenforce.WithFailOpenObserver(newGroupPolicyFailOpenObserver(logger)),
		subscriptionenforce.WithFailClosedObserver(newGroupPolicyFailClosedObserver(logger)),
	)
	// SUB2-EGRESS-03: per-account 5h window spend cap gate.
	// windowCostReader nil → WindowCostGate is fail-open (AllowAll equivalent).
	gates.WindowCost = pool.WindowCostGate{Reader: windowCostReader}
	// SUB2-EGRESS-02: per-account max concurrent sessions cap gate.
	// sessionCapRegistry nil -> SessionCountGate is fail-open.
	gates.SessionCount = pool.SessionCountGate{Registry: sessionCapRegistry}
	return gates
}
