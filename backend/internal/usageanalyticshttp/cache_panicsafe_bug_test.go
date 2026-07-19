package usageanalyticshttp

import (
	"testing"
	"time"
)

// TestGetOrLoad_PanicSafe_S2 证明审计 S2.5:
// GetOrLoad 调 loader() 前注册了 state.inflight[key] 但没有 panic-safe 的清理。
// 若 loader() panic,后面的 delete(inflight)+wg.Done() 永不执行 → 该 key 的 inflight
// 条目孤立、WaitGroup 卡在 1,之后每个同 key 请求都永久阻塞在 call.wg.Wait()
// (死锁 + 每请求泄漏一个 goroutine)。net/http 只 recover 触发 panic 的那一个请求。
//
// 判别:第一次 loader panic 后,第二次同 key 调用必须仍能正常返回。
// 修复前第二次永久死锁 → 测试 5s 超时 RED;修复后正常返回 → GREEN。
func TestGetOrLoad_PanicSafe_S2(t *testing.T) {
	const key = "s2-panic-safe-unique-key"

	// 第一次:loader panic;用 recover 吞掉,模拟 net/http 对该请求的 recover。
	func() {
		defer func() { _ = recover() }()
		_, _, _ = GetOrLoad(key, time.Minute, func() (any, error) { panic("boom") })
	}()

	// 第二次:同 key 正常 loader。若 inflight 未清理(bug),这里永久卡在 call.wg.Wait()。
	done := make(chan struct{})
	go func() {
		v, _, err := GetOrLoad(key, time.Minute, func() (any, error) { return 123, nil })
		if err == nil && v == 123 {
			close(done)
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("BUG(S2): loader panic 后同 key 永久死锁 —— GetOrLoad 缺 panic-safe defer,inflight 未清理")
	}
}
