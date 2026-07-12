// A07.3:三作用域刷新风暴策略合成器。
// 规格:docs/specs/upstream-credential-management.md §A07 / synthesis §1 A07。
//
// 把 A07.1(TokenBucket)与 A07.2(SingleFlight)组合为三作用域的
// 刷新风暴控制器:
//
//   作用域 1(account): SingleFlight 去重 —— N 个并发的同账号调用者
//                       最终只会触发一次内层执行;跟随者共享
//                       领头者的结果,不额外消耗预算。
//                       这是关键的 OAuth 风暴防护:100 个同
//                       账号 401 ⇒ 仅 1 次 vendor refresh 调用。
//
//   作用域 2(endpoint): 按 (provider, oauth_url) 维度的 TokenBucket —— 保护 vendor
//                       endpoint 免受 M 个账号同时过期的踩踏冲击。
//
//   作用域 3(global): 进程级 TokenBucket —— 最后一道封顶。
//
// Acquire 顺序:account 层 singleflight 包裹 endpoint→global→fn,这样跟随者
// 不付任何预算。仅当某个 bucket 拒绝时才退还(fn 报错时不退还),
// 这样一次失败的 vendor 调用仍会消耗其预算 —— 在失败时退还
// 会重新打开风暴窗口。
//
// 无 IO、无网络、不接触凭证:纯组合 + 准入。
package gateway

import (
	"sync"
	"time"
)

// DenyReason 标识是哪个预算作用域(若有)拒绝了一次 Acquire 调用。
// 故意不设 DenyAccount:SingleFlight 作用域只做去重,
// 它从不拒绝 —— 跟随者会原样收到领头者的结果。
type DenyReason string

const (
	DenyNone     DenyReason = ""
	DenyEndpoint DenyReason = "endpoint"
	DenyGlobal   DenyReason = "global"
)

// StormPolicyConfig 持有两个 bucket 作用域的 rate/burst 参数。
// AccountSF 可注入,以便在多个 StormPolicy 实例间共享账号去重状态;
// nil 表示每个 StormPolicy 各自持有一个私有实例。
type StormPolicyConfig struct {
	GlobalRate       float64 // 共享 global bucket 的每秒 token 数
	GlobalBurst      float64 // global bucket 的容量上限
	PerEndpointRate  float64 // 每个 per-endpoint bucket 的每秒 token 数
	PerEndpointBurst float64 // 每个 per-endpoint bucket 的容量上限
	AccountSF        *SingleFlight
}

// StormPolicy 是三作用域刷新风暴控制器。所有方法
// 均可安全并发使用。
type StormPolicy struct {
	cfg             StormPolicyConfig
	globalBucket    *TokenBucket
	endpointBuckets sync.Map // map[string]*TokenBucket —— 首次 Acquire 时惰性创建
	accountSF       *SingleFlight
}

// stormPolicyResult 是 singleflight 内层执行器的返回值。外层
// Acquire 把它拆回 (val, err, denied)。
type stormPolicyResult struct {
	val    any
	denied DenyReason
}

// NewStormPolicy 返回一个初始化好的策略。Rate/burst 值按字面取用 ——
// 0 表示「永不准入」,正值表示其字面 token 预算。调用方
// 负责传入有意义的默认值;这个原语不会悄悄注入「宽松」哨兵值
// (那样会把配错的 operator 策略伪装成「全部放行」)。
//
// 可提供 AccountSF 以在多个 StormPolicy 实例间共享去重状态;
// nil 表示每个策略各自持有一个私有 SingleFlight。
func NewStormPolicy(cfg StormPolicyConfig) *StormPolicy {
	accountSF := cfg.AccountSF
	if accountSF == nil {
		accountSF = NewSingleFlight()
	}
	cfg.AccountSF = accountSF
	return &StormPolicy{
		cfg:          cfg,
		globalBucket: NewTokenBucket(cfg.GlobalRate, cfg.GlobalBurst),
		accountSF:    accountSF,
	}
}

// endpointBucket 返回 endpointKey 对应的 TokenBucket,首次访问时
// 惰性创建一个(通过 sync.Map LoadOrStore 做到竞态安全)。
func (p *StormPolicy) endpointBucket(endpointKey string) *TokenBucket {
	if v, ok := p.endpointBuckets.Load(endpointKey); ok {
		return v.(*TokenBucket)
	}
	nb := NewTokenBucket(p.cfg.PerEndpointRate, p.cfg.PerEndpointBurst)
	actual, _ := p.endpointBuckets.LoadOrStore(endpointKey, nb)
	return actual.(*TokenBucket)
}

// Acquire 执行三作用域策略,对每组并发的同账号调用者
// 集合最多运行一次 fn。
//
// 返回:
//   - (val, fn-err, DenyNone)        —— fn 已执行(或其结果被某个跟随者共享)
//   - (nil, nil, DenyEndpoint)       —— endpoint bucket 耗尽;未运行 fn
//   - (nil, nil, DenyGlobal)         —— global bucket 耗尽;未运行 fn
//
// 仅在 bucket 拒绝时才退还。fn 报错时 token 保持已消耗状态,这样一次
// 失败的尝试不会重新打开风暴窗口。
func (p *StormPolicy) Acquire(
	now time.Time,
	accountID, endpointKey string,
	fn func() (any, error),
) (val any, err error, denied DenyReason) {
	eb := p.endpointBucket(endpointKey)

	wrapped, fnErr, _ := p.accountSF.Do(accountID, func() (any, error) {
		// 作用域 2:endpoint
		if !eb.TryAcquire(now) {
			return stormPolicyResult{denied: DenyEndpoint}, nil
		}
		// 作用域 3:global —— global 拒绝时退还 endpoint
		if !p.globalBucket.TryAcquire(now) {
			eb.Refund(now)
			return stormPolicyResult{denied: DenyGlobal}, nil
		}
		// 两个 bucket 都准入;运行 fn。若 fn 报错,token 保持已消耗
		// (失败的尝试绝不能重新打开风暴窗口)。
		v, e := fn()
		return stormPolicyResult{val: v, denied: DenyNone}, e
	})

	result, ok := wrapped.(stormPolicyResult)
	if !ok {
		// 防御性:fn 不知何故在我们的 wrapper 之外运行;直接返回原始值。
		return wrapped, fnErr, DenyNone
	}
	return result.val, fnErr, result.denied
}

// NextEligibleAt 返回针对 endpointKey 的 Acquire 可能成功的最早
// 墙钟时间。调度器用它在「等待」与「故障转移到另一个 endpoint」
// 之间做选择。
//
// 返回 max(global.NextAvailableAt, endpoint.NextAvailableAt)。若任一
// bucket 报告零时间(「永不」),则零值会向上传播,以便调用方
// 检测出不可满足的情形。
func (p *StormPolicy) NextEligibleAt(now time.Time, endpointKey string) time.Time {
	eb := p.endpointBucket(endpointKey)
	globalNext := p.globalBucket.NextAvailableAt(now)
	endpointNext := eb.NextAvailableAt(now)
	if globalNext.IsZero() || endpointNext.IsZero() {
		return time.Time{}
	}
	if globalNext.After(endpointNext) {
		return globalNext
	}
	return endpointNext
}
