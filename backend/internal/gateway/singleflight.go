// A07.2：SingleFlight 原语——对带 key 的工作做并发去重。
// 规格：docs/specs/upstream-credential-management.md §A07（不变量 4）/
// synthesis §1 A07。
//
// A07 三作用域刷新风暴控制器的地基原语。
// A07.1（TokenBucket）提供速率预算；本文件提供同 key 去重，
// 让 N 个并发调用方不会全都去执行那个带 key 的函数。
// A07.3（三作用域策略组合器）把两者接进 OAuth refresh 流程。
//
// 无 IO、无网络、不接触凭据：纯并发控制。
package gateway

import (
	"fmt"
	"sync"
)

// singleFlightCall 持有一次 Do 调用的进行中或已完成状态。
type singleFlightCall struct {
	wg  sync.WaitGroup
	val any
	err error
}

// SingleFlight 把同 key 的并发调用收敛，使 fn 至多执行一次；其余调用方
// 都等待并共享结果。被 A07 刷新风暴控制器用来对 per-account 刷新尝试去重。
type SingleFlight struct {
	mu    sync.Mutex
	calls map[string]*singleFlightCall
}

// NewSingleFlight 返回一个已初始化的 SingleFlight。
func NewSingleFlight() *SingleFlight {
	return &SingleFlight{calls: make(map[string]*singleFlightCall)}
}

// Do 在该 key 没有进行中调用时为其执行 fn。同 key 的并发调用方会等待这次
// 唯一的进行中执行并共享其结果。当调用方是跟随者（自己没有执行 fn）时，
// 返回的 shared 为 true。
//
// 若 fn panic，则该 panic 会被 recover，转成形如
// "singleflight: function panicked: <value>" 的 error，并广播给所有跟随者
// （这样它们绝不会被永久阻塞的调用一直挂着）。
func (sf *SingleFlight) Do(key string, fn func() (any, error)) (val any, err error, shared bool) {
	sf.mu.Lock()
	if call, ok := sf.calls[key]; ok {
		sf.mu.Unlock()
		call.wg.Wait()
		return call.val, call.err, true
	}

	call := &singleFlightCall{}
	call.wg.Add(1)
	sf.calls[key] = call
	sf.mu.Unlock()

	// 全 defer 模式：即使发生 panic，也会设置 val/err、清掉进行中条目，
	// 并通过 wg.Done 唤醒跟随者。顺序很重要——必须在 wg.Done 之前设置
	// val/err，跟随者才能看到最终结果。
	defer func() {
		if r := recover(); r != nil {
			val = nil
			err = fmt.Errorf("singleflight: function panicked: %v", r)
		}
		call.val = val
		call.err = err

		sf.mu.Lock()
		// 仅当该条目仍是我们的才删除；Forget() 可能已经替换了它。
		if sf.calls[key] == call {
			delete(sf.calls, key)
		}
		sf.mu.Unlock()

		call.wg.Done()
	}()

	val, err = fn()
	return val, err, false
}

// Forget 把 key 从进行中 map 移除，这样下一次针对该 key 的 Do 调用会重新
// 执行 fn，而不是搭上一个陈旧结果。可在某次 Do 进行中安全调用；进行中的
// 调用方仍看到原结果，但新调用方会开启一次全新执行。
func (sf *SingleFlight) Forget(key string) {
	sf.mu.Lock()
	delete(sf.calls, key)
	sf.mu.Unlock()
}

// InFlight 报告该 key 是否当前有正在进行的调用。
// 非-.
func (sf *SingleFlight) InFlight(key string) bool {
	sf.mu.Lock()
	_, ok := sf.calls[key]
	sf.mu.Unlock()
	return ok
}
