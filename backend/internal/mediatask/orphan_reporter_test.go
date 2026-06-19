package mediatask

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeOrphanPersister 捕获 PersistOrphan 入参,并可注入错误以验证 fire-and-forget 行为。
type fakeOrphanPersister struct {
	calls int
	last  OrphanRecord
	err   error
}

func (f *fakeOrphanPersister) PersistOrphan(_ context.Context, rec OrphanRecord) error {
	f.calls++
	f.last = rec
	return f.err
}

// TestPersistingOrphanReporter_MapsEventToRecord 守"孤儿事件被正确映射成落库记录"。
// 判别核心:逐字段断言 OrphanProviderTask→OrphanRecord 的映射;若把任一字段映射写错
// (如 LeaseOwner 误取 Provider、丢掉 ObservedAt)→ 本测试变红。
func TestPersistingOrphanReporter_MapsEventToRecord(t *testing.T) {
	persister := &fakeOrphanPersister{}
	reporter := NewPersistingOrphanReporter(persister, nil)
	observed := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)

	reporter.ReportOrphanProviderTask(context.Background(), OrphanProviderTask{
		TaskID: 9, TenantID: 7, UserID: 42, Provider: "midjourney",
		ProviderTaskID: "up-orphan-9", Owner: "wA", ObservedAt: observed,
	})

	if persister.calls != 1 {
		t.Fatalf("PersistOrphan 调用次数=%d want 1", persister.calls)
	}
	got := persister.last
	if got.TaskID != 9 || got.TenantID != 7 || got.UserID != 42 ||
		got.Provider != "midjourney" || got.ProviderTaskID != "up-orphan-9" ||
		got.LeaseOwner != "wA" || !got.ObservedAt.Equal(observed) {
		t.Fatalf("孤儿事件映射错误 got=%+v", got)
	}
}

// TestPersistingOrphanReporter_PersistErrorDoesNotPropagate 守"落库报错只记日志、不 panic、不阻塞"
// (OrphanReporter 契约:不得阻塞/拖垮 worker 主循环)。判别核心:注入 PersistOrphan 错误后调用
// 仍正常返回且确已尝试一次;若把错误分支改成 panic/向上抛 → 本测试变红。
func TestPersistingOrphanReporter_PersistErrorDoesNotPropagate(t *testing.T) {
	persister := &fakeOrphanPersister{err: errors.New("db down")}
	reporter := NewPersistingOrphanReporter(persister, nil)

	reporter.ReportOrphanProviderTask(context.Background(), OrphanProviderTask{
		TaskID: 1, ProviderTaskID: "x", ObservedAt: time.Now(),
	})

	if persister.calls != 1 {
		t.Fatalf("即便落库报错也应尝试持久化一次,calls=%d", persister.calls)
	}
}

// TestPersistingOrphanReporter_NilStoreNoop 守"未配置持久化(store=nil)时静默跳过、不 panic"。
func TestPersistingOrphanReporter_NilStoreNoop(t *testing.T) {
	reporter := NewPersistingOrphanReporter(nil, nil)
	reporter.ReportOrphanProviderTask(context.Background(), OrphanProviderTask{TaskID: 1, ProviderTaskID: "x"})
}
