package gatewayhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/observability"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

type concurrentSettler struct {
	calls int64
}

func (s *concurrentSettler) Settle(_ context.Context, _ billing.SettleRequest) (*billing.SettleResult, error) {
	atomic.AddInt64(&s.calls, 1)
	return &billing.SettleResult{}, nil
}

func (s *concurrentSettler) Abort(context.Context, int64, int64, string, string) error {
	return nil
}

func (s *concurrentSettler) CommitCacheHit(context.Context, billing.SettleRequest) error {
	return nil
}

func (s *concurrentSettler) Refund(context.Context, billing.RefundRequest) (*billing.RefundResult, error) {
	return &billing.RefundResult{}, nil
}

type fastCanonicalDispatcher struct{}

func (fastCanonicalDispatcher) DispatchHCSF(_ context.Context, requestEnvelope *proto.HCSF) (*proto.HCSF, error) {
	env := proto.NewEmptyEnvelope()
	env.RequestMeta = requestEnvelope.RequestMeta
	env.BufferedResponse = &proto.CanonicalResponse{
		ID:         "chatcmpl-eventbus-test",
		Model:      requestEnvelope.RequestMeta.UpstreamModel,
		Content:    []proto.CanonicalContentBlock{{Type: "text", Text: "eventbus ok"}},
		Usage:      proto.CanonicalUsage{InputTokens: 1, OutputTokens: 1},
		StopReason: proto.CanonicalStopEndTurn,
	}
	env.Accounting.Usage = env.BufferedResponse.Usage
	env.Accounting.EvidenceLabel = proto.EvidenceMock
	return env, nil
}

func TestChatCompletionsEventBusHotPathIgnoresSlowSuffix(t *testing.T) {
	enableHCSFDispatchForTest(t)
	settler := &concurrentSettler{}
	bus := eventbus.New(eventbus.Config{
		HighWorkers:          2,
		LowWorkers:           1,
		HighBuffer:           128,
		LowBuffer:            2,
		HandlerTimeout:       250 * time.Millisecond,
		ShutdownDrainTimeout: 20 * time.Millisecond,
	})
	mustRegisterEventHandler(t, bus, observability.NewBillingPersisterHandler(settler, 250*time.Millisecond))
	mustRegisterEventHandler(t, bus, observability.NewAuditLoggerHandler(250*time.Millisecond))
	mustRegisterEventHandler(t, bus, eventbus.HandlerFunc{
		HandlerID:      eventbus.HandlerMetricsAggregator,
		HandlerTier:    eventbus.TierLow,
		HandlerOrder:   50,
		HandlerTimeout: 250 * time.Millisecond,
		Fn: func(ctx context.Context, _ eventbus.RequestCompletionEvent) error {
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		},
	})
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_ = bus.Stop(ctx)
	}()

	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = fastCanonicalDispatcher{}
	d.Settler = settler
	d.CompletionBus = bus
	handler := NewChatCompletionsHandler(d)

	const n = 100
	errs := make(chan error, n)
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler(rec, req)
			if rec.Code != http.StatusOK {
				errs <- fmt.Errorf("status=%d body=%s", rec.Code, rec.Body.String())
				return
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				errs <- err
			}
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("100 hot-path requests exceeded 2s; slow async suffix is leaking into response path")
	}
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("100 hot-path requests took %s; want under 2s", elapsed)
	}
	if got := atomic.LoadInt64(&settler.calls); got != n {
		t.Fatalf("settler calls=%d want %d", got, n)
	}
}

func mustRegisterEventHandler(t *testing.T, bus *eventbus.Bus, h eventbus.Handler) {
	t.Helper()
	if err := bus.Register(h); err != nil {
		t.Fatalf("register %s: %v", h.ID(), err)
	}
}
