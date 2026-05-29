// Package circuitbreaker 提供 HUAKAI 计费熔断器的内存态核心。
package circuitbreaker

import (
	"sync"
	"time"
)

const (
	defaultFailureThreshold         = 5
	defaultOpenCooldown             = time.Minute
	defaultHalfOpenMaxProbes        = 1
	defaultHalfOpenSuccessesToClose = 1
)

// State 表示单个熔断 key 的状态。
type State int

const (
	Closed State = iota
	Open
	HalfOpen
)

// FailMode 表示 Open 状态下的放行策略。
type FailMode int

const (
	FailClosed FailMode = iota
	FailOpen
)

// Config 控制熔断状态机的阈值、冷却和测试时钟。
type Config struct {
	FailureThreshold         int
	OpenCooldown             time.Duration
	HalfOpenMaxProbes        int
	HalfOpenSuccessesToClose int
	Clock                    func() time.Time
}

// Decision 是 Allow 对一次请求的判定。
type Decision struct {
	Allowed          bool
	State            State
	Reason           string
	ServingUntracked bool
}

// StateView 是给运维查询使用的只读状态快照。
type StateView struct {
	State        State
	FailureCount int
	OpenUntil    time.Time
	FailMode     FailMode
}

// Breaker 是按不透明 key 隔离的并发安全熔断器。
type Breaker struct {
	mu      sync.Mutex
	cfg     Config
	entries map[string]*entry
}

type entry struct {
	state             State
	failureCount      int
	openUntil         time.Time
	failMode          FailMode
	halfOpenProbes    int
	halfOpenSuccesses int
}

// New 构造一个内存态熔断器, 缺省策略为 fail-closed。
func New(cfg Config) *Breaker {
	return &Breaker{
		cfg:     normalizeConfig(cfg),
		entries: make(map[string]*entry),
	}
}

// Allow 判断指定 key 当前是否允许进入计费主路径。
func (b *Breaker) Allow(key string) Decision {
	if b == nil {
		return Decision{Allowed: true, State: Closed, Reason: "breaker_not_configured"}
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	e := b.entries[key]
	if e == nil {
		return Decision{Allowed: true, State: Closed, Reason: "closed"}
	}

	now := b.cfg.Clock()
	switch e.state {
	case Closed:
		return Decision{Allowed: true, State: Closed, Reason: "closed"}
	case Open:
		if !now.Before(e.openUntil) {
			b.enterHalfOpenLocked(e)
			return b.allowHalfOpenProbeLocked(e)
		}
		return b.openDecisionLocked(e, Open)
	case HalfOpen:
		return b.allowHalfOpenProbeLocked(e)
	default:
		return Decision{Allowed: false, State: Open, Reason: "unknown_state"}
	}
}

// RecordSuccess 记录一次计费成功, 用于 Closed 清零和 HalfOpen 闭合。
func (b *Breaker) RecordSuccess(key string) {
	if b == nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	e := b.entries[key]
	if e == nil {
		return
	}

	switch e.state {
	case Closed:
		e.failureCount = 0
	case HalfOpen:
		if e.halfOpenProbes > 0 {
			e.halfOpenProbes--
		}
		e.halfOpenSuccesses++
		if e.halfOpenSuccesses >= b.cfg.HalfOpenSuccessesToClose {
			b.closeLocked(e)
		}
	}
}

// RecordFailure 记录一次计费失败, 达阈值或 HalfOpen 失败时开闸。
func (b *Breaker) RecordFailure(key string) {
	if b == nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	e := b.getOrCreateLocked(key)
	e.failureCount++

	switch e.state {
	case Closed:
		if e.failureCount >= b.cfg.FailureThreshold {
			b.openLocked(e, b.cfg.Clock())
		}
	case HalfOpen:
		b.openLocked(e, b.cfg.Clock())
	}
}

// StateOf 返回指定 key 的运维可见状态快照。
func (b *Breaker) StateOf(key string) StateView {
	if b == nil {
		return StateView{State: Closed, FailMode: FailClosed}
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	e := b.entries[key]
	if e == nil {
		return StateView{State: Closed, FailMode: FailClosed}
	}
	return StateView{
		State:        e.state,
		FailureCount: e.failureCount,
		OpenUntil:    e.openUntil,
		FailMode:     e.failMode,
	}
}

// SetFailMode 设置指定 key 在 Open 状态下的 fail-closed/fail-open 策略。
func (b *Breaker) SetFailMode(key string, mode FailMode) {
	if b == nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	e := b.getOrCreateLocked(key)
	e.failMode = normalizeFailMode(mode)
}

// ForceOpen 强制指定 key 进入 Open, 角色门禁由后续 wiring 层负责。
func (b *Breaker) ForceOpen(key string) {
	if b == nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.openLocked(b.getOrCreateLocked(key), b.cfg.Clock())
}

// ForceClose 强制指定 key 进入 Closed, 角色门禁由后续 wiring 层负责。
func (b *Breaker) ForceClose(key string) {
	if b == nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.closeLocked(b.getOrCreateLocked(key))
}

func normalizeConfig(cfg Config) Config {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = defaultFailureThreshold
	}
	if cfg.OpenCooldown <= 0 {
		cfg.OpenCooldown = defaultOpenCooldown
	}
	if cfg.HalfOpenMaxProbes <= 0 {
		cfg.HalfOpenMaxProbes = defaultHalfOpenMaxProbes
	}
	if cfg.HalfOpenSuccessesToClose <= 0 {
		cfg.HalfOpenSuccessesToClose = defaultHalfOpenSuccessesToClose
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	return cfg
}

func normalizeFailMode(mode FailMode) FailMode {
	if mode == FailOpen {
		return FailOpen
	}
	return FailClosed
}

func (b *Breaker) getOrCreateLocked(key string) *entry {
	e := b.entries[key]
	if e != nil {
		return e
	}
	e = &entry{state: Closed, failMode: FailClosed}
	b.entries[key] = e
	return e
}

func (b *Breaker) enterHalfOpenLocked(e *entry) {
	e.state = HalfOpen
	e.halfOpenProbes = 0
	e.halfOpenSuccesses = 0
}

func (b *Breaker) allowHalfOpenProbeLocked(e *entry) Decision {
	if e.halfOpenProbes >= b.cfg.HalfOpenMaxProbes {
		decision := b.openDecisionLocked(e, HalfOpen)
		if decision.Reason == "open_fail_closed" {
			decision.Reason = "half_open_probe_limit_fail_closed"
		}
		if decision.Reason == "open_fail_open" {
			decision.Reason = "half_open_probe_limit_fail_open"
		}
		return decision
	}
	e.halfOpenProbes++
	return Decision{Allowed: true, State: HalfOpen, Reason: "half_open_probe"}
}

func (b *Breaker) openDecisionLocked(e *entry, state State) Decision {
	if e.failMode == FailOpen {
		return Decision{
			Allowed:          true,
			State:            state,
			Reason:           "open_fail_open",
			ServingUntracked: true,
		}
	}
	return Decision{Allowed: false, State: state, Reason: "open_fail_closed"}
}

func (b *Breaker) openLocked(e *entry, now time.Time) {
	e.state = Open
	e.openUntil = now.Add(b.cfg.OpenCooldown)
	e.halfOpenProbes = 0
	e.halfOpenSuccesses = 0
}

func (b *Breaker) closeLocked(e *entry) {
	e.state = Closed
	e.failureCount = 0
	e.openUntil = time.Time{}
	e.halfOpenProbes = 0
	e.halfOpenSuccesses = 0
}
