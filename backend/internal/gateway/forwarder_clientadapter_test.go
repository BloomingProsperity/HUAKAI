package gateway

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func TestForwarderClientAdapterFinalizeCalledAndChunksWritten(t *testing.T) {
	clientAdapter := &recordingForwarderClientAdapter{
		eventChunks: [][]byte{[]byte("data: event\n\n")},
		finalChunks: [][]byte{[]byte("data: final\n\n")},
	}
	f := newForwarder()
	f.ProtocolAdapters = &stubSingleAdapterRegistry{
		family: "anthropic_messages",
		adapter: &forwarderClientAdapterUpstreamStub{events: []any{
			&proto.CanonicalEvent{Type: "message_stop"},
		}},
	}
	f.ClientAdapter = clientAdapter

	rec := httptest.NewRecorder()
	draft, err := f.Forward(
		context.Background(),
		bytes.NewReader([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")),
		rec,
		anthropicForwardRequest(1, 100),
	)
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if draft.EndClass != StreamEndGraceful {
		t.Fatalf("EndClass=%q want %q", draft.EndClass, StreamEndGraceful)
	}
	if clientAdapter.finalizeCalls != 1 {
		t.Fatalf("FinalizeClientStream calls=%d want 1", clientAdapter.finalizeCalls)
	}
	got := rec.Body.String()
	if !strings.Contains(got, "data: event\n\n") {
		t.Fatalf("client event chunk not written; body=%q", got)
	}
	if !strings.Contains(got, "data: final\n\n") {
		t.Fatalf("finalize chunk not written; body=%q", got)
	}
}

// TestForwardAccumulatesPerEventProtocolLoss 守 S1-025-fu item 4:
// StreamForwarder 之前丢弃 ProviderEventToCanonicalEvents 与 CanonicalEventToClientChunk
// 的逐事件协议损失;现在累积进 acc 并经 finishDraft 落到 draft.StreamProtocolLoss。
func TestForwardAccumulatesPerEventProtocolLoss(t *testing.T) {
	clientAdapter := &recordingForwarderClientAdapter{
		eventChunks: [][]byte{[]byte("data: chunk\n\n")},
		finalChunks: [][]byte{[]byte("data: final\n\n")},
		eventLosses: []proto.ProtocolLossEntry{{Severity: proto.ProtocolLossWarning, Code: "client_event_loss_sentinel", Reason: "client chunk conversion"}},
	}
	f := newForwarder()
	f.ProtocolAdapters = &stubSingleAdapterRegistry{
		family: "anthropic_messages",
		adapter: &forwarderClientAdapterUpstreamStub{
			events: []any{&proto.CanonicalEvent{Type: "message_stop"}},
			losses: []proto.ProtocolLossEntry{{Severity: proto.ProtocolLossWarning, Code: "provider_event_loss_sentinel", Reason: "provider event conversion"}},
		},
	}
	f.ClientAdapter = clientAdapter

	rec := httptest.NewRecorder()
	draft, err := f.Forward(
		context.Background(),
		bytes.NewReader([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")),
		rec,
		anthropicForwardRequest(1, 100),
	)
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	// MUTATION: forwarder.go 还原 provider(handleEventWithAdapter)或 client(clientChunks)损失丢弃(_)
	// → draft.StreamProtocolLoss 缺对应 sentinel → RED。
	if !forwarderLossHasCode(draft.StreamProtocolLoss, "provider_event_loss_sentinel") {
		t.Fatalf("draft.StreamProtocolLoss missing provider sentinel: %+v", draft.StreamProtocolLoss)
	}
	if !forwarderLossHasCode(draft.StreamProtocolLoss, "client_event_loss_sentinel") {
		t.Fatalf("draft.StreamProtocolLoss missing client sentinel: %+v", draft.StreamProtocolLoss)
	}
}

func forwarderLossHasCode(losses []proto.ProtocolLossEntry, code string) bool {
	for _, l := range losses {
		if l.Code == code {
			return true
		}
	}
	return false
}

// TestCanonicalVisibleEstimateExcludesReasoning 守 S2-163-fu 流式交叉校验:逐事件可见输出
// 估算计入 Delta.Text(+ PartialJSON / ContentBlock.Text),但**排除** Delta.ReasoningText ——
// 隐藏推理已由 CanonicalUsage.ReasoningTokens 单列、交叉校验时从 reported 扣除。self-proving:
// 同一可见文本、其一附带大段 reasoning delta,断言两者估算相等。
func TestCanonicalVisibleEstimateExcludesReasoning(t *testing.T) {
	visibleText := "the visible answer streamed to the client token by token"
	visibleOnly := proto.CanonicalEvent{Type: "content_block_delta", Delta: &proto.CanonicalContentDelta{Text: visibleText}}
	withReasoning := proto.CanonicalEvent{Type: "content_block_delta", Delta: &proto.CanonicalContentDelta{
		Text:          visibleText,
		ReasoningText: strings.Repeat("hidden chain of thought reasoning tokens ", 50),
	}}

	gotVisible := canonicalVisibleEstimate(visibleOnly)
	if gotVisible <= 0 {
		t.Fatalf("visible estimate=%d want positive", gotVisible)
	}
	// MUTATION: canonicalVisibleEstimate 把 d.ReasoningText 也计进估算 → withReasoning > visible → RED。
	if got := canonicalVisibleEstimate(withReasoning); got != gotVisible {
		t.Fatalf("estimate with hidden reasoning=%d want == visible-only %d (reasoning must be excluded)", got, gotVisible)
	}
}

// TestCanonicalReasoningEstimateCountsOnlyReasoningText 守 S2-163-fu review R2 修复:
// canonicalReasoningEstimate **只**统计可见 reasoning 文本(Delta.ReasoningText),不计可见输出
// 文本(Delta.Text)—— 它与 canonicalVisibleEstimate 互补,供 crossCheckAudit 在 reasoning 文本
// 流出但缺 ReasoningTokens 时跳过校验。self-proving:reasoning-only delta 估算为正,而 visible-only
// delta 的 reasoning 估算必须为 0。
func TestCanonicalReasoningEstimateCountsOnlyReasoningText(t *testing.T) {
	reasoningOnly := proto.CanonicalEvent{Type: "content_block_delta", Delta: &proto.CanonicalContentDelta{
		ReasoningText: strings.Repeat("extended thinking chain of thought tokens ", 20),
	}}
	visibleOnly := proto.CanonicalEvent{Type: "content_block_delta", Delta: &proto.CanonicalContentDelta{
		Text: "the visible answer streamed to the client",
	}}

	if got := canonicalReasoningEstimate(reasoningOnly); got <= 0 {
		t.Fatalf("reasoning-only estimate=%d want positive (ReasoningText must count)", got)
	}
	// MUTATION: canonicalReasoningEstimate 把 d.Text 也计进 → visible-only 估算 >0 → RED。
	if got := canonicalReasoningEstimate(visibleOnly); got != 0 {
		t.Fatalf("visible-only reasoning estimate=%d want 0 (only ReasoningText counts)", got)
	}
}

// TestCanonicalVisibleEstimateCountsContentBlockInput 守 S2-163-fu review R2 finding 2:
// 部分 provider(如 Gemini)在 content_block_start 一次性发完整 tool call 参数(ContentBlock.Input)
// 而非 input_json_delta。估算须计入 cb.Input,否则 tool-only 流估算为 0 → CrossCheck Unknown 绕过审计。
func TestCanonicalVisibleEstimateCountsContentBlockInput(t *testing.T) {
	oneShotTool := proto.CanonicalEvent{Type: "content_block_start", ContentBlock: &proto.CanonicalContentBlock{
		Type:  "tool_use",
		Name:  "search",
		Input: []byte(`{"query":"weather in hangzhou","units":"metric","verbose":true}`),
	}}
	// MUTATION: canonicalVisibleEstimate 丢弃 cb.Input(传 nil)→ 0 → tool-only 流被 CrossCheck 判 Unknown → RED。
	if got := canonicalVisibleEstimate(oneShotTool); got <= 0 {
		t.Fatalf("one-shot tool content_block_start estimate=%d want positive (Input must count)", got)
	}
}

// TestDrainWithAdapterAccumulatesOutputEstimate 守 S2-163-fu review R2 finding 1:
// 客户端断连后 bounded drain 读完剩余流;drain 期产生的可见输出也须累加进估算,使其与
// reported OutputTokens(含 drain 期 usage)同步,否则断连后 drain 完成的长响应被误判假 pending。
func TestDrainWithAdapterAccumulatesOutputEstimate(t *testing.T) {
	adapter := &forwarderClientAdapterUpstreamStub{
		events: []any{proto.CanonicalEvent{Type: "content_block_delta", Delta: &proto.CanonicalContentDelta{
			Text: "drained visible completion text produced after the client disconnected",
		}}},
	}
	f := newForwarder()
	acc := &UsageAccumulator{}
	events := make(chan scanResult, 1)
	events <- scanResult{event: SSEEvent{Type: "content_block_delta", Data: []byte(`{"type":"content_block_delta"}`)}}
	close(events)

	f.drainWithAdapter(context.Background(), adapter, events, nil, acc)

	// MUTATION: drain 循环不累加 canonicalVisibleEstimate → acc.EstimatedOutputTokens==0 → RED。
	if acc.EstimatedOutputTokens <= 0 {
		t.Fatalf("acc.EstimatedOutputTokens=%d want positive (drained visible output must be estimated)", acc.EstimatedOutputTokens)
	}
}

// TestHandleEventWithAdapterAccumulatesLossOnErrorReturn 守 S1-025-fu review R1 finding 2:
// 部分上游 adapter 把 ProtocolLossEntry 连同 error 一起返回(anthropic/sse.go:228 未知事件
// → loss + ErrUnknownEventType)。handleEventWithAdapter 原先在 append providerLosses 之前
// 就 error 早返,证据丢失;现在累积先于 error 返回。
func TestHandleEventWithAdapterAccumulatesLossOnErrorReturn(t *testing.T) {
	adapter := &forwarderClientAdapterUpstreamStub{
		losses: []proto.ProtocolLossEntry{{Severity: proto.ProtocolLossWarning, Code: "provider_error_loss_sentinel", Reason: "unknown upstream event"}},
		err:    proto.ErrUnknownEventType,
	}
	f := newForwarder()
	acc := &UsageAccumulator{}
	rec := httptest.NewRecorder()

	_, _, _, err := f.handleEventWithAdapter(
		context.Background(),
		adapter,
		SSEEvent{Type: "unknown_event", Data: []byte(`{"type":"unknown_event"}`)},
		rec,
		nil,
		nil,
		acc,
		ForwardRequest{RequestID: "req-r1-provider"},
	)
	if err == nil {
		t.Fatal("expected adapter error to propagate")
	}
	// MUTATION: 把 providerLosses append 移回 error 早返之后 → acc.StreamProtocolLoss 为空 → RED。
	if !forwarderLossHasCode(acc.StreamProtocolLoss, "provider_error_loss_sentinel") {
		t.Fatalf("acc.StreamProtocolLoss missing sentinel after errored provider event: %+v", acc.StreamProtocolLoss)
	}
}

// TestDrainWithAdapterAccumulatesLossOnErrorReturn 守 finding 2 的 drain 镜像:
// drainWithAdapter 原先只在 err==nil 时 append drainLosses,drain 期未知/畸形事件
// (loss+error)证据丢失;现在累积不受 err 影响,usage 仍仅在 err==nil 时采信。
func TestDrainWithAdapterAccumulatesLossOnErrorReturn(t *testing.T) {
	adapter := &forwarderClientAdapterUpstreamStub{
		losses: []proto.ProtocolLossEntry{{Severity: proto.ProtocolLossWarning, Code: "drain_error_loss_sentinel", Reason: "unknown drain event"}},
		err:    proto.ErrUnknownEventType,
	}
	f := newForwarder()
	acc := &UsageAccumulator{}
	events := make(chan scanResult, 1)
	events <- scanResult{event: SSEEvent{Type: "unknown_event", Data: []byte(`{"type":"unknown_event"}`)}}
	close(events)

	f.drainWithAdapter(context.Background(), adapter, events, nil, acc)

	// MUTATION: 把 drainLosses append 移回 `if err == nil` 内 → acc.StreamProtocolLoss 为空 → RED。
	if !forwarderLossHasCode(acc.StreamProtocolLoss, "drain_error_loss_sentinel") {
		t.Fatalf("acc.StreamProtocolLoss missing drain sentinel after errored drain event: %+v", acc.StreamProtocolLoss)
	}
}

func TestForwarderClientStateInitializedByAdapterType(t *testing.T) {
	if got := (&StreamForwarder{ClientAdapter: &proto.AnthropicMessagesClient{}}).newClientState(); got == nil {
		t.Fatal("anthropic_messages client state is nil")
	} else if _, ok := got.(*proto.AnthropicMessagesStreamState); !ok {
		t.Fatalf("anthropic_messages client state type=%T", got)
	}

	if got := (&StreamForwarder{ClientAdapter: &proto.OpenAIChatClient{}}).newClientState(); got == nil {
		t.Fatal("openai_chat client state is nil")
	} else if _, ok := got.(*proto.OpenAIChatStreamState); !ok {
		t.Fatalf("openai_chat client state type=%T", got)
	}

	if got := (&StreamForwarder{ClientAdapter: &proto.OpenAIResponsesClient{}}).newClientState(); got == nil {
		t.Fatal("openai_responses client state is nil")
	} else if _, ok := got.(*proto.OpenAIResponsesStreamState); !ok {
		t.Fatalf("openai_responses client state type=%T", got)
	}

	if got := (&StreamForwarder{}).newClientState(); got != nil {
		t.Fatalf("nil ClientAdapter client state=%T want nil", got)
	}
}

func TestForwarderBufferedResponseHappyPath(t *testing.T) {
	clientAdapter := &recordingForwarderClientAdapter{
		bufferedBody: []byte(`{"ok":true}`),
	}
	f := &StreamForwarder{ClientAdapter: clientAdapter}
	canonical := proto.NewEmptyEnvelope()

	got, err := f.BufferedResponse(context.Background(), canonical)
	if err != nil {
		t.Fatalf("BufferedResponse: %v", err)
	}
	if string(got) != `{"ok":true}` {
		t.Fatalf("BufferedResponse body=%s", got)
	}
	if clientAdapter.bufferedCalls != 1 {
		t.Fatalf("CanonicalToClientResponse calls=%d want 1", clientAdapter.bufferedCalls)
	}
	if clientAdapter.bufferedCanonical != canonical {
		t.Fatalf("CanonicalToClientResponse received canonical=%p want %p", clientAdapter.bufferedCanonical, canonical)
	}
}

func TestForwarderNilClientAdapterFallbackRawPassthrough(t *testing.T) {
	f := newForwarder()
	f.ProtocolAdapters = &stubSingleAdapterRegistry{
		family: "anthropic_messages",
		adapter: &forwarderClientAdapterUpstreamStub{events: []any{
			&proto.CanonicalEvent{Type: "message_start"},
		}},
	}

	upstream := []byte("event: passthrough\ndata: {\"x\":1}\n\n")
	rec := httptest.NewRecorder()
	_, err := f.Forward(context.Background(), bytes.NewReader(upstream), rec, anthropicForwardRequest(1, 100))
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if got := rec.Body.String(); got != string(upstream) {
		t.Fatalf("raw passthrough body=%q want %q", got, upstream)
	}
}

func TestHandleEventWithAdapterSanitizesProtocolErrorWhenAdapterNil(t *testing.T) {
	assertProtocolErrorSanitized(t, nil)
}

func TestHandleEventWithAdapterSanitizesProtocolErrorBeforeAdapter(t *testing.T) {
	assertProtocolErrorSanitized(t, &forwarderClientAdapterUpstreamStub{
		err: errors.New("adapter must not receive protocol error"),
	})
}

func TestHandleEventWithAdapterKeepsBoundedSummaryForComplexProtocolError(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() {
		slog.SetDefault(prev)
	})

	const marker = "RAWPROMPT_SECRET_MARKER"
	payload := []byte(`{"message":"` + marker + ` upstream rate limited","detail":"` + marker + ` detail","details":"` + marker + ` details","error":"` + marker + ` error","reason":"` + marker + ` reason","status":429,"type":"rate_limit","retryable":true,"unknown1":"` + marker + ` a","unknown2":"` + marker + ` b","api_key":"sk-` + marker + `"}`)
	f := newForwarder()
	rec := httptest.NewRecorder()

	_, wrote, _, err := f.handleEventWithAdapter(
		context.Background(),
		nil,
		SSEEvent{Type: "error", Data: payload},
		rec,
		nil,
		nil,
		&UsageAccumulator{},
		ForwardRequest{RequestID: "req-complex-protocol-error"},
	)
	if err != nil {
		t.Fatalf("handleEventWithAdapter returned err=%v", err)
	}
	if !wrote {
		t.Fatal("canonical error frame was not written")
	}
	gotLog := logs.String()
	for _, want := range []string{"req-complex-protocol-error", "stream_protocol_error", "payload_bytes", "payload_summary_sha256_prefix", "payload_snippet"} {
		if !strings.Contains(gotLog, want) {
			t.Fatalf("complex protocol log missing %q: %s", want, gotLog)
		}
	}
	for _, forbidden := range []string{marker, "sk-", "privacy_guard_hit"} {
		if strings.Contains(gotLog, forbidden) {
			t.Fatalf("complex protocol log leaked or over-redacted %q: %s", forbidden, gotLog)
		}
	}
}

// forwarderClientAdapterUpstreamStub 只负责把 scanner 事件映射成测试指定的 canonical events。
type forwarderClientAdapterUpstreamStub struct {
	events []any
	losses []proto.ProtocolLossEntry
	err    error
}

func (s *forwarderClientAdapterUpstreamStub) CanonicalToProviderRequest(context.Context, *proto.HCSF) ([]byte, []proto.ProtocolLossEntry, error) {
	return nil, nil, errors.New("not implemented")
}

func (s *forwarderClientAdapterUpstreamStub) ProviderResponseToCanonical(context.Context, []byte) (*proto.HCSF, []proto.ProtocolLossEntry, error) {
	return nil, nil, errors.New("not implemented")
}

func (s *forwarderClientAdapterUpstreamStub) ProviderEventToCanonicalEvents(context.Context, any, any) ([]any, []proto.ProtocolLossEntry, error) {
	return s.events, s.losses, s.err
}

func (s *forwarderClientAdapterUpstreamStub) FinalizeUpstreamStream(context.Context, any) ([]any, error) {
	return nil, nil
}

// recordingForwarderClientAdapter 记录 forwarder 是否调用 client adapter hookpoint。
type recordingForwarderClientAdapter struct {
	eventChunks       [][]byte
	finalChunks       [][]byte
	bufferedBody      []byte
	eventLosses       []proto.ProtocolLossEntry
	eventCalls        int
	finalizeCalls     int
	bufferedCalls     int
	eventState        any
	finalizeState     any
	bufferedCanonical *proto.HCSF
}

func (a *recordingForwarderClientAdapter) RequestToCanonical(context.Context, []byte) (*proto.HCSF, []proto.ProtocolLossEntry, error) {
	return nil, nil, errors.New("not implemented")
}

func (a *recordingForwarderClientAdapter) CanonicalToClientResponse(_ context.Context, canonical *proto.HCSF) ([]byte, []proto.ProtocolLossEntry, error) {
	a.bufferedCalls++
	a.bufferedCanonical = canonical
	return a.bufferedBody, nil, nil
}

func (a *recordingForwarderClientAdapter) CanonicalEventToClientChunk(_ context.Context, _ any, state any) ([][]byte, []proto.ProtocolLossEntry, error) {
	a.eventCalls++
	a.eventState = state
	return a.eventChunks, a.eventLosses, nil
}

func (a *recordingForwarderClientAdapter) FinalizeClientStream(_ context.Context, state any) ([][]byte, error) {
	a.finalizeCalls++
	a.finalizeState = state
	return a.finalChunks, nil
}

func assertProtocolErrorSanitized(t *testing.T, adapter proto.UpstreamAdapter) {
	t.Helper()

	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() {
		slog.SetDefault(prev)
	})

	const marker = "RAWPROMPT_SECRET_MARKER"
	payload := sensitiveBedrockPayload(marker)
	f := newForwarder()
	rec := httptest.NewRecorder()
	terminalSeen, wrote, delivered, err := f.handleEventWithAdapter(
		context.Background(),
		adapter,
		SSEEvent{Type: "error", Data: []byte(payload)},
		rec,
		nil,
		nil,
		&UsageAccumulator{},
		ForwardRequest{RequestID: "req-c18"},
	)
	if err != nil {
		t.Fatalf("handleEventWithAdapter returned err=%v", err)
	}
	if terminalSeen {
		t.Fatalf("protocol error frame should not masquerade as terminal model event")
	}
	if !wrote {
		t.Fatalf("sanitized error frame was not written")
	}
	if delivered != 0 {
		t.Fatalf("protocol error frame delivered chunks=%d want 0", delivered)
	}
	body := rec.Body.String()
	if strings.Contains(body, marker) {
		t.Fatalf("client SSE leaked raw protocol error payload: %q", body)
	}
	if !strings.Contains(body, `"code":"upstream_error"`) {
		t.Fatalf("client SSE missing fixed upstream_error code: %q", body)
	}
	if strings.Count(body, "event: error") != 1 {
		t.Fatalf("client SSE should contain exactly one canonical error event, got %q", body)
	}
	gotLog := logs.String()
	assertSafePayloadSummary(t, gotLog, payload, marker)
	for _, want := range []string{"req-c18", "upstream_error", "stream_protocol_error"} {
		if !strings.Contains(gotLog, want) {
			t.Fatalf("internal log missing %q context: %s", want, gotLog)
		}
	}
	if strings.Contains(gotLog, "sk-") {
		t.Fatalf("internal log leaked fake token prefix: %s", gotLog)
	}
	if !strings.Contains(gotLog, "privacy.system") {
		t.Fatalf("internal log missing request/code context: %s", gotLog)
	}
}
