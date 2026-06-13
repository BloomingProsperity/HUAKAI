package hermesadmin

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/email"
)

// MessageSender is the narrow slice of the gateway notification email sender the
// worker needs. The live *email.AuthSender satisfies it via SendTenantMessage —
// the SAME tenant-aware send path (load SMTP settings, validate, SMTP dispatch,
// transient-failure -> outbox/DLQ retry) that subscription reminders reuse. The
// worker builds NO new transport.
type MessageSender interface {
	SendTenantMessage(ctx context.Context, tenantID int64, msg email.Message) error
}

// RunRecorder records the outcome of each inspection run (sent / failed). The
// default implementation is a structured zap log — no schema is added this wave
// (a log carries the same sanitized enums/counts an audit row would, and the
// report is emitted as an email regardless, so a DB row adds storage + a CHECK
// migration with no operator-visible benefit here). Injectable so tests can
// assert what is (and is NOT) recorded.
type RunRecorder interface {
	RecordRun(ctx context.Context, outcome RunOutcome)
}

// RunOutcome is the sanitized per-run record. It carries ONLY enums/counts and a
// fixed recipient-shape flag — never the report body, never the raw recipient
// address (only whether one resolved), never any diagnostic detail string.
type RunOutcome struct {
	At            time.Time
	Sent          bool
	IssueCount    int
	CriticalCount int
	SourceErrors  int
	HasRecipient  bool
	FailureClass  string // "" on success; a fixed class on failure (no error text)
}

// zapRecorder is the default structured-log recorder.
type zapRecorder struct{ log *zap.Logger }

func (z zapRecorder) RecordRun(_ context.Context, o RunOutcome) {
	if z.log == nil {
		return
	}
	z.log.Info("hermes daily inspection run",
		zap.Bool("sent", o.Sent),
		zap.Int("issue_count", o.IssueCount),
		zap.Int("critical_count", o.CriticalCount),
		zap.Int("source_errors", o.SourceErrors),
		zap.Bool("has_recipient", o.HasRecipient),
		zap.String("failure_class", o.FailureClass),
	)
}

// InspectionWorker is the ExpiryWorker-pattern scheduled worker: a single
// ticker goroutine that, on each tick, runs the inspection, formats the report,
// and mails it to the resolved recipient. It never panics; a send failure logs +
// counts and the loop continues.
type InspectionWorker struct {
	svc       *InspectionService
	sender    MessageSender
	recorder  RunRecorder
	recipient string
	tenantID  int64
	interval  time.Duration
	log       *zap.Logger

	mu      sync.Mutex
	started bool
	stop    chan struct{}
	done    chan struct{}

	running     atomic.Bool   // re-entrance guard
	tickCount   atomic.Uint64 // total ticks
	reportsSent atomic.Uint64 // successful sends
	failedTicks atomic.Uint64 // ticks where the send failed
}

// InspectionWorkerConfig constructs the worker.
type InspectionWorkerConfig struct {
	Service   *InspectionService
	Sender    MessageSender
	Recorder  RunRecorder // nil => structured zap log
	Recipient string
	TenantID  int64
	Interval  time.Duration // <=0 => DefaultInterval
	Logger    *zap.Logger
}

// NewInspectionWorker builds the worker. It does NOT validate the recipient — the
// caller (wiring) resolves + checks the recipient and only constructs the worker
// when one exists, so an unconfigured deployment never reaches Start.
func NewInspectionWorker(cfg InspectionWorkerConfig) *InspectionWorker {
	interval := cfg.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	tenantID := cfg.TenantID
	if tenantID <= 0 {
		tenantID = 1
	}
	log := cfg.Logger
	if log == nil {
		log = zap.NewNop()
	}
	recorder := cfg.Recorder
	if recorder == nil {
		recorder = zapRecorder{log: log}
	}
	return &InspectionWorker{
		svc:       cfg.Service,
		sender:    cfg.Sender,
		recorder:  recorder,
		recipient: cfg.Recipient,
		tenantID:  tenantID,
		interval:  interval,
		log:       log,
	}
}

// Start launches the ticker goroutine. Repeated Start is a no-op; Stop then Start
// restarts. It refuses to start if the service/sender/recipient is missing — a
// hard fail-safe so a half-wired worker never spins burning ticks with no send
// target.
func (w *InspectionWorker) Start(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.started || w.svc == nil || w.sender == nil || w.recipient == "" {
		return
	}
	w.stop = make(chan struct{})
	w.done = make(chan struct{})
	w.started = true
	go w.loop(ctx)
}

func (w *InspectionWorker) loop(ctx context.Context) {
	defer close(w.done)
	// On exit, emit the cumulative run metrics so an operator can see how the
	// daily-inspection worker fared over its lifetime (ticks / reports accepted
	// for delivery / failed ticks) without a separate metrics surface.
	defer func() {
		w.log.Info("hermes daily inspection worker stopped",
			zap.Uint64("ticks", w.TickCount()),
			zap.Uint64("reports_sent", w.ReportsSent()),
			zap.Uint64("failed_ticks", w.FailedTicks()),
		)
	}()
	t := time.NewTicker(w.interval)
	defer t.Stop()
	w.tick(ctx) // run once immediately on start
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stop:
			return
		case <-t.C:
			w.tick(ctx)
		}
	}
}

// tick runs one inspection -> format -> send cycle. The re-entrance guard makes a
// slow tick skip rather than overlap. A panic anywhere in the cycle is recovered
// so one bad run can never crash the loop.
func (w *InspectionWorker) tick(ctx context.Context) {
	if !w.running.CompareAndSwap(false, true) {
		return
	}
	defer w.running.Store(false)
	defer func() {
		if rec := recover(); rec != nil {
			w.failedTicks.Add(1)
			w.log.Error("hermes daily inspection tick panicked", zap.Any("recover", rec))
		}
	}()
	w.tickCount.Add(1)

	report := w.svc.Inspect(ctx)
	formatted := Format(report)

	outcome := RunOutcome{
		At:            report.GeneratedAt,
		IssueCount:    report.IssueCount(),
		CriticalCount: report.CriticalCount(),
		SourceErrors:  len(report.SourceErrors),
		HasRecipient:  w.recipient != "",
	}

	err := w.sender.SendTenantMessage(ctx, w.tenantID, email.Message{
		TenantID: w.tenantID,
		To:       w.recipient,
		Subject:  formatted.Subject,
		HTMLBody: formatted.HTMLBody,
	})
	if err != nil {
		w.failedTicks.Add(1)
		outcome.Sent = false
		outcome.FailureClass = "send_failed"
		w.log.Warn("hermes daily inspection send failed", zap.Error(err))
	} else {
		w.reportsSent.Add(1)
		outcome.Sent = true
	}
	w.recorder.RecordRun(ctx, outcome)
}

// Stop gracefully stops the loop and waits for it to exit. Repeated Stop is a
// no-op.
func (w *InspectionWorker) Stop() {
	w.mu.Lock()
	if !w.started {
		w.mu.Unlock()
		return
	}
	close(w.stop)
	w.started = false
	doneChan := w.done
	w.mu.Unlock()
	if doneChan != nil {
		<-doneChan
	}
}

// TickCount returns the total ticks.
func (w *InspectionWorker) TickCount() uint64 { return w.tickCount.Load() }

// ReportsSent returns the count of successful sends.
func (w *InspectionWorker) ReportsSent() uint64 { return w.reportsSent.Load() }

// FailedTicks returns the count of ticks whose send failed (or panicked).
func (w *InspectionWorker) FailedTicks() uint64 { return w.failedTicks.Load() }
