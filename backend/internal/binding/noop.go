// noop.go — U1-A atomic：BindingCache 的 noop 实现。
//
// 用途：
//   - production 启动期默认注入，让 binding-aware 调用方代码可以无前置条件
//     地写 cache.Lookup() 而不必判 nil
//   - 单测 stub：测试场景不需要真 binding 时 inject NoopCache 即可
//   - U1-B/U1-C 实施时可用 noop 作 baseline benchmark
//
// 行为契约：
//   - Lookup 永远返回 (zero, false, nil) — 没有 binding，不是错误
//   - 不持有 state，并发安全
//   - 零分配，O(1)
package binding

import "context"

// NoopCache 是 BindingCache 的零功能实现：所有 Lookup 都 miss。
//
// 用法：
//
//	cache := binding.NoopCache{}
//	snap, hit, err := cache.Lookup(ctx, binding.BindingScope{TenantID: 1})
//	// snap = zero, hit = false, err = nil
type NoopCache struct{}

// Lookup 总是 miss。无副作用。
func (NoopCache) Lookup(ctx context.Context, scope BindingScope) (BindingSnapshot, bool, error) {
	_ = ctx
	_ = scope
	return BindingSnapshot{}, false, nil
}

// 编译期 interface 断言。
var _ BindingCache = NoopCache{}
