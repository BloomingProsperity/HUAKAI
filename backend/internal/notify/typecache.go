package notify

import (
	"sync"
	"time"
)

// DefaultNotifyTypeCacheTTL 限定一个被缓存的 notify_type 在 NotifyLowBalance
// 重新读取数据库之前,可以为廉价的进程内短路把守多久。从 "none" 切换到某个活跃
// 渠道的用户,最多在这个时间窗内就能看到投递恢复;"none" 是迄今最常见的稳态,
// 因此该缓存在结算热路径上让这种情况免于触碰共享数据库连接池。
const DefaultNotifyTypeCacheTTL = 30 * time.Second

// notifyTypeCache 按 (tenant,user) 记忆最后一次观测到的 notify_type,
// 使结算热路径在通知被禁用时可以跳过 GetSettings 的数据库读取。它从不缓存
// 投递报文或密钥 —— 只缓存廉价的路由判别值 —— 因此活跃渠道总会落入一次
// 全新的数据库读取和完整校验。
type notifyTypeCache struct {
	ttl time.Duration
	mu  sync.Mutex
	m   map[notifyTypeCacheKey]notifyTypeCacheEntry
}

type notifyTypeCacheKey struct {
	tenantID int64
	userID   int64
}

type notifyTypeCacheEntry struct {
	notifyType Type
	storedAt   time.Time
}

func newNotifyTypeCache(ttl time.Duration) *notifyTypeCache {
	if ttl <= 0 {
		return nil
	}
	return &notifyTypeCache{
		ttl: ttl,
		m:   make(map[notifyTypeCacheKey]notifyTypeCacheEntry),
	}
}

// disabled reports whether a fresh cache entry says notifications are off for
// this (tenant,user). Only a fresh TypeNone entry returns true; everything else
// (miss, stale, or an active channel) returns false so the caller reads the DB.
func (c *notifyTypeCache) disabled(tenantID, userID int64, now time.Time) bool {
	if c == nil {
		return false
	}
	key := notifyTypeCacheKey{tenantID: tenantID, userID: userID}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.m[key]
	if !ok {
		return false
	}
	if now.Sub(entry.storedAt) >= c.ttl {
		delete(c.m, key)
		return false
	}
	return entry.notifyType == TypeNone
}

// store records the freshly observed notify_type for this (tenant,user).
func (c *notifyTypeCache) store(tenantID, userID int64, notifyType Type, now time.Time) {
	if c == nil {
		return
	}
	key := notifyTypeCacheKey{tenantID: tenantID, userID: userID}
	c.mu.Lock()
	c.m[key] = notifyTypeCacheEntry{notifyType: notifyType, storedAt: now}
	c.mu.Unlock()
}
