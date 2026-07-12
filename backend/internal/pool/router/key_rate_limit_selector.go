package router

import (
	"context"
	"errors"

	"github.com/BloomingProsperity/HUAKAI/internal/rate/precheck"
)

// ErrKeyRateLimited 由 KeyRateLimitSelector 返回:当已认证的 API key 用尽了
// 其每分钟请求(RPM)或 token(TPM)预算时。网关把它映射成 HTTP 429。SEC-249/250。
var ErrKeyRateLimited = errors.New("pool: per-key rate limit exceeded")

// KeyRateLimitSelector 在账号选择之前(SEC-249/250)对每个已认证 API key 强制
// RPM/TPM 预算。由于预算以解析出的 APIKeyID —— 而非客户端 IP —— 为键,调用方无法
// 靠轮换 IP 绕过它,而此前响应式的按 IP 路径是允许这么绕的。限额是全局的(同一上限
// 适用于每个 key),来源于配置;限额为零表示无限,因此该限流器严格按需启用、默认关闭。
//
// 选择成功后,该请求会被记入该 key 的预算(选中即预留)。counter 为 nil 或
// APIKeyID<=0 时,该包装器变成透明直通。
type KeyRateLimitSelector struct {
	inner   Selector
	counter *precheck.Counter
	limits  precheck.Limits
}

// NewKeyRateLimitSelector 用每 key 的 RPM/TPM 预算包装 inner。rpm/tpm <=0
// 表示该维度无限;两者都 <=0 时该包装器为惰性(不生效)。
func NewKeyRateLimitSelector(inner Selector, counter *precheck.Counter, rpm, tpm int64) *KeyRateLimitSelector {
	return &KeyRateLimitSelector{inner: inner, counter: counter, limits: precheck.Limits{RPM: rpm, TPM: tpm}}
}

func (s *KeyRateLimitSelector) active(req SelectionRequest) bool {
	return s.counter != nil && req.APIKeyID > 0 && (s.limits.RPM > 0 || s.limits.TPM > 0)
}

func estTokens(req SelectionRequest) int64 {
	if req.EstimatedInputTokens < 0 {
		return 0
	}
	return int64(req.EstimatedInputTokens)
}

func (s *KeyRateLimitSelector) Select(ctx context.Context, req SelectionRequest) (*SelectionResult, error) {
	if s.active(req) {
		if d := s.counter.Check(req.APIKeyID, s.limits, estTokens(req)); !d.Allowed {
			return nil, ErrKeyRateLimited
		}
	}
	res, err := s.inner.Select(ctx, req)
	if err == nil && res != nil && res.AccountID != 0 && res.WaitPlan == nil && s.active(req) {
		s.counter.Record(req.APIKeyID, estTokens(req))
	}
	return res, err
}

var _ Selector = (*KeyRateLimitSelector)(nil)

// ErrBindingRateLimited 表示命中的 model→pool binding 已耗尽其 per-binding 每分钟请求(RPM)
// 或 token(TPM)预算。网关映射为 HTTP 429。与 per-key(SEC-249/250)、per-account(ROUTE-121)
// 限流同层,但配额来自 binding 自身(model_pool_bindings.rpm_limit/tpm_limit),逐 binding opt-in。
var ErrBindingRateLimited = errors.New("pool: per-binding rate limit exceeded")

// BindingRateLimitSelector 在账号选号前强制 per-binding 的 RPM/TPM 预算。与 KeyRateLimitSelector
// 的关键差别:限流值不是构造时的全局 config,而是**逐请求**从 SelectionRequest 携带的 binding 配额
// 读取(每个 binding 各有自己的 rpm/tpm)。预算键为 req.BindingID,与 account/key 计数器各用独立
// *precheck.Counter 实例,故 BindingID 与 AccountID/APIKeyID 数值即便相同也不会串预算。
//
// 选号成功后按 binding 预算做 reserve-on-select(与 key/account 一致,用估算输入 token)。
// counter==nil、BindingID<=0、或该 binding 两维度都无限额 → 透明 pass-through(逐请求自禁用),
// 故默认(env 关 或 binding 未设限额)逐字节保持现有行为。binding 限流是"整条 model 路由"的粗
// 粒度闸:超限即拒(无其它 binding 可 failover),映射 429,而非像 health gate 那样排除单个候选。
type BindingRateLimitSelector struct {
	inner   Selector
	counter *precheck.Counter
}

// NewBindingRateLimitSelector 用 per-binding 预算计数器包装 inner。counter==nil 时包装器恒透明。
func NewBindingRateLimitSelector(inner Selector, counter *precheck.Counter) *BindingRateLimitSelector {
	return &BindingRateLimitSelector{inner: inner, counter: counter}
}

func (s *BindingRateLimitSelector) limits(req SelectionRequest) precheck.Limits {
	return precheck.Limits{RPM: req.BindingRPMLimit, TPM: req.BindingTPMLimit}
}

// active 报告本请求是否真正受 binding 限流约束:计数器已接线 + 命中了具体 binding + 该 binding
// 至少一个维度设了正限额。任一不满足即 pass-through。
func (s *BindingRateLimitSelector) active(req SelectionRequest) bool {
	lim := s.limits(req)
	return s.counter != nil && req.BindingID > 0 && (lim.RPM > 0 || lim.TPM > 0)
}

func (s *BindingRateLimitSelector) Select(ctx context.Context, req SelectionRequest) (*SelectionResult, error) {
	if s.active(req) {
		if d := s.counter.Check(req.BindingID, s.limits(req), estTokens(req)); !d.Allowed {
			return nil, ErrBindingRateLimited
		}
	}
	res, err := s.inner.Select(ctx, req)
	// 仅在整条内层选号链成功(拿到账号、非 WaitPlan)后才消费 binding 预算,避免为失败/排队请求扣额。
	if err == nil && res != nil && res.AccountID != 0 && res.WaitPlan == nil && s.active(req) {
		s.counter.Record(req.BindingID, estTokens(req))
	}
	return res, err
}

var _ Selector = (*BindingRateLimitSelector)(nil)
