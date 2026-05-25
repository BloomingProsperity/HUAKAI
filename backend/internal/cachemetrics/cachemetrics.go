// Package cachemetrics — vendor prompt cache 命中率观测 (expvar 暴露)。
//
// Anthropic 在 message_delta usage 中携带 cache_creation_input_tokens (新写
// 入缓存的 prompt token) 与 cache_read_input_tokens (从缓存命中的 token)。
// HUAKAI proto adapter 解析后调本包累计到全局计数器, 通过 /debug/vars
// 暴露给运维:
//
//	"cache_token_count": {
//	  "creation_total":  N,
//	  "read_total":      M,
//	  "request_count":   K     // 总观测到 cache fields 的请求数
//	}
//
// 命中率粗算: read_total / (creation_total + read_total)。
//
// 为什么不分 tenant:
//   - 分 tenant cardinality 会爆 (多租户 → 多 expvar key)
//   - 如需 per-tenant, future U6+ 改 prometheus / 自定义 storage
//
// 为什么 stdlib expvar:
//   - 与 clientid/metrics 同模式 (sonnet U6-C SHOULD_FIX 已挂 /debug/vars)
//   - 零新依赖
package cachemetrics

import (
	"expvar"
	"strconv"
	"sync"
)

const (
	keyCreationTotal = "creation_total"
	keyReadTotal     = "read_total"
	keyRequestCount  = "request_count"
)

var (
	once              sync.Once
	counters          *expvar.Map
	countersByAccount *expvar.Map
	// accountMu 保护 countersByAccount 的 lazy-init Get/Set 序列;
	// expvar.Map 内部对单 key Get/Set 各自 thread-safe, 但 "Get-then-Set"
	// 这种复合操作不是 atomic, 多 goroutine 同 accountID 首次观测时可能
	// 都进入 nil 分支双 Set, 第一个 sub.Add 写到被覆盖的孤立 map → 计数丢失.
	// (sonnet F1 MEDIUM 修复)
	accountMu sync.Mutex
)

func initCounters() {
	once.Do(func() {
		counters = expvar.NewMap("cache_token_count")
		counters.Add(keyCreationTotal, 0)
		counters.Add(keyReadTotal, 0)
		counters.Add(keyRequestCount, 0)

		// per-account 维度（万人级运维 audit: 哪些 provider_account 缓存
		// 命中率高/低）。expvar.Map 嵌套: 每 account_id → expvar.Map(三计数)
		countersByAccount = expvar.NewMap("cache_token_count_by_account")
	})
}

// Observe 累计一次 cache token 观测。
//
// 调用场景: proto adapter 解析 vendor usage event 时, 如果 cacheCreation
// 或 cacheRead 任一**正数**, 调本函数。
//
// 防御:
//   - 0/0 输入 (vendor 未启用 caching) 不应增 request_count, 避免 inflate 分母
//   - **负数输入** (sonnet F6 LOW): vendor 不应该返回负数 token 但有种边界
//     情况 vendor cached_tokens=null → unmarshal 到 int=0; 但若 caller 把
//     数据通过 int 转换有溢出, 显式 < 0 早返避免 silent-drop 后期歧义
//
// 已知 gap (sonnet F2 HIGH): TCP 连接在 message_start 之后、message_delta
// 之前断开 → AccumulatedUsage cache 字段为 0 → 静默 skip 本次观测。这是
// 设计取舍——partial observation 比无观测更糟; cache 字段必须在 message_delta
// 中累计才被记录, 没收到 message_delta 时无可信值可上报。
func Observe(cacheCreation, cacheRead int64) {
	initCounters()
	// 防御负数 (sonnet F6 LOW)——理论上不应发生但 fail-fast 比 silent-drop 清晰
	if cacheCreation < 0 || cacheRead < 0 {
		return
	}
	if cacheCreation == 0 && cacheRead == 0 {
		return
	}
	if cacheCreation > 0 {
		counters.Add(keyCreationTotal, cacheCreation)
	}
	if cacheRead > 0 {
		counters.Add(keyReadTotal, cacheRead)
	}
	counters.Add(keyRequestCount, 1)
}

// Snapshot 给测试 / introspection 用——返回当前累计值。
func Snapshot() (creation, read, requests int64) {
	initCounters()
	if v, ok := counters.Get(keyCreationTotal).(*expvar.Int); ok {
		creation = v.Value()
	}
	if v, ok := counters.Get(keyReadTotal).(*expvar.Int); ok {
		read = v.Value()
	}
	if v, ok := counters.Get(keyRequestCount).(*expvar.Int); ok {
		requests = v.Value()
	}
	return
}

// ObserveByAccount 在 Observe 基础上额外累计 per-account 维度计数器。
// accountID == 0 时退化为只调 Observe (与全局等价)。
//
// expvar 结构暴露 (/debug/vars):
//
//	"cache_token_count_by_account": {
//	  "42": {"creation_total": ..., "read_total": ..., "request_count": ...},
//	  "99": {...}
//	}
//
// 运维查 hit_ratio_by_account = read_total/(creation+read) 找出哪个账号
// 缓存命中率显著低 → 流量调度或换 prompt-pool 类型。
func ObserveByAccount(cacheCreation, cacheRead int64, accountID int64) {
	Observe(cacheCreation, cacheRead) // 先全局
	if accountID == 0 {
		return
	}
	if cacheCreation == 0 && cacheRead == 0 {
		return
	}
	if cacheCreation < 0 || cacheRead < 0 {
		return
	}
	initCounters()

	key := strconv.FormatInt(accountID, 10)
	// sonnet F1 race 修复: lazy-init Get-then-Set 必须 atomic
	accountMu.Lock()
	subVar := countersByAccount.Get(key)
	if subVar == nil {
		sub := new(expvar.Map).Init()
		sub.Add(keyCreationTotal, 0)
		sub.Add(keyReadTotal, 0)
		sub.Add(keyRequestCount, 0)
		countersByAccount.Set(key, sub)
		subVar = sub
	}
	accountMu.Unlock()
	sub, ok := subVar.(*expvar.Map)
	if !ok {
		return
	}
	if cacheCreation > 0 {
		sub.Add(keyCreationTotal, cacheCreation)
	}
	if cacheRead > 0 {
		sub.Add(keyReadTotal, cacheRead)
	}
	sub.Add(keyRequestCount, 1)
}

// CacheObservation 是一次 cache token 观测的结构化事件 (PASR-lite A4 用)。
// 比 ObserveByAccount 多 PrefixHash + TenantID 字段, 让 PASR-lite 调度器把
// cache 信号反馈到正确的 (tenant, prefix) 段。
//
// M5b (2026-05-09): 新增 TenantID 字段。 之前 CacheObservation 不含 tenant,
// PASRCacheFeedback.handle 找段时跨租户共享 segments map → 同 prompt 跨 tenant
// 段成员混选, cache locality 失效。 现在 SegmentTable.Lookup(tenantID, prefix)
// 用双字段查段, 跨租户隔离恢复。
type CacheObservation struct {
	TenantID      int64 // M5b: 必填; 0 时退化只走全局 + per-account counter, observer 静默跳过段
	AccountID     int64
	PrefixHash    string // anthropic.UpstreamState.PrefixHash 透传, 可能为空
	CacheCreation int64
	CacheRead     int64
}

// observerRegistry 用单独锁保护订阅列表, 不与 expvar.Map 锁竞争。
type observerRegistry struct {
	mu        sync.RWMutex
	observers []func(CacheObservation)
}

var globalObservers = &observerRegistry{}

// RegisterCacheObserver 订阅 cache 观测事件。
// 用法: PASR-lite 调度器在初始化期注册一个 observer, 把 (accountID,
// prefixHash, creation, read) 转化为 segment.MarkCacheSeen / MarkRead。
//
// 线程安全: 注册和触发都加锁; observers 加进来后整个生命周期保留, 没
// Unregister。如需 Unregister 后续 atomic 加。
func RegisterCacheObserver(fn func(CacheObservation)) {
	if fn == nil {
		return
	}
	globalObservers.mu.Lock()
	globalObservers.observers = append(globalObservers.observers, fn)
	globalObservers.mu.Unlock()
}

// notifyObservers 在每次 ObserveByAccountWithPrefix 内部调, 把事件推给
// 所有订阅者。observer 内部异常不应影响 caller, 用 panic recover 隔离。
func notifyObservers(obs CacheObservation) {
	globalObservers.mu.RLock()
	defer globalObservers.mu.RUnlock()
	for _, fn := range globalObservers.observers {
		func() {
			defer func() {
				_ = recover() // observer 不能炸掉 cachemetrics 调用方
			}()
			fn(obs)
		}()
	}
}

// ObserveByAccountWithPrefix 是 ObserveByAccount 的扩展形态, 把 prefixHash
// 一起携带, 让 PASR-lite 等订阅者能更新自己的 segment 状态。
//
// 行为:
//   - 现有 expvar global + per-account counter 累计完全等价 ObserveByAccount
//   - 额外触发所有 RegisterCacheObserver 订阅的 observer fn
//
// M5b (2026-05-09): tenantID 参数新增, 透传到 CacheObservation 让 observer
// 用 (TenantID, PrefixHash) 双字段查段, 修跨租户共段 cache locality 失效。
// caller (forwarder + vendor SSE adapter) 必须从 ForwardRequest.TenantID 注入。
// tenantID == 0 时 observer 仍能记 expvar counter 但跳过段表更新 (无 tenant 信息)。
func ObserveByAccountWithPrefix(cacheCreation, cacheRead int64, tenantID, accountID int64, prefixHash string) {
	ObserveByAccount(cacheCreation, cacheRead, accountID)
	if cacheCreation == 0 && cacheRead == 0 {
		return
	}
	if cacheCreation < 0 || cacheRead < 0 {
		return
	}
	notifyObservers(CacheObservation{
		TenantID:      tenantID,
		AccountID:     accountID,
		PrefixHash:    prefixHash,
		CacheCreation: cacheCreation,
		CacheRead:     cacheRead,
	})
}

// SnapshotByAccount 给测试用——返回某 account_id 的累计计数。
// account 未观测过返回 0/0/0。
func SnapshotByAccount(accountID int64) (creation, read, requests int64) {
	initCounters()
	if accountID == 0 {
		return
	}
	key := strconv.FormatInt(accountID, 10)
	subVar := countersByAccount.Get(key)
	if subVar == nil {
		return
	}
	sub, ok := subVar.(*expvar.Map)
	if !ok {
		return
	}
	if v, ok := sub.Get(keyCreationTotal).(*expvar.Int); ok {
		creation = v.Value()
	}
	if v, ok := sub.Get(keyReadTotal).(*expvar.Int); ok {
		read = v.Value()
	}
	if v, ok := sub.Get(keyRequestCount).(*expvar.Int); ok {
		requests = v.Value()
	}
	return
}
