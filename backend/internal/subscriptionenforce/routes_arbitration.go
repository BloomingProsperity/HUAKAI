// HUAKAI · iKun

package subscriptionenforce

// matchedRoute 是一条已命中请求 model 的有效路由的裁决输入(目标池 + 优先级)。
type matchedRoute struct {
	poolGroupID int64
	priority    int
}

// highestPriorityAllowed 实现 match_priority 真裁决(路由向导 slice B, Owner Q3):
// 在所有已命中本 model 的有效路由中, 只保留【最高优先档】的目标 pool_group。
//
// 方向：小值 = 高优先。它与 store List 的 match_priority 升序和数据库默认值 100 一致，故最高档 =
// 最小 match_priority。并列同档(优先级相等)取并集。
// 前置条件: priority 由写侧 (routeadmin.Service Create/Update) 应用层校验为 >=0; 本函数只做
// 纯整数比较 (不 panic、不校验), 负值若绕过写侧入库也只是按更小值当更高档处理, 不破坏不变量。
//
// 不变量(防 S0): 命中集非空 ⇒ 结果非空。最小档必然存在且至少含一个池; 任何把非空命中收成
// 空集的实现都是 bug。命中集为空时返回空集(非 nil), 与"已配置但本 model 未命中→拒"的白名单
// 语义一致。
//
// 行为变更(Owner 已拍板收窄): 旧逻辑放行全部命中池, 新逻辑只放最高档。全部为默认优先级(同值)
// 的配置因并列并集与旧全量集逐元素相等(向后兼容: 仅同档同模型多路由且优先级显式不同时才收窄)。
func highestPriorityAllowed(matched []matchedRoute) map[int64]struct{} {
	allowed := make(map[int64]struct{})
	best := 0
	haveBest := false
	for _, m := range matched {
		switch {
		case !haveBest || m.priority < best:
			// 发现更高优先档(更小值): 重置为只含本池的新档。
			best = m.priority
			haveBest = true
			allowed = map[int64]struct{}{m.poolGroupID: {}}
		case m.priority == best:
			// 并列同档: 并入。
			allowed[m.poolGroupID] = struct{}{}
			// m.priority > best: 低优先档, 丢弃。
		}
	}
	return allowed
}
