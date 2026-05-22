// 注意：本文件是 PATCH 提案文件（_patch.go 后缀），不参与 Go build。
// 将此文件内容合并到 backend/internal/gateway/forwarder.go 后删除本文件。
package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/anthropic"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/gemini"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/openai"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// StreamingHopChainBuilder 允许测试或后续 provider endpoint 观测层替换 hop 链构造。
// nil 时使用 BuildHopChain，保持与 non-streaming ledger 相同的六跳形态。
type StreamingHopChainBuilder func(req ForwardRequest, providerEndpoint string, startedAt, completedAt time.Time) []proto.HopAttestation

// StreamForwarder 执行 F-GW-002 A-D 阶段的流式转发流水线。
//
// 变更说明（A1 atomic）：
//   - 加 Scanners 字段（StreamScannerRegistry），替代之前硬编码的
//     ScanSSEEvents 调用。Forward 按 ForwardRequest.ProtocolFamily 查询
//     scanner，把 wire-format 切帧职责从 forwarder 解耦出去。
//   - SSE 行为通过 SSEStreamScanner 保持等价（行为不变 refactor）。
//   - Bedrock binary EventStream 在 A2+A3 atomic 接入对应 scanner，
//     无需再动 forwarder 主流程。
//
// 历史变更说明（wire-up 重构）：
//   - 新增 ProtocolAdapters 字段，替代原先硬编码的 anthropic.Adapter。
//   - 删除原 UpstreamAdapter 字段（由 ProtocolAdapters + ForwardRequest.ProtocolFamily 动态解析）。
//   - Forward 入口校验 ProtocolFamily 非空、ProtocolAdapters 非 nil。
type StreamForwarder struct {
	// ProtocolAdapters 是协议适配器注册表，Forward 按 ForwardRequest.ProtocolFamily 查询。
	// 必须非 nil；若为 nil，Forward 将立即返回错误。
	ProtocolAdapters ProtocolAdapterRegistry

	// Scanners 是 wire-format scanner 注册表，按 ForwardRequest.ProtocolFamily
	// 查询 StreamScanner。必须非 nil（A1 之后）；nil 时 Forward 立即返回错误，
	// 避免静默回落到 SSE 把 binary 流切碎。
	Scanners StreamScannerRegistry

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

	// AuditLedger / Signer 是 T12 streaming trust-chain ledger 的可选依赖。
	// 两者任一缺失时 graceful skip，并通过 LedgerWarning 记录一次 loss。
	AuditLedger auditledger.Ledger
	Signer      *sign.Signer

	// HopChainBuilder 默认走 BuildHopChain；LedgerCallback 在 ledger entry
	// 生成后、首个 SSE chunk 写出前触发，供 HTTP handler 写 X-HUAKAI-* 头。
	HopChainBuilder StreamingHopChainBuilder
	LedgerCallback  func(entryID, sigFingerprint string)
	LedgerWarning   func(code, reason string)
}

// ErrNilStreamScannerRegistry 表示 StreamForwarder.Scanners 未注入。
// 与 ErrNilProtocolAdapterRegistry 同形态：fail-loud，禁止静默 fallback。
var ErrNilStreamScannerRegistry = errors.New("gateway: StreamForwarder.Scanners 未注入")

const (
	streamProtocolErrorCode    = "upstream_error"
	streamProtocolErrorMessage = "upstream returned an error"
)

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

	// --- 入口校验：Scanners 注册表必须已注入（A1 之后强制） ---
	// 不允许 nil fallback 到 SSE — 那样 Bedrock binary 流会被切碎，
	// 不如 fail-loud 让启动期 misconfig 立刻暴露。
	if f.Scanners == nil {
		return UsageRecordDraft{}, ErrNilStreamScannerRegistry
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

	// --- 按请求的 ProtocolFamily 解析 wire-format scanner；不 fallback 到 SSE ---
	scanner, err := f.Scanners.For(req.ProtocolFamily)
	if err != nil {
		return UsageRecordDraft{}, err
	}

	start := time.Now()
	ledgerWriter := newStreamingLedgerHeaderWriter(clientWriter, func(completedAt time.Time) {
		f.emitStreamingLedger(ctx, req, "", start, completedAt)
	})
	clientWriter = ledgerWriter
	finish := func(d UsageRecordDraft, acc UsageAccumulator, err error) (UsageRecordDraft, error) {
		ledgerWriter.ensureLedger(time.Now())
		return f.finishDraft(d, acc, start, err)
	}
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
	go scanInto(upstreamCtx, scanner, upstreamReader, f.ScannerBufferCap, events)

	upstreamState := f.newUpstreamState(req)
	clientState := f.newClientState()
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
					if err := f.emitFinalUpstreamEvents(ctx, adapter, upstreamState, clientWriter, clientState, &acc, req); err != nil {
						if errors.Is(err, ErrClientDisconnect) {
							draft.EndClass = ClientDisconnect
						} else {
							draft.EndClass = UnknownTermination
						}
						return finish(draft, acc, err)
					}
				}
				if err := f.finalizeClientStream(ctx, clientWriter, clientState); err != nil {
					if errors.Is(err, ErrClientDisconnect) {
						draft.EndClass = ClientDisconnect
					} else {
						draft.EndClass = UnknownTermination
					}
					return finish(draft, acc, err)
				}
				return finish(draft, acc, nil)
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
				seen, wrote, delivered, err := f.handleEventWithAdapter(upstreamCtx, adapter, res.event, clientWriter, upstreamState, clientState, &acc, req)
				terminalSeen = terminalSeen || seen
				if delivered > 0 {
					acc.DeliveredChunkCount += delivered
				}
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
		return finish(draft, acc, endErr)
	}
}

// BufferedResponse 将 canonical buffered envelope 转回客户端协议响应。
// 它只服务非 streaming 路径；Forward 的 streaming 热路径不调用此方法。
func (f *StreamForwarder) BufferedResponse(ctx context.Context, canonical *proto.HCSF) ([]byte, error) {
	if f.ClientAdapter == nil {
		return nil, errors.New("gateway: ClientAdapter 未注入")
	}
	body, _, err := f.ClientAdapter.CanonicalToClientResponse(ctx, canonical)
	return body, err
}

// handleEventWithAdapter 使用调用方已解析的 adapter 处理单个 SSE 事件。
// 原 handleEvent 被替换为此方法，消除对 f.UpstreamAdapter 的依赖。
//
// 旁路 adapter 的特殊事件类型：
//   - evt.Type == "error" — protocol-level error 帧（如 Bedrock exception
//     scanner 在 yield ErrBedrockException 前 emit 的 error SSEEvent）。
//     这些 payload 不是 model 事件，喂给 adapter 会触发 JSON 解析失败；
//     客户端只接收 canonical public error，raw payload 进内部日志。
func (f *StreamForwarder) handleEventWithAdapter(
	ctx context.Context,
	adapter proto.UpstreamAdapter,
	evt SSEEvent,
	w http.ResponseWriter,
	upstreamState any,
	clientState any,
	acc *UsageAccumulator,
	req ForwardRequest,
) (bool, bool, int64, error) {
	terminalSeen := evt.Type == "message_stop" || string(evt.Data) == "[DONE]"

	if evt.Type == "error" {
		clienterr.LogInternal(ctx, req.RequestID, streamProtocolErrorCode, fmt.Errorf("upstream stream error event: %s", evt.Data))
		if err := writeAndFlush(w, canonicalStreamErrorSSE()); err != nil {
			return terminalSeen, false, 0, ErrClientDisconnect
		}
		return terminalSeen, true, 0, nil
	}

	// adapter 为 nil 时透传原始 SSE（保留既有 nil-adapter 行为）
	if adapter == nil {
		if err := writeAndFlush(w, rawSSE(evt)); err != nil {
			return terminalSeen, false, 0, ErrClientDisconnect
		}
		return terminalSeen, true, 1, nil
	}

	canonicalEvents, _, err := adapter.ProviderEventToCanonicalEvents(ctx, evt.Data, upstreamState)
	if err != nil {
		return terminalSeen, false, 0, err
	}
	annotateForwardHopChainEvents(canonicalEvents, req)
	wrote := false
	var delivered int64
	for _, canonical := range canonicalEvents {
		eventDelivered := canonicalDeliveredChunks(canonical)
		if usage, ok := canonicalUsage(canonical); ok {
			acc.Update(UsageSourceReported, usage)
		}
		if canonicalTerminal(canonical) {
			terminalSeen = true
			acc.Freeze()
		}
		chunks, err := f.clientChunks(ctx, canonical, clientState, evt)
		if err != nil {
			return terminalSeen, wrote, delivered, err
		}
		wroteEvent := false
		for _, chunk := range chunks {
			if len(chunk) == 0 {
				continue
			}
			if err := writeAndFlush(w, chunk); err != nil {
				return terminalSeen, wrote, delivered, ErrClientDisconnect
			}
			wrote = true
			wroteEvent = true
		}
		if wroteEvent && eventDelivered > 0 {
			delivered += eventDelivered
		}
	}
	return terminalSeen, wrote, delivered, nil
}

// finalizeClientStream 在上游 reader 结束后调用 client adapter 收尾 hook。
// nil ClientAdapter 保留 raw passthrough：不合成任何客户端尾块。
func (f *StreamForwarder) finalizeClientStream(ctx context.Context, w http.ResponseWriter, state any) error {
	if f.ClientAdapter == nil {
		return nil
	}
	chunks, err := f.ClientAdapter.FinalizeClientStream(ctx, state)
	if err != nil {
		return err
	}
	for _, chunk := range chunks {
		if len(chunk) == 0 {
			continue
		}
		if err := writeAndFlush(w, chunk); err != nil {
			return ErrClientDisconnect
		}
	}
	return nil
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
	d.DeliveredTokenCount = acc.DeliveredTokenCount()
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
	if d.StreamTerminatedReason == "" {
		d.StreamTerminatedReason = streamTerminatedReason(d.EndClass, d.DeliveredTokenCount)
	}
	d.TotalDurationMillis = millisSince(startedAt)
	return d, err
}

// newUpstreamState 构造上游协议状态对象。
//
// 修复 (sonnet F3 HIGH): 之前一律返回 *anthropic.UpstreamState — 但 openai.Adapter
// 与 gemini.Adapter 在 ProviderEventToCanonicalEvents 内 type-assert 到
// 各自的 state 类型, OpenAI/Gemini 流过来 type assertion 失败直接报错。
// 当前按 ProtocolAdapters 注册的 adapter 实际类型选 state 类型.
//
// AccountID 注入: Track P per-account cache metrics 需要 adapter 知道
// 当前选定的 provider_account_id, 终态 ObserveByAccount 时分账号累计.
//
// PrefixHash 注入 (PASR-lite A4): 复用 ForwardRequest.SessionHash 作 prefix
// hash, 终态调 ObserveByAccountWithPrefix 让 PASR-lite observer 把
// (acc, prefix, creation, read) 反馈到 PrefixSegment.HasCacheBitmap。
func (f *StreamForwarder) newUpstreamState(req ForwardRequest) any {
	// M5b: TenantID 透传 — observer 用 (TenantID, PrefixHash) 查段防跨租户混选
	if f.ProtocolAdapters != nil {
		if adapter, err := f.ProtocolAdapters.For(req.ProtocolFamily); err == nil {
			switch adapter.(type) {
			case *openai.Adapter:
				return &openai.UpstreamState{TenantID: req.TenantID, AccountID: req.AccountID, PrefixHash: req.SessionHash}
			case *gemini.Adapter:
				return &gemini.UpstreamState{TenantID: req.TenantID, AccountID: req.AccountID, PrefixHash: req.SessionHash}
			}
		}
	}
	// fallthrough: Anthropic / Bedrock-on-Anthropic / 其它都用 UpstreamState
	return &anthropic.UpstreamState{TenantID: req.TenantID, AccountID: req.AccountID, PrefixHash: req.SessionHash}
}

// newClientState 按 client adapter 的具体协议创建 per-stream 状态。
// 未注入 ClientAdapter 时返回 nil，让 raw SSE passthrough 行为保持原样。
func (f *StreamForwarder) newClientState() any {
	switch f.ClientAdapter.(type) {
	case *proto.AnthropicMessagesClient:
		return proto.NewAnthropicMessagesStreamState()
	case *proto.OpenAIChatClient:
		return proto.NewOpenAIChatStreamState()
	case *proto.OpenAIResponsesClient:
		return proto.NewOpenAIResponsesStreamState()
	default:
		return nil
	}
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

// scanInto 把 StreamScanner 切出的事件流送到 channel。A1 之前直接 hard-call
// ScanSSEEvents；现在通过 scanner 抽象，由调用方决定 wire format。
func scanInto(ctx context.Context, scanner StreamScanner, r io.Reader, cap int, out chan<- scanResult) {
	defer close(out)
	for evt, err := range scanner.Scan(ctx, r, cap) {
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
	case errors.Is(err, ErrBedrockException), errors.Is(err, io.ErrUnexpectedEOF):
		return UpstreamError5xx, err
	case errors.Is(err, context.Canceled):
		return OrchestratorCancel, err
	case isNetworkReadError(err):
		return UpstreamError5xx, err
	default:
		return UnknownTermination, err
	}
}

func isNetworkReadError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

func canonicalDeliveredChunks(v any) int64 {
	evt, ok := v.(proto.CanonicalEvent)
	if !ok || evt.Delta == nil {
		return 0
	}
	switch evt.Delta.Type {
	case "text_delta", "tool_input_delta", "reasoning_delta":
		return 1
	default:
		return 0
	}
}

func streamTerminatedReason(endClass StreamEndClass, delivered int64) string {
	switch endClass {
	case ClientDisconnect:
		return "client_gone"
	case FirstTokenTimeout, InterEventTimeout, TotalStreamTimeout:
		return "upstream_timeout"
	case UpstreamError5xx:
		return "upstream_5xx"
	case StreamEndGraceful:
		return ""
	case UpstreamEOFNoTerminal, UnknownTermination:
		if delivered > 0 {
			return "upstream_5xx"
		}
		return "output_token_zero"
	case AmbiguousUsage:
		return "output_token_zero"
	default:
		if delivered > 0 {
			return "upstream_5xx"
		}
		return "output_token_zero"
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

func canonicalStreamErrorSSE() []byte {
	return []byte(`event: error
data: {"error":{"code":"` + streamProtocolErrorCode + `","message":"` + streamProtocolErrorMessage + `"}}

`)
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

type streamingLedgerHeaderWriter struct {
	http.ResponseWriter
	before func(time.Time)
	done   bool
}

func newStreamingLedgerHeaderWriter(w http.ResponseWriter, before func(time.Time)) *streamingLedgerHeaderWriter {
	return &streamingLedgerHeaderWriter{ResponseWriter: w, before: before}
}

func (w *streamingLedgerHeaderWriter) ensureLedger(completedAt time.Time) {
	if w == nil || w.done {
		return
	}
	w.done = true
	if completedAt.IsZero() {
		completedAt = time.Now()
	}
	if w.before != nil {
		w.before(completedAt)
	}
}

func (w *streamingLedgerHeaderWriter) WriteHeader(statusCode int) {
	w.ensureLedger(time.Now())
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *streamingLedgerHeaderWriter) Write(b []byte) (int, error) {
	w.ensureLedger(time.Now())
	return w.ResponseWriter.Write(b)
}

func (w *streamingLedgerHeaderWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (f *StreamForwarder) emitStreamingLedger(ctx context.Context, req ForwardRequest, providerEndpoint string, startedAt, completedAt time.Time) {
	if f.AuditLedger == nil {
		f.warnLedgerLoss("audit_ledger_not_configured", "streaming trust-chain ledger skipped because audit ledger is nil")
		return
	}
	switch f.AuditLedger.(type) {
	case auditledger.NoopLedger, *auditledger.NoopLedger:
		f.warnLedgerLoss("audit_ledger_noop", "streaming trust-chain ledger skipped because audit ledger is noop")
		return
	}
	if f.Signer == nil {
		f.warnLedgerLoss("audit_signer_not_configured", "streaming trust-chain ledger skipped because signer is nil")
		return
	}
	requestID := req.RequestID
	if requestID == "" {
		requestID = uuid.NewString()
	}
	builder := f.HopChainBuilder
	if builder == nil {
		builder = BuildHopChain
	}
	entry := auditledger.LedgerEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		RequestID: requestID,
		TenantID:  req.TenantID,
		HopChain:  builder(req, providerEndpoint, startedAt, completedAt),
	}
	prepared, err := auditledger.PrepareEntry(ctx, entry)
	if err != nil {
		f.warnLedgerLoss("audit_ledger_prepare_failed", err.Error())
		return
	}
	appended, err := f.AuditLedger.Append(ctx, prepared)
	if err != nil {
		f.warnLedgerLoss("audit_ledger_append_failed", err.Error())
		return
	}
	if f.LedgerCallback != nil {
		f.LedgerCallback(appended.LedgerID, appended.PubkeyFingerprint)
	}
}

func (f *StreamForwarder) warnLedgerLoss(code, reason string) {
	if f != nil && f.LedgerWarning != nil {
		f.LedgerWarning(code, reason)
	}
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
