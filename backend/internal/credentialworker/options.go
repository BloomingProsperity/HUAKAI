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

// WithRotationScan 启用 CRED-288/288c 的定时 rotation-due 扫描:每个 tick,在
// refresh 过程之后,签发时间早于 maxAge 的凭据会被路由进恢复流程——可刷新的
// (OAuth/session)凭据被拉回现有 refresh 流,使其在保持服务的同时重新铸造一个
// 新 token;而静态 API key 只告警(绝不仅凭年龄就下线)。maxAge <= 0 时保持
// 关闭,因此严格属于 opt-in。limit 限制每个 tick 处理的行数(<=0 → 取 store 默认值)。
// 默认的可刷新性分类器读取 canonical 的 credentialstore mode-handler registry。
func WithRotationScan(store RotationStore, maxAge time.Duration, limit int, alert RotationAlert) Option {
	return func(s *Scheduler) {
		s.rotationStore = store
		s.rotationMaxAge = maxAge
		s.rotationLimit = limit
		s.rotationAlert = alert
		if s.rotationClassifier == nil {
			s.rotationClassifier = DefaultRefreshClassifier()
		}
	}
}

// withRotationClassifier 覆盖可刷新性分类器(仅供测试:让单元测试可以钉死哪个
// (vendor, auth_mode) 被当作可刷新,而无需搭起完整的 mode-handler registry)。
func withRotationClassifier(classifier RefreshClassifier) Option {
	return func(s *Scheduler) {
		if classifier != nil {
			s.rotationClassifier = classifier
		}
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

// WithProviderAccountDownDeliverer 接入 operator-alert deliverer,在一次
// credential-refresh 的 health 转换升起 Alert 标志时触发(CRED-293)。
// nil-safe:传 nil 时告警保持仅记录日志。
func WithProviderAccountDownDeliverer(d ProviderAccountDownDeliverer) Option {
	return func(s *Scheduler) {
		if d != nil {
			s.alertDeliverer = d
		}
	}
}

// withAlertAsync 为确定性测试注入一个同步的告警 runner;生产环境不设置它,
// 因此 best-effort 发送使用一个脱离的 goroutine。
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
