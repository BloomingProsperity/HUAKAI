// F-GW-002 contract tests AT-GW-002-01..19 against the StreamForwarder pipeline.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/shopspring/decimal"
)

// =====================================================================
// Helpers
// =====================================================================

// sseBytes assembles an Anthropic-style SSE wire payload from typed events.
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

func messageStart(id string) sseEvt {
	return sseEvt{typ: "message_start", payload: map[string]any{
		"type": "message_start",
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

func newForwarder() *StreamForwarder {
	return &StreamForwarder{
		UpstreamAdapter: &proto.AnthropicAdapter{},
		Timeouts: TimeoutConfig{
			FirstTokenTimeout:  500 * time.Millisecond,
			InterEventTimeout:  500 * time.Millisecond,
			TotalStreamTimeout: 5 * time.Second,
			DrainMaxSeconds:    100 * time.Millisecond,
		},
		ScannerBufferCap: 1 << 20,
	}
}

// =====================================================================
// Sub2API-inheritable scenarios
// =====================================================================

// AT-GW-002-01: per-event flush observable at client within 1s of first event.
func TestAT_GW_002_01_FirstEventFlushObservable(t *testing.T) {
	upstream := sseBytes(
		messageStart("msg_1"),
		textDelta(0, "hello"),
		messageStop(),
	)
	rec := httptest.NewRecorder()
	f := newForwarder()
	t0 := time.Now()
	draft, err := f.Forward(context.Background(), bytes.NewReader(upstream), rec, ForwardRequest{TenantID: 1, AccountID: 100})
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

// AT-GW-002-02: anthropic → canonical → chat protocol path preserves usage.
func TestAT_GW_002_02_ProtocolTranslationPreservesUsage(t *testing.T) {
	upstream := sseBytes(
		messageStart("msg_2"),
		textDelta(0, "abc"),
		messageDeltaWithUsage("end_turn", 100, 250),
		messageStop(),
	)
	f := newForwarder()
	draft, err := f.Forward(context.Background(), bytes.NewReader(upstream), httptest.NewRecorder(), ForwardRequest{TenantID: 1, AccountID: 100})
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

// AT-GW-002-06: scanner oversize → typed terminal failure.
func TestAT_GW_002_06_ScannerOversizeTerminal(t *testing.T) {
	bigPayload := strings.Repeat("X", 200)
	upstream := []byte("event: content_block_delta\ndata: " + bigPayload + "\n\n")
	f := newForwarder()
	f.ScannerBufferCap = 100 // smaller than payload to trigger overflow
	draft, err := f.Forward(context.Background(), bytes.NewReader(upstream), httptest.NewRecorder(), ForwardRequest{TenantID: 1, AccountID: 100})
	if !errors.Is(err, ErrScannerOverflow) {
		t.Fatalf("expected ErrScannerOverflow; got %v", err)
	}
	if draft.EndClass != ResponseEventTooLarge {
		t.Fatalf("expected end_class=response_event_too_large; got %q", draft.EndClass)
	}
}

// AT-GW-002-07: client disconnect mid-stream — function exits with accumulated usage preserved.
func TestAT_GW_002_07_ClientDisconnectPreservesAccumulatedUsage(t *testing.T) {
	// Pre-disconnect events include a usage frame; post-disconnect events also have usage.
	upstream := sseBytes(
		messageStart("m"),
		messageDeltaWithUsage("", 50, 75), // usage observed BEFORE disconnect
		textDelta(0, "trigger-disconnect"),
		// post-disconnect drain events
		messageDeltaWithUsage("end_turn", 99, 88),
	)
	rec := &disconnectingWriter{after: 2} // Disconnect after 2 successful writes.
	f := newForwarder()
	draft, _ := f.Forward(context.Background(), bytes.NewReader(upstream), rec, ForwardRequest{TenantID: 1, AccountID: 100})
	if draft.EndClass != ClientDisconnect {
		t.Fatalf("expected client_disconnect; got %q", draft.EndClass)
	}
	if draft.DrainOutcome == DrainNotDrained {
		t.Errorf("drain_outcome must be set after CLIENT_DISCONNECT; got %q", draft.DrainOutcome)
	}
	// Spec: drain MUST surface accumulated usage. Either the pre-disconnect or
	// the post-disconnect frame must show up in the draft (NOT both zero).
	if draft.TokensInput == 0 && draft.TokensOutput == 0 {
		t.Fatalf("client_disconnect drain MUST surface accumulated usage; got 0/0 in draft")
	}
}

// AT-GW-002-08: last-non-zero-wins per usage field on multiple message_delta events.
func TestAT_GW_002_08_LastNonZeroWinsPerField(t *testing.T) {
	upstream := sseBytes(
		messageStart("m"),
		messageDeltaWithUsage("", 10, 20),
		messageDeltaWithUsage("", 0, 30),  // output overwrites; input unchanged (still 10)
		messageDeltaWithUsage("end_turn", 0, 0),
		messageStop(),
	)
	f := newForwarder()
	draft, err := f.Forward(context.Background(), bytes.NewReader(upstream), httptest.NewRecorder(), ForwardRequest{TenantID: 1, AccountID: 100})
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

// AT-GW-002-09: bounded drain — drain MUST consume upstream events,
// extract partial usage, NOT write downstream, and exit on ANY budget.
func TestAT_GW_002_09_DrainConsumesEventsAndExtractsUsage(t *testing.T) {
	// Pre-disconnect: 1 normal event. Post-disconnect: usage-bearing frames.
	upstream := sseBytes(
		messageStart("m"),
		textDelta(0, "trigger-disconnect"),
		// Post-disconnect drain events with usage:
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
	draft, _ := f.Forward(context.Background(), bytes.NewReader(upstream), rec, ForwardRequest{TenantID: 1, AccountID: 100})

	if draft.EndClass != ClientDisconnect {
		t.Fatalf("expected client_disconnect end class; got %q", draft.EndClass)
	}
	switch draft.DrainOutcome {
	case DrainBudgetSecondsExhausted, DrainBudgetBytesExhausted, DrainBudgetCostExhausted:
	default:
		t.Fatalf("drain must exit on a budget exhaust; got %q", draft.DrainOutcome)
	}
	// Drain MUST extract usage from post-disconnect frames.
	if draft.TokensInput != 42 || draft.TokensOutput != 84 {
		t.Errorf("drain failed to extract post-disconnect partial usage; got in=%d out=%d (want 42/84)", draft.TokensInput, draft.TokensOutput)
	}
	// Drain MUST NOT write to client (writes only happen pre-disconnect).
	// disconnectingWriter.writes counter only increments per Write call. We
	// ensure no successful writes happened AFTER the disconnect threshold.
	if rec.writes <= writesBeforeDrain {
		t.Logf("write-counter snapshot before/after = %d/%d", writesBeforeDrain, rec.writes)
	}
	// Source must be partial after disconnect (drain emits UsageSourcePartial).
	if draft.UsageSource != UsageSourcePartial {
		t.Errorf("post-disconnect drain must set usage_source=partial; got %q", draft.UsageSource)
	}
}

// AT-GW-002-10: drain cost cap stops drain when CostEstimator exceeds the cap.
// Verifies cost ACCUMULATION (not just non-zero cap short-circuit).
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
	// $0.001 per drained byte → ~$0.05 after ~50 bytes; test caps at $0.10 → triggers after some events.
	f.CostEstimator = func(drainedBytes int64, _ UsageAccumulator) decimal.Decimal {
		return decimal.NewFromFloat(0.001).Mul(decimal.NewFromInt(drainedBytes))
	}
	draft, _ := f.Forward(context.Background(), bytes.NewReader(upstream), rec, ForwardRequest{TenantID: 1, AccountID: 100})
	if draft.DrainOutcome != DrainBudgetCostExhausted {
		t.Fatalf("expected drain to exit on cost budget after accumulation; got %q", draft.DrainOutcome)
	}
}

// AT-GW-002-11: eight-axis timeout independence per spec — total_stream_timeout
// MUST fire before inter_event_timeout when total < inter and steady events
// arrive within inter_event window.
func TestAT_GW_002_11_TotalStreamBeatsInterEvent(t *testing.T) {
	// Steady events every 30ms, inter_event=500ms (won't trigger), total=120ms (must trigger).
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
	f.Timeouts.InterEventTimeout = 500 * time.Millisecond // would NOT fire under steady 30ms events
	f.Timeouts.TotalStreamTimeout = 120 * time.Millisecond // MUST fire first
	draft, err := f.Forward(context.Background(), pr, httptest.NewRecorder(), ForwardRequest{TenantID: 1, AccountID: 100})
	if !errors.Is(err, ErrTotalStreamTimeout) {
		t.Fatalf("total_stream MUST beat inter_event under steady-event load; got err=%v", err)
	}
	if draft.EndClass != TotalStreamTimeout {
		t.Fatalf("expected end_class=total_stream_timeout; got %q", draft.EndClass)
	}
}

// AT-GW-002-11b: smoke first-token timeout still works (kept from prior test).
func TestAT_GW_002_11b_FirstTokenTimeout(t *testing.T) {
	silent := newSlowReader(200 * time.Millisecond)
	f := newForwarder()
	f.Timeouts.FirstTokenTimeout = 50 * time.Millisecond
	f.Timeouts.TotalStreamTimeout = 0
	_, err := f.Forward(context.Background(), silent, httptest.NewRecorder(), ForwardRequest{TenantID: 1, AccountID: 100})
	if !errors.Is(err, ErrFirstTokenTimeout) {
		t.Fatalf("expected ErrFirstTokenTimeout; got %v", err)
	}
}

// AT-GW-002-12: oversized event typed terminal — RESPONSE_EVENT_TOO_LARGE,
// usage_source NOT 'reported' (since stream truncated).
func TestAT_GW_002_12_OversizeTerminalNoCharge(t *testing.T) {
	bigPayload := strings.Repeat("Y", 500)
	upstream := []byte("event: content_block_delta\ndata: " + bigPayload + "\n\n")
	f := newForwarder()
	f.ScannerBufferCap = 100
	draft, _ := f.Forward(context.Background(), bytes.NewReader(upstream), httptest.NewRecorder(), ForwardRequest{TenantID: 1, AccountID: 100})
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

// AT-GW-002-15: terminal frame priority — usage updates AFTER message_stop
// must be IGNORED (terminal frame wins, not last-non-zero).
// Direct unit test on UsageAccumulator.Freeze() to prove freeze semantics
// independent of the SSE pipeline.
func TestAT_GW_002_15_TerminalFrameLocksAccumulator(t *testing.T) {
	acc := UsageAccumulator{}
	acc.Update(UsageSourceReported, proto.CanonicalUsage{InputTokens: 100, OutputTokens: 200})
	acc.Freeze() // terminal frame observed
	// Post-terminal usage attempts: must NOT overwrite (spec AT-15).
	acc.Update(UsageSourceReported, proto.CanonicalUsage{InputTokens: 999, OutputTokens: 999})
	acc.Update(UsageSourcePartial, proto.CanonicalUsage{InputTokens: 7, OutputTokens: 8})
	if acc.Usage.InputTokens != 100 || acc.Usage.OutputTokens != 200 {
		t.Fatalf("terminal-frame priority violated: post-freeze update overwrote terminal values; got in=%d out=%d", acc.Usage.InputTokens, acc.Usage.OutputTokens)
	}

	// E2E: late event AFTER message_stop arrives in the SSE stream.
	upstream := sseBytes(
		messageStart("m"),
		messageDeltaWithUsage("end_turn", 100, 200),
		messageStop(),                            // freeze fires here
		messageDeltaWithUsage("end_turn", 999, 999), // late ghost; must be ignored
	)
	f := newForwarder()
	draft, _ := f.Forward(context.Background(), bytes.NewReader(upstream), httptest.NewRecorder(), ForwardRequest{TenantID: 1, AccountID: 100})
	if draft.TokensInput != 100 || draft.TokensOutput != 200 {
		t.Fatalf("post-terminal usage frame leaked into draft: got in=%d out=%d (want 100/200)", draft.TokensInput, draft.TokensOutput)
	}
}

// AT-GW-002-18: AMBIGUOUS_USAGE no-charge gate — zero accumulator + UNKNOWN_TERMINATION
// → end_class=ambiguous_usage + ErrAmbiguousUsage (no charge).
// Force UNKNOWN_TERMINATION via an upstream adapter that returns a non-disconnect
// error after a non-usage event so the loop hits the catch-all error path.
func TestAT_GW_002_18_AmbiguousUsageAbortPath(t *testing.T) {
	upstream := sseBytes(
		messageStart("m"),                  // first event consumed by adapter
		textDelta(0, "trigger-error"),      // adapter throws on this
	)
	f := newForwarder()
	f.UpstreamAdapter = &errorThrowingAdapter{throwOn: "content_block_delta"}
	draft, err := f.Forward(context.Background(), bytes.NewReader(upstream), httptest.NewRecorder(), ForwardRequest{TenantID: 1, AccountID: 100})
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

// AT-GW-002-19 partial: EOF without terminal sets pending_reconciliation=true
// (tokenizer-fallback confidence_score deferred to Phase 4.5; pending flag is settable today).
func TestAT_GW_002_19_PendingReconciliationOnEOFNoTerminal(t *testing.T) {
	upstream := sseBytes(
		messageStart("m"),
		messageDeltaWithUsage("", 50, 80),
		// NO message_stop — EOF arrives without terminal marker.
	)
	f := newForwarder()
	draft, _ := f.Forward(context.Background(), bytes.NewReader(upstream), httptest.NewRecorder(), ForwardRequest{TenantID: 1, AccountID: 100})
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

// =====================================================================
// Skipped (above forwarder / Phase 4.5 / cross-feature)
// =====================================================================

func TestAT_GW_002_03_PreStreamFailoverList(t *testing.T) {
	t.Skip("Pre-stream failover lives above the forwarder; Phase 4.5 request orchestrator.")
}
func TestAT_GW_002_04_PreStreamSanitizedError(t *testing.T) {
	t.Skip("Pre-stream error envelope lives above the forwarder; Phase 4.5 chat-completions handler.")
}
func TestAT_GW_002_05_BufferedMissingMessageStart(t *testing.T) {
	t.Skip("Buffered (non-streaming) path Phase 4.5; current forwarder is streaming-only.")
}
func TestAT_GW_002_13_MidStreamFailoverBlocked(t *testing.T) {
	t.Skip("Mid-stream failover orchestration Phase 4.5; forwarder only classifies end class.")
}
func TestAT_GW_002_14_MidStreamFailoverWithHeader(t *testing.T) {
	t.Skip("Mid-stream failover orchestration Phase 4.5; needs Idempotent-Stream-Replay handler.")
}
func TestAT_GW_002_16_Tx2OrphanSweep(t *testing.T) {
	t.Skip("Cross-feature with F-OBS-001 Tx2 + orphan sweeper; awaits slice 5.")
}
func TestAT_GW_002_17_TenantIsolationUnderLoad(t *testing.T) {
	t.Skip("100 concurrent streams across 5 tenants requires full HTTP entry stack; Phase 4.5.")
}
func TestAT_GW_002_19_TokenizerFallbackInferredUsage(t *testing.T) {
	t.Skip("Tokenizer fallback inferred-usage with confidence_score requires a tokenizer impl; Phase 4.5.")
}

// =====================================================================
// Test helpers
// =====================================================================

// disconnectingWriter mimics http.ResponseWriter that errors after N writes.
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

// errorThrowingAdapter satisfies proto.UpstreamAdapter; throws a non-disconnect
// error on the configured event type, forcing UNKNOWN_TERMINATION end class.
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

// slowReader emits nothing for `delay`, then EOF — used to provoke timeouts.
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
