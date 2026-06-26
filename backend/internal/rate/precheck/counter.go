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
	window time.Duration
	now    func() time.Time

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
		window: window,
		now:    now,
		reqs:   make(map[int64]*windowCount),
		toks:   make(map[int64]*windowCount),
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

// live 返回 accountID 的当前窗口桶,并在时钟跨入一个新的固定窗口时重置它。
// 调用方必须持有 c.mu。
func (c *Counter) live(m map[int64]*windowCount, accountID int64, now time.Time) *windowCount {
	start := now.Truncate(c.window)
	wc := m[accountID]
	if wc == nil || wc.start.Before(start) {
		wc = &windowCount{start: start}
		m[accountID] = wc
	}
	return wc
}
