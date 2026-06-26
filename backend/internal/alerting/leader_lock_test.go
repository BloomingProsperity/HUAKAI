package alerting

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

type fakeLeaderLock struct {
	acquired bool
	err      error
	calls    int
	released bool
}

func (l *fakeLeaderLock) TryAcquire(context.Context) (bool, func(), error) {
	l.calls++
	if l.err != nil {
		return false, nil, l.err
	}
	if !l.acquired {
		return false, nil, nil
	}
	return true, func() { l.released = true }, nil
}

func newLeaderLockScheduler(lock LeaderLock) (*Scheduler, *schedulerMetricSourceStub) {
	metric := &schedulerMetricSourceStub{snapshots: map[int64]map[string]float64{1: {"x": 1}}}
	return NewScheduler(SchedulerConfig{
		Evaluator:    &schedulerEvaluatorStub{called: make(chan struct{}, 10)},
		Store:        &schedulerTenantListerStub{tenants: []int64{1}},
		MetricSource: metric,
		LeaderLock:   lock,
	}), metric
}

// evaluated() 当且仅当 evaluateOnce 进入了按租户处理的逻辑（即 metric
// source 被快照过）时返回 true。
func evaluated(m *schedulerMetricSourceStub) bool { return len(m.tenants()) > 0 }

func TestScheduler_LeaderLock_LeaderEvaluatesAndReleases(t *testing.T) {
	lock := &fakeLeaderLock{acquired: true}
	s, metric := newLeaderLockScheduler(lock)
	s.evaluateOnce(context.Background())
	if !evaluated(metric) {
		t.Fatal("leader must evaluate the tick")
	}
	if !lock.released {
		t.Fatal("leader must release the lock after evaluating")
	}
}

// MUTATION：移除 `else if !acquired { return }` 分支会让非 leader 副本
// 也去评估——本测试转红（在本不该快照时 metric 被快照了）。
func TestScheduler_LeaderLock_NonLeaderSkips(t *testing.T) {
	lock := &fakeLeaderLock{acquired: false}
	s, metric := newLeaderLockScheduler(lock)
	s.evaluateOnce(context.Background())
	if evaluated(metric) {
		t.Fatal("a non-leader replica must skip evaluation (would emit duplicate alerts)")
	}
	if lock.calls != 1 {
		t.Fatalf("lock must be tried exactly once, got %d", lock.calls)
	}
}

// 锁故障必须 fail OPEN——照样评估，而不是悄悄丢掉告警。
func TestScheduler_LeaderLock_FailsOpenOnError(t *testing.T) {
	lock := &fakeLeaderLock{err: errors.New("lock backend down")}
	s, metric := newLeaderLockScheduler(lock)
	s.evaluateOnce(context.Background())
	if !evaluated(metric) {
		t.Fatal("a lock fault must fail open and still evaluate")
	}
}

// 未配置锁 = 与当前单副本行为完全一致（总是评估）。
func TestScheduler_LeaderLock_NilEvaluates(t *testing.T) {
	s, metric := newLeaderLockScheduler(nil)
	s.evaluateOnce(context.Background())
	if !evaluated(metric) {
		t.Fatal("nil leader lock must evaluate every tick")
	}
}

// 池为 nil 的 PostgresLeaderLock 是一个安全的唯一-leader 透传。
func TestPostgresLeaderLock_NilPool(t *testing.T) {
	var l *PostgresLeaderLock
	got, release, err := l.TryAcquire(context.Background())
	if err != nil || !got || release == nil {
		t.Fatalf("nil PostgresLeaderLock must act as sole leader, got=%v err=%v", got, err)
	}
	release() // 不得 panic
}

// 针对真实 Postgres，advisory lock 是互斥的：当一个持有者占有它时，第二次
// TryAcquire 返回 false，释放之后该锁再次可用。这正是用来在多副本间对告警
// 去重的特性。变异守卫：如果 TryAcquire 忽略 pg_try_advisory_lock 的结果而
// 总是返回 true，则“已持有”这一断言会转红。
func TestPostgresLeaderLock_MutualExclusionPG(t *testing.T) {
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer pool.Close()
	lock := NewPostgresLeaderLock(pool)

	got1, release1, err := lock.TryAcquire(ctx)
	if err != nil || !got1 {
		t.Fatalf("first acquire must win, got=%v err=%v", got1, err)
	}
	got2, release2, err := lock.TryAcquire(ctx)
	if err != nil {
		t.Fatalf("second try errored: %v", err)
	}
	if got2 {
		if release2 != nil {
			release2()
		}
		release1()
		t.Fatal("second acquire must fail while the lock is held (would allow duplicate alerts)")
	}
	release1()

	got3, release3, err := lock.TryAcquire(ctx)
	if err != nil || !got3 {
		t.Fatalf("acquire after release must win, got=%v err=%v", got3, err)
	}
	release3()
}
