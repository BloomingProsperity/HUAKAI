// Package precheck 持有内存中的 RPM/TPM 预算跟踪器,供主动式限流预检查
// 选择门(ROUTE-121)使用。它让 router 跳过一个即将超出其每分钟请求或 token
// 预算的上游账号,这样平台便能避免引发用户可见的 429,而不只是事后被动应对。
//
// 该跟踪器是一个按上游 account id 索引的固定窗口计数器,有两个独立窗口:
// 每分钟请求数(requests-per-minute)和每分钟 token 数(tokens-per-minute)。
// 零 limit 表示「无限制」(该账号在该维度上永不被阻塞),因此没有配置预算的
// 账号保持其当前行为。nil 的 *Counter 可安全使用:Check 放行,Record 是空操作,
// 从而在跟踪器未接线时让该门保持 fail-open。
package precheck

import (
	"sync"
	"time"
)

// DefaultWindow 是当向 New 传入非正窗口时所用的预算窗口长度。
const DefaultWindow = time.Minute

// defaultMaxKeys 是 reqs/toks 各自 map 的软上限。达到上限且要新增账号键时,先清扫已过期
// 窗口的陈旧条目(它们下次访问本就会被重置,提前删除等价且释放内存),使 map 规模收敛到
// 「当前窗口内活跃账号数」而非「历史出现过的全部账号数」——否则曾出现、后被删/长期不再请求
// 的账号条目永不回收,map 单调增长(本计数器此前是该限流子系统唯一无界者)。
// 本计数器 fail-open(纯限流预检优化,非安全闸),故清扫后即便仍达上限也照常放行新键,
// 绝不因容量拒绝;这点与 loginthrottle 等 fail-closed 兄弟限流器不同(它们达上限拒绝)。
const defaultMaxKeys = 100_000

// Limits 是单个账号的每窗口预算。某维度上为零(或负)值表示该维度无限制。
type Limits struct {
	RPM int64
	TPM int64
}

func (l Limits) rpmLimited() bool { return l.RPM > 0 }
func (l Limits) tpmLimited() bool { return l.TPM > 0 }

// Dimension 标识某个 Decision 是在哪个预算维度上触发的。
type Dimension string

const (
	// DimensionNone 表示请求符合预算。
	DimensionNone Dimension = ""
	// DimensionRPM 表示每分钟请求数预算已满。
	DimensionRPM Dimension = "rpm"
	// DimensionTPM 表示每分钟 token 数预算已满。
	DimensionTPM Dimension = "tpm"
)

// Decision 是对单个账号进行预算预检查的结果。
type Decision struct {
	Allowed   bool
	Dimension Dimension
}

// Counter 是一个并发安全的固定窗口 RPM/TPM 预算跟踪器。
type Counter struct {
	window  time.Duration
	now     func() time.Time
	maxKeys int

	mu   sync.Mutex
	reqs map[int64]*windowCount
	toks map[int64]*windowCount
}

type windowCount struct {
	start time.Time
	count int64
}

// New 返回一个使用给定窗口长度和时钟的 Counter。非正窗口回退到
// DefaultWindow;nil 时钟回退到 time.Now。
func New(window time.Duration, now func() time.Time) *Counter {
	if window <= 0 {
		window = DefaultWindow
	}
	if now == nil {
		now = time.Now
	}
	return &Counter{
		window:  window,
		now:     now,
		maxKeys: defaultMaxKeys,
		reqs:    make(map[int64]*windowCount),
		toks:    make(map[int64]*windowCount),
	}
}

// Check 报告再来一个估算为 estTokens 个 token 的请求是否能容入账号 accountID
// 的预算,但不消耗其中任何额度。真正派发该请求的调用方必须随后调用 Record。
// 在 nil Counter、account id <= 0,或完全无限制的 limits 上,Check 总是放行。
func (c *Counter) Check(accountID int64, lim Limits, estTokens int64) Decision {
	if c == nil || accountID <= 0 || (!lim.rpmLimited() && !lim.tpmLimited()) {
		return Decision{Allowed: true, Dimension: DimensionNone}
	}
	if estTokens < 0 {
		estTokens = 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if lim.rpmLimited() {
		if c.live(c.reqs, accountID, now).count+1 > lim.RPM {
			return Decision{Allowed: false, Dimension: DimensionRPM}
		}
	}
	if lim.tpmLimited() {
		if c.live(c.toks, accountID, now).count+estTokens > lim.TPM {
			return Decision{Allowed: false, Dimension: DimensionTPM}
		}
	}
	return Decision{Allowed: true, Dimension: DimensionNone}
}

// Record 在当前窗口为 accountID 消耗一个请求以及相应 tokens 的预算。在 nil
// Counter 或 account id <= 0 时为空操作。
func (c *Counter) Record(accountID int64, tokens int64) {
	if c == nil || accountID <= 0 {
		return
	}
	if tokens < 0 {
		tokens = 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.live(c.reqs, accountID, now).count++
	c.live(c.toks, accountID, now).count += tokens
}

// TryRecord 原子地完成预算检查与消费，供无法经过 selector 两阶段流程的固定账号
// 后台请求使用。它避免并发轮询同时通过 Check 后再一起 Record 而突破 RPM 上限。
func (c *Counter) TryRecord(accountID int64, lim Limits, tokens int64) Decision {
	if c == nil || accountID <= 0 || (!lim.rpmLimited() && !lim.tpmLimited()) {
		return Decision{Allowed: true, Dimension: DimensionNone}
	}
	if tokens < 0 {
		tokens = 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	requests := c.live(c.reqs, accountID, now)
	tokenCount := c.live(c.toks, accountID, now)
	if lim.rpmLimited() && requests.count+1 > lim.RPM {
		return Decision{Allowed: false, Dimension: DimensionRPM}
	}
	if lim.tpmLimited() && tokenCount.count+tokens > lim.TPM {
		return Decision{Allowed: false, Dimension: DimensionTPM}
	}
	requests.count++
	tokenCount.count += tokens
	return Decision{Allowed: true, Dimension: DimensionNone}
}

// live 返回 accountID 的当前窗口桶,并在时钟跨入一个新的固定窗口时重置它。
// 调用方必须持有 c.mu。
func (c *Counter) live(m map[int64]*windowCount, accountID int64, now time.Time) *windowCount {
	start := now.Truncate(c.window)
	wc := m[accountID]
	if wc == nil || wc.start.Before(start) {
		// 仅在「新增一个此前未见的账号键」且 map 已达上限时,先清扫陈旧条目;摊还 O(n)
		// 清扫开销到稀有的扩容时刻,常规路径零额外成本。已存在但跨窗的键是就地重置、不增长
		// map,无需清扫。
		if wc == nil && c.maxKeys > 0 && len(m) >= c.maxKeys {
			sweepStale(m, start)
		}
		wc = &windowCount{start: start}
		m[accountID] = wc
	}
	return wc
}

// sweepStale 删除窗口起点早于 start 的陈旧条目(已不属于当前窗口,下次访问本会被重置,
// 提前删除语义等价)。调用方必须持有 c.mu。
func sweepStale(m map[int64]*windowCount, start time.Time) {
	for id, wc := range m {
		if wc.start.Before(start) {
			delete(m, id)
		}
	}
}
