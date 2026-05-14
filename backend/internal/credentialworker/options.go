package credentialworker

import (
	"context"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
)

func WithAuditLedger(ledger auditledger.Ledger) Option {
	return func(s *Scheduler) { s.AuditLedger = ledger }
}

func WithInterval(d time.Duration) Option {
	return func(s *Scheduler) {
		if d > 0 {
			s.interval = d
		}
	}
}

func WithWarningWindow(d time.Duration) Option {
	return func(s *Scheduler) {
		if d > 0 {
			s.warningWindow = d
		}
	}
}

func WithMaxAttempts(n int) Option {
	return func(s *Scheduler) {
		if n > 0 {
			s.maxAttempts = n
		}
	}
}

func WithBackoff(fn func(attempt int) time.Duration) Option {
	return func(s *Scheduler) {
		if fn != nil {
			s.backoff = fn
		}
	}
}

func WithTickChannel(ch <-chan time.Time) Option {
	return func(s *Scheduler) { s.ticks = ch }
}

func withRefreshQueries(q refreshQueries) Option {
	return func(s *Scheduler) { s.queryer = q }
}

func withStormAcquirer(a stormAcquirer) Option {
	return func(s *Scheduler) { s.acquirer = a }
}

func withAuditWriter(w auth.AuditWriter) Option {
	return func(s *Scheduler) { s.auditWriter = w }
}

func withSleep(fn func(context.Context, time.Duration) error) Option {
	return func(s *Scheduler) {
		if fn != nil {
			s.sleep = fn
		}
	}
}

func withNow(fn func() time.Time) Option {
	return func(s *Scheduler) {
		if fn != nil {
			s.now = fn
		}
	}
}
