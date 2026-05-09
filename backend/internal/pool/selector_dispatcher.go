// selector_dispatcher.go — PASR-lite main-wire M4 atomic: 实现 pool.Selector 接口
// 的 5-mode dispatcher, 与 DefaultSelector + PASRSelector 协作, 不替换。
//
// 架构 (synthesis §3.3 + D2/D4/D5/D6):
//
//	┌────────────────┐    mode dispatch
//	│ chat handler   │──────────────────► SelectorDispatcher.Select
//	└────────────────┘                       │
//	                                         ▼
//	     ┌─────── default ────► DefaultSelector.Select
//	     │
//	     ├─────── shadow  ────► DefaultSelector (主路径, 同步返)
//	     │                      │
//	     │                      └─► async shadow goroutine 比对 PASR 选择
//	     │                          (500ms ctx, panic recover, drop counter)
//	     │
//	     ├─────── canary  ────► fnv64a(salt+SessionHash) % 100 < canary_pct
//	     │                      ├ 命中桶 → PASR actual (写 slot+claim)
//	     │                      │   ├ ErrPASRPreMutationFail → fallback default
//	     │                      │   └ ErrPASRPostMutationFail → 已 release, fail closed
//	     │                      └ miss → DefaultSelector
//	     │
//	     ├─── pasr-primary ───► PASR actual (D4: pre-mutation 可 fallback)
//	     │
//	     └──── pasr-strict ───► PASR actual (任何错误 fail closed, 不 fallback)
//
// shadow 不变量 (synthesis B2 + D2):
//   - shadow 实例 ReadOnlySegments=true → SegmentTable.Lookup 不创建段
//   - shadow 实例 Slots=nil + Claims=nil + dispatcher 抹 ClaimID=0 (三层防御)
//   - shadow goroutine 用 context.WithoutCancel(主 ctx) + 500ms timeout, 不被
//     主响应 cancel 带走
//   - buffered chan 1024 + 非阻塞 send + drop counter, 主路径绝不 block
//
// canary 不变量 (D4):
//   - PASR 失败前未 mutate (Slot.Acquire 失败) → ErrPASRPreMutationFail → fallback OK
//   - PASR 失败时已 mutate (claim 写失败) → ErrPASRPostMutationFail → release 已发生,
//     绝不能再走 default 否则双 claim race
package pool

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"sync"
	"time"
)

// 5 mode 字面量与 internal/config/pool_selector.go 常量必须一致。 集成测试
// (M7) 通过 cross-check 测试守门, 防字符串漂移。
const (
	DispatchModeDefault     = "default"
	DispatchModeShadow      = "shadow"
	DispatchModeCanary      = "canary"
	DispatchModePASRPrimary = "pasr-primary"
	DispatchModePASRStrict  = "pasr-strict"
)

// shadowQueueCap shadow 异步队列容量 — 1024 在 5k qps + shadow 100% 下覆盖
// ~200ms 突发, 与下文 shadowSelectTimeout 配合可保护主路径不 block。
const shadowQueueCap = 1024

// shadowSelectTimeout shadow PASR Select 单次超时 (synthesis D5)。 复用
// req.Context 会因主响应 return 而提前 cancel, 用 context.WithoutCancel
// 派生独立 ctx + 500ms 上限, 避免泄漏 + 信号失真。
const shadowSelectTimeout = 500 * time.Millisecond

// SelectorDispatcher 实现 pool.Selector 接口, 按 mode 把请求路由到 default /
// PASR actual / PASR shadow 子 selector。 持有所有 sub-selector 实例 + shadow
// goroutine 生命周期。
type SelectorDispatcher struct {
	mode          string
	shadowPercent int
	canaryPercent int
	samplingSalt  string

	defaultSel pool_selectorImpl // 任意 Selector 实现 (production 用 DefaultSelector)
	pasrSel    pool_selectorImpl // PASR actual (canary/pasr-primary/pasr-strict 用), nil → 仅 default 模式合法
	shadowSel  pool_selectorImpl // PASR shadow 实例 (Slots=nil, Claims=nil, ReadOnlySegments=true), nil → shadow 模式不可用

	// shadow async 生命周期
	shadowQueue chan shadowJob
	shadowWG    sync.WaitGroup
	shadowOnce  sync.Once
	shadowDone  chan struct{}
}

// pool_selectorImpl 局部别名 — 避免与包 export Selector 接口的命名冲突
// (Selector 是接口, 这里只是内部存储类型, 用接口任何实现都可注入)。
type pool_selectorImpl = Selector

// shadowJob 一次 shadow 比对任务 — main goroutine 拿到 default 结果后入队。
type shadowJob struct {
	parentCtx    context.Context
	req          SelectionRequest
	defaultAccID int64 // default 选的 account, shadow PASR 选完比对
}

// SelectorDispatcherConfig 构造期参数。
type SelectorDispatcherConfig struct {
	Mode          string
	ShadowPercent int // 0-100; shadow 模式下采样比例
	CanaryPercent int // 0-100; canary 模式下走 PASR 桶比例
	SamplingSalt  string

	Default Selector // 必填 (default mode 的 actual selector, 通常 DefaultSelector)
	PASR    Selector // canary / pasr-* 模式必填 (PASRSelector with Slots+Claims)
	Shadow  Selector // shadow 模式必填 (PASRSelector with Slots=nil + Claims=nil + ReadOnly)
}

// NewSelectorDispatcher 构造实例。 mode 必须已通过 config.Validate 校验,
// 这里只检查 sub-selector 注入与 mode 配套。
func NewSelectorDispatcher(cfg SelectorDispatcherConfig) (*SelectorDispatcher, error) {
	if cfg.Default == nil {
		return nil, errors.New("dispatcher: Default selector 必填")
	}
	switch cfg.Mode {
	case DispatchModeDefault:
		// 仅 default — 不需要 PASR/shadow 注入
	case DispatchModeShadow:
		if cfg.Shadow == nil {
			return nil, errors.New("dispatcher: shadow mode 需注入 Shadow selector")
		}
	case DispatchModeCanary, DispatchModePASRPrimary, DispatchModePASRStrict:
		if cfg.PASR == nil {
			return nil, fmt.Errorf("dispatcher: %s mode 需注入 PASR selector", cfg.Mode)
		}
	default:
		return nil, fmt.Errorf("dispatcher: invalid mode %q", cfg.Mode)
	}
	if cfg.ShadowPercent < 0 || cfg.ShadowPercent > 100 {
		return nil, fmt.Errorf("dispatcher: ShadowPercent=%d 越界 [0,100]", cfg.ShadowPercent)
	}
	if cfg.CanaryPercent < 0 || cfg.CanaryPercent > 100 {
		return nil, fmt.Errorf("dispatcher: CanaryPercent=%d 越界 [0,100]", cfg.CanaryPercent)
	}

	d := &SelectorDispatcher{
		mode:          cfg.Mode,
		shadowPercent: cfg.ShadowPercent,
		canaryPercent: cfg.CanaryPercent,
		samplingSalt:  cfg.SamplingSalt,
		defaultSel:    cfg.Default,
		pasrSel:       cfg.PASR,
		shadowSel:     cfg.Shadow,
		shadowQueue:   make(chan shadowJob, shadowQueueCap),
		shadowDone:    make(chan struct{}),
	}
	if cfg.Mode == DispatchModeShadow && cfg.Shadow != nil {
		d.startShadowWorker()
	}
	return d, nil
}

// Select 实现 pool.Selector 接口主入口, 按 mode 分发。
func (d *SelectorDispatcher) Select(ctx context.Context, req SelectionRequest) (*SelectionResult, error) {
	IncDispatchMode(d.mode)
	// D2: 同时按 vendor 切片记录 mode 命中, dashboard 可看 per-(vendor, mode)
	// 数据 (memory: project_real_vendor_account_scope)。 req.Vendor 空时静默。
	IncDispatchVendorMode(d.mode, req.Vendor)
	switch d.mode {
	case DispatchModeDefault:
		return d.defaultSel.Select(ctx, req)
	case DispatchModeShadow:
		return d.dispatchShadow(ctx, req)
	case DispatchModeCanary:
		return d.dispatchCanary(ctx, req)
	case DispatchModePASRPrimary:
		return d.dispatchPASR(ctx, req, false /* not strict */)
	case DispatchModePASRStrict:
		return d.dispatchPASR(ctx, req, true /* strict */)
	default:
		// LoadPoolSelector + Validate 已守门, 此分支理论不可达; 兜底返 default
		return d.defaultSel.Select(ctx, req)
	}
}

// dispatchShadow: 主路径走 default; 命中 shadow 桶则把 (req, default 选的 acc)
// 入异步队列由 worker 跑 shadow PASR 比对。 主路径绝不阻塞。
func (d *SelectorDispatcher) dispatchShadow(ctx context.Context, req SelectionRequest) (*SelectionResult, error) {
	res, err := d.defaultSel.Select(ctx, req)
	// 仅在 default 拿到 valid result (有 AccountID 且非 wait plan) 时做 shadow 比对。
	// err / wait plan 路径不入 shadow — 比对没意义。
	if err == nil && res != nil && res.AccountID != 0 && res.WaitPlan == nil {
		if d.shouldSample(req.SessionHash, d.shadowPercent) {
			IncDispatchShadowSampled()
			d.enqueueShadow(ctx, req, res.AccountID)
		}
	}
	return res, err
}

// dispatchCanary: 命中桶 (5%/25%) 走 PASR actual, 其他走 default。 PASR
// pre-mutation 失败时 fallback default; post-mutation 必 fail closed。
func (d *SelectorDispatcher) dispatchCanary(ctx context.Context, req SelectionRequest) (*SelectionResult, error) {
	if d.shouldSample(req.SessionHash, d.canaryPercent) {
		res, err := d.pasrSel.Select(ctx, req)
		switch {
		case err == nil:
			IncDispatchCanaryPASRUsed()
			return res, nil
		case errors.Is(err, ErrPASRPostMutationFail):
			// release 已在 PASRSelector 内做; 绝不能再走 default 否则双 claim race
			IncDispatchCanaryPostMutationRelease()
			return nil, err
		case errors.Is(err, ErrPASRPreMutationFail):
			IncDispatchCanaryPreMutationFallback()
			return d.defaultSel.Select(ctx, req)
		default:
			// 其他错误 (ListAccounts 失败 / ErrNoEligibleAccount): 视作 pre-mutation
			// 安全 fallback default — PASR 没机会写任何 state。
			IncDispatchCanaryPreMutationFallback()
			return d.defaultSel.Select(ctx, req)
		}
	}
	IncDispatchCanaryDefaultUsed()
	return d.defaultSel.Select(ctx, req)
}

// dispatchPASR: pasr-primary / pasr-strict 共用主体, strict=true 时任何错误
// 都 fail closed (不 fallback, 验收终态)。
func (d *SelectorDispatcher) dispatchPASR(ctx context.Context, req SelectionRequest, strict bool) (*SelectionResult, error) {
	res, err := d.pasrSel.Select(ctx, req)
	if err == nil {
		return res, nil
	}
	if errors.Is(err, ErrPASRPostMutationFail) {
		IncDispatchCanaryPostMutationRelease()
		return nil, err
	}
	if strict {
		// pasr-strict 模式 — 任何 PASR 错误都 fail closed, 不 fallback。
		return nil, err
	}
	// pasr-primary 模式 — pre-mutation 错误可 fallback default
	if errors.Is(err, ErrPASRPreMutationFail) {
		IncDispatchCanaryPreMutationFallback()
		return d.defaultSel.Select(ctx, req)
	}
	// 其他错误 (ListAccounts / NoEligibleAccount) 视作 pre-mutation, fallback OK
	IncDispatchCanaryPreMutationFallback()
	return d.defaultSel.Select(ctx, req)
}

// enqueueShadow 非阻塞 send 入队; 队列满直接 drop + 累计 drop counter。 主路径
// 不 block 是 shadow 模式硬不变量 (synthesis R7 / R8 缓解)。
func (d *SelectorDispatcher) enqueueShadow(parentCtx context.Context, req SelectionRequest, defaultAccID int64) {
	// 抹掉 ClaimID 防 shadow PASR 任何路径误写 billing_claims (三层防御之一)
	shadowReq := req
	shadowReq.ClaimID = 0
	job := shadowJob{
		parentCtx:    parentCtx,
		req:          shadowReq,
		defaultAccID: defaultAccID,
	}
	select {
	case d.shadowQueue <- job:
	default:
		IncDispatchShadowDrop()
	}
}

// startShadowWorker 起单 goroutine 消费 shadowQueue。 单 worker 足够 —
// shadow 比对 hot path 是纯 PASR Select (内存 + 一次 ListAccounts), 单核
// 5k+ rps 没压力; 多 worker 反而增加段表锁竞争 (synthesis B4)。
func (d *SelectorDispatcher) startShadowWorker() {
	d.shadowOnce.Do(func() {
		d.shadowWG.Add(1)
		go d.shadowLoop()
	})
}

// shadowLoop 从 queue 读 job, 跑 shadow PASR Select 与 default 比对, 累指标。
// 收到 close(shadowQueue) → drain 完剩余 jobs 退出。 panic 在 worker 内 recover
// 避免拖垮 main goroutine。
func (d *SelectorDispatcher) shadowLoop() {
	defer d.shadowWG.Done()
	defer close(d.shadowDone)
	for job := range d.shadowQueue {
		d.runShadowJob(job)
	}
}

// runShadowJob 单次 shadow 比对 — recover panic, 跑 PASR Select with 500ms
// 独立 ctx, 比对 default 选的 account, 累 metrics。
func (d *SelectorDispatcher) runShadowJob(job shadowJob) {
	defer func() {
		if r := recover(); r != nil {
			IncDispatchShadowPanic()
		}
	}()
	// 派生独立 ctx — context.WithoutCancel 保留 parentCtx 的 values (request id /
	// trace span 等) 但 cancel 不传染 (主响应 return 后 ctx canceled 不影响 shadow)。
	shadowCtx, cancel := context.WithTimeout(
		context.WithoutCancel(job.parentCtx), shadowSelectTimeout)
	defer cancel()

	res, err := d.shadowSel.Select(shadowCtx, job.req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			IncDispatchShadowTimeout()
			return
		}
		IncDispatchShadowPASRErr()
		return
	}
	if res == nil || res.AccountID == 0 {
		IncDispatchShadowPASRErr()
		return
	}
	if res.AccountID == job.defaultAccID {
		IncDispatchShadowMatch()
	} else {
		IncDispatchShadowDiff()
	}
}

// Stop 优雅关闭 shadow goroutine。 close(queue) → worker drain 剩余 jobs →
// 退出。 调多次 Stop 安全 (Once 保护)。
func (d *SelectorDispatcher) Stop() {
	if d.shadowQueue == nil {
		return
	}
	// 仅 shadow mode 启动了 worker; 其他 mode close queue 但没 worker 等待 done。
	closeOnce(&d.shadowOnce, d.shadowQueue, &d.shadowWG, d.shadowDone)
}

// closeOnce 守门 — 已 Stop 后再次 Stop 不重复 close (避免 close of closed chan panic)。
// 用 sync.Once 包装但需要外部判断: 通过尝试关闭 done chan 间接判断是否已 Stop。
func closeOnce(once *sync.Once, queue chan shadowJob, wg *sync.WaitGroup, done <-chan struct{}) {
	select {
	case <-done:
		// worker 已退出, 无需再 close
		return
	default:
	}
	// 用 defer + recover 防止重复 close (Stop 并发竞争)
	defer func() { _ = recover() }()
	close(queue)
	wg.Wait()
}

// shouldSample fnv64a(salt + SessionHash) % 100 < pct, deterministic 同 hash
// 永远落同侧。 SessionHash 空时用 salt 单独哈希避免 hash="0" 总落桶 0。
func (d *SelectorDispatcher) shouldSample(sessionHash string, pct int) bool {
	if pct <= 0 {
		return false
	}
	if pct >= 100 {
		return true
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(d.samplingSalt))
	_, _ = h.Write([]byte("|"))
	_, _ = h.Write([]byte(sessionHash))
	bucket := int(h.Sum64() % 100)
	return bucket < pct
}

// 编译期断言: SelectorDispatcher 实现 Selector 接口
var _ Selector = (*SelectorDispatcher)(nil)
