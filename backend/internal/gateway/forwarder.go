// 流式 forwarder:SSE 上游转发(超时/取消/审计)。本文件参与 Go build。
package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway/streamdelivery"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/anthropic"
	protodify "github.com/BloomingProsperity/HUAKAI/internal/proto/dify"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/gemini"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/geminicodeassist"
	protoollama "github.com/BloomingProsperity/HUAKAI/internal/proto/ollama"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/openai"
	"github.com/BloomingProsperity/HUAKAI/internal/redact"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/BloomingProsperity/HUAKAI/internal/tokencheck"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// StreamingHopChainBuilder 允许测试或后续 provider endpoint 观测层替换 hop 链构造。
// nil 时使用 BuildHopChain，保持与 non-streaming ledger 相同的六跳形态。
type StreamingHopChainBuilder func(req ForwardRequest, providerEndpoint string, startedAt, completedAt time.Time) []proto.HopAttestation

// StreamForwarder 执行流式扫描、协议转换、交付判定、审计与用量汇总。
type StreamForwarder struct {
	// ProtocolAdapters 是协议适配器注册表，Forward 按 ForwardRequest.ProtocolFamily 查询。
	// 必须非 nil；若为 nil，Forward 将立即返回错误。
	ProtocolAdapters ProtocolAdapterRegistry

	// Scanners 是线格式 scanner 注册表，按 ForwardRequest.ProtocolFamily
	// 查询 StreamScanner。必须非 nil（A1 之后）；nil 时 Forward 立即返回错误，
	// 避免静默回落到 SSE 把 binary 流切碎。
	Scanners StreamScannerRegistry

	// ClientAdapter 将 canonical event 转换为客户端协议块（可选）。
	// 若为 nil，则透传原始 SSE 给客户端。
	ClientAdapter proto.ClientAdapter
	// ForceOpenAIChatFormat 为可选开启项,会在面向客户端的 SSE data 帧上补齐
	// OpenAI Chat chunk 必需的键。默认 false,保持原有的透传行为。
	ForceOpenAIChatFormat bool

	Timeouts         TimeoutConfig
	ScannerBufferCap int
	DrainBudgets     DrainBudgets

	// CostEstimator 根据已 drain 字节数 + 累加器状态估算费用。
	// 当 DrainBudgets.MaxEstimatedCost > 0 且估算值超限时，drain 以 DrainBudgetCostExhausted 退出。
	// nil 表示禁用基于费用的 drain 退出。
	CostEstimator func(drainedBytes int64, acc UsageAccumulator) decimal.Decimal

	// AuditLedger / Signer 是 T12 streaming trust-chain ledger 的可选依赖。
	// 两者任一缺失时温和跳过，并通过 LedgerWarning 记录一次 loss。
	AuditLedger    auditledger.Ledger
	AuditLedgerDLQ auditledger.DLQEnqueuer
	Signer         *sign.Signer

	// HopChainBuilder 默认走 BuildHopChain；LedgerCallback 在 ledger entry
	// 生成后、首个 SSE chunk 写出前触发，供 HTTP handler 写 X-HUAKAI-* 头。
	HopChainBuilder StreamingHopChainBuilder
	LedgerCallback  func(auditledger.AuditLedgerResult)
	LedgerWarning   func(code, reason string)
	// BeforeFirstBusinessFrame 在首个业务帧写出前调用一次。返回错误时禁止
	// 写出业务字节，调用方据此释放预留且不得重放已成功的上游请求。
	BeforeFirstBusinessFrame func(time.Time) error
}

// ErrNilStreamScannerRegistry 表示 StreamForwarder.Scanners 未注入。
// 与 ErrNilProtocolAdapterRegistry 同形态：显式失败，禁止静默回退。
var ErrNilStreamScannerRegistry = errors.New("gateway: StreamForwarder.Scanners 未注入")

const (
	streamProtocolErrorCode    = "upstream_error"
	streamProtocolErrorMessage = "upstream returned an error"
)

// Forward 执行 F-GW-002 Phase A 扫描、Phase B 处理、Phase C 分类、
// Phase C-bis drain，并返回 Phase D draft。
//
// 入口按 adapter、scanner、协议族顺序校验，配置缺失时显式失败。
func (f *StreamForwarder) Forward(ctx context.Context, upstreamReader io.Reader, clientWriter http.ResponseWriter, req ForwardRequest) (UsageRecordDraft, error) {
	// --- 入口校验：ProtocolAdapters 注册表必须已注入 ---
	if f.ProtocolAdapters == nil {
		return UsageRecordDraft{}, ErrNilProtocolAdapterRegistry
	}

	// --- 入口校验：Scanners 注册表必须已注入（A1 之后强制） ---
	// 不允许 nil 回退到 SSE — 那样 Bedrock binary 流会被切碎，
	// 不如显式失败，让启动期配置错误立刻暴露。
	if f.Scanners == nil {
		return UsageRecordDraft{}, ErrNilStreamScannerRegistry
	}

	// --- 入口校验：ProtocolFamily 不得为空，调用方必须明确指定协议族 ---
	if req.ProtocolFamily == "" {
		return UsageRecordDraft{}, fmt.Errorf("%w: ProtocolFamily 未指定", ErrUnknownProtocolFamily)
	}

	// --- 按请求的 ProtocolFamily 解析 upstream adapter；不回退到默认 ---
	adapter, err := f.ProtocolAdapters.For(req.ProtocolFamily)
	if err != nil {
		return UsageRecordDraft{}, err
	}

	// --- 按请求的 ProtocolFamily 解析线格式 scanner；不回退到 SSE ---
	scanner, err := f.Scanners.For(req.ProtocolFamily)
	if err != nil {
		return UsageRecordDraft{}, err
	}

	start := time.Now()
	ledgerWriter := newStreamingLedgerHeaderWriter(clientWriter, func(completedAt time.Time) {
		f.emitStreamingLedger(ctx, req, "", start, completedAt)
	})
	clientWriter = ledgerWriter
	var businessFrameDelivered bool
	var beforeBusinessFrameOnce sync.Once
	var beforeBusinessFrameErr error
	beforeBusinessFrame := func() error {
		beforeBusinessFrameOnce.Do(func() {
			if f.BeforeFirstBusinessFrame != nil {
				beforeBusinessFrameErr = f.BeforeFirstBusinessFrame(time.Now().UTC())
			}
		})
		return beforeBusinessFrameErr
	}
	finish := func(d UsageRecordDraft, acc UsageAccumulator, err error) (UsageRecordDraft, error) {
		d.BusinessFrameDelivered = businessFrameDelivered
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
	var keepaliveCommitted bool   // 仅心跳已向客户端提交 200、尚无真实内容(用于错误收尾时补发显式 error 事件)
	var terminalFrameWritten bool // 已写过协议终止 error 帧(上游主动 error 帧),防终止收尾时双写
	var endErr error

	totalTimer := newTimer(f.Timeouts.TotalStreamTimeout)
	firstTimer := newTimer(f.Timeouts.FirstTokenTimeout)
	interTimer := newTimer(0)
	keepaliveTimer := newTimer(f.Timeouts.KeepAliveInterval) // 0 = 关闭(永不触发)
	// 用闭包在 return 时按变量"当前值"停表:interTimer / keepaliveTimer 会在循环里被 newTimer 重新赋值,
	// 若用 `defer stopTimer(interTimer)` 形式会捕获最初的 timer 值,导致重新赋值后的活动 timer 在返回后
	// 仍存活到下次触发——高频短流会累积一个 keepalive 间隔的悬挂 timer。闭包按引用捕获变量,
	// 停的是返回时刻真正在跑的那个 timer。
	defer func() {
		stopTimer(totalTimer)
		stopTimer(firstTimer)
		stopTimer(interTimer)
		stopTimer(keepaliveTimer)
	}()

	for {
		select {
		case <-ctx.Done():
			draft.EndClass, endErr = OrchestratorCancel, ctx.Err()
		case <-timerC(totalTimer):
			draft.EndClass, endErr = TotalStreamTimeout, ErrTotalStreamTimeout
		case <-timerC(firstTimer):
			draft.EndClass, endErr = FirstTokenTimeout, ErrFirstTokenTimeout
		case <-timerC(interTimer):
			// 上游在交付首事件后静默过久(稀疏/卡死)→ inter-event 超时放弃。心跳 case(见下)不重置
			// interTimer,因此保活心跳不会掩盖真正的上游停滞:心跳只维持连接,interTimer 照常计到点。
			draft.EndClass, endErr = InterEventTimeout, ErrInterEventTimeout
		case <-timerC(keepaliveTimer):
			// CF/长跑保活:长 TTFT 或稀疏 token 间隙时向客户端发 SSE 注释行心跳,避开 Cloudflare 等
			// 反代 ~100s 空闲超时断链。心跳是独立于 First/Inter/Total 的保活,不重置那些"上游静默即放弃"
			// 检测器(见上 interTimer case)。写失败 = 客户端/反代已断 → 按 ClientDisconnect 收尾。
			if err := streamdelivery.WriteAndFlush(clientWriter, sseKeepaliveComment); err != nil {
				draft.EndClass, endErr = ClientDisconnect, err
			} else {
				// 心跳一旦写出即向客户端提交了 HTTP 200 响应头+字节:此后无法再改 HTTP 状态码或换上游重试。
				// 记录"仅心跳已提交、尚无真实内容",以便首字节/inter/total 超时收尾时补发一个显式 SSE
				// error 事件,而非静默关闭一个只含 ": hk" 注释的空 200 流。
				if !firstEmitted {
					keepaliveCommitted = true
				}
				stopTimer(keepaliveTimer)
				keepaliveTimer = newTimer(f.Timeouts.KeepAliveInterval)
				continue
			}
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
					if err := f.emitFinalUpstreamEvents(ctx, adapter, upstreamState, clientWriter, clientState, &acc, req, beforeBusinessFrame); err != nil {
						if errors.Is(err, ErrClientDisconnect) {
							draft.EndClass = ClientDisconnect
						} else {
							draft.EndClass = UnknownTermination
						}
						return finish(draft, acc, err)
					}
				}
				if err := f.finalizeClientStream(ctx, clientWriter, clientState, beforeBusinessFrame); err != nil {
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
				stopTimer(keepaliveTimer)
				keepaliveTimer = newTimer(f.Timeouts.KeepAliveInterval) // 真事件来 → 重置心跳,只在空闲间隙发
				// 将解析好的 adapter 传入 handleEvent，避免重复 registry 查询
				seen, wrote, businessWritten, delivered, err := f.handleEventWithAdapter(upstreamCtx, adapter, res.event, clientWriter, upstreamState, clientState, &acc, req, beforeBusinessFrame)
				terminalSeen = terminalSeen || seen
				businessFrameDelivered = businessFrameDelivered || businessWritten
				// 上游主动 error 帧已由 handleEventWithAdapter 写出协议终止帧,记账防双写。
				if res.event.Type == "error" && wrote {
					terminalFrameWritten = true
				}
				if delivered > 0 {
					acc.DeliveredChunkCount += delivered
				}
				if wrote && !firstEmitted {
					firstEmitted = true
					draft.FirstTokenLatencyMillis = millisSince(start)
					// 首字绝对墙钟时刻:结算写入 usage_records.first_byte_at,供 TTFT=first_byte_at-requested_at。
					draft.FirstByteAt = time.Now().UTC()
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
		// 流以错误收尾(超时/scan/adapter 错误)时,只要已向客户端提交过 200 字节
		// (心跳或真实内容),上层"未交付→可重试/写 HTTP 错误状态"路径已不可走,必须在
		// 流内补发一个【按客户端协议正确的】终止 error 帧,否则严格 SDK(Codex CLI)只见
		// 静默 TCP EOF,报 "stream closed before response.completed",且无从区分完整/截断流。
		// (keepaliveCommitted||firstEmitted)=deliveryStarted;此前只覆盖"仅心跳零交付",
		// "已交付内容后出错"被漏掉(delta-mine #1)。ClientDisconnect 没人收、terminalSeen/
		// terminalFrameWritten 已有终止帧:均跳过。补帧只写 socket,不碰 settle 计费。
		if endErr != nil && draft.EndClass != ClientDisconnect &&
			(keepaliveCommitted || firstEmitted) && !terminalSeen && !terminalFrameWritten {
			for _, fr := range terminalErrorFrame(req.ClientProtocol) {
				_ = streamdelivery.WriteAndFlush(clientWriter, forceOpenAIChatSSEChunkFormat(fr, f.ForceOpenAIChatFormat))
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
//     scanner 在 yield ErrBedrockException 前发出的 error SSEEvent）。
//     这些 payload 不是 model 事件，喂给 adapter 会触发 JSON 解析失败；
//     客户端只接收 canonical public error，内部日志只记录脱敏摘要。
func (f *StreamForwarder) handleEventWithAdapter(
	ctx context.Context,
	adapter proto.UpstreamAdapter,
	evt SSEEvent,
	w http.ResponseWriter,
	upstreamState any,
	clientState any,
	acc *UsageAccumulator,
	req ForwardRequest,
	beforeBusinessFrame ...func() error,
) (bool, bool, bool, int64, error) {
	beforeWrite := func() error {
		if len(beforeBusinessFrame) > 0 && beforeBusinessFrame[0] != nil {
			return beforeBusinessFrame[0]()
		}
		return nil
	}
	terminalSeen := evt.Type == "message_stop" || string(evt.Data) == "[DONE]"

	if evt.Type == "error" {
		attrs := redact.SafePayloadLogAttrs(evt.Data)
		attrs["event_class"] = "stream_protocol_error"
		_ = privacy.LogSystem(ctx, privacy.SystemEvent{
			Severity:   privacy.SeverityError,
			Component:  "gateway.stream_forwarder",
			RequestID:  req.RequestID,
			ErrorClass: streamProtocolErrorCode,
			Attrs:      attrs,
		})
		// 上游主动 error 帧按客户端协议合成(canonicalStreamErrorSSE 是 anthropic-only
		// event: error,对 openai_chat 客户端只认 data: 行 → 被忽略=静默截断)。
		for _, fr := range terminalErrorFrame(req.ClientProtocol) {
			if err := streamdelivery.WriteAndFlush(w, forceOpenAIChatSSEChunkFormat(fr, f.ForceOpenAIChatFormat)); err != nil {
				return terminalSeen, false, false, 0, ErrClientDisconnect
			}
		}
		return terminalSeen, true, false, 0, nil
	}

	// adapter 为 nil 时透传原始 SSE（保留既有 nil-adapter 行为）
	if adapter == nil {
		if err := beforeWrite(); err != nil {
			return terminalSeen, false, false, 0, err
		}
		businessWritten, err := streamdelivery.WriteBusinessAndFlush(w, forceOpenAIChatSSEChunkFormat(rawSSE(evt), f.ForceOpenAIChatFormat))
		if err != nil {
			return terminalSeen, businessWritten, businessWritten, 0, ErrClientDisconnect
		}
		return terminalSeen, true, businessWritten, 1, nil
	}

	canonicalEvents, providerLosses, err := adapter.ProviderEventToCanonicalEvents(ctx, evt.Data, upstreamState)
	// 逐事件 provider→canonical 协议损失之前被丢弃(_);累积进 acc,
	// finishDraft 拷入 draft,settle 路径合并(item 4)。
	// 部分 adapter 把 loss 连同 error 一起返回(如 anthropic 未知事件 →
	// loss + ErrUnknownEventType, anthropic/sse.go:228),所以必须在 error 早返之前
	// 累积,否则 AmbiguousUsage settle 行对未知/畸形上游事件无证据。
	if len(providerLosses) > 0 {
		acc.StreamProtocolLoss = append(acc.StreamProtocolLoss, providerLosses...)
	}
	if err != nil {
		return terminalSeen, false, false, 0, err
	}
	annotateForwardHopChainEvents(canonicalEvents, req)
	wrote := false
	businessWritten := false
	var delivered int64
	// raw 直通(ClientAdapter==nil):把【原始上游帧】恰好转发一次,与 provider adapter 的
	// canonical 展开数无关。此前缺陷=主循环按 canonical 数逐次调 clientChunks,而 clientChunks
	// 在 nil ClientAdapter 下恒返回原始 evt 的 rawSSE,故 1 上游帧→N canonical 时同一帧被写 N 次
	// (客户端拿到重复内容,openai 兼容全族同协议流式 S0)。修法=nil 直通下 canonical 循环只做
	// usage/token 计费 tap 与交付记账,原始帧在循环外只转发一次;翻译路径(非 nil ClientAdapter)
	// 不变,仍每 canonical 产客户块(正确的 1→N 翻译)。
	rawPassthrough := f.ClientAdapter == nil
	for _, canonical := range canonicalEvents {
		eventDelivered := canonicalDeliveredChunks(canonical)
		if usage, ok := canonicalUsage(canonical); ok {
			acc.Update(UsageSourceReported, usage)
		}
		// 逐事件累加可见输出 token 估算(排除隐藏 reasoning delta),settle 时与 reported
		// OutputTokens 交叉校验。O(1) 内存、不滞留响应内容(流式交叉校验)。
		acc.EstimatedOutputTokens += canonicalVisibleEstimate(canonical)
		// 单独累加可见 reasoning 文本估算:Anthropic 扩展思考 / Gemini thought 把 thinking 以
		// ReasoningText 流出却不单列 ReasoningTokens,settle 时据此判断 reasoning-folding 是否可知,
		// 不可知则跳过交叉校验以免误报主路径 thinking 流。
		acc.EstimatedReasoningTokens += canonicalReasoningEstimate(canonical)
		if canonicalTerminal(canonical) {
			terminalSeen = true
			acc.Freeze()
		}
		if rawPassthrough {
			// 原始帧在循环外统一转发一次;此处只累计交付量(内容交付记账,与帧写几次无关)。
			delivered += eventDelivered
			continue
		}
		chunks, clientLosses, err := f.clientChunks(ctx, canonical, clientState, evt)
		if err != nil {
			return terminalSeen, wrote, businessWritten, delivered, err
		}
		if len(clientLosses) > 0 {
			acc.StreamProtocolLoss = append(acc.StreamProtocolLoss, clientLosses...)
		}
		wroteEvent := false
		for _, chunk := range chunks {
			if len(chunk) == 0 {
				continue
			}
			if err := beforeWrite(); err != nil {
				return terminalSeen, wrote, businessWritten, delivered, err
			}
			chunkWritten, err := streamdelivery.WriteBusinessAndFlush(w, chunk)
			wrote = wrote || chunkWritten
			businessWritten = businessWritten || chunkWritten
			if err != nil {
				return terminalSeen, wrote, businessWritten, delivered, ErrClientDisconnect
			}
			wroteEvent = true
		}
		if wroteEvent && eventDelivered > 0 {
			delivered += eventDelivered
		}
	}
	// raw 直通:原始上游帧只转发一次(仅当该帧确有 canonical 展开时;adapter 产 0 事件的帧
	// 此前就不透传,保持既有 drop 语义不变,不在本 S0 修复内改动)。
	if rawPassthrough && len(canonicalEvents) > 0 {
		if err := beforeWrite(); err != nil {
			return terminalSeen, wrote, businessWritten, delivered, err
		}
		frameWritten, err := streamdelivery.WriteBusinessAndFlush(w, forceOpenAIChatSSEChunkFormat(rawSSE(evt), f.ForceOpenAIChatFormat))
		wrote = wrote || frameWritten
		businessWritten = businessWritten || frameWritten
		if err != nil {
			return terminalSeen, wrote, businessWritten, delivered, ErrClientDisconnect
		}
	}
	return terminalSeen, wrote, businessWritten, delivered, nil
}

// finalizeClientStream 在上游 reader 结束后调用 client adapter 收尾 hook。
// nil ClientAdapter 保留 raw passthrough：不合成任何客户端尾块。
func (f *StreamForwarder) finalizeClientStream(ctx context.Context, w http.ResponseWriter, state any, beforeBusinessFrame ...func() error) error {
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
		if len(beforeBusinessFrame) > 0 && beforeBusinessFrame[0] != nil {
			if err := beforeBusinessFrame[0](); err != nil {
				return err
			}
		}
		if err := streamdelivery.WriteAndFlush(w, chunk); err != nil {
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
	deadline := time.NewTimer(budgets.MaxDuration)
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
				canonicalEvents, drainLosses, err := adapter.ProviderEventToCanonicalEvents(ctx, res.event.Data, upstreamState)
				// loss 可能连同 error 一起返回(anthropic/sse.go:228 未知事件 → loss +
				// ErrUnknownEventType),drain 阶段同样在 err 判断之前累积,否则 drain 期的
				// 未知/畸形事件证据丢失;usage 仅在 err==nil 时可信。
				if len(drainLosses) > 0 {
					acc.StreamProtocolLoss = append(acc.StreamProtocolLoss, drainLosses...)
				}
				if err == nil {
					for _, canonical := range canonicalEvents {
						if usage, ok := canonicalUsage(canonical); ok {
							acc.Update(UsageSourcePartial, usage)
						}
						// drain 阶段产生的可见输出也累加进估算:settle 比对的 reported
						// OutputTokens 已含 drain 期 usage(上方 acc.Update),估算须同步含
						// drain 期可见内容,否则断连后 drain 完成的长响应会因估算偏低被误判
						// 假 pending_reconciliation。
						acc.EstimatedOutputTokens += canonicalVisibleEstimate(canonical)
						acc.EstimatedReasoningTokens += canonicalReasoningEstimate(canonical)
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

func (f *StreamForwarder) clientChunks(ctx context.Context, canonical any, state any, fallback SSEEvent) ([][]byte, []proto.ProtocolLossEntry, error) {
	if f.ClientAdapter == nil {
		return [][]byte{forceOpenAIChatSSEChunkFormat(rawSSE(fallback), f.ForceOpenAIChatFormat)}, nil, nil
	}
	chunks, losses, err := f.ClientAdapter.CanonicalEventToClientChunk(ctx, canonical, state)
	if f.ForceOpenAIChatFormat {
		for i := range chunks {
			chunks[i] = forceOpenAIChatSSEChunkFormat(chunks[i], true)
		}
	}
	return chunks, losses, err
}

func forceOpenAIChatSSEChunkFormat(chunk []byte, force bool) []byte {
	if !force {
		return chunk
	}
	lines := bytes.Split(chunk, []byte("\n"))
	for i, line := range lines {
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}
		data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data: ")))
		formatted, err := proto.ForceOpenAIChatChunkFormat(data, true)
		if err != nil {
			continue
		}
		next := make([]byte, 0, len("data: ")+len(formatted))
		next = append(next, "data: "...)
		next = append(next, formatted...)
		lines[i] = next
	}
	return bytes.Join(lines, []byte("\n"))
}

func (f *StreamForwarder) finishDraft(d UsageRecordDraft, acc UsageAccumulator, startedAt time.Time, err error) (UsageRecordDraft, error) {
	if d.EndClass == UnknownTermination && acc.Empty() {
		d.EndClass = AmbiguousUsage
		err = errors.Join(err, ErrAmbiguousUsage)
	}
	d.TokensInput = acc.Usage.InputTokens
	d.TokensOutput = acc.Usage.OutputTokens
	d.CacheCreationTokens = acc.Usage.CacheCreationInputTokens
	if d.CacheCreationTokens == 0 {
		d.CacheCreationTokens = acc.Usage.CacheCreationInputTokens5m + acc.Usage.CacheCreationInputTokens1h
	}
	d.CacheCreation5mTokens = acc.Usage.CacheCreationInputTokens5m
	d.CacheCreation1hTokens = acc.Usage.CacheCreationInputTokens1h
	d.CacheReadTokens = acc.Usage.CacheReadInputTokens
	// 流式服务端工具调用次数(web_search/file_search/image_generation):累加器在 SSE
	// content_block_start 已按次累加进 acc.Usage,此处必须随其余用量字段一并落入 draft——
	// 否则 usageFromDraft 取到的工具计数恒 0,ApplyToolCallSurcharge 判 IsZero 直接跳过,
	// 流式工具调用的上游按次附加费永不向租户计收(我方付了上游成本却对客户收 $0)。
	d.WebSearchCalls = acc.Usage.WebSearchCalls
	d.FileSearchCalls = acc.Usage.FileSearchCalls
	d.ImageGenerationCalls = acc.Usage.ImageGenerationCalls
	d.DeliveredTokenCount = acc.DeliveredTokenCount()
	d.StreamProtocolLoss = acc.StreamProtocolLoss
	// 流式 token 交叉校验信号(审计-only,settle 时在 gatewayhttp 比对):隐藏 reasoning、
	// 逐事件累加的可见输出估算、以及可见 reasoning 文本估算(用于 folding-不可知时跳过)。
	d.ReasoningTokens = acc.Usage.ReasoningTokens
	d.EstimatedOutputTokens = acc.EstimatedOutputTokens
	d.EstimatedReasoningTokens = acc.EstimatedReasoningTokens
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
	// 流末最后事件绝对时刻,供 TPS=tokens_output/(last_event_at-first_byte_at)。仅在确有首字时成对
	// 落库(无首字=无输出流,两列都留 NULL,避免 first_byte NULL 而 last_event 非 NULL 的半截数据)。
	if !d.FirstByteAt.IsZero() {
		d.LastEventAt = time.Now().UTC()
	}
	return d, err
}

// newUpstreamState 构造上游协议状态对象。
//
// 之前一律返回 *anthropic.UpstreamState — 但 openai.Adapter
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
			case *openai.ResponsesAdapter:
				return &openai.ResponsesUpstreamState{}
			case *gemini.Adapter:
				return &gemini.UpstreamState{TenantID: req.TenantID, AccountID: req.AccountID, PrefixHash: req.SessionHash}
			case *geminicodeassist.Adapter:
				// 族集对称第 8 站(protosse.newUpstreamState 的孪生):
				// geminicodeassist.Adapter 委托内嵌 gemini.Adapter 解析,需 gemini
				// 的 state 类型;漏此 case 落 default=anthropic state,委托内
				// type-assert *gemini.UpstreamState 失败,整族流式不可恢复。
				return &gemini.UpstreamState{TenantID: req.TenantID, AccountID: req.AccountID, PrefixHash: req.SessionHash}
			case *protodify.Adapter:
				return &protodify.UpstreamState{TenantID: req.TenantID, AccountID: req.AccountID, PrefixHash: req.SessionHash}
			case *protoollama.Adapter:
				return &protoollama.UpstreamState{TenantID: req.TenantID, AccountID: req.AccountID, PrefixHash: req.SessionHash}
			}
		}
	}
	// fallthrough: Anthropic / Bedrock-on-Anthropic / 其它都用 UpstreamState
	return &anthropic.UpstreamState{TenantID: req.TenantID, AccountID: req.AccountID, RequestID: req.RequestID, PrefixHash: req.SessionHash}
}

// newClientState 委托 client adapter 创建每条流独占的协议状态。
// 未注入 ClientAdapter 时返回 nil，让 raw SSE passthrough 行为保持原样。
func (f *StreamForwarder) newClientState() any {
	if f.ClientAdapter == nil {
		return nil
	}
	return f.ClientAdapter.NewClientStreamState()
}

func (f *StreamForwarder) effectiveDrainBudgets() DrainBudgets {
	b := f.DrainBudgets
	if b.MaxDuration <= 0 {
		b.MaxDuration = f.Timeouts.DrainMax
	}
	if b.MaxDuration <= 0 {
		b.MaxDuration = 30 * time.Second
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
// ScanSSEEvents；现在通过 scanner 抽象，由调用方决定线格式。
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
	case OrchestratorCancel:
		return "orchestrator_cancelled"
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

// canonicalVisibleEstimate 估算单个 canonical 事件对**可见**输出 token 的贡献:可见文本
// delta(Delta.Text)+工具参数增量(Delta.PartialJSON)+初始可见文本与一次性 tool 参数
// (ContentBlock.Text / ContentBlock.Input)。**排除** Delta.ReasoningText —— 思考/隐藏 reasoning
// 由 canonicalReasoningEstimate 单独累加,交叉校验时按 reasoning-folding 是否可知分别处理
// (流式交叉校验)。
func canonicalVisibleEstimate(v any) int {
	evt, ok := v.(proto.CanonicalEvent)
	if !ok {
		return 0
	}
	total := 0
	if d := evt.Delta; d != nil {
		total += tokencheck.EstimateStreamDelta(d.Text, d.PartialJSON)
	}
	if cb := evt.ContentBlock; cb != nil {
		// 计入 ContentBlock.Input —— 部分 provider(如 Gemini)在 content_block_start 一次性
		// 发完整 tool call 参数而非 input_json_delta(PartialJSON)。用 delta 流式发参数的
		// provider(如 Anthropic)其 content_block_start.Input 为空,不会与 PartialJSON 重复计数。
		// 与非流估算器对 tool 节点的口径一致。
		total += tokencheck.EstimateStreamDelta(cb.Text, cb.Input)
	}
	return total
}

// canonicalReasoningEstimate 估算单个 canonical 事件的**可见 reasoning 文本**贡献
// (Delta.ReasoningText,以及罕见的 ContentBlock.Thinking)。与 canonicalVisibleEstimate 分开累加:
// Anthropic 扩展思考 / Gemini thought 把 thinking 以 ReasoningText 流出且不单列 ReasoningTokens,
// 而 reported OutputTokens 是否含 thinking 因 provider 而异(Anthropic 计入 output_tokens /
// Gemini 不计入 candidatesTokenCount),canonical 层无此 folding 信号。crossCheckAudit 据此在
// reasoning 文本流出但缺对应 ReasoningTokens 时跳过交叉校验,避免误报主路径 thinking 流
func canonicalReasoningEstimate(v any) int {
	evt, ok := v.(proto.CanonicalEvent)
	if !ok {
		return 0
	}
	total := 0
	if d := evt.Delta; d != nil {
		total += tokencheck.EstimateStreamDelta(d.ReasoningText, nil)
	}
	if cb := evt.ContentBlock; cb != nil {
		// 流式 Anthropic 当前不在 content_block_start 给 thinking(只 text/tool_use),
		// 但计入 ContentBlock.Thinking 以保持与可见估算对 ContentBlock 的对称口径,兼容未来
		// 在起始块直接携带思考文本的 provider。
		total += tokencheck.EstimateStreamDelta(cb.Thinking, nil)
	}
	return total
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

// terminalErrorFrame 按客户端协议合成协议正确的终止 error 帧(可能多帧)。
// 不走 client adapter(CanonicalEvent 无 error 字段、各 adapter 无 error case),也不
// 裸用 anthropic-only 的 canonicalStreamErrorSSE()。脱敏口径固定为 upstream_error,
// 不含任何上游原文。req.ClientProtocol 为空/未知 → openai_chat 形态(最广兼容)。
func terminalErrorFrame(clientProtocol string) [][]byte {
	code, msg := streamProtocolErrorCode, streamProtocolErrorMessage
	switch clientProtocol {
	case string(proto.ClientProtocolAnthropicMessages):
		// 复用既有 anthropic 风格(event: error),与历史字节一致。
		return [][]byte{canonicalStreamErrorSSE()}
	case string(proto.ClientProtocolOpenAIResponses):
		// Responses 全程命名事件;顶层 {type:error},不杜撰 response 包裹字段免 SDK 拒解析。
		return [][]byte{proto.EmitSSEEvent("error", []byte(fmt.Sprintf(`{"type":"error","code":%q,"message":%q}`, code, msg)))}
	case string(proto.ClientProtocolGemini):
		// Gemini streamGenerateContent 错误体走 data: 行,无具名 event,无 [DONE]。
		return [][]byte{proto.EmitSSEDataLine([]byte(fmt.Sprintf(`{"error":{"code":502,"status":"UNAVAILABLE","message":%q}}`, msg)))}
	default:
		// openai_chat(及空/未知):裸 data: 行(严格 chat SDK 只认 data:)。不发 data: [DONE]
		// ——[DONE] 是"成功完成"标记,出错时发它会让截断流伪装成正常完成;OpenAI 真实
		// 错误流也是发 error chunk 后流结束、不发 [DONE]。客户端见 error chunk 即抛 APIError。
		return [][]byte{
			proto.EmitSSEDataLine([]byte(fmt.Sprintf(`{"error":{"message":%q,"type":%q,"code":%q}}`, msg, code, code))),
		}
	}
}

func canonicalStreamErrorSSE() []byte {
	return []byte(`event: error
data: {"error":{"code":"` + streamProtocolErrorCode + `","message":"` + streamProtocolErrorMessage + `"}}

`)
}

// sseKeepaliveComment 是发给客户端的 SSE 注释行心跳。行首 ':' 的行是 SSE 注释,合规客户端会
// 忽略其内容,但这次写入会让反代/客户端看到连接仍活跃,从而避开 ~100s 空闲超时断链。
var sseKeepaliveComment = []byte(": hk\n\n")

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
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *streamingLedgerHeaderWriter) Write(b []byte) (int, error) {
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
	if auditledger.IsNoopLedger(f.AuditLedger) {
		f.warnLedgerLoss("audit_ledger_noop", "streaming trust-chain ledger skipped because audit ledger is noop")
		return
	}
	if f.Signer == nil {
		f.warnLedgerLoss("audit_signer_not_configured", "streaming trust-chain ledger skipped because signer is nil")
		return
	}
	ledgerCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
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
	prepared, err := auditledger.PrepareEntry(ledgerCtx, entry)
	if err != nil {
		f.warnLedgerLoss("audit_ledger_prepare_failed", err.Error())
		return
	}
	appended, err := f.AuditLedger.Append(ledgerCtx, prepared)
	if err != nil {
		if errors.Is(err, auditledger.ErrDuplicateRequestID) {
			f.warnLedgerLoss("audit_ledger_duplicate_request_id", err.Error())
			return
		}
		f.warnLedgerLoss("audit_ledger_append_failed", err.Error())
		dlqRef, dlqErr := auditledger.EnqueuePreparedEntryToDLQ(ledgerCtx, f.AuditLedgerDLQ, prepared, err)
		if dlqErr != nil {
			f.warnLedgerLoss("audit_ledger_dlq_enqueue_failed", dlqErr.Error())
			return
		}
		if f.LedgerCallback != nil {
			f.LedgerCallback(auditledger.DeferredLedgerResult(dlqRef))
		}
		return
	}
	if f.LedgerCallback != nil {
		f.LedgerCallback(auditledger.PersistedLedgerResult(appended))
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
