// prefix_segment.go — PASR-lite A2: PrefixSegment + segmentTable + 老化 + LRU evict。
//
// PrefixSegment 是 PASR-lite 调度器的核心运行时数据：每个 prompt prefix 在
// HRW ring 上的 K=3 段 + 段内每个成员是否见过 vendor cache 的 bitmap +
// 老化锚（最近一次 cache_read 时间）。
//
// SegmentTable 是 in-memory 主权威 segment 表（per codex synthesis D4 拓扑
// 创新）：hot path 完全脱离 DB，PG 持久化在 A6 atomic 加，运行时只读
// in-memory。
//
// 设计决策（per pasr-lite-v2-synthesis §3 §4 §7）:
//   - bitmap = 3 bits / segment (member 是否见过 cache_creation_input_tokens > 0)
//   - LastReadAt 老化锚: cache_read_input_tokens > 0 时刷新
//   - 段过期: 30min 无 cache_read OR 段表上限 100k LRU evict (codex D8 双触发)
//   - rebalance 软迁移 1h: 见 A6 atomic
package pool

import (
	"container/list"
	"crypto/sha256"
	"sync"
	"sync/atomic"
	"time"
)

// PASRSegmentSize K=3 是 Owner 锁定的段大小 (Owner 2026-05-08 "可以K3")。
const PASRSegmentSize = 3

// DefaultSegmentMaxAge 默认段无 cache_read 老化时间 (synthesis D8 30min)。
const DefaultSegmentMaxAge = 30 * time.Minute

// DefaultSegmentTableCap 默认段表上限 (synthesis D8: codex 100k)。
const DefaultSegmentTableCap = 100_000

// PrefixSegment 是 PASR-lite 调度器的运行时段元数据。
//
// 不变量:
//   - len(Members) == PASRSegmentSize (始终 3)
//   - HasCacheBitmap 仅低 3 bit 有意义；bit_i=1 表示 Members[i] 见过
//     vendor cache_creation_input_tokens > 0
//   - LastReadAt 单调递增（atomic store, 不回退）
//
// 线程安全: 字段读写都用 atomic 操作（避免 per-segment mutex 在 hot path
// 形成竞争点）。HasCacheBitmap 用 atomic.Uint32 实际上只用低 3 bit。
type PrefixSegment struct {
	// PrefixKey 段的 prefix hash 字符串形式（用 string key 避免 []byte 不能
	// 作为 map key 的限制）。
	PrefixKey string

	// Members 段内 K=3 个 provider account ID, 顺序按 HRW score 降序
	// (Members[0] 是首选 steward, [1][2] 候补)。
	Members [PASRSegmentSize]int64

	// HasCacheBitmap 标记段成员是否见过 cache_creation_input_tokens > 0。
	// 用 atomic.Uint32 包装方便原子位操作（实际仅低 3 bit 有意义）。
	HasCacheBitmap atomic.Uint32

	// LastReadAt 最近一次 cache_read_input_tokens > 0 的 unix nanos。
	// PASR-lite 用此时间戳老化（30min 无命中即回收）。
	LastReadAt atomic.Int64

	// LastWriteAt 最近一次 cache_creation_input_tokens > 0 的 unix nanos。
	// 弱信号: 段成员"刚开始"积累 cache, 未必稳定。
	LastWriteAt atomic.Int64

	// CreatedAt 段创建 unix nanos (用于 ops 调试 + 软迁移过渡判断)。
	CreatedAt int64
}

// HasCache 检查 Members[idx] 是否标记为见过 cache_creation。
// idx 越界返 false。
func (s *PrefixSegment) HasCache(idx int) bool {
	if idx < 0 || idx >= PASRSegmentSize {
		return false
	}
	return s.HasCacheBitmap.Load()&(1<<idx) != 0
}

// MarkCacheSeen 原子设置 Members[idx] 见过 cache_creation 位。
// idx 越界 no-op。
func (s *PrefixSegment) MarkCacheSeen(idx int) {
	if idx < 0 || idx >= PASRSegmentSize {
		return
	}
	mask := uint32(1) << idx
	for {
		old := s.HasCacheBitmap.Load()
		new := old | mask
		if old == new || s.HasCacheBitmap.CompareAndSwap(old, new) {
			return
		}
	}
}

// CountCached 段内见过 cache 的成员数（0..3）。
func (s *PrefixSegment) CountCached() int {
	bm := s.HasCacheBitmap.Load()
	cnt := 0
	for i := 0; i < PASRSegmentSize; i++ {
		if bm&(1<<i) != 0 {
			cnt++
		}
	}
	return cnt
}

// IndexOf 返回 Members 中 accountID 的下标; 不存在返 -1。
// hot path 频繁调用（after_response 时根据 acc 反查 idx），应保持快。
func (s *PrefixSegment) IndexOf(accountID int64) int {
	for i, m := range s.Members {
		if m == accountID {
			return i
		}
	}
	return -1
}

// SegmentTable 是 in-memory 段表 + LRU 索引。
//
// 线程安全: 用 sync.RWMutex 保护表结构 (map 读写 + LRU list 读写)。
// per-segment 字段（bitmap, LastReadAt 等）走 atomic, 不与 table mu 竞争。
//
// hot path: LookupOrCreate 是热路径核心；典型一次 schedule = 1 次表读 +
// 0 或 1 次表写 (新 prefix)。
type SegmentTable struct {
	mu sync.RWMutex

	// segments string-keyed map (key = string(prefix_hash))。
	segments map[string]*segmentEntry

	// lruOrder LRU 链表; front = 最新, back = 最老 (淘汰候选)。
	// 每次 LookupOrCreate / cache hit 调 mu 锁内 lruOrder.MoveToFront。
	lruOrder *list.List

	// 配置
	maxSegments int           // 段表上限, 超出时 LRU evict back
	maxAge      time.Duration // 段无 cache_read 老化时间

	// 时间注入 (测试用)
	now func() time.Time
}

// segmentEntry segment + 它在 LRU 链表中的元素引用。
type segmentEntry struct {
	seg     *PrefixSegment
	lruNode *list.Element
}

// SegmentTableConfig 构造时配置参数。
type SegmentTableConfig struct {
	MaxSegments int           // 0 用 DefaultSegmentTableCap
	MaxAge      time.Duration // 0 用 DefaultSegmentMaxAge
	Now         func() time.Time
}

// NewSegmentTable 构造一个空段表。
func NewSegmentTable(cfg SegmentTableConfig) *SegmentTable {
	if cfg.MaxSegments <= 0 {
		cfg.MaxSegments = DefaultSegmentTableCap
	}
	if cfg.MaxAge <= 0 {
		cfg.MaxAge = DefaultSegmentMaxAge
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &SegmentTable{
		segments:    make(map[string]*segmentEntry),
		lruOrder:    list.New(),
		maxSegments: cfg.MaxSegments,
		maxAge:      cfg.MaxAge,
		now:         cfg.Now,
	}
}

// Size 当前段数。
func (t *SegmentTable) Size() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.segments)
}

// Lookup 仅查找已存在段, 不创建。命中时 LRU MoveToFront (touch).
func (t *SegmentTable) Lookup(prefixHash []byte) *PrefixSegment {
	key := segmentKey(prefixHash)
	t.mu.Lock() // touch LRU 需写锁
	defer t.mu.Unlock()
	if entry, ok := t.segments[key]; ok {
		t.lruOrder.MoveToFront(entry.lruNode)
		return entry.seg
	}
	return nil
}

// LookupOrCreate 返回已存段或新建一个 (HRW.Top3(prefix, ring))。
// 命中已存段 → MoveToFront；新建段 → push front + 必要时 LRU evict back。
//
// 这是 PASR-lite hot path; 单请求 1 次调用。
func (t *SegmentTable) LookupOrCreate(prefixHash []byte, ring *AccountRing) *PrefixSegment {
	key := segmentKey(prefixHash)

	// 第一遍: 读锁查命中 (常见情况, 走快路径)
	t.mu.RLock()
	if entry, ok := t.segments[key]; ok {
		seg := entry.seg
		t.mu.RUnlock()
		// touch LRU 在写锁内
		t.mu.Lock()
		if entry, ok := t.segments[key]; ok {
			t.lruOrder.MoveToFront(entry.lruNode)
		}
		t.mu.Unlock()
		return seg
	}
	t.mu.RUnlock()

	// 第二遍: 写锁创建 (并发竞争时再检查一次)
	t.mu.Lock()
	defer t.mu.Unlock()
	if entry, ok := t.segments[key]; ok {
		t.lruOrder.MoveToFront(entry.lruNode)
		return entry.seg
	}

	// 真新建: HRW Top3 选段成员
	top3 := ring.TopK(prefixHash, PASRSegmentSize)
	var members [PASRSegmentSize]int64
	for i, id := range top3 {
		if i >= PASRSegmentSize {
			break
		}
		members[i] = id
	}
	now := t.now().UnixNano()
	seg := &PrefixSegment{
		PrefixKey: key,
		Members:   members,
		CreatedAt: now,
	}
	seg.LastReadAt.Store(now)
	seg.LastWriteAt.Store(now)
	node := t.lruOrder.PushFront(key)
	t.segments[key] = &segmentEntry{seg: seg, lruNode: node}
	IncSegmentCreates() // metrics: cold-start 频率

	// LRU evict back if cap exceeded
	if len(t.segments) > t.maxSegments {
		t.evictOldestLocked()
		AddEvictions(1)
	}

	return seg
}

// evictOldestLocked 移除 LRU 末尾段。caller 已持写锁。
func (t *SegmentTable) evictOldestLocked() {
	back := t.lruOrder.Back()
	if back == nil {
		return
	}
	key, ok := back.Value.(string)
	if !ok {
		return
	}
	t.lruOrder.Remove(back)
	delete(t.segments, key)
}

// MarkRead 在段上原子记录 cache_read 命中: 刷新 LastReadAt + LRU 提前。
// hot path: 每次 cache hit 调一次。
func (t *SegmentTable) MarkRead(seg *PrefixSegment, now time.Time) {
	seg.LastReadAt.Store(now.UnixNano())
	t.mu.Lock()
	if entry, ok := t.segments[seg.PrefixKey]; ok {
		t.lruOrder.MoveToFront(entry.lruNode)
	}
	t.mu.Unlock()
}

// EvictExpired 老化清理: 删除 LastReadAt 早于 (now - maxAge) 的段。
// 返回被 evict 的段数。
//
// 调用方: 5min ticker goroutine (A5 atomic 实现)。
func (t *SegmentTable) EvictExpired(now time.Time) int {
	cutoff := now.Add(-t.maxAge).UnixNano()
	t.mu.Lock()
	defer t.mu.Unlock()

	// 从 LRU 末尾扫: 一旦碰到非 expired 的段就停 (LRU 顺序保证后面的都新)
	count := 0
	for {
		back := t.lruOrder.Back()
		if back == nil {
			break
		}
		key, ok := back.Value.(string)
		if !ok {
			t.lruOrder.Remove(back)
			continue
		}
		entry, exists := t.segments[key]
		if !exists {
			t.lruOrder.Remove(back)
			continue
		}
		if entry.seg.LastReadAt.Load() >= cutoff {
			break // 该段还活着，后面（更新）的也活着
		}
		t.lruOrder.Remove(back)
		delete(t.segments, key)
		count++
	}
	return count
}

// Delete 单个段删除 (rebalance / 测试用)。
func (t *SegmentTable) Delete(prefixHash []byte) bool {
	key := segmentKey(prefixHash)
	t.mu.Lock()
	defer t.mu.Unlock()
	entry, ok := t.segments[key]
	if !ok {
		return false
	}
	t.lruOrder.Remove(entry.lruNode)
	delete(t.segments, key)
	return true
}

// PrefixHashes 当前所有段的 prefix hash 列表（rebalance 时遍历用）。
// caller 不应在 hot path 调本函数（O(N) 复制）。
func (t *SegmentTable) PrefixHashes() [][]byte {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([][]byte, 0, len(t.segments))
	for k := range t.segments {
		out = append(out, []byte(k))
	}
	return out
}

// segmentKey 把 prefix hash 转成 string map key。
// 当前直接 string conversion；如 prefix 长度极不一致 (32B vs 64B) 后续可
// 改用 sha256 截 16B 统一长度，节省 map memory。
func segmentKey(prefixHash []byte) string {
	if len(prefixHash) <= 32 {
		return string(prefixHash)
	}
	// 长 prefix (> 32B) 哈希到 sha256 前 16B 当 key, 控制 map 内存占用。
	h := sha256.Sum256(prefixHash)
	return string(h[:16])
}
