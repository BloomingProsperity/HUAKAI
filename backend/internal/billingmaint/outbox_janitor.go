// Package billingmaint 承载 billing 域的表卫生后台工:周期修剪只增不删的辅助表,
// 防止随请求量无界增长。
package billingmaint

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	dbbillingmaint "github.com/BloomingProsperity/HUAKAI/internal/db/billingmaint"
)

const (
	defaultOutboxJanitorInterval = 10 * time.Minute
	// 未消费行保留窗口。outbox 设计消费延迟以秒计(lag 告警阈值默认 60s),
	// 7 天已是极保守上限——超龄未消费行只说明消费者长期缺位,失效通知早无价值。
	defaultOutboxUnconsumedRetention = 7 * 24 * time.Hour
	// 已消费行短期保留供排障,之后修剪。
	defaultOutboxConsumedRetention = 24 * time.Hour
	// 单批删除上限:首次启用面对历史积压时避免一次性长事务/WAL 尖峰,按批排空。
	defaultOutboxPruneBatch = 5000
)

// OutboxPruneStore 是 janitor 的存储面:按保留策略删一批,返回删除行数。
type OutboxPruneStore interface {
	PruneBatch(ctx context.Context, limit int64) (int64, error)
}

// PostgresOutboxPruneStore 用 db/billingmaint 查询实现修剪。
type PostgresOutboxPruneStore struct {
	q                   *dbbillingmaint.Queries
	unconsumedRetention time.Duration
	consumedRetention   time.Duration
}

// NewOutboxPruneStore 构造修剪存储;pool 为 nil 时返回未配置实例(PruneBatch no-op)。
func NewOutboxPruneStore(pool *pgxpool.Pool) *PostgresOutboxPruneStore {
	s := &PostgresOutboxPruneStore{
		unconsumedRetention: defaultOutboxUnconsumedRetention,
		consumedRetention:   defaultOutboxConsumedRetention,
	}
	if pool != nil {
		s.q = dbbillingmaint.New(pool)
	}
	return s
}

func (s *PostgresOutboxPruneStore) PruneBatch(ctx context.Context, limit int64) (int64, error) {
	if s == nil || s.q == nil {
		return 0, nil
	}
	now := time.Now().UTC()
	return s.q.PruneSchedulerOutboxRows(ctx, dbbillingmaint.PruneSchedulerOutboxRowsParams{
		CreatedBefore:  now.Add(-s.unconsumedRetention),
		ConsumedBefore: now.Add(-s.consumedRetention),
		BatchLimit:     limit,
	})
}

// SchedulerOutboxJanitor 周期修剪 scheduler_outbox。每 tick 按批排空到位
// (删满一批说明还有,继续;不足一批停),接 context cancellation 与 Stop 优雅退出。
type SchedulerOutboxJanitor struct {
	store    OutboxPruneStore
	interval time.Duration
	batch    int64

	mu      sync.Mutex
	running bool
	stop    chan struct{}
	done    chan struct{}
}

// NewSchedulerOutboxJanitor 构造修剪 worker;interval <= 0 用默认 10min,batch <= 0 用默认 5000。
func NewSchedulerOutboxJanitor(store OutboxPruneStore, interval time.Duration, batch int64) *SchedulerOutboxJanitor {
	if interval <= 0 {
		interval = defaultOutboxJanitorInterval
	}
	if batch <= 0 {
		batch = defaultOutboxPruneBatch
	}
	return &SchedulerOutboxJanitor{store: store, interval: interval, batch: batch}
}

// Start 启动 ticker goroutine。重复 Start no-op (idempotent)。
func (j *SchedulerOutboxJanitor) Start(ctx context.Context) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.running || j.store == nil {
		return
	}
	j.stop = make(chan struct{})
	j.done = make(chan struct{})
	j.running = true
	go j.loop(ctx)
}

func (j *SchedulerOutboxJanitor) loop(ctx context.Context) {
	defer close(j.done)
	t := time.NewTicker(j.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-j.stop:
			return
		case <-t.C:
			// best-effort: 失败 (DB 抖动) 等下个 tick 重试。
			j.sweepOnce(ctx)
		}
	}
}

// sweepOnce 排空一轮:删满一批说明还有积压继续删,不足一批即当前无更多可修剪。
// 每批之间检查取消信号,长积压排空可被优雅打断。
func (j *SchedulerOutboxJanitor) sweepOnce(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-j.stop:
			return
		default:
		}
		n, err := j.store.PruneBatch(ctx, j.batch)
		if err != nil || n < j.batch {
			return
		}
	}
}

// Stop 优雅停止, 等 loop 退出。 多次调 no-op (idempotent)。
func (j *SchedulerOutboxJanitor) Stop() {
	j.mu.Lock()
	if !j.running {
		j.mu.Unlock()
		return
	}
	close(j.stop)
	j.running = false
	done := j.done
	j.mu.Unlock()
	if done != nil {
		<-done
	}
}
