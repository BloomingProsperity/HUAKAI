package mediatask

import (
	"context"
	"testing"
	"time"
)

// panicConfigSource 的 Load 必 panic,用于在 RunOnce 内部触发 panic(配置加载是 RunOnce 通过 nil 校验后
// 的第一步)。
type panicConfigSource struct{}

func (panicConfigSource) Load(context.Context) (Config, error) { panic("boom: config load 故意 panic") }

// TestWorker_RunOnceRecoveredSurvivesPanic 抓对抗 bug-hunt S3:
// worker 主循环单轮 RunOnce 内任意位置的 panic 必须被 recover、绝不杀死 worker goroutine。否则 loop 的
// defer close(w.done) 触发但内部状态僵死,媒体任务永久停滞、已 Reserve 的预扣久挂。
// §14 变异:删 runOnceRecovered 的 recover → panic 传播至测试函数 → 测试崩溃为 FAIL(判别成立)。
func TestWorker_RunOnceRecoveredSurvivesPanic(t *testing.T) {
	store := newWorkerStore(Task{}) // 不会被走到(configs.Load 先 panic),仅满足非 nil 校验
	w := NewWorker(store, panicConfigSource{}, StaticProviderRegistry{}, WorkerOptions{
		Owner: "w",
		Now:   func() time.Time { return time.Unix(0, 0).UTC() },
	})
	// 若 recover 缺失,下一行的 panic 会向上传播使测试崩溃;有 recover 则正常返回。
	w.runOnceRecovered(context.Background())
}
