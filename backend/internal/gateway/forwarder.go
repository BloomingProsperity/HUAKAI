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

// StreamForwarder runs the F-GW-002 Phase A-D streaming pipeline.
type StreamForwarder struct {
	UpstreamAdapter  proto.UpstreamAdapter
	ClientAdapter    proto.ClientAdapter
	Timeouts         TimeoutConfig
	ScannerBufferCap int
	DrainBudgets     DrainBudgets
	// CostEstimator returns the estimated cost given drained bytes + accumulator
	// state. When DrainBudgets.MaxEstimatedCost > 0 and the estimator's value
	// exceeds the cap, drain exits with DrainBudgetCostExhausted. Nil estimator
	// disables cost-based drain exit.
	CostEstimator func(drainedBytes int64, acc UsageAccumulator) decimal.Decimal
}

// Forward executes F-GW-002 Phase A scan, Phase B processing, Phase C
// classification, Phase C-bis drain, and returns the Phase D draft.
func (f *StreamForwarder) Forward(ctx context.Context, upstreamReader io.Reader, clientWriter http.ResponseWriter, req ForwardRequest) (UsageRecordDraft, error) {
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
				seen, wrote, err := f.handleEvent(upstreamCtx, res.event, clientWriter, upstreamState, clientState, &acc)
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
					draft.DrainOutcome = f.drain(upstreamCtx, events, upstreamState, &acc)
				} else {
					draft.EndClass, endErr = UnknownTermination, err
				}
			}
		}
		return f.finishDraft(draft, acc, start, endErr)
	}
}

func (f *StreamForwarder) handleEvent(ctx context.Context, evt SSEEvent, w http.ResponseWriter, upstreamState any, clientState any, acc *UsageAccumulator) (bool, bool, error) {
	terminalSeen := evt.Type == "message_stop" || string(evt.Data) == "[DONE]"
	if f.UpstreamAdapter == nil {
		if err := writeAndFlush(w, rawSSE(evt)); err != nil {
			return terminalSeen, false, ErrClientDisconnect
		}
		return terminalSeen, true, nil
	}
	canonicalEvents, _, err := f.UpstreamAdapter.ProviderEventToCanonicalEvents(ctx, evt.Data, upstreamState)
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

func (f *StreamForwarder) clientChunks(ctx context.Context, canonical any, state any, fallback SSEEvent) ([][]byte, error) {
	if f.ClientAdapter == nil {
		return [][]byte{rawSSE(fallback)}, nil
	}
	chunks, _, err := f.ClientAdapter.CanonicalEventToClientChunk(ctx, canonical, state)
	return chunks, err
}

func (f *StreamForwarder) drain(ctx context.Context, events <-chan scanResult, upstreamState any, acc *UsageAccumulator) DrainOutcome {
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
			if f.UpstreamAdapter != nil {
				canonicalEvents, _, err := f.UpstreamAdapter.ProviderEventToCanonicalEvents(ctx, res.event.Data, upstreamState)
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

func (f *StreamForwarder) newUpstreamState(req ForwardRequest) any {
	if req.UpstreamProtocol == "anthropic" || req.UpstreamProtocol == "anthropic_messages" {
		return &proto.UpstreamState{}
	}
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
