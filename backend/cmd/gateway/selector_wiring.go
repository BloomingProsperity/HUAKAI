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
//     AgingWorker (每分钟 ticker) + RegisterPASRCacheFeedback (cachemetrics
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
	"os"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/config"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/rate/precheck"
	"github.com/BloomingProsperity/HUAKAI/internal/sessioncap"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionenforce"
	"github.com/BloomingProsperity/HUAKAI/internal/windowcost"
)

// envInt64 读取一个非负的 int64 配置值;缺失/非法/为负 → 0。
func envInt64(key string) int64 {
	v, err := strconv.ParseInt(os.Getenv(key), 10, 64)
	if err != nil || v < 0 {
		return 0
	}
	return v
}

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
) (pool.Selector, *precheck.Counter, func(), error) {
	if selectorCfg == nil {
		return nil, nil, nil, errors.New("buildSelector: PoolSelectorConfig 必填")
	}
	if err := selectorCfg.Validate(); err != nil {
		return nil, nil, nil, fmt.Errorf("buildSelector: invalid config: %w", err)
	}

	// ROUTE-121:opt-in 的主动 RPM/TPM 限流器。默认关闭 → counter 为 nil
	//(gate fail-open + RecordingSelector 透传 = 与现状完全一致)。
	// 同一个共享 counter 同时供给 gate(选号时读取预算)和
	// RecordingSelector(在 dispatch 时消耗预算),覆盖全部 5 个 mode。
	var ratePrecheckCounter *precheck.Counter
	if on, _ := strconv.ParseBool(os.Getenv("HUAKAI_RATE_PRECHECK_ENABLED")); on {
		ratePrecheckCounter = pool.NewRatePrecheckCounter()
		logger.Info("ROUTE-121 proactive RPM/TPM rate pre-check ENABLED (per-account, opt-in via rpm_limit/tpm_limit)")
	}

	// SEC-249/250:opt-in 的全局按 API-key RPM/TPM 限额,以解析出的
	// APIKeyID 为键(无法通过轮换 IP 绕过)。限额为 0 = 默认关闭。
	keyRPM := envInt64("HUAKAI_KEY_RPM_LIMIT")
	keyTPM := envInt64("HUAKAI_KEY_TPM_LIMIT")
	var keyRateCounter *precheck.Counter
	if keyRPM > 0 || keyTPM > 0 {
		keyRateCounter = pool.NewRatePrecheckCounter()
		logger.Info("SEC-249/250 per-API-key rate limit ENABLED", zap.Int64("rpm", keyRPM), zap.Int64("tpm", keyTPM))
	}

	// 按 binding 的 RPM/TPM 限额,以解析出的 BindingID 为键。与按 key 的封顶(一份
	// 全局配置限额)不同,这里读取每个 binding 自身的 model_pool_bindings.rpm_limit/tpm_limit,
	// 由 SelectionRequest 随每次请求携带。经 env opt-in(默认关闭 → counter 为 nil →
	// BindingRateLimitSelector 透传 = 与现状完全一致);即便启用,未配置限额的 binding
	// 也保持惰性。它有自己独立的 counter 实例 → 即便某个 BindingID 恰好等于某个
	// AccountID/APIKeyID,也不会与按账号/按 key 的 counter 发生键碰撞。
	var bindingRateCounter *precheck.Counter
	if on, _ := strconv.ParseBool(os.Getenv("HUAKAI_BINDING_RATE_LIMIT_ENABLED")); on {
		bindingRateCounter = pool.NewRatePrecheckCounter()
		logger.Info("per-binding RPM/TPM rate limit ENABLED (opt-in via model_pool_bindings.rpm_limit/tpm_limit)")
	}

	// 同一个 DBSlotManager 同时承担三件事：default/PASR 的真实槽获取、
	// binding 外层快速计数读取、以及 acquire 事务内的权威并发硬闸。
	// 共享实例只共享连接池与查询句柄，不保存进程内并发计数。
	slotManager := pool.NewDBSlotManager(pgPool)

	// wrapRecording 组合这些 opt-in 的 selector 包装层(全部在关闭时惰性):
	// BindingRateLimit(按 binding,最外层)套在 KeyRateLimit(按 key,在选号前就拒绝)外,
	// 再套在 Recording(按账号消耗预算,ROUTE-121)外。
	wrapRecording := func(s pool.Selector) pool.Selector {
		if ratePrecheckCounter != nil {
			s = pool.NewRecordingSelector(s, ratePrecheckCounter)
		}
		if keyRateCounter != nil {
			s = pool.NewKeyRateLimitSelector(s, keyRateCounter, keyRPM, keyTPM)
		}
		if bindingRateCounter != nil {
			s = pool.NewBindingRateLimitSelector(s, bindingRateCounter)
		}
		// binding 并发字段本身就是开关，无额外 env 门。该包装层最外置，
		// 饱和时不消耗 key/binding RPM/TPM，也不进入账号选号。
		s = pool.NewBindingConcurrencySelector(s, slotManager)
		return s
	}

	// 1. default selector 总是构造 — 即使 PASR 模式也作为 fallback 实例。
	// gate 链构造抽到 buildGroupRoutingGates 便于直接单测生产激活接线 (否则漏接订阅
	// gate 静默退回 AllowAll 无测可抓)。同一 gates 值流入 default selector + actual/shadow PASR,
	// 一处接线覆盖全 5 mode。
	gates := buildGroupRoutingGates(subscriptionenforce.NewPostgresRoutesRepo(pgPool), healthService, windowCostReader, sessionCapRegistry, ratePrecheckCounter, logger)
	defaultSel := pool.NewDefaultSelector(
		pool.NewDBAccountSource(q),
		pool.WithGateChain(gates),
		pool.WithSlotManager(slotManager),
		pool.WithClaimGate(pool.NewDBClaimGate(q)),
		pool.WithStickyStore(pool.NewDBStickyStore(q)),
		// 路由加权激活闭环:注入生产 RoutingPolicySource,据请求命中 binding 的 selection_mode
		// 返回选号策略。此前缺该注入 → policy() 恒 nil → priority_weighted 分支永不可达(断点1)。
		// 默认 strict_priority 行为不变(opt-in 激活,非全局翻转)。
		pool.WithRoutingPolicySource(newBindingRoutingPolicySource(q)),
	)

	// 2. default mode: 直接返, 不启动 PASR 基础设施
	if !selectorCfg.IsPASR() {
		logger.Info("selector mode=default — PASR 基础设施未启动")
		return wrapRecording(defaultSel), ratePrecheckCounter, func() {}, nil
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
			Slots:    slotManager,
			Segments: segments,
			Gates:    gates,
			// RingProvider 不注入 — 走 request-scoped ring。
		})
		if err != nil {
			agingWorker.Stop()
			return nil, nil, nil, fmt.Errorf("buildSelector: actual PASR: %w", err)
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
			return nil, nil, nil, fmt.Errorf("buildSelector: shadow PASR: %w", err)
		}
		dispatcherCfg.Shadow = shadow
	}

	dispatcher, err := pool.NewSelectorDispatcher(dispatcherCfg)
	if err != nil {
		agingWorker.Stop()
		return nil, nil, nil, fmt.Errorf("buildSelector: dispatcher: %w", err)
	}

	cleanup := func() {
		dispatcher.Stop()
		agingWorker.Stop()
	}
	return wrapRecording(dispatcher), ratePrecheckCounter, cleanup, nil
}

// buildGroupRoutingGates 构造 selector 用的 gate 链 (R-SUB-WIRE-1 生产激活点)。在
// DefaultGateChain 基础上把 GroupPolicy 槽换成接 routes 的真订阅 gate, 并按需替换 Health。
// 抽成独立函数便于单测直接验证激活接线: 漏接订阅 gate 时 GroupPolicy 退回 AllowAll, 单测
// 必红 (TestBuildGroupRoutingGates_WiresRealGroupPolicyGate)。
// routes repo 不可用或查询失败时 fail-closed，防止普通分组进入更高等级账号池；
// 成功读到 Configured=false 才按“从未配置”兼容放行。
func buildGroupRoutingGates(routesRepo subscriptionenforce.RoutesRepo, healthService *channelhealth.Service, windowCostReader windowcost.CostReader, sessionCapRegistry *sessioncap.Registry, ratePrecheckCounter *precheck.Counter, logger *zap.Logger) pool.GateChain {
	gates := pool.DefaultGateChain()
	if healthService != nil {
		gates.Health = channelhealth.NewServicePoolGate(healthService, nil)
	}
	gates.GroupPolicy = subscriptionenforce.NewGroupPolicyGate(
		routesRepo,
		subscriptionenforce.WithFailClosedObserver(newGroupPolicyFailClosedObserver(logger)),
	)
	// ACCOUNT-WINDOW-COST：按账号的 5 小时窗口消费封顶 gate。
	// windowCostReader 为 nil → WindowCostGate 为 fail-open(等价 AllowAll)。
	gates.WindowCost = pool.WindowCostGate{Reader: windowCostReader}
	// ACCOUNT-SESSION-CAP：按账号的最大并发会话封顶 gate。
	// sessionCapRegistry 为 nil → SessionCountGate 为 fail-open。
	gates.SessionCount = pool.SessionCountGate{Registry: sessionCapRegistry}
	// ROUTE-023:按模型的上下文窗口准入 gate。无注入依赖
	//(只读 SelectionRequest 字段);显式设置可让接线回归被测试抓到,
	// 而非默默依赖默认值。
	gates.ContextWindow = pool.ContextWindowGate{}
	// ROUTE-121:按账号的主动 RPM/TPM 预检。counter 为 nil → gate 为
	// fail-open(默认关闭);buildSelector 仅在设置了 HUAKAI_RATE_PRECHECK_ENABLED 时
	// 才注入 counter,并把它与一个 RecordingSelector 配对,这样预算会在 dispatch 时消耗。
	gates.RatePrecheck = pool.RatePrecheckGate{Counter: ratePrecheckCounter}
	return gates
}
