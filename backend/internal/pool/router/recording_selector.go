package router

import (
	"context"

	"github.com/BloomingProsperity/HUAKAI/internal/rate/precheck"
)

// RecordingSelector 包装一个 Selector,在确定某个具体账号选择后,在共享的
// precheck.Counter 中针对该账号的 RPM/TPM 预算记录一次请求(连同其估算的 input
// token)。与 RatePrecheckGate 配对,它闭合了 ROUTE-121 的主动限流回路:gate 在选择
// 账号时读取预算;这里在某个胜出者被提交后消费预算,于是下一个请求看到的是更新后的
// 计数(选定即预留,reserve-on-select)。
//
// 只有具体的选择才会消费预算——wait-plan 准入(Layer-3 队列)以及 error/空结果都被
// 跳过,与 dispatcher 用于影子采样的「有效结果」条件一致。counter 为 nil 时 Record
// 是空操作,因此该包装器可以无条件安装;网关仅在主动限流器启用时才进行包装。
type RecordingSelector struct {
	inner   Selector
	counter *precheck.Counter
}

// NewRecordingSelector 包装 inner,使一次成功的 Select 消费限流预算。
func NewRecordingSelector(inner Selector, counter *precheck.Counter) *RecordingSelector {
	return &RecordingSelector{inner: inner, counter: counter}
}

func (s *RecordingSelector) Select(ctx context.Context, req SelectionRequest) (*SelectionResult, error) {
	res, err := s.inner.Select(ctx, req)
	if err == nil && res != nil && res.AccountID != 0 && res.WaitPlan == nil {
		est := int64(req.EstimatedInputTokens)
		if est < 0 {
			est = 0
		}
		s.counter.Record(res.AccountID, est)
	}
	return res, err
}

var _ Selector = (*RecordingSelector)(nil)
