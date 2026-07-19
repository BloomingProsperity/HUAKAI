package channelhealth

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// b9SerializationFailingStore 模拟真实 PostgresStore 行为:前 failsLeft 次 WithTx 在
// (相当于)Commit 处抛 40001 序列化失败(事务回滚,无状态落库),之后的调用正常提交。
// 这精确复刻同账号并发健康信号写在 SERIALIZABLE 下互撞、败者 40001 的场景。
type b9SerializationFailingStore struct {
	*MemoryStore
	failsLeft int
	txCalls   int
}

func (s *b9SerializationFailingStore) WithTx(ctx context.Context, fn func(Store) error) error {
	s.txCalls++
	if s.failsLeft > 0 {
		s.failsLeft--
		// 序列化冲突在 Commit 时抛出并回滚:不运行 fn,无任何写入落库——与丢样本等价。
		return &pgconn.PgError{
			Code:    "40001",
			Message: "could not serialize access due to read/write dependencies among transactions",
		}
	}
	return fn(s)
}

// TestChannelHealth_B9_SerializationFailureRetried 守卫 B9[S3]:
// 健康信号写运行在 SERIALIZABLE 事务里,同账号并发突发(正是应触发冷却的 429/5xx 风暴)
// 会让写入互撞、败者 Commit 抛 40001。withMutation 必须在 40001 上有限重试;否则该样本被
// 静默丢弃(生产唯一调用方 chat_completions_error.go:216 丢弃返回),延迟 CoolingDown/
// Disabled 生效,已降级账号继续被选中。
//
// 缺陷代码(withMutation 只调 WithTx 一次、无重试)下:ApplySignal 直接把首个 40001
// 原样返回 → err != nil、txCalls == 1 → RED。
// 修复后:重试吸收瞬时冲突 → err == nil、txCalls == 3(2 次失败 + 1 次成功) → GREEN。
func TestChannelHealth_B9_SerializationFailureRetried(t *testing.T) {
	ctx := context.Background()
	clock := &fixedClock{now: time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)}
	store := &b9SerializationFailingStore{MemoryStore: NewMemoryStore(), failsLeft: 2}
	svc := NewService(store, testPolicy(), clock)
	key := testKey()

	if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalChannelError}); err != nil {
		t.Fatalf("ApplySignal must transparently retry 40001 serialization failure, got: %v", err)
	}
	if store.txCalls < 3 {
		t.Fatalf("expected retry after transient 40001 (>=3 tx attempts), got %d", store.txCalls)
	}
}
