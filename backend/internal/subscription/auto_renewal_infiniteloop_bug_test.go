package subscription

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeUnrenewableStore 只实现 tick→ProcessAutoRenewal 用到的两个方法:
//   - ListAutoRenewDue 每次都返回一整批(batchSize 条)"到期且 auto_renew"的订阅,
//     模拟生产里 >=batchSize 条订阅因余额不足/套餐停用而【续不掉、也不离开 due 集合】。
//   - TryAutoRenewSubscription 每条都返回 Renewed=false(跳过,不可续)。
//
// 嵌入 Store 接口(nil)满足其余未被调用的方法;若哪个未预期方法被调会 nil-panic 暴露。
type fakeUnrenewableStore struct {
	Store
	batch      []UserSubscription
	listCalls  int
	breakAfter int // 安全阀:第 breakAfter 次 List 之后返回 err 打断循环,避免测试真挂死。
}

func (f *fakeUnrenewableStore) ListAutoRenewDue(_ context.Context, _ time.Time, _ int) ([]UserSubscription, error) {
	f.listCalls++
	if f.listCalls > f.breakAfter {
		return nil, errors.New("test-safety-stop") // 让 tick 走 err 分支返回,测试可终止。
	}
	return f.batch, nil
}

func (f *fakeUnrenewableStore) TryAutoRenewSubscription(_ context.Context, _ autoRenewRecord) (AutoRenewResult, error) {
	return AutoRenewResult{Renewed: false}, nil // 不可续 → Skipped
}

// TestAutoRenewTick_NoInfiniteLoopOnUnrenewableFullBatch_S1 证明审计 S1.3:
//
// tick() 的 for{} 只在 err!=nil 或 res.Scanned<batchSize 时退出。当 >=batchSize 条到期
// 订阅【全部续不掉】(Renewed=0, Scanned=batchSize)且不离开 due 集合时,ListAutoRenewDue
// 每轮返回同一整批 → scanned 恒 == batchSize → 无限循环(worker 卡死、CPU 空转)。
//
// 判别:断言 tick() 对"全不可续的满批"只应有界地扫少数几次(正确实现在无进展时应停,
// 或推进游标)。当前缺陷实现会一直循环,直到 fake 的安全阀强制报错才停 → listCalls 巨大
// → 测试 RED,实证 bug 真实。修复后 listCalls 会很小 → GREEN。
func TestAutoRenewTick_NoInfiniteLoopOnUnrenewableFullBatch_S1(t *testing.T) {
	const batchSize = 5
	fake := &fakeUnrenewableStore{
		batch:      make([]UserSubscription, batchSize), // 满批
		breakAfter: 1000,                                // 安全阀,防真死循环把测试挂死
	}
	svc := NewService(fake)
	w := NewAutoRenewWorker(AutoRenewWorkerConfig{Service: svc, BatchSize: batchSize})

	done := make(chan struct{})
	go func() { w.tick(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("tick 30s 未返回 —— 安全阀未生效")
	}

	const sane = 20 // 正确实现对全不可续满批应远少于此(~1 次)
	if fake.listCalls >= sane {
		t.Fatalf("BUG(S1) 实证:tick() 对全不可续满批无限循环 —— ListAutoRenewDue 被调 %d 次"+
			"(被安全阀 breakAfter=%d 强制打断才停;正确实现在无进展时应 <%d 次)。",
			fake.listCalls, fake.breakAfter, sane)
	}
}
