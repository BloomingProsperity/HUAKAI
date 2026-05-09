// pasr_feedback.go — PASR-lite A4: cache observation 反馈钩子。
//
// 把 cachemetrics.RegisterCacheObserver 的事件流转换为 PrefixSegment 状态
// 更新: cache_creation_input_tokens > 0 → MarkCacheSeen(idx);
// cache_read_input_tokens > 0 → MarkRead(seg, now)。
//
// 调用流向 (PASR-lite 全闭环):
//
//  1. PASRSelector.Select 时段成员被选 → claim 写入 → forwarder 派发请求
//  2. 上游响应 SSE 终态事件 (anthropic message_stop / openai final chunk)
//     → proto adapter 调 cachemetrics.ObserveByAccountWithPrefix
//  3. 本文件注册的 observer fn 触发 → 找段 + 找成员 idx → 更新 bitmap/age
//  4. 下次相同 prefix 请求 PASRSelector.Select → 优先选 bitmap 标记成员
//
// caller 在启动期把 PASR 实例 + segment table 给本工厂, 调
// RegisterCacheObserver 一次后就不再操心。
package pool

import (
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/cachemetrics"
)

// PASRCacheFeedback 是 cachemetrics observer 的封装, 由 selector + 段表
// 协作产出反馈到段状态。
type PASRCacheFeedback struct {
	segments *SegmentTable
	now      func() time.Time
}

// NewPASRCacheFeedback 构造 feedback 实例。
//   - segments: PASRSelector 用的同一段表 (引用一致才能正确 lookup)
//   - now: 时间注入 (测试用, nil 用 time.Now)
func NewPASRCacheFeedback(segments *SegmentTable, now func() time.Time) *PASRCacheFeedback {
	if now == nil {
		now = time.Now
	}
	return &PASRCacheFeedback{segments: segments, now: now}
}

// Observer 返回可注册到 cachemetrics.RegisterCacheObserver 的回调函数。
// caller 在启动期调一次, 终生订阅。
func (f *PASRCacheFeedback) Observer() func(cachemetrics.CacheObservation) {
	return f.handle
}

// handle 处理一次 cache observation 事件。
// 静默 no-op 条件:
//   - prefixHash 空 → caller 没传 SessionHash, 退化路径不参与 PASR
//   - accountID 0    → 测试或退化 case
//   - 段不存在        → 该 prefix 段已老化或从未通过 PASRSelector 创建
//   - 段成员中找不到 accountID → rebalance 滞后, 段已迁移; 跳过避免误标
func (f *PASRCacheFeedback) handle(obs cachemetrics.CacheObservation) {
	if obs.PrefixHash == "" || obs.AccountID == 0 {
		return
	}
	// M5b: TenantID 必填; 0 时段表查不到 (segmentKey 编码 tenant=0 vs 真实
	// tenant!=0 自然区分), 直接 short-circuit 省 lookup 开销。
	if obs.TenantID == 0 {
		return
	}
	if f.segments == nil {
		return
	}
	seg := f.segments.Lookup(obs.TenantID, []byte(obs.PrefixHash))
	if seg == nil {
		return
	}
	idx := seg.IndexOf(obs.AccountID)
	if idx < 0 {
		return
	}
	if obs.CacheCreation > 0 {
		seg.MarkCacheSeen(idx)
		seg.LastWriteAt.Store(f.now().UnixNano())
		IncCacheCreationObs()
	}
	if obs.CacheRead > 0 {
		f.segments.MarkRead(seg, f.now())
		IncCacheHitObs()
	}
}

// RegisterPASRCacheFeedback 启动期一行调用注册 feedback。caller 持有 PASR
// selector + segments, 本函数把 feedback 接入 cachemetrics observer 链。
//
// 用法: cmd/gateway/main.go 启动 PASR-lite 时调用本函数即可关闭 A4 闭环。
func RegisterPASRCacheFeedback(segments *SegmentTable) {
	fb := NewPASRCacheFeedback(segments, nil)
	cachemetrics.RegisterCacheObserver(fb.Observer())
}
