// 注意：本文件是 PATCH 提案文件（_patch.go 后缀），不参与 Go build。
// 将此文件内容合并到 backend/internal/gateway/forwarder.go 后删除本文件。
package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/shopspring/decimal"
)

// StreamForwarder 执行 F-GW-002 A-D 阶段的流式转发流水线。
//
// 变更说明（wire-up 重构）：
//   - 新增 ProtocolAdapters 字段，替代原先硬编码的 proto.AnthropicAdapter。
//   - 删除原 UpstreamAdapter 字段（由 ProtocolAdapters + ForwardRequest.ProtocolFamily 动态解析）。
//   - Forward 入口校验 ProtocolFamily 非空、ProtocolAdapters 非 nil。
type StreamForwarder struct {
	// ProtocolAdapters 是协议适配器注册表，Forward 按 ForwardRequest.ProtocolFamily 查询。
	// 必须非 nil；若为 nil，Forward 将立即返回错误。
	ProtocolAdapters ProtocolAdapterRegistry

	// ClientAdapter 将 canonical event 转换为客户端协议块（可选）。
	// 若为 nil，则透传原始 SSE 给客户端。
	ClientAdapter proto.ClientAdapter

	Timeouts         TimeoutConfig
	ScannerBufferCap int
	DrainBudgets     DrainBudgets

	// CostEstimator 根据已 drain 字节数 + 累加器状态估算费用。
	// 当 DrainBudgets.MaxEstimatedCost > 0 且估算值超限时，drain 以 DrainBudgetCostExhausted 退出。
	// nil 表示禁用基于费用的 drain 退出。
	CostEstimator func(drainedBytes int64, acc UsageAccumulator) decimal.Decimal
}

// Forward 执行 F-GW-002 Phase A 扫描、Phase B 处理、Phase C 分类、
// Phase C-bis drain，并返回 Phase D draft。
//
// 校验顺序：
//  1. ProtocolAdapters 为 nil → ErrNilProtocolAdapterRegistry
//  2. ProtocolFamily 为空    → ErrEmptyProtocolFamily（封装 ErrUnknownProtocolFamily）
//  3. ProtocolAdapters.For 失败 → 透传 registry 返回的 error
func (f *StreamForwarder) Forward(ctx context.Context, upstreamReader io.Reader, clientWriter http.ResponseWriter, req ForwardRequest) (UsageRecordDraft, error) {
	// --- 入口校验：ProtocolAdapters 注册表必须已注入 ---
	if f.ProtocolAdapters == nil {
		return UsageRecordDraft{}, ErrNilProtocolAdapterRegistry
	}

	// --- 入口校验：ProtocolFamily 不得为空，调用方必须明确指定协议族 ---
	if req.ProtocolFamily == "" {
		return UsageRecordDraft{}, fmt.Errorf("%w: ProtocolFamily 未指定", ErrUnknownProtocolFamily)
	}

	// --- 按请求的 ProtocolFamily 解析 upstream adapter；不 fallback 到默认 ---
	adapter, err := f.ProtocolAdapters.For(req.ProtocolFamily)
	if err != nil {
		return UsageRecordDraft{}, err
	}

	start := time.Now()
	draft := UsageRecordDraft{
		RoutingReason: req.RoutingReasonPayload,
		EndClass:      UnknownTermination,
		UsageSource:   UsageSourceAmbiguous,
		DrainOutcome:  DrainNotDrained,
	}
	acc := UsageAccumulator{Source: UsageSourceReported}
	upstreamCtx, upstreamCancel := context.WithCancel(context.Background())
	defer upstreamCancel()

	events := make(chan scanResult, 1)
	go scanInto(upstreamCtx, upstreamReader, f.ScannerBufferCap, events)

	upstreamState := f.newUpstreamState(req)
	var clientState any
	var terminalSeen bool
	var firstEmitted bool
	var endErr error

	totalTimer := newTimer(f.Timeouts.TotalStreamTimeout)
	firstTimer := newTimer(f.Timeouts.FirstTokenTimeout)
	interTimer := newTimer(0)
	defer stopTimer(totalTimer)
	defer stopTimer(firstTimer)
	defer stopTimer(interTimer)

	for {
		select {
		case <-ctx.Done():
			draft.EndClass, endErr = OrchestratorCancel, ctx.Err()
		case <-timerC(totalTimer):
			draft.EndClass, endErr = TotalStreamTimeout, ErrTotalStreamTimeout
		case <-timerC(firstTimer):
			draft.EndClass, endErr = FirstTokenTimeout, ErrFirstTokenTimeout
		case <-timerC(interTimer):
			draft.EndClass, endErr = InterEventTimeout, ErrInterEventTimeout
		case res, ok := <-events:
			if !ok {
				if terminalSeen {
					draft.EndClass = StreamEndGraceful
				} else {
					draft.EndClass = UpstreamEOFNoTerminal
					draft.PendingReconciliation = true
					if !acc.Empty() {
						acc.Source = UsageSourceInferred
					}
				}
				return f.finishDraft(draft, acc, start, nil)
			}
			if res.err != nil {
				draft.EndClass, endErr = classifyScanError(res.err)
			} else {
				if !firstEmitted {
					stopTimer(firstTimer)
				}
				stopTimer(interTimer)
				interTimer = newTimer(f.Timeouts.InterEventTimeout)
				// 将解析好的 adapter 传入 handleEvent，避免重复 registry 查询
				seen, wrote, err := f.handleEventWithAdapter(upstreamCtx, adapter, res.event, clientWriter, upstreamState, clientState, &acc)
				terminalSeen = terminalSeen || seen
				if wrote && !firstEmitted {
					firstEmitted = true
					draft.FirstTokenLatencyMillis = millisSince(start)
				}
				if err == nil {
					continue
				}
				if errors.Is(err, ErrClientDisconnect) {
					draft.EndClass, endErr = ClientDisconnect, err
					// drain 阶段同样使用同一 adapter，保证 usage 解析一致
					draft.DrainOutcome = f.drainWithAdapter(upstreamCtx, adapter, events, upstreamState, &acc)
				} else {
					draft.EndClass, endErr = UnknownTermination, err
				}
			}
		}
		return f.finishDraft(draft, acc, start, endErr)
	}
}

// handleEventWithAdapter 使用调用方已解析的 adapter 处理单个 SSE 事件。
// 原 handleEvent 被替换为此方法，消除对 f.UpstreamAdapter 的依赖。
func (f *StreamForwarder) handleEventWithAdapter(
	ctx context.Context,
	adapter proto.UpstreamAdapter,
	evt SSEEvent,
	w http.ResponseWriter,
	upstreamState any,
	clientState any,
	acc *UsageAccumulator,
) (bool, bool, error) {
	terminalSeen := evt.Type == "message_stop" || string(evt.Data) == "[DONE]"

	// adapter 为 nil 时透传原始 SSE（保留既有 nil-adapter 行为）
	if adapter == nil {
		if err := writeAndFlush(w, rawSSE(evt)); err != nil {
			return terminalSeen, false, ErrClientDisconnect
		}
		return terminalSeen, true, nil
	}

	canonicalEvents, _, err := adapter.ProviderEventToCanonicalEvents(ctx, evt.Data, upstreamState)
	if err != nil {
		return terminalSeen, false, err
	}
	wrote := false
	for _, canonical := range canonicalEvents {
		if usage, ok := canonicalUsage(canonical); ok {
			acc.Update(UsageSourceReported, usage)
		}
		if canonicalTerminal(canonical) {
			terminalSeen = true
			acc.Freeze()
		}
		chunks, err := f.clientChunks(ctx, canonical, clientState, evt)
		if err != nil {
			return terminalSeen, wrote, err
		}
		for _, chunk := range chunks {
			if len(chunk) == 0 {
				continue
			}
			if err := writeAndFlush(w, chunk); err != nil {
				return terminalSeen, wrote, ErrClientDisconnect
			}
			wrote = true
		}
	}
	return terminalSeen, wrote, nil
}

// drainWithAdapter 使用调用方已解析的 adapter 执行 Phase C-bis bounded drain。
// 原 drain 方法被替换为此方法，消除对 f.UpstreamAdapter 的依赖。
func (f *StreamForwarder) drainWithAdapter(
	ctx context.Context,
	adapter proto.UpstreamAdapter,
	events <-chan scanResult,
	upstreamState any,
	acc *UsageAccumulator,
) DrainOutcome {
	budgets := f.effectiveDrainBudgets()
	deadline := time.NewTimer(budgets.MaxSeconds)
	defer deadline.Stop()
	var drainedBytes int64
	for {
		select {
		case <-deadline.C:
			return DrainBudgetSecondsExhausted
		case res, ok := <-events:
			if !ok {
				return DrainBudgetSecondsExhausted
			}
			if res.err != nil {
				return DrainBudgetSecondsExhausted
			}
			drainedBytes += int64(len(res.event.Data))
			if budgets.MaxBytes > 0 && drainedBytes > budgets.MaxBytes {
				return DrainBudgetBytesExhausted
			}
			// 用传入的 adapter 解析 drain 阶段的 usage，保证与主流水线一致
			if adapter != nil {
				canonicalEvents, _, err := adapter.ProviderEventToCanonicalEvents(ctx, res.event.Data, upstreamState)
				if err == nil {
					for _, canonical := range canonicalEvents {
						if usage, ok := canonicalUsage(canonical); ok {
							acc.Update(UsageSourcePartial, usage)
						}
					}
				}
			}
			if !budgets.MaxEstimatedCost.IsZero() && f.CostEstimator != nil {
				if f.CostEstimator(drainedBytes, *acc).Cmp(budgets.MaxEstimatedCost) >= 0 {
					return DrainBudgetCostExhausted
				}
			}
		}
	}
}

func (f *StreamForwarder) clientChunks(ctx context.Context, canonical any, state any, fallback SSEEvent) ([][]byte, error) {
	if f.ClientAdapter == nil {
		return [][]byte{rawSSE(fallback)}, nil
	}
	chunks, _, err := f.ClientAdapter.CanonicalEventToClientChunk(ctx, canonical, state)
	return chunks, err
}

func (f *StreamForwarder) finishDraft(d UsageRecordDraft, acc UsageAccumulator, startedAt time.Time, err error) (UsageRecordDraft, error) {
	if d.EndClass == UnknownTermination && acc.Empty() {
		d.EndClass = AmbiguousUsage
		err = errors.Join(err, ErrAmbiguousUsage)
	}
	d.TokensInput = acc.Usage.InputTokens
	d.TokensOutput = acc.Usage.OutputTokens
	if d.UsageSource == UsageSourceAmbiguous && acc.Source != "" {
		d.UsageSource = acc.Source
	}
	if d.EndClass != StreamEndGraceful && d.UsageSource == UsageSourceReported {
		d.UsageSource = UsageSourcePartial
	}
	if d.EndClass == UpstreamEOFNoTerminal && !acc.Empty() {
		d.UsageSource = UsageSourceInferred
	}
	if d.EndClass == AmbiguousUsage {
		d.UsageSource = UsageSourceAmbiguous
	}
	d.TotalDurationMillis = millisSince(startedAt)
	return d, err
}

// newUpstreamState 构造上游协议状态对象。
// 重构后：所有协议族均用 proto.UpstreamState{}；未来按需扩展。
func (f *StreamForwarder) newUpstreamState(req ForwardRequest) any {
	// 当前 proto.UpstreamState 对所有已注册协议族均适用。
	// 若未来 gemini / openai 需要专属状态，在此按 req.ProtocolFamily switch。
	return &proto.UpstreamState{}
}

func (f *StreamForwarder) effectiveDrainBudgets() DrainBudgets {
	b := f.DrainBudgets
	if b.MaxSeconds <= 0 {
		b.MaxSeconds = f.Timeouts.DrainMaxSeconds
	}
	if b.MaxSeconds <= 0 {
		b.MaxSeconds = 30 * time.Second
	}
	if b.MaxBytes <= 0 {
		b.MaxBytes = 1 << 20
	}
	return b
}

type scanResult struct {
	event SSEEvent
	err   error
}

func scanInto(ctx context.Context, r io.Reader, cap int, out chan<- scanResult) {
	defer close(out)
	for evt, err := range ScanSSEEvents(ctx, r, cap) {
		select {
		case out <- scanResult{event: evt, err: err}:
		case <-ctx.Done():
			return
		}
		if err != nil {
			return
		}
	}
}

func classifyScanError(err error) (StreamEndClass, error) {
	switch {
	case errors.Is(err, ErrScannerOverflow):
		return ResponseEventTooLarge, ErrScannerOverflow
	case errors.Is(err, context.Canceled):
		return OrchestratorCancel, err
	default:
		return UnknownTermination, err
	}
}

func canonicalUsage(v any) (proto.CanonicalUsage, bool) {
	evt, ok := v.(proto.CanonicalEvent)
	if !ok || evt.Usage == nil {
		return proto.CanonicalUsage{}, false
	}
	return *evt.Usage, true
}

func canonicalTerminal(v any) bool {
	evt, ok := v.(proto.CanonicalEvent)
	return ok && evt.Type == "message_stop"
}

func rawSSE(evt SSEEvent) []byte {
	if evt.Type == "" {
		return []byte(fmt.Sprintf("data: %s\n\n", evt.Data))
	}
	return []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", evt.Type, evt.Data))
}

func writeAndFlush(w http.ResponseWriter, b []byte) error {
	if _, err := w.Write(b); err != nil {
		return err
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

func newTimer(d time.Duration) *time.Timer {
	t := time.NewTimer(time.Hour)
	if !t.Stop() {
		<-t.C
	}
	if d > 0 {
		t.Reset(d)
	}
	return t
}

func stopTimer(t *time.Timer) {
	if t == nil {
		return
	}
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
}

func timerC(t *time.Timer) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

func millisSince(t time.Time) int64 {
	return time.Since(t).Milliseconds()
}
