package hermesadmin

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/email"
)

// fakeSender records every send call (tenant, recipient, subject, body) and can
// be configured to fail.
type fakeSender struct {
	mu    sync.Mutex
	calls []email.Message
	err   error
}

func (f *fakeSender) SendTenantMessage(_ context.Context, tenantID int64, msg email.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	msg.TenantID = tenantID
	f.calls = append(f.calls, msg)
	return f.err
}

func (f *fakeSender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeSender) last() email.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

func newTestWorker(sender MessageSender, recipient string) *InspectionWorker {
	return NewInspectionWorker(InspectionWorkerConfig{
		Service:   NewInspectionService(healthySources(), testTenant, fixedNow),
		Sender:    sender,
		Recipient: recipient,
		TenantID:  testTenant,
	})
}

// TestWorkerTickSendsOnce: one tick runs the inspection and calls the sender
// exactly once with the resolved recipient + a non-empty body; reports_sent
// increments, failed stays 0.
// Regression caught: if the tick double-sent (e.g. once per section) or sent to
// the wrong recipient, the call count / recipient assertion goes red.
func TestWorkerTickSendsOnce(t *testing.T) {
	sender := &fakeSender{}
	w := newTestWorker(sender, "ops@huakai.test")
	w.TickOnce(context.Background())

	if sender.count() != 1 {
		t.Fatalf("expected exactly 1 send, got %d", sender.count())
	}
	msg := sender.last()
	if msg.To != "ops@huakai.test" {
		t.Fatalf("expected recipient ops@huakai.test, got %q", msg.To)
	}
	if msg.TenantID != testTenant {
		t.Fatalf("expected tenant %d, got %d", testTenant, msg.TenantID)
	}
	if len(msg.HTMLBody) == 0 || len(msg.Subject) == 0 {
		t.Fatalf("expected non-empty subject+body, got subject=%q body-len=%d", msg.Subject, len(msg.HTMLBody))
	}
	if w.ReportsSent() != 1 || w.FailedTicks() != 0 {
		t.Fatalf("expected sent=1 failed=0, got sent=%d failed=%d", w.ReportsSent(), w.FailedTicks())
	}
}

// TestWorkerSendErrorCountsAndContinues: a send error increments failed_ticks,
// leaves reports_sent at 0, and the loop survives a second tick (no panic / no
// stop).
// Regression caught: if a send error propagated as a panic or stopped the loop,
// the second tick would not run and TickCount would be 1 not 2.
func TestWorkerSendErrorCountsAndContinues(t *testing.T) {
	sender := &fakeSender{err: errors.New("smtp down")}
	w := newTestWorker(sender, "ops@huakai.test")

	w.TickOnce(context.Background())
	w.TickOnce(context.Background())

	if w.FailedTicks() != 2 {
		t.Fatalf("expected failed=2, got %d", w.FailedTicks())
	}
	if w.ReportsSent() != 0 {
		t.Fatalf("expected sent=0 on send errors, got %d", w.ReportsSent())
	}
	if w.TickCount() != 2 {
		t.Fatalf("expected 2 ticks (loop survived send error), got %d", w.TickCount())
	}
}

// TestWorkerRecorderReceivesSanitizedOutcome: the recorder is invoked with the
// run outcome and the failure class is the fixed "send_failed" enum, never the
// underlying error text.
// Regression caught: if the worker passed err.Error() into the outcome, the raw
// "smtp down" text (a potential leak vector) would appear instead of the enum.
func TestWorkerRecorderReceivesSanitizedOutcome(t *testing.T) {
	var got RunOutcome
	rec := captureRecorder{out: &got}
	w := NewInspectionWorker(InspectionWorkerConfig{
		Service:   NewInspectionService(healthySources(), testTenant, fixedNow),
		Sender:    &fakeSender{err: errors.New("smtp down secret-host:25")},
		Recorder:  rec,
		Recipient: "ops@huakai.test",
		TenantID:  testTenant,
	})
	w.TickOnce(context.Background())

	if got.Sent {
		t.Fatalf("expected sent=false on send error")
	}
	if got.FailureClass != "send_failed" {
		t.Fatalf("expected fixed failure class, got %q", got.FailureClass)
	}
}

// TestWorkerNotStartedWhenNoRecipient: Start with an empty recipient is a no-op
// (the loop never runs), even though the worker is otherwise wired. No panic.
// Regression caught: if Start did not gate on the recipient, an enabled-but-
// unconfigured deployment would start ticking and attempt sends to "".
func TestWorkerNotStartedWhenNoRecipient(t *testing.T) {
	sender := &fakeSender{}
	w := newTestWorker(sender, "")
	w.Start(context.Background())
	defer w.Stop()

	if w.started {
		t.Fatalf("worker must not start without a recipient")
	}
	if sender.count() != 0 {
		t.Fatalf("no send should occur without a recipient, got %d", sender.count())
	}
}

// TestWorkerNotStartedWhenSenderNil: Start with a nil sender is a no-op (fail-
// safe). Regression caught: a nil-sender Start would later nil-panic on send.
func TestWorkerNotStartedWhenSenderNil(t *testing.T) {
	w := NewInspectionWorker(InspectionWorkerConfig{
		Service:   NewInspectionService(healthySources(), testTenant, fixedNow),
		Sender:    nil,
		Recipient: "ops@huakai.test",
	})
	w.Start(context.Background())
	if w.started {
		t.Fatalf("worker must not start without a sender")
	}
}

// TestWorkerStartStopLifecycle: Start runs the immediate tick (>=1 send), Stop
// returns cleanly, and a restart works.
// Regression caught: a Stop that deadlocked or a Start that did not run the
// immediate tick would hang or leave sent=0.
func TestWorkerStartStopLifecycle(t *testing.T) {
	sender := &fakeSender{}
	w := newTestWorker(sender, "ops@huakai.test")
	w.Start(context.Background())
	// Stop waits for the loop to exit; the immediate tick has run by then.
	w.Stop()
	if w.ReportsSent() < 1 {
		t.Fatalf("expected the immediate-on-start tick to send at least once, got %d", w.ReportsSent())
	}
	// Restart must work (Start after Stop).
	w.Start(context.Background())
	w.Stop()
	if w.ReportsSent() < 2 {
		t.Fatalf("expected a restart to tick again, got %d", w.ReportsSent())
	}
}
