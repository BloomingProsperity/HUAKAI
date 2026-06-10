// 变更摘要：
//  1. newForwarder() 改为注入 BuildDefaultProtocolAdapterRegistry()，删除 UpstreamAdapter 字段。
//  2. 所有 ForwardRequest{} 构造补充 ProtocolFamily: "anthropic_messages"。
//  3. TestAT_GW_002_18 的 f.UpstreamAdapter 赋值改为通过 stubSingleAdapterRegistry 注入。
//  4. 新增 stubSingleAdapterRegistry 辅助函数，将任意 proto.UpstreamAdapter 包装成注册表。
//
// F-GW-002 contract tests AT-GW-002-01..19 against the StreamForwarder pipeline.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	protoanthropic "github.com/BloomingProsperity/HUAKAI/internal/proto/anthropic"
	protodify "github.com/BloomingProsperity/HUAKAI/internal/proto/dify"
	protoollamafwd "github.com/BloomingProsperity/HUAKAI/internal/proto/ollama"
	"github.com/BloomingProsperity/HUAKAI/internal/protosse"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/shopspring/decimal"
)

// =====================================================================
// Helpers
// =====================================================================

// sseBytes 将 Anthropic 风格的 SSE 事件序列化为 wire payload。
func sseBytes(events ...sseEvt) []byte {
	var b bytes.Buffer
	for _, e := range events {
		if e.typ != "" {
			fmt.Fprintf(&b, "event: %s\n", e.typ)
		}
		raw, _ := json.Marshal(e.payload)
		fmt.Fprintf(&b, "data: %s\n\n", raw)
	}
	return b.Bytes()
}

type sseEvt struct {
	typ     string
	payload map[string]any
}

type rstAfterOneScanner struct{}

func (rstAfterOneScanner) Scan(ctx context.Context, _ io.Reader, _ int) iter.Seq2[SSEEvent, error] {
	return func(yield func(SSEEvent, error) bool) {
		select {
		case <-ctx.Done():
			yield(SSEEvent{}, ctx.Err())
			return
		default:
		}
		if !yield(SSEEvent{
			Type: "content_block_delta",
			Data: []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"visible"}}`),
		}, nil) {
			return
		}
		yield(SSEEvent{}, io.ErrUnexpectedEOF)
	}
}

type delayedTerminalScanner struct {
	delay      time.Duration
	terminalAt time.Time
}

func (s *delayedTerminalScanner) Scan(ctx context.Context, _ io.Reader, _ int) iter.Seq2[SSEEvent, error] {
	return func(yield func(SSEEvent, error) bool) {
		if !yield(sseEventFromTestEvent(messageStart("msg-delayed-terminal")), nil) {
			return
		}
		if !yield(sseEventFromTestEvent(textDelta(0, "first")), nil) {
			return
		}
		timer := time.NewTimer(s.delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			yield(SSEEvent{}, ctx.Err())
			return
		case <-timer.C:
		}
		s.terminalAt = time.Now()
		yield(sseEventFromTestEvent(messageStop()), nil)
	}
}

func messageStart(id string) sseEvt {
	return sseEvt{typ: "message_start", payload: map[string]any{
		"type":    "message_start",
		"message": map[string]any{"id": id, "model": "claude-3-5-sonnet"},
	}}
}

func textDelta(idx int, text string) sseEvt {
	return sseEvt{typ: "content_block_delta", payload: map[string]any{
		"type":  "content_block_delta",
		"index": idx,
		"delta": map[string]any{"type": "text_delta", "text": text},
	}}
}

func messageDeltaWithUsage(stopReason string, in, out int) sseEvt {
	return sseEvt{typ: "message_delta", payload: map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": stopReason},
		"usage": map[string]any{"input_tokens": in, "output_tokens": out},
	}}
}

func messageStop() sseEvt {
	return sseEvt{typ: "message_stop", payload: map[string]any{"type": "message_stop"}}
}

func sseEventFromTestEvent(e sseEvt) SSEEvent {
	raw, _ := json.Marshal(e.payload)
	return SSEEvent{Type: e.typ, Data: raw}
}

// newForwarder 构造测试用 StreamForwarder。
// 重构后（A1）：除 ProtocolAdapters 之外注入 Scanners 默认注册表（全部 SSE）。
func newForwarder() *StreamForwarder {
	return &StreamForwarder{
		// 注入默认 protocol adapter 注册表
		ProtocolAdapters: BuildDefaultProtocolAdapterRegistry(),
		// 注入默认 stream scanner 注册表（19 个 family 全部 SSE，行为与 A1 之前等价）
		Scanners: BuildDefaultStreamScannerRegistry(),
		Timeouts: TimeoutConfig{
			FirstTokenTimeout:  500 * time.Millisecond,
			InterEventTimeout:  500 * time.Millisecond,
			TotalStreamTimeout: 5 * time.Second,
			DrainMaxSeconds:    100 * time.Millisecond,
		},
		ScannerBufferCap: 1 << 20,
	}
}

// stubSingleAdapterRegistry 将单个 proto.UpstreamAdapter 包装成 ProtocolAdapterRegistry。
// 仅用于测试场景：将任意 adapter 注入为指定 family（或所有 family）。
type stubSingleAdapterRegistry struct {
	family  string
	adapter proto.UpstreamAdapter
}

// For 返回注册的 adapter；当请求的 family 与注册的 family 不一致时仍返回（stub 行为）。
func (r *stubSingleAdapterRegistry) For(family string) (proto.UpstreamAdapter, error) {
	// stub 不做 family 校验，方便测试专注于 adapter 逻辑而非 registry 查询
	return r.adapter, nil
}

// anthropicForwardRequest 构造一个携带 "anthropic_messages" ProtocolFamily 的 ForwardRequest。
// 消除测试中 ProtocolFamily 重复字面量。
func anthropicForwardRequest(tenantID, accountID int64) ForwardRequest {
	return ForwardRequest{
		TenantID:       tenantID,
		AccountID:      accountID,
		ProtocolFamily: "anthropic_messages",
		ClientProtocol: "anthropic_messages",
	}
}

// =====================================================================
// Sub2API-inheritable scenarios
// =====================================================================

// AT-GW-002-01: 首个事件在 1s 内可被客户端观测到。
func TestAT_GW_002_01_FirstEventFlushObservable(t *testing.T) {
	upstream := sseBytes(
		messageStart("msg_1"),
		textDelta(0, "hello"),
		messageStop(),
	)
	rec := httptest.NewRecorder()
	f := newForwarder()
	t0 := time.Now()
	draft, err := f.Forward(context.Background(), bytes.NewReader(upstream), rec, anthropicForwardRequest(1, 100))
	elapsed := time.Since(t0)
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if elapsed > time.Second {
		t.Errorf("forwarder took %v; spec requires first-event flush within 1s", elapsed)
	}
	if rec.Body.Len() == 0 {
		t.Fatalf("no client output captured")
	}
	if draft.FirstTokenLatencyMillis < 0 {
		t.Errorf("first_token_latency_ms must be set; got %d", draft.FirstTokenLatencyMillis)
	}
}

// AT-GW-002-02: anthropic → canonical → chat 协议转换保留 usage。
func TestAT_GW_002_02_ProtocolTranslationPreservesUsage(t *testing.T) {
	upstream := sseBytes(
		messageStart("msg_2"),
		textDelta(0, "abc"),
		messageDeltaWithUsage("end_turn", 100, 250),
		messageStop(),
	)
	f := newForwarder()
	draft, err := f.Forward(context.Background(), bytes.NewReader(upstream), httptest.NewRecorder(), anthropicForwardRequest(1, 100))
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if draft.TokensInput != 100 || draft.TokensOutput != 250 {
		t.Fatalf("usage not preserved through translation: in=%d out=%d (want 100/250)", draft.TokensInput, draft.TokensOutput)
	}
	if draft.EndClass != StreamEndGraceful {
		t.Fatalf("graceful end expected; got %q", draft.EndClass)
	}
}

// AT-GW-002-06: 扫描器超大事件 → 类型化终态失败。
func TestAT_GW_002_06_ScannerOversizeTerminal(t *testing.T) {
	bigPayload := strings.Repeat("X", 200)
	upstream := []byte("event: content_block_delta\ndata: " + bigPayload + "\n\n")
	f := newForwarder()
	f.ScannerBufferCap = 100 // 小于 payload，触发溢出
	draft, err := f.Forward(context.Background(), bytes.NewReader(upstream), httptest.NewRecorder(), anthropicForwardRequest(1, 100))
	if !errors.Is(err, ErrScannerOverflow) {
		t.Fatalf("expected ErrScannerOverflow; got %v", err)
	}
	if draft.EndClass != ResponseEventTooLarge {
		t.Fatalf("expected end_class=response_event_too_large; got %q", draft.EndClass)
	}
}

// AT-GW-002-07: 客户端中途断连 — 函数退出时累积 usage 已保存。
func TestAT_GW_002_07_ClientDisconnectPreservesAccumulatedUsage(t *testing.T) {
	upstream := sseBytes(
		messageStart("m"),
		messageDeltaWithUsage("", 50, 75), // 断连前已有 usage
		textDelta(0, "trigger-disconnect"),
		// 断连后 drain 事件
		messageDeltaWithUsage("end_turn", 99, 88),
	)
	rec := &disconnectingWriter{after: 2} // 第 2 次写入后断连
	f := newForwarder()
	draft, _ := f.Forward(context.Background(), bytes.NewReader(upstream), rec, anthropicForwardRequest(1, 100))
	if draft.EndClass != ClientDisconnect {
		t.Fatalf("expected client_disconnect; got %q", draft.EndClass)
	}
	if draft.DrainOutcome == DrainNotDrained {
		t.Errorf("drain_outcome must be set after CLIENT_DISCONNECT; got %q", draft.DrainOutcome)
	}
	if draft.TokensInput == 0 && draft.TokensOutput == 0 {
		t.Fatalf("client_disconnect drain MUST surface accumulated usage; got 0/0 in draft")
	}
}

// AT-GW-002-08: 多个 message_delta 事件中各字段各自取最后非零值。
func TestAT_GW_002_08_LastNonZeroWinsPerField(t *testing.T) {
	upstream := sseBytes(
		messageStart("m"),
		messageDeltaWithUsage("", 10, 20),
		messageDeltaWithUsage("", 0, 30), // output 覆盖；input 保持 10
		messageDeltaWithUsage("end_turn", 0, 0),
		messageStop(),
	)
	f := newForwarder()
	draft, err := f.Forward(context.Background(), bytes.NewReader(upstream), httptest.NewRecorder(), anthropicForwardRequest(1, 100))
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if draft.TokensInput != 10 {
		t.Errorf("input_tokens last-non-zero violation: got %d want 10", draft.TokensInput)
	}
	if draft.TokensOutput != 30 {
		t.Errorf("output_tokens last-non-zero violation: got %d want 30", draft.TokensOutput)
	}
}

// =====================================================================
// HUAKAI-design scenarios
// =====================================================================

// AT-GW-002-09: 有界 drain — drain 必须消费上游事件、提取 partial usage、
// 不写下游，并在任意预算耗尽时退出。
func TestAT_GW_002_09_DrainConsumesEventsAndExtractsUsage(t *testing.T) {
	upstream := sseBytes(
		messageStart("m"),
		textDelta(0, "trigger-disconnect"),
		messageDeltaWithUsage("", 42, 84),
	)
	tail := make([]sseEvt, 0, 50)
	for i := 0; i < 50; i++ {
		tail = append(tail, textDelta(0, "drain-byte"))
	}
	upstream = append(upstream, sseBytes(tail...)...)

	rec := &disconnectingWriter{after: 1}
	writesBeforeDrain := rec.writes
	f := newForwarder()
	f.DrainBudgets = DrainBudgets{MaxSeconds: 200 * time.Millisecond, MaxBytes: 100}
	draft, _ := f.Forward(context.Background(), bytes.NewReader(upstream), rec, anthropicForwardRequest(1, 100))

	if draft.EndClass != ClientDisconnect {
		t.Fatalf("expected client_disconnect end class; got %q", draft.EndClass)
	}
	switch draft.DrainOutcome {
	case DrainBudgetSecondsExhausted, DrainBudgetBytesExhausted, DrainBudgetCostExhausted:
	default:
		t.Fatalf("drain must exit on a budget exhaust; got %q", draft.DrainOutcome)
	}
	if draft.TokensInput != 42 || draft.TokensOutput != 84 {
		t.Errorf("drain failed to extract post-disconnect partial usage; got in=%d out=%d (want 42/84)", draft.TokensInput, draft.TokensOutput)
	}
	if rec.writes <= writesBeforeDrain {
		t.Logf("write-counter snapshot before/after = %d/%d", writesBeforeDrain, rec.writes)
	}
	if draft.UsageSource != UsageSourcePartial {
		t.Errorf("post-disconnect drain must set usage_source=partial; got %q", draft.UsageSource)
	}
}

// AT-GW-002-10: drain 费用上限：CostEstimator 超限时停止 drain。
func TestAT_GW_002_10_DrainCostCapTriggers(t *testing.T) {
	tail := make([]sseEvt, 0, 50)
	for i := 0; i < 50; i++ {
		tail = append(tail, textDelta(0, "drain-byte"))
	}
	upstream := sseBytes(messageStart("m"), textDelta(0, "x"))
	upstream = append(upstream, sseBytes(tail...)...)

	rec := &disconnectingWriter{after: 1}
	f := newForwarder()
	f.DrainBudgets = DrainBudgets{
		MaxSeconds:       1 * time.Second,
		MaxBytes:         1 << 20,
		MaxEstimatedCost: decimal.NewFromFloat(0.10),
	}
	f.CostEstimator = func(drainedBytes int64, _ UsageAccumulator) decimal.Decimal {
		return decimal.NewFromFloat(0.001).Mul(decimal.NewFromInt(drainedBytes))
	}
	draft, _ := f.Forward(context.Background(), bytes.NewReader(upstream), rec, anthropicForwardRequest(1, 100))
	if draft.DrainOutcome != DrainBudgetCostExhausted {
		t.Fatalf("expected drain to exit on cost budget after accumulation; got %q", draft.DrainOutcome)
	}
}

// AT-GW-002-11: 八轴超时独立性 — total < inter 时 total 必须先触发。
func TestAT_GW_002_11_TotalStreamBeatsInterEvent(t *testing.T) {
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		_, _ = pw.Write(sseBytes(messageStart("m")))
		for i := 0; i < 20; i++ {
			time.Sleep(30 * time.Millisecond)
			_, _ = pw.Write(sseBytes(textDelta(0, "x")))
		}
	}()
	f := newForwarder()
	f.Timeouts.FirstTokenTimeout = 1 * time.Second
	f.Timeouts.InterEventTimeout = 500 * time.Millisecond
	f.Timeouts.TotalStreamTimeout = 120 * time.Millisecond
	draft, err := f.Forward(context.Background(), pr, httptest.NewRecorder(), anthropicForwardRequest(1, 100))
	if !errors.Is(err, ErrTotalStreamTimeout) {
		t.Fatalf("total_stream MUST beat inter_event under steady-event load; got err=%v", err)
	}
	if draft.EndClass != TotalStreamTimeout {
		t.Fatalf("expected end_class=total_stream_timeout; got %q", draft.EndClass)
	}
}

// AT-GW-002-11b: 首字节超时冒烟测试。
func TestAT_GW_002_11b_FirstTokenTimeout(t *testing.T) {
	silent := newSlowReader(200 * time.Millisecond)
	f := newForwarder()
	f.Timeouts.FirstTokenTimeout = 50 * time.Millisecond
	f.Timeouts.TotalStreamTimeout = 0
	_, err := f.Forward(context.Background(), silent, httptest.NewRecorder(), anthropicForwardRequest(1, 100))
	if !errors.Is(err, ErrFirstTokenTimeout) {
		t.Fatalf("expected ErrFirstTokenTimeout; got %v", err)
	}
}

// TestForwarderEmitsKeepaliveDuringLongTTFT guards the CF/long-run fix: during a long
// silent upstream wait (slow codex/o1 thinking before first token), the forwarder must
// write periodic SSE keepalive comments to the client so a fronting Cloudflare/proxy does
// not drop the idle connection (~100s 524) before the answer arrives.
//
// Mutation check: delete the keepaliveTimer select case in Forward and no ": hk" comment is
// ever written → this assertion fails. The interval (20ms) is well under the first-token
// timeout (200ms) so multiple keepalives fire during the wait.
func TestForwarderEmitsKeepaliveDuringLongTTFT(t *testing.T) {
	silent := newSlowReader(300 * time.Millisecond)
	f := newForwarder()
	f.Timeouts.KeepAliveInterval = 20 * time.Millisecond
	f.Timeouts.FirstTokenTimeout = 200 * time.Millisecond
	f.Timeouts.InterEventTimeout = 0
	f.Timeouts.TotalStreamTimeout = 0
	rec := httptest.NewRecorder()
	_, err := f.Forward(context.Background(), silent, rec, anthropicForwardRequest(1, 100))
	if !errors.Is(err, ErrFirstTokenTimeout) {
		t.Fatalf("expected first-token timeout after silent upstream; got %v", err)
	}
	if !strings.Contains(rec.Body.String(), ": hk") {
		t.Fatalf("expected SSE keepalive comments during long TTFT to keep the proxy connection alive; body=%q", rec.Body.String())
	}
	// once a heartbeat has been written the response is committed (HTTP 200 +
	// bytes on the wire), so deliveryTracker.started() flips true and the upstream caller can no
	// longer turn the first-token timeout into a retryable failure / HTTP error status. To avoid
	// handing the client a silent, comment-only 200 stream, Forward must emit an explicit in-band
	// SSE error event on the terminating error. Mutation check: delete the keepaliveCommitted
	// error-emit block in Forward and this assertion goes red (only ": hk" comments, no error).
	if !strings.Contains(rec.Body.String(), "event: error") {
		t.Fatalf("expected an explicit in-band SSE error event after keepalive-committed first-token timeout (not a silent comment-only 200); body=%q", rec.Body.String())
	}
}

// TestForwarderKeepaliveDisabledWhenIntervalZero proves KeepAliveInterval=0 is OFF (no
// stray keepalive frames injected when the operator disables it), so the feature is opt-out.
func TestForwarderKeepaliveDisabledWhenIntervalZero(t *testing.T) {
	silent := newSlowReader(200 * time.Millisecond)
	f := newForwarder()
	f.Timeouts.KeepAliveInterval = 0
	f.Timeouts.FirstTokenTimeout = 100 * time.Millisecond
	f.Timeouts.InterEventTimeout = 0
	f.Timeouts.TotalStreamTimeout = 0
	rec := httptest.NewRecorder()
	_, _ = f.Forward(context.Background(), silent, rec, anthropicForwardRequest(1, 100))
	if strings.Contains(rec.Body.String(), ": hk") {
		t.Fatalf("keepalive must be OFF when interval=0; body=%q", rec.Body.String())
	}
}

// TestForwarderInterEventTimeoutFiresAfterFirstEvent guards
// select case must NOT cannibalise the inter-event-timeout case. After the upstream delivers its
// first event and then stalls (sparse/stuck stream), InterEventTimeout must still fire on its own
// short deadline — the heartbeat keeps the connection warm but must not reset interTimer or mask
// the stall.
//
// Mutation check: remove the `case <-timerC(interTimer)` branch in Forward. interTimer is
// still armed after the event but nothing selects on it, so the stall runs
// to the much longer TotalStreamTimeout instead → err becomes ErrTotalStreamTimeout and this test
// goes red on both assertions. Discriminating fixture: InterEventTimeout (50ms) is 40× shorter
// than TotalStreamTimeout (2s), so the two outcomes are unambiguous.
func TestForwarderInterEventTimeoutFiresAfterFirstEvent(t *testing.T) {
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		_, _ = pw.Write(sseBytes(messageStart("m")))
		<-stop // 首事件后上游卡死:不再产出、也不 EOF,直到测试结束
	}()
	f := newForwarder()
	f.Timeouts.FirstTokenTimeout = 1 * time.Second
	f.Timeouts.InterEventTimeout = 50 * time.Millisecond
	f.Timeouts.TotalStreamTimeout = 2 * time.Second
	f.Timeouts.KeepAliveInterval = 0
	draft, err := f.Forward(context.Background(), pr, httptest.NewRecorder(), anthropicForwardRequest(1, 100))
	if !errors.Is(err, ErrInterEventTimeout) {
		t.Fatalf("inter-event timeout MUST fire when upstream stalls after the first event; got err=%v (end_class=%q)", err, draft.EndClass)
	}
	if draft.EndClass != InterEventTimeout {
		t.Fatalf("expected end_class=inter_event_timeout; got %q", draft.EndClass)
	}
}

// TestForwarderKeepaliveDoesNotResetInterEventTimeout proves the heartbeat is a pure liveness
// signal: with keepalive ON and firing several times inside one inter-event gap, the stall is
// still detected at InterEventTimeout — the heartbeat must NOT push the inter-event deadline out.
//
// Mutation check: add `stopTimer(interTimer); interTimer = newTimer(...)` to the keepalive case
// (i.e. let the heartbeat reset the stall detector). Then the heartbeat (15ms) keeps re-arming the
// 60ms inter-event timer forever and the stream runs to TotalStreamTimeout → red.
func TestForwarderKeepaliveDoesNotResetInterEventTimeout(t *testing.T) {
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		_, _ = pw.Write(sseBytes(messageStart("m")))
		<-stop
	}()
	f := newForwarder()
	f.Timeouts.FirstTokenTimeout = 1 * time.Second
	f.Timeouts.InterEventTimeout = 60 * time.Millisecond
	f.Timeouts.TotalStreamTimeout = 2 * time.Second
	f.Timeouts.KeepAliveInterval = 15 * time.Millisecond // 在一个 inter-event 间隙内会触发多次
	draft, err := f.Forward(context.Background(), pr, httptest.NewRecorder(), anthropicForwardRequest(1, 100))
	if !errors.Is(err, ErrInterEventTimeout) {
		t.Fatalf("keepalive heartbeats must not reset the inter-event stall detector; got err=%v (end_class=%q)", err, draft.EndClass)
	}
	if draft.EndClass != InterEventTimeout {
		t.Fatalf("expected end_class=inter_event_timeout despite active keepalive; got %q", draft.EndClass)
	}
}

// AT-GW-002-12: 超大事件类型化终态 — RESPONSE_EVENT_TOO_LARGE，usage_source 非 reported。
func TestAT_GW_002_12_OversizeTerminalNoCharge(t *testing.T) {
	bigPayload := strings.Repeat("Y", 500)
	upstream := []byte("event: content_block_delta\ndata: " + bigPayload + "\n\n")
	f := newForwarder()
	f.ScannerBufferCap = 100
	draft, _ := f.Forward(context.Background(), bytes.NewReader(upstream), httptest.NewRecorder(), anthropicForwardRequest(1, 100))
	if draft.EndClass != ResponseEventTooLarge {
		t.Fatalf("expected response_event_too_large; got %q", draft.EndClass)
	}
	if draft.UsageSource == UsageSourceReported {
		t.Errorf("usage_source must NOT be reported for truncated stream; got %q", draft.UsageSource)
	}
	if draft.TokensInput != 0 || draft.TokensOutput != 0 {
		t.Errorf("oversize terminal must produce zero billable usage; got in=%d out=%d", draft.TokensInput, draft.TokensOutput)
	}
}

// AT-GW-002-15: 终态帧优先 — message_stop 之后的 usage 更新必须被忽略。
func TestAT_GW_002_15_TerminalFrameLocksAccumulator(t *testing.T) {
	acc := UsageAccumulator{}
	acc.Update(UsageSourceReported, proto.CanonicalUsage{InputTokens: 100, OutputTokens: 200})
	acc.Freeze()
	acc.Update(UsageSourceReported, proto.CanonicalUsage{InputTokens: 999, OutputTokens: 999})
	acc.Update(UsageSourcePartial, proto.CanonicalUsage{InputTokens: 7, OutputTokens: 8})
	if acc.Usage.InputTokens != 100 || acc.Usage.OutputTokens != 200 {
		t.Fatalf("terminal-frame priority violated: post-freeze update overwrote terminal values; got in=%d out=%d", acc.Usage.InputTokens, acc.Usage.OutputTokens)
	}

	upstream := sseBytes(
		messageStart("m"),
		messageDeltaWithUsage("end_turn", 100, 200),
		messageStop(),
		messageDeltaWithUsage("end_turn", 999, 999), // 晚到的幽灵帧，必须忽略
	)
	f := newForwarder()
	draft, _ := f.Forward(context.Background(), bytes.NewReader(upstream), httptest.NewRecorder(), anthropicForwardRequest(1, 100))
	if draft.TokensInput != 100 || draft.TokensOutput != 200 {
		t.Fatalf("post-terminal usage frame leaked into draft: got in=%d out=%d (want 100/200)", draft.TokensInput, draft.TokensOutput)
	}
}

func TestUsageAccumulatorUpdatePropagatesCacheTokensToFinishDraft(t *testing.T) {
	acc := UsageAccumulator{}
	acc.Update(UsageSourceReported, proto.CanonicalUsage{
		InputTokens:                10,
		OutputTokens:               20,
		CacheCreationInputTokens5m: 100,
		CacheCreationInputTokens1h: 50,
		CacheReadInputTokens:       200,
	})

	// MUTATION: if Update drops cache token fields, these assert 0 -> RED.
	if acc.Usage.CacheCreationInputTokens5m != 100 {
		t.Fatalf("expected cache_creation_input_tokens_5m=100; got %d", acc.Usage.CacheCreationInputTokens5m)
	}
	if acc.Usage.CacheCreationInputTokens1h != 50 {
		t.Fatalf("expected cache_creation_input_tokens_1h=50; got %d", acc.Usage.CacheCreationInputTokens1h)
	}
	if acc.Usage.CacheReadInputTokens != 200 {
		t.Fatalf("expected cache_read_input_tokens=200; got %d", acc.Usage.CacheReadInputTokens)
	}

	draft, err := (&StreamForwarder{}).finishDraft(UsageRecordDraft{}, acc, time.Now(), nil)
	if err != nil {
		t.Fatalf("finishDraft returned unexpected error: %v", err)
	}
	if draft.CacheCreation5mTokens != 100 {
		t.Fatalf("expected draft cache_creation_5m_tokens=100; got %d", draft.CacheCreation5mTokens)
	}
	if draft.CacheCreation1hTokens != 50 {
		t.Fatalf("expected draft cache_creation_1h_tokens=50; got %d", draft.CacheCreation1hTokens)
	}
	if draft.CacheReadTokens != 200 {
		t.Fatalf("expected draft cache_read_tokens=200; got %d", draft.CacheReadTokens)
	}
	if draft.CacheCreationTokens != 150 {
		t.Fatalf("expected draft cache_creation_tokens=150; got %d", draft.CacheCreationTokens)
	}
}

func TestUsageAccumulatorEmptyTreatsCacheOnlyUsageAsNonEmpty(t *testing.T) {
	acc := UsageAccumulator{}
	acc.Update(UsageSourceReported, proto.CanonicalUsage{CacheReadInputTokens: 200})

	// MUTATION: if Empty() ignores cache fields, this returns true -> RED.
	if acc.Empty() {
		t.Fatalf("expected cache-only usage to be non-empty")
	}
}

// AT-GW-002-18: AMBIGUOUS_USAGE 无费用门控 — zero 累加器 + UNKNOWN_TERMINATION
// → end_class=ambiguous_usage + ErrAmbiguousUsage。
// 通过 stubSingleAdapterRegistry 注入会抛出错误的 adapter，强制 UNKNOWN_TERMINATION。
func TestAT_GW_002_18_AmbiguousUsageAbortPath(t *testing.T) {
	upstream := sseBytes(
		messageStart("m"),
		textDelta(0, "trigger-error"),
	)
	f := newForwarder()
	// 用 stubSingleAdapterRegistry 将 errorThrowingAdapter 注入为 "anthropic_messages"
	f.ProtocolAdapters = &stubSingleAdapterRegistry{
		family:  "anthropic_messages",
		adapter: &errorThrowingAdapter{throwOn: "content_block_delta"},
	}
	draft, err := f.Forward(context.Background(), bytes.NewReader(upstream), httptest.NewRecorder(), anthropicForwardRequest(1, 100))
	if draft.EndClass != AmbiguousUsage {
		t.Fatalf("zero-acc + UNKNOWN_TERMINATION must convert to AMBIGUOUS_USAGE; got end_class=%q", draft.EndClass)
	}
	if !errors.Is(err, ErrAmbiguousUsage) {
		t.Fatalf("AMBIGUOUS_USAGE must surface ErrAmbiguousUsage to caller; got %v", err)
	}
	if draft.TokensInput != 0 || draft.TokensOutput != 0 {
		t.Fatalf("AMBIGUOUS_USAGE must produce zero billable usage; got in=%d out=%d", draft.TokensInput, draft.TokensOutput)
	}
	if draft.UsageSource != UsageSourceAmbiguous {
		t.Fatalf("AMBIGUOUS_USAGE must set usage_source=ambiguous; got %q", draft.UsageSource)
	}
}

// AT-GW-002-19 partial: EOF 无终态帧设置 pending_reconciliation=true。
func TestAT_GW_002_19_PendingReconciliationOnEOFNoTerminal(t *testing.T) {
	upstream := sseBytes(
		messageStart("m"),
		messageDeltaWithUsage("", 50, 80),
		// 无 message_stop — EOF 到达时没有终态标记
	)
	f := newForwarder()
	draft, _ := f.Forward(context.Background(), bytes.NewReader(upstream), httptest.NewRecorder(), anthropicForwardRequest(1, 100))
	if draft.EndClass != UpstreamEOFNoTerminal {
		t.Fatalf("EOF without terminal must classify as upstream_eof_no_terminal; got %q", draft.EndClass)
	}
	if !draft.PendingReconciliation {
		t.Fatalf("EOF without terminal must set pending_reconciliation=true (spec line 115)")
	}
	if draft.UsageSource != UsageSourceInferred {
		t.Fatalf("EOF without terminal + non-empty acc → usage_source=inferred per spec; got %q", draft.UsageSource)
	}
}

func TestF_OBS_003_RSTAfterChunkProducesPartialDraft(t *testing.T) {
	scanners := NewStaticStreamScannerRegistry()
	scanners.MustRegister("anthropic_messages", rstAfterOneScanner{})
	f := newForwarder()
	f.Scanners = scanners

	draft, err := f.Forward(context.Background(), bytes.NewReader(nil), httptest.NewRecorder(), anthropicForwardRequest(1, 100))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err=%v want io.ErrUnexpectedEOF", err)
	}
	if draft.EndClass != UpstreamError5xx {
		t.Fatalf("end_class=%q want upstream_error_5xx", draft.EndClass)
	}
	if draft.DeliveredTokenCount <= 0 {
		t.Fatalf("delivered_token_count=%d want >0", draft.DeliveredTokenCount)
	}
	if draft.StreamTerminatedReason != "upstream_5xx" {
		t.Fatalf("reason=%q want upstream_5xx", draft.StreamTerminatedReason)
	}
}

func TestStreamingLedgerSubmitAfterForwardHasEntry(t *testing.T) {
	ctx := context.Background()
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	req := anthropicForwardRequest(7, 42)
	req.RequestID = "req-stream-ledger"
	req.RouteID = "route-stream"
	req.PoolID = "pool-42"
	req.Provider = "anthropic"

	f := newForwarder()
	f.AuditLedger = ledger
	f.Signer = signer
	upstream := sseBytes(
		messageStart("msg-ledger"),
		messageDeltaWithUsage("end_turn", 3, 5),
		messageStop(),
	)
	if _, err := f.Forward(ctx, bytes.NewReader(upstream), httptest.NewRecorder(), req); err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if got := ledger.Size(ctx); got != 1 {
		t.Fatalf("ledger size=%d want 1", got)
	}
	entry, err := ledger.GetByRequestID(ctx, req.RequestID)
	if err != nil {
		t.Fatalf("ledger entry by request_id: %v", err)
	}
	if len(entry.HopChain) != 6 {
		t.Fatalf("hop chain len=%d want 6: %+v", len(entry.HopChain), entry.HopChain)
	}
	if entry.PubkeyFingerprint != signer.Fingerprint() {
		t.Fatalf("fingerprint=%q want %q", entry.PubkeyFingerprint, signer.Fingerprint())
	}
}

func TestStreamingLedgerCallbackAtStreamTerminal(t *testing.T) {
	// Risk killed: C-13 must not emit the streaming ledger at first byte.
	// Mutation self-check: restoring Write/WriteHeader ledger emission makes
	// firstWriteCallbackSeen true and fails this test.
	ctx := context.Background()
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	callbackCalled := false
	var callbackResult auditledger.AuditLedgerResult
	writer := &firstChunkHeaderWriter{header: http.Header{}, callbackSeen: &callbackCalled}
	req := anthropicForwardRequest(7, 42)
	req.RequestID = "req-stream-header-order"

	f := newForwarder()
	f.AuditLedger = ledger
	f.Signer = signer
	f.LedgerCallback = func(result auditledger.AuditLedgerResult) {
		callbackCalled = true
		callbackResult = result
	}
	upstream := sseBytes(messageStart("msg-order"), textDelta(0, "first"), messageStop())
	if _, err := f.Forward(ctx, bytes.NewReader(upstream), writer, req); err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if writer.writes == 0 {
		t.Fatal("writer saw no chunks")
	}
	if writer.firstWriteCallbackSeen {
		t.Fatalf("ledger callback ran before first chunk; first-write headers=%v", writer.firstWriteHeader)
	}
	if !callbackCalled {
		t.Fatal("ledger callback did not run before Forward returned")
	}
	if callbackResult.State != auditledger.LedgerResultStatePersisted {
		t.Fatalf("LedgerCallback state=%v want Persisted", callbackResult.State)
	}
	if callbackResult.LedgerID == "" {
		t.Fatal("LedgerCallback LedgerID is empty")
	}
	if callbackResult.Fingerprint != signer.Fingerprint() {
		t.Fatalf("callback fingerprint=%q want %q", callbackResult.Fingerprint, signer.Fingerprint())
	}
}

func TestStreamingLedgerPersistsAfterClientContextCancel(t *testing.T) {
	// Risk killed: a client disconnect after partial delivery must not cancel
	// the terminal streaming ledger write. Mutation self-check: replacing the
	// detached ledger context with the request ctx makes Append observe
	// context.Canceled, leaves the callback non-Persisted, and this test fails.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	inner, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	ledger := &contextRejectingStreamLedger{inner: inner}
	req := anthropicForwardRequest(7, 42)
	req.RequestID = "req-stream-client-cancel-ledger"
	writer := &cancelOnFirstWriteWriter{cancel: cancel}
	var callback auditledger.AuditLedgerResult

	f := newForwarder()
	f.AuditLedger = ledger
	f.Signer = signer
	f.LedgerCallback = func(result auditledger.AuditLedgerResult) { callback = result }
	upstream := sseBytes(messageStart("msg-client-cancel"), textDelta(0, "partial"), messageStop())

	_, err = f.Forward(ctx, bytes.NewReader(upstream), writer, req)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Forward err=%v want nil or context.Canceled", err)
	}
	if writer.body.Len() == 0 {
		t.Fatal("fixture delivered no client bytes before cancel")
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("request ctx err=%v want context.Canceled", ctx.Err())
	}
	if ledger.appendSawCanceled {
		t.Fatal("ledger Append saw canceled request context; want detached bounded context")
	}
	if got := inner.Size(context.Background()); got != 1 {
		t.Fatalf("ledger size=%d want 1 after client cancel", got)
	}
	if callback.State != auditledger.LedgerResultStatePersisted {
		t.Fatalf("LedgerCallback state=%v want Persisted; result=%+v", callback.State, callback)
	}
	if callback.LedgerID == "" {
		t.Fatal("LedgerCallback LedgerID is empty after client cancel")
	}
}

func TestStreamingLedgerCompletionTimeUsesTerminalTime(t *testing.T) {
	// Risk killed: C-13 must record true stream completion time, not first-byte
	// time. Mutation self-check: moving ledger emission back to first write
	// makes the response-hop timestamp too close to firstWriteAt and fails.
	ctx := context.Background()
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	scanners := NewStaticStreamScannerRegistry()
	scanner := &delayedTerminalScanner{delay: 120 * time.Millisecond}
	scanners.MustRegister("anthropic_messages", scanner)
	writer := &firstChunkHeaderWriter{header: http.Header{}}
	req := anthropicForwardRequest(7, 42)
	req.RequestID = "req-stream-terminal-time"

	f := newForwarder()
	f.Scanners = scanners
	f.AuditLedger = ledger
	f.Signer = signer
	if _, err := f.Forward(ctx, bytes.NewReader(nil), writer, req); err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if writer.firstWriteAt.IsZero() {
		t.Fatal("fixture did not observe first client write")
	}
	if scanner.terminalAt.IsZero() {
		t.Fatal("fixture did not observe delayed terminal event")
	}
	entry, err := ledger.GetByRequestID(ctx, req.RequestID)
	if err != nil {
		t.Fatalf("ledger entry by request_id: %v", err)
	}
	completedAt := responseHopTime(t, entry)
	if delta := completedAt.Sub(writer.firstWriteAt); delta < 75*time.Millisecond {
		t.Fatalf("completion timestamp too close to first byte: delta=%v want >=75ms", delta)
	}
	if delta := absDuration(completedAt.Sub(scanner.terminalAt)); delta > 150*time.Millisecond {
		t.Fatalf("completion timestamp=%s terminalAt=%s delta=%v want <=150ms", completedAt.Format(time.RFC3339Nano), scanner.terminalAt.Format(time.RFC3339Nano), delta)
	}
}

func TestStreamingNilLedgerGracefulSkip(t *testing.T) {
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	var warnings []string
	callbackCalled := false
	f := newForwarder()
	f.Signer = signer
	f.LedgerCallback = func(auditledger.AuditLedgerResult) { callbackCalled = true }
	f.LedgerWarning = func(code, _ string) { warnings = append(warnings, code) }

	upstream := sseBytes(messageStart("msg-nil-ledger"), textDelta(0, "ok"), messageStop())
	if _, err := f.Forward(context.Background(), bytes.NewReader(upstream), httptest.NewRecorder(), anthropicForwardRequest(7, 42)); err != nil {
		t.Fatalf("Forward with nil ledger: %v", err)
	}
	if callbackCalled {
		t.Fatal("ledger callback must not run when ledger is nil")
	}
	if len(warnings) != 1 || warnings[0] != "audit_ledger_not_configured" {
		t.Fatalf("warnings=%v want [audit_ledger_not_configured]", warnings)
	}
}

func TestStreamingLedgerAppendFailureEnqueuesDLQAndCallbacksDeferred(t *testing.T) {
	// Risk killed: C-14 Append failure must not be warning-only; it must enqueue
	// the prepared intent and give the caller a Deferred result. Mutation
	// self-check: deleting the DLQ enqueue leaves events empty and callback
	// state unset, so this test fails while the stream itself can still finish.
	ctx := context.Background()
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	req := anthropicForwardRequest(7, 42)
	req.RequestID = "req-stream-ledger-dlq"
	dlqSink := &recordingStreamAuditLedgerDLQ{id: 727}
	var callback auditledger.AuditLedgerResult
	var warnings []string

	f := newForwarder()
	f.AuditLedger = &failingStreamAppendLedger{appendErr: errors.New("ledger unavailable")}
	f.AuditLedgerDLQ = dlqSink
	f.Signer = signer
	f.LedgerCallback = func(result auditledger.AuditLedgerResult) { callback = result }
	f.LedgerWarning = func(code, _ string) { warnings = append(warnings, code) }
	rec := httptest.NewRecorder()
	upstream := sseBytes(messageStart("msg-dlq"), textDelta(0, "first"), messageStop())

	if _, err := f.Forward(ctx, bytes.NewReader(upstream), rec, req); err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("stream body is empty; fixture must prove request still completed")
	}
	if len(dlqSink.events) != 1 {
		t.Fatalf("DLQ events=%d want 1", len(dlqSink.events))
	}
	event := dlqSink.events[0]
	if event.EventKind != dlq.EventKindAuditLedgerEntry || event.IdempotencyKey != "audit_ledger:req-stream-ledger-dlq" {
		t.Fatalf("DLQ envelope mismatch: %+v", event)
	}
	if callback.State != auditledger.LedgerResultStateDeferred || callback.DLQRef != "audit_ledger_dlq:727" || callback.Fingerprint != "" {
		t.Fatalf("callback result=%+v want Deferred DLQRef without fingerprint", callback)
	}
	if len(warnings) == 0 || warnings[0] != "audit_ledger_append_failed" {
		t.Fatalf("warnings=%v want append failure signal", warnings)
	}
}

func TestResponsesFamilyMarshalRoundTrip(t *testing.T) {
	env := proto.NewEmptyEnvelope()
	env.RequestMeta.UpstreamModel = "gpt-4o"
	env.CapabilityGraph.Nodes = []proto.CapabilityNode{{
		ID:          "n_text_responses",
		Kind:        proto.CapabilityText,
		StreamReady: proto.StreamReadyYes,
		Text: &proto.TextNode{
			Role:  "user",
			Block: proto.CanonicalContentBlock{Type: "text", Text: "hello responses"},
		},
	}}
	raw, err := MarshalToProviderRequest(env, "openai_responses")
	if err != nil {
		t.Fatalf("MarshalToProviderRequest(openai_responses): %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("responses body json: %v\n%s", err, raw)
	}
	if body["model"] != "gpt-4o" {
		t.Fatalf("model=%q want gpt-4o", body["model"])
	}
	input, ok := body["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("input=%T/%v want one responses input item", body["input"], body["input"])
	}
	msg := input[0].(map[string]any)
	content := msg["content"].([]any)[0].(map[string]any)
	if msg["type"] != "message" || content["type"] != "input_text" || content["text"] != "hello responses" {
		t.Fatalf("responses projection wrong: %+v", msg)
	}
}

// =====================================================================
// 新增：ProtocolFamily 校验测试
// =====================================================================

// AT-GW-002-PF-01: ProtocolFamily 为空时 Forward 返回 ErrUnknownProtocolFamily。
func TestAT_GW_002_PF_01_EmptyProtocolFamilyReturnsError(t *testing.T) {
	f := newForwarder()
	upstream := sseBytes(messageStart("m"), messageStop())
	_, err := f.Forward(context.Background(), bytes.NewReader(upstream), httptest.NewRecorder(), ForwardRequest{
		TenantID:  1,
		AccountID: 100,
		// ProtocolFamily 故意留空
	})
	if !errors.Is(err, ErrUnknownProtocolFamily) {
		t.Fatalf("empty ProtocolFamily must return ErrUnknownProtocolFamily; got %v", err)
	}
}

// AT-GW-002-PF-02: ProtocolAdapters 为 nil 时 Forward 返回 ErrNilProtocolAdapterRegistry。
func TestAT_GW_002_PF_02_NilRegistryReturnsError(t *testing.T) {
	f := &StreamForwarder{
		ProtocolAdapters: nil, // 故意不注入
		Timeouts: TimeoutConfig{
			FirstTokenTimeout: 500 * time.Millisecond,
		},
		ScannerBufferCap: 1 << 20,
	}
	upstream := sseBytes(messageStart("m"), messageStop())
	_, err := f.Forward(context.Background(), bytes.NewReader(upstream), httptest.NewRecorder(), ForwardRequest{
		TenantID:       1,
		AccountID:      100,
		ProtocolFamily: "anthropic_messages",
	})
	if !errors.Is(err, ErrNilProtocolAdapterRegistry) {
		t.Fatalf("nil ProtocolAdapters must return ErrNilProtocolAdapterRegistry; got %v", err)
	}
}

// AT-GW-002-PF-02b: Scanners 为 nil 时 Forward 返回 ErrNilStreamScannerRegistry。
// fail-loud — 不静默 fallback 到 SSE，否则 Bedrock binary 会被切碎。
func TestAT_GW_002_PF_02b_NilScannerRegistryReturnsError(t *testing.T) {
	f := &StreamForwarder{
		ProtocolAdapters: BuildDefaultProtocolAdapterRegistry(),
		Scanners:         nil, // 故意不注入 — 启动 misconfig 必须 fail-loud
		Timeouts: TimeoutConfig{
			FirstTokenTimeout: 500 * time.Millisecond,
		},
		ScannerBufferCap: 1 << 20,
	}
	upstream := sseBytes(messageStart("m"), messageStop())
	_, err := f.Forward(context.Background(), bytes.NewReader(upstream), httptest.NewRecorder(), ForwardRequest{
		TenantID:       1,
		AccountID:      100,
		ProtocolFamily: "anthropic_messages",
	})
	if !errors.Is(err, ErrNilStreamScannerRegistry) {
		t.Fatalf("nil Scanners must return ErrNilStreamScannerRegistry; got %v", err)
	}
}

// AT-GW-002-PF-03: 未注册的 ProtocolFamily 返回 ErrUnknownProtocolFamily。
func TestAT_GW_002_PF_03_UnknownProtocolFamilyReturnsError(t *testing.T) {
	f := newForwarder()
	upstream := sseBytes(messageStart("m"), messageStop())
	_, err := f.Forward(context.Background(), bytes.NewReader(upstream), httptest.NewRecorder(), ForwardRequest{
		TenantID:       1,
		AccountID:      100,
		ProtocolFamily: "nonexistent_protocol_xyz",
	})
	if !errors.Is(err, ErrUnknownProtocolFamily) {
		t.Fatalf("unknown ProtocolFamily must return ErrUnknownProtocolFamily; got %v", err)
	}
}

// =====================================================================
// Retired skip guards and forwarder-owned AT-GW-002 coverage
// =====================================================================

func TestAT_GW_002_NoRetiredPlaceholderSkipsRemain(t *testing.T) {
	// Risk killed: found false-green AT-GW-002 acceptance placeholders
	// implemented only as t.Skip. Mutation self-check: reintroducing t.Skip or
	// t.Skipf in any retired AT function below makes this parser guard fail even
	// though Go would otherwise report the skipped test file as PASS.
	targets := map[string]struct{}{
		"TestAT_GW_002_03_PreStreamFailoverList":                          {},
		"TestAT_GW_002_04_PreStreamSanitizedError":                        {},
		"TestAT_GW_002_05_BufferedMissingMessageStart":                    {},
		"TestAT_GW_002_13_MidStreamFailoverBlocked":                       {},
		"TestAT_GW_002_14_StreamingIdempotencyReplayRecordsSSEAndReplays": {},
		"TestAT_GW_002_16_PostDeliverySettleFailureEnqueuesRecovery":      {},
		"TestAT_GW_002_17_TenantIsolationUnderLoad":                       {},
		"TestAT_GW_002_19_TokenizerFallbackInferredUsage":                 {},
	}
	files := []string{
		currentTestFile(t),
		"../gatewayhttp/chat_completions_stream_test.go",
		"../gatewayhttp/post_delivery_recovery_test.go",
	}
	seen := make(map[string]bool, len(targets))
	for _, file := range files {
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name == nil {
				continue
			}
			if _, tracked := targets[fn.Name.Name]; !tracked {
				continue
			}
			seen[fn.Name.Name] = true
			if functionCallsTestSkip(fn) {
				t.Fatalf("%s still calls t.Skip/t.Skipf; S2-003 skip regression", fn.Name.Name)
			}
		}
	}
	for name := range targets {
		if !seen[name] {
			t.Fatalf("%s missing; S2-003 AT coverage must remain executable", name)
		}
	}
}

func TestAT_GW_002_03_PreStreamFailoverList(t *testing.T) {
	// Risk killed: pre-delivery upstream failures must classify into retryable
	// attempt decisions before the handler executor chooses the next account.
	// Mutation self-check: disabling RetryableBeforeDelivery or SwitchAccount
	// in decisionFromHTTPClassification turns the affected table row red.
	tests := []struct {
		name       string
		status     int
		body       []byte
		wantAbort  string
		wantAuth   bool
		wantPool   bool
		wantStatus int
	}{
		{name: "5xx", status: http.StatusInternalServerError, wantAbort: "upstream_5xx", wantPool: true, wantStatus: http.StatusBadGateway},
		{name: "overload_529", status: 529, wantAbort: "upstream_overloaded", wantPool: true, wantStatus: http.StatusServiceUnavailable},
		{name: "rate_limit_429", status: http.StatusTooManyRequests, wantAbort: "upstream_rate_limited", wantPool: true, wantStatus: http.StatusServiceUnavailable},
		{name: "auth_401", status: http.StatusUnauthorized, body: []byte(`{"error":"invalid_grant"}`), wantAbort: "upstream_auth_failure", wantAuth: true, wantStatus: http.StatusUnauthorized},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			decision, _, err := ClassifyAttemptHTTPError(tt.status, nil, tt.body, "openai")
			if err != nil {
				t.Fatalf("ClassifyAttemptHTTPError: %v", err)
			}
			if !decision.RetryableBeforeDelivery {
				t.Fatalf("RetryableBeforeDelivery=false for %s", tt.name)
			}
			if !decision.SwitchAccount {
				t.Fatalf("SwitchAccount=false for %s", tt.name)
			}
			if decision.SwitchPool != tt.wantPool {
				t.Fatalf("SwitchPool=%v want %v", decision.SwitchPool, tt.wantPool)
			}
			if decision.CountsAgainstAuthFailoverBudget != tt.wantAuth {
				t.Fatalf("CountsAgainstAuthFailoverBudget=%v want %v", decision.CountsAgainstAuthFailoverBudget, tt.wantAuth)
			}
			if decision.AbortReason != tt.wantAbort || decision.ClientStatus != tt.wantStatus {
				t.Fatalf("decision=%+v want abort=%q status=%d", decision, tt.wantAbort, tt.wantStatus)
			}
		})
	}
}

func TestAT_GW_002_04_PreStreamSanitizedError(t *testing.T) {
	// Risk killed: upstream error bodies must remain available for internal
	// classification while staying out of the public error string.
	// Mutation self-check: formatting UpstreamHTTPError.Error with Body leaks the
	// marker; dropping Body prevents the iron-clad class assertion.
	const marker = "SENSITIVE_UPSTREAM_MARKER"
	upstreamErr := &UpstreamHTTPError{
		StatusCode: http.StatusUnauthorized,
		Body:       []byte(`{"error":"token_revoked","raw":"` + marker + `"}`),
		Header:     make(http.Header),
	}
	if strings.Contains(upstreamErr.Error(), marker) || strings.Contains(upstreamErr.Error(), "token_revoked") {
		t.Fatalf("UpstreamHTTPError.Error leaked upstream body: %q", upstreamErr.Error())
	}
	decision, classification, err := ClassifyAttemptHTTPError(upstreamErr.StatusCode, upstreamErr.Header, upstreamErr.Body, "openai")
	if err != nil {
		t.Fatalf("ClassifyAttemptHTTPError: %v", err)
	}
	if classification.Class != ErrorClassTokenRevoked || decision.AbortReason != "upstream_auth_failure" {
		t.Fatalf("classification/decision=%+v/%+v want token_revoked auth failure", classification, decision)
	}
}

func TestAT_GW_002_05_BufferedMissingMessageStart(t *testing.T) {
	// Risk killed: an Anthropic-shaped buffered SSE body that starts with content
	// but never establishes message_start must not be reconstructed into a valid
	// buffered response.
	// Mutation self-check: accepting content deltas before message_start produces
	// a non-nil response and this test fails.
	raw := []byte(strings.Join([]string{
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"orphan"}}`,
		``,
		``,
	}, "\n"))
	env, losses, ok := protosse.ReconstructBufferedFromSSE(&protoanthropic.Adapter{}, raw)
	if !ok {
		t.Fatal("fixture must be recognized as SSE-shaped buffered upstream data")
	}
	if env != nil && env.BufferedResponse != nil {
		t.Fatalf("missing message_start reconstructed response: %+v", env.BufferedResponse)
	}
	if len(losses) == 0 {
		t.Fatal("missing message_start should emit reconstruction loss evidence")
	}
}

func TestAT_GW_002_13_MidStreamFailoverBlocked(t *testing.T) {
	// Risk killed: once content has been delivered, the forwarder must return a
	// partial draft for settlement/recovery instead of presenting the failure as
	// a clean pre-delivery retry candidate.
	// Mutation self-check: if delivered chunks are no longer tracked before the
	// scanner error, DeliveredTokenCount becomes zero and the handler can mistake
	// the stream for a pre-delivery failure.
	scanners := NewStaticStreamScannerRegistry()
	scanners.MustRegister("anthropic_messages", scannerEventsThenError{
		events: []SSEEvent{
			sseEventFromTestEvent(messageStart("msg-midstream")),
			sseEventFromTestEvent(textDelta(0, "visible-before-error")),
		},
		err: io.ErrUnexpectedEOF,
	})
	f := newForwarder()
	f.Scanners = scanners
	rec := httptest.NewRecorder()
	draft, err := f.Forward(context.Background(), bytes.NewReader(nil), rec, anthropicForwardRequest(1, 100))
	if err == nil {
		t.Fatal("mid-stream scanner error must be returned")
	}
	if draft.EndClass != UpstreamError5xx {
		t.Fatalf("EndClass=%q want %q", draft.EndClass, UpstreamError5xx)
	}
	if draft.DeliveredTokenCount == 0 || !strings.Contains(rec.Body.String(), "visible-before-error") {
		t.Fatalf("delivered=%d body=%s; want visible partial delivery before error", draft.DeliveredTokenCount, rec.Body.String())
	}
}

func currentTestFile(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return file
}

func functionCallsTestSkip(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel == nil {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if ok && ident.Name == "t" && (sel.Sel.Name == "Skip" || sel.Sel.Name == "Skipf") {
			found = true
			return false
		}
		return true
	})
	return found
}

type scannerEventsThenError struct {
	events []SSEEvent
	err    error
}

func (s scannerEventsThenError) Scan(context.Context, io.Reader, int) iter.Seq2[SSEEvent, error] {
	return func(yield func(SSEEvent, error) bool) {
		for _, evt := range s.events {
			if !yield(evt, nil) {
				return
			}
		}
		if s.err != nil {
			yield(SSEEvent{}, s.err)
		}
	}
}

// =====================================================================
// Test helpers（与原文件相同，保留完整）
// =====================================================================

// disconnectingWriter 模拟 http.ResponseWriter 在 N 次写入后报错。
type disconnectingWriter struct {
	body   bytes.Buffer
	header http.Header
	after  int
	writes int
}

func (d *disconnectingWriter) Header() http.Header {
	if d.header == nil {
		d.header = http.Header{}
	}
	return d.header
}
func (d *disconnectingWriter) WriteHeader(int) {}
func (d *disconnectingWriter) Write(p []byte) (int, error) {
	d.writes++
	if d.writes > d.after {
		return 0, errors.New("client disconnected")
	}
	return d.body.Write(p)
}
func (d *disconnectingWriter) Flush() {}

type cancelOnFirstWriteWriter struct {
	body   bytes.Buffer
	header http.Header
	cancel context.CancelFunc
	writes int
}

func (w *cancelOnFirstWriteWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}

func (w *cancelOnFirstWriteWriter) WriteHeader(int) {}

func (w *cancelOnFirstWriteWriter) Write(p []byte) (int, error) {
	if w.writes == 0 && w.cancel != nil {
		w.cancel()
	}
	w.writes++
	return w.body.Write(p)
}

func (w *cancelOnFirstWriteWriter) Flush() {}

// firstChunkHeaderWriter 记录第一次 body write 发生时 header 的状态，
// 用于守住 T12：ledger callback 必须早于首个 SSE chunk。
type firstChunkHeaderWriter struct {
	header                   http.Header
	body                     bytes.Buffer
	writes                   int
	firstWriteHeader         http.Header
	firstWriteLedgerID       string
	firstWriteSigFingerprint string
	firstWriteCallbackSeen   bool
	firstWriteAt             time.Time
	callbackSeen             *bool
}

func (w *firstChunkHeaderWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}

func (w *firstChunkHeaderWriter) WriteHeader(int) {}

func (w *firstChunkHeaderWriter) Write(p []byte) (int, error) {
	if w.writes == 0 {
		w.firstWriteAt = time.Now()
		w.firstWriteHeader = w.Header().Clone()
		w.firstWriteLedgerID = w.firstWriteHeader.Get("X-Test-Ledger-ID")
		w.firstWriteSigFingerprint = w.firstWriteHeader.Get("X-Test-Sig-Fingerprint")
		if w.callbackSeen != nil {
			w.firstWriteCallbackSeen = *w.callbackSeen
		}
	}
	w.writes++
	return w.body.Write(p)
}

func (w *firstChunkHeaderWriter) Flush() {}

type contextRejectingStreamLedger struct {
	inner             *auditledger.MemoryLedger
	appendSawCanceled bool
}

func (l *contextRejectingStreamLedger) Append(ctx context.Context, entry auditledger.PreparedEntry) (auditledger.LedgerEntry, error) {
	if err := ctx.Err(); err != nil {
		l.appendSawCanceled = true
		return auditledger.LedgerEntry{}, err
	}
	return l.inner.Append(ctx, entry)
}

func (l *contextRejectingStreamLedger) GetByRequestID(ctx context.Context, requestID string) (auditledger.LedgerEntry, error) {
	return l.inner.GetByRequestID(ctx, requestID)
}

func (l *contextRejectingStreamLedger) GetByRequestIDAndTenantScope(ctx context.Context, requestID, tenantScopeRef string) (auditledger.LedgerEntry, error) {
	return l.inner.GetByRequestIDAndTenantScope(ctx, requestID, tenantScopeRef)
}

func (l *contextRejectingStreamLedger) LatestMerkleRoot(ctx context.Context) ([32]byte, error) {
	return l.inner.LatestMerkleRoot(ctx)
}

func (l *contextRejectingStreamLedger) Size(ctx context.Context) int {
	return l.inner.Size(ctx)
}

type failingStreamAppendLedger struct {
	appendErr error
}

func (l *failingStreamAppendLedger) Append(context.Context, auditledger.PreparedEntry) (auditledger.LedgerEntry, error) {
	return auditledger.LedgerEntry{}, l.appendErr
}

func (l *failingStreamAppendLedger) GetByRequestID(context.Context, string) (auditledger.LedgerEntry, error) {
	return auditledger.LedgerEntry{}, auditledger.ErrLedgerEntryNotFound
}

func (l *failingStreamAppendLedger) GetByRequestIDAndTenantScope(context.Context, string, string) (auditledger.LedgerEntry, error) {
	return auditledger.LedgerEntry{}, auditledger.ErrLedgerEntryNotFound
}

func (l *failingStreamAppendLedger) LatestMerkleRoot(context.Context) ([32]byte, error) {
	return auditledger.ZeroRoot, nil
}

func (l *failingStreamAppendLedger) Size(context.Context) int { return 0 }

type recordingStreamAuditLedgerDLQ struct {
	id     int64
	events []dlq.Event
	err    error
}

func (q *recordingStreamAuditLedgerDLQ) Enqueue(_ context.Context, event dlq.Event) (int64, error) {
	q.events = append(q.events, event)
	if q.err != nil {
		return 0, q.err
	}
	return q.id, nil
}

func responseHopTime(t *testing.T, entry auditledger.LedgerEntry) time.Time {
	t.Helper()
	for _, hop := range entry.HopChain {
		if hop.Hop != proto.HopResponse {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, hop.Timestamp)
		if err != nil {
			t.Fatalf("response hop timestamp parse: %v; hop=%+v", err, hop)
		}
		return ts
	}
	t.Fatalf("response hop missing from hop chain: %+v", entry.HopChain)
	return time.Time{}
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// errorThrowingAdapter 满足 proto.UpstreamAdapter；在配置的事件类型上抛出非断连错误，
// 强制产生 UNKNOWN_TERMINATION 终态。
type errorThrowingAdapter struct {
	throwOn string
}

func (a *errorThrowingAdapter) CanonicalToProviderRequest(_ context.Context, _ *proto.HCSF) ([]byte, []proto.ProtocolLossEntry, error) {
	return nil, nil, errors.New("not implemented")
}
func (a *errorThrowingAdapter) ProviderResponseToCanonical(_ context.Context, _ []byte) (*proto.HCSF, []proto.ProtocolLossEntry, error) {
	return nil, nil, errors.New("not implemented")
}
func (a *errorThrowingAdapter) ProviderEventToCanonicalEvents(_ context.Context, evt any, _ any) ([]any, []proto.ProtocolLossEntry, error) {
	raw, _ := evt.([]byte)
	if bytes.Contains(raw, []byte("\""+a.throwOn+"\"")) {
		return nil, nil, errors.New("synthetic adapter failure")
	}
	return nil, nil, nil
}
func (a *errorThrowingAdapter) FinalizeUpstreamStream(_ context.Context, _ any) ([]any, error) {
	return nil, nil
}

// TestNewUpstreamStateDifyChat 抓的回归(六站接线第 7 个易漏点):dify_chat
// 注册了专用 proto adapter 但 newUpstreamState 仍回落 *anthropic.UpstreamState
// → ProviderEventToCanonicalEvents 内 type assertion 失败,dify 流式链路收到
// 第一帧即报错。同时钉住 TenantID/AccountID/PrefixHash 三字段注入约定。
func TestNewUpstreamStateDifyChat(t *testing.T) {
	f := newForwarder()
	state := f.newUpstreamState(ForwardRequest{
		ProtocolFamily: "dify_chat",
		TenantID:       7,
		AccountID:      21,
		SessionHash:    "prefix-h",
	})
	st, ok := state.(*protodify.UpstreamState)
	if !ok {
		t.Fatalf("state 类型=%T 期望 *dify.UpstreamState(回落别家 state 会让 dify adapter type assertion 失败)", state)
	}
	if st.TenantID != 7 || st.AccountID != 21 || st.PrefixHash != "prefix-h" {
		t.Fatalf("forwarder 注入字段不齐: %+v", st)
	}
}

// TestNewUpstreamStateOllamaNative 抓的回归(八站接线第 7 站):ollama_native
// 注册了专用 proto adapter 但 newUpstreamState 仍回落 *anthropic.UpstreamState
// → ProviderEventToCanonicalEvents 内 type assertion 失败,ollama 流式链路
// 收到第一帧即报错。同时钉住 TenantID/AccountID/PrefixHash 三字段注入约定。
func TestNewUpstreamStateOllamaNative(t *testing.T) {
	f := newForwarder()
	state := f.newUpstreamState(ForwardRequest{
		ProtocolFamily: "ollama_native",
		TenantID:       3,
		AccountID:      17,
		SessionHash:    "prefix-o",
	})
	st, ok := state.(*protoollamafwd.UpstreamState)
	if !ok {
		t.Fatalf("state 类型=%T 期望 *ollama.UpstreamState(回落别家 state 会让 ollama adapter type assertion 失败)", state)
	}
	if st.TenantID != 3 || st.AccountID != 17 || st.PrefixHash != "prefix-o" {
		t.Fatalf("forwarder 注入字段不齐: %+v", st)
	}
}

// slowReader 在 delay 内不产生任何数据，之后返回 EOF — 用于触发超时。
type slowReader struct {
	delay time.Duration
	fired bool
}

func newSlowReader(d time.Duration) *slowReader { return &slowReader{delay: d} }

func (s *slowReader) Read(p []byte) (int, error) {
	if !s.fired {
		s.fired = true
		time.Sleep(s.delay)
	}
	return 0, io.EOF
}
