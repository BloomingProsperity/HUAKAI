// windowcost 包实现按账号的 5 小时会话窗口花费上限。
// 一个后台 Worker 把 usage_records 中的 actual_cost 聚合进内存中的 Cache;
// pool gate 在热点选择路径上读取该缓存,不走 SQL。
//
// 安全设计:
//   - 经由 provider_accounts 上的 window_cost_limit_cents > 0 选择性启用(opt-in)。
//   - fail-open:缓存条目缺失/陈旧或 limit<=0 → 账号保持可用(eligible)。
//   - bug 只会让上限不那么有效,绝不会错误地把一个健康账号下线。
package windowcost

import (
	"sync"
	"time"
)

// 陈旧阈值:比此更老的缓存条目被视为陈旧,并触发 fail-open(账号保持可用),
// 直到 worker 刷新它为止。
const staleDuration = 3 * time.Minute

// entry 保存单个账号聚合后的窗口花费。
type entry struct {
	cents     int64
	updatedAt time.Time
}

// Cache 是按账号窗口花费的线程安全内存存储。
// 零值可直接使用(空缓存 → 对所有账号 fail-open)。
type Cache struct {
	mu      sync.RWMutex
	entries map[int64]entry
}

// NewCache 构造一个空的 Cache。
func NewCache() *Cache {
	return &Cache{entries: make(map[int64]entry)}
}

// Set 存储某账号的聚合花费。
func (c *Cache) Set(accountID, cents int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[int64]entry)
	}
	c.entries[accountID] = entry{cents: cents, updatedAt: time.Now()}
}

// CurrentCost 返回 (cents, fresh)。当条目缺失或比 staleDuration 更老时 fresh 为 false;
// 调用方必须把 fresh=false 当作 fail-open 处理。
func (c *Cache) CurrentCost(accountID int64) (cents int64, fresh bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[accountID]
	if !ok {
		return 0, false
	}
	if time.Since(e.updatedAt) > staleDuration {
		return 0, false
	}
	return e.cents, true
}

// CostReader 是 gate 使用的只读接口;允许注入 nil 以得到 fail-open 默认行为。
type CostReader interface {
	CurrentCost(accountID int64) (cents int64, fresh bool)
}
