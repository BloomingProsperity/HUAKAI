package dlq

import (
	"context"
	"testing"
	"time"
)

// panicRecordStore 内嵌 recordStore 接口(其余方法 nil 不被调用),仅令 Claim panic——它是 ProcessClaim
// 通过 nil 校验后的第一步存储调用。
type panicRecordStore struct{ recordStore }

func (panicRecordStore) Claim(context.Context, Lane, string, time.Duration) (*Record, error) {
	panic("boom: claim 故意 panic")
}

// TestWorker_ProcessClaimRecoveredSurvivesPanic 抓对抗 bug-hunt S3:
// 泳道单轮 ProcessClaim 内的 panic 必须被 recover、绝不杀死该泳道 worker goroutine(否则该泳道永久静默、
// DLQ 不再消费)。recover 后应把本轮当作一次失败返回(processed=false + err)使循环进入 IdleSleep。
// §14 变异:删 processClaimRecovered 的 recover → panic 传播 → 测试崩溃为 FAIL。
// 注:NewService 只接受具体 *Store,故此处白盒直接注入 service.store=panicRecordStore(同包可见)。
func TestWorker_ProcessClaimRecoveredSurvivesPanic(t *testing.T) {
	w := &Worker{
		service: &Service{store: panicRecordStore{}},
		cfg:     WorkerConfig{},
	}
	processed, err := w.processClaimRecovered(context.Background(), LaneLow, "wid")
	if processed {
		t.Fatalf("panic 轮应 processed=false, 实得 true")
	}
	if err == nil {
		t.Fatalf("panic 轮应返回非 nil err(使循环进入 IdleSleep 而非崩溃)")
	}
}
