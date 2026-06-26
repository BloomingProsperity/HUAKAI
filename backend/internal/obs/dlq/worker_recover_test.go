package dlq

import (
	"context"
	"testing"
)

// panicOutbox 内嵌 Outbox 接口(其余方法保持 nil 不被调用),仅令 Dequeue panic——它是 RunOnce 的第一步。
type panicOutbox struct{ Outbox }

func (panicOutbox) Dequeue(context.Context, DequeueOptions) (OutboxEvent, bool, error) {
	panic("boom: dequeue 故意 panic")
}

// TestWorker_RunOnceRecoveredSurvivesPanic 抓对抗 bug-hunt S3:
// 泳道单轮 RunOnce 内的 panic 必须被 recover、绝不杀死该优先级泳道 goroutine(否则该泳道永久静默、
// DLQ 积压不再被消费)。recover 后应把本轮当作一次失败返回(processed=false + err)使循环进入 IdleSleep。
// §14 变异:删 runOnceRecovered 的 recover → panic 传播 → 测试崩溃为 FAIL。
func TestWorker_RunOnceRecoveredSurvivesPanic(t *testing.T) {
	w := NewWorker(panicOutbox{}, WorkerConfig{})
	processed, err := w.runOnceRecovered(context.Background(), PriorityHigh, "wid")
	if processed {
		t.Fatalf("panic 轮应 processed=false, 实得 true")
	}
	if err == nil {
		t.Fatalf("panic 轮应返回非 nil err(使循环进入 IdleSleep 而非崩溃)")
	}
}
