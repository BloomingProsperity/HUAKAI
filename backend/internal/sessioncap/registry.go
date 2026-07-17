// 包 sessioncap 实现 per-account 并发 session 上限。
// 内存版 Registry 按账号追踪不同的活跃 sessionHash 值; 当账号已达到
// 其配置的上限时, pool gate 会拒绝新 session。
//
// 安全设计:
//   - 通过 provider_accounts 上的 max_sessions > 0 显式开启 (0 = 无限 = 当前
//     的行为, 一个真正的 no-op)。
//   - Fail-open: registry 为 nil 或发生任何错误 -> 账号仍保持可调度。
//   - 既有 session 始终放行: WouldExceed 在计数时排除当前
//     sessionHash, 因此 re-binding 请求绝不会被拒。
//   - 仅进程级: 每个 gateway 实例只追踪自己的 session。
//     fail-open + 显式开启使其在多实例部署中也安全。
package sessioncap

import (
	"sync"
	"time"
)

// DefaultIdleTTL 是空闲 session entry 过期前的时长。
const DefaultIdleTTL = 5 * time.Minute

// entry 持有某个 session 的 last-seen 时间戳。
type entry struct {
	lastSeen time.Time
}

// Registry 是按账号存储活跃 session 的线程安全内存存储。
// 零值不可用; 请使用 NewRegistry。
type Registry struct {
	mu sync.RWMutex
	// sessions 映射 accountID -> sessionHash -> entry
	sessions map[int64]map[string]entry
	idleTTL  time.Duration
	now      func() time.Time
}

// NewRegistry 用给定的 idle TTL 构造一个 Registry。
// idleTTL <= 0 时使用 DefaultIdleTTL。
func NewRegistry(idleTTL time.Duration) *Registry {
	if idleTTL <= 0 {
		idleTTL = DefaultIdleTTL
	}
	return &Registry{
		sessions: make(map[int64]map[string]entry),
		idleTTL:  idleTTL,
		now:      time.Now,
	}
}

// Register 为 (accountID, sessionHash) 添加或刷新 session。
// 幂等: 对同一对多次调用只会更新 lastSeen。
func (r *Registry) Register(accountID int64, sessionHash string) {
	if sessionHash == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.sessions[accountID]
	if m == nil {
		m = make(map[string]entry)
		r.sessions[accountID] = m
	}
	m[sessionHash] = entry{lastSeen: r.now()}
}

// WouldExceed 报告把 sessionHash 作为一个新 session 加入后, 是否会让
// 该账号超过最大并发 session 数。
//
// 当前 sessionHash 会被排除在计数之外 —— 该账号上已经活跃的
// session (或通过 stickiness 进行 re-binding 的) 绝不会被拒。
// 只有当账号已有 max 个不同的其它 session 时, 真正的新 session
// 才会被拒。
//
// TTL 过期在本次调用中惰性应用 (过期 entry 会被移除)。
func (r *Registry) WouldExceed(accountID int64, sessionHash string, max int) bool {
	if max <= 0 {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	m := r.sessions[accountID]
	if len(m) == 0 {
		return false
	}

	now := r.now()
	count := 0
	for hash, e := range m {
		if now.Sub(e.lastSeen) > r.idleTTL {
			// 惰性 TTL 过期
			delete(m, hash)
			continue
		}
		if hash == sessionHash {
			// 既有 session: 从上限计数中排除
			continue
		}
		count++
	}
	return count >= max
}
