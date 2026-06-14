package credentialworker

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	dbauth "github.com/BloomingProsperity/HUAKAI/internal/db/auth"
)

func WithAuditLedger(ledger auditledger.Ledger) Option {
	return func(s *Scheduler) { s.AuditLedger = ledger }
}

// WithRotationScan enables the CRED-288 scheduled rotation-due scan: each tick,
// after the refresh pass, active credentials whose issuance is older than maxAge
// are flagged needs_rotation (+ optional alert). maxAge <= 0 keeps it OFF, so it
// is strictly opt-in. limit caps rows flagged per tick (<=0 → store default).
func WithRotationScan(store RotationStore, maxAge time.Duration, limit int, alert RotationAlert) Option {
	return func(s *Scheduler) {
		s.rotationStore = store
		s.rotationMaxAge = maxAge
		s.rotationLimit = limit
		s.rotationAlert = alert
	}
}

func WithInterval(d time.Duration) Option {
	return func(s *Scheduler) {
		if d > 0 {
			s.interval = d
		}
	}
}

func WithRefreshTimeout(d time.Duration) Option {
	return func(s *Scheduler) {
		if d > 0 {
			s.refreshTimeout = d
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

func WithRefreshQueries(q refreshQueries) Option {
	return func(s *Scheduler) { s.queryer = q }
}

func WithVendorRefresher(name string, r Refresher) Option {
	return func(s *Scheduler) {
		name = normalizeProviderName(name)
		if name == "" || r == nil {
			return
		}
		if s.vendorRefreshers == nil {
			s.vendorRefreshers = make(map[string]Refresher)
		}
		s.vendorRefreshers[name] = r
	}
}

func WithAuditQueries(q *dbauth.Queries) Option {
	return func(s *Scheduler) {
		if q != nil {
			s.auditWriter = dbAuditWriter{queries: q}
			s.auditQueries = q // 同时供 tx 路径 gate 检查 (RR-W5-002 步骤 3)
		}
	}
}

// WithTxPool 注入 pgxpool 供 recordAudit 同事务路径 (RR-W5-002 步骤 1)。
// 必须配合 WithAuditQueries + WithAuditLedgerSigner 才会启用 tx 模式;否则
// recordAudit 退回 legacy 2-step 路径。
func WithTxPool(pool *pgxpool.Pool) Option {
	return func(s *Scheduler) {
		if pool != nil {
			s.txPool = pool
		}
	}
}

// WithAuditLedgerSigner 注入 ledger signer (auditledger.AppendInTransaction
// 接受 any 类型);production 必须装,signer nil → tx 路径不启用。
func WithAuditLedgerSigner(signer any) Option {
	return func(s *Scheduler) {
		if signer != nil {
			s.auditSigner = signer
		}
	}
}

func WithProviderAccountHealthPolicy(policy ProviderAccountHealthPolicy) Option {
	return func(s *Scheduler) {
		s.healthPolicy = policy.normalized()
	}
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

func withProviderAccountHealthStore(store providerAccountHealthStore) Option {
	return func(s *Scheduler) { s.healthStore = store }
}

// WithProviderAccountDownDeliverer wires the operator-alert deliverer fired when
// a credential-refresh health transition raises the Alert flag (CRED-293).
// nil-safe: passing nil leaves alerts log-only.
func WithProviderAccountDownDeliverer(d ProviderAccountDownDeliverer) Option {
	return func(s *Scheduler) {
		if d != nil {
			s.alertDeliverer = d
		}
	}
}

// withAlertAsync injects a synchronous alert runner for deterministic tests;
// production leaves it unset so the best-effort send uses a detached goroutine.
func withAlertAsync(run func(func())) Option {
	return func(s *Scheduler) {
		if run != nil {
			s.alertAsync = run
		}
	}
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
