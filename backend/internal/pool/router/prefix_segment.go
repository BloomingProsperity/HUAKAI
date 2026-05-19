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
// 设计决策（per pasr-lite-v2-synthesis §3 §4 §7 + cache-aware A1）:
//   - bitmap = 3 bits / segment (member 是否见过 cache_creation_input_tokens > 0)
//   - LastReadAt 老化锚: cache_read_input_tokens > 0 时刷新
//   - 段过期: 5min 无 cache_read (Anthropic default cache TTL 对齐) OR 段
//     标 ExtendedCacheTTL=1h (extended cache 客户场景) OR 段表上限 100k
//     LRU evict (cache-aware A1 双 + 单触发组合)
//   - rebalance 软迁移 1h: 见 A6 atomic
package router

import (
	"container/list"
	"crypto/sha256"
	"encoding/binary"
	"sync"
	"sync/atomic"
	"time"
)

// PASRSegmentSize K=3 是 Owner 锁定的段大小 (Owner 2026-05-08 "可以K3")。
const PASRSegmentSize = 3

// DefaultSegmentMaxAge 默认段无 cache_read 老化时间。
//
// 设计 (cache-aware A1, Owner 2026-05-09): 5min 与 Anthropic prompt cache
// default TTL 对齐 (memory: 之前 30min 偏长导致段表保留时间超出 vendor 实
// 际 cache 寿命 → 路由到 steward 时 vendor cache 已掉 → 白费精确路由)。
// extended cache 客户场景另行设 PrefixSegment.ExtendedCacheTTL=1h, EvictExpired
// 每段独立判断有效 TTL。
const DefaultSegmentMaxAge = 5 * time.Minute

// DefaultExtendedCacheTTL 标记为 extended cache 的段允许保留的最大无命中
// 时间 (1h, 与 Anthropic extended cache TTL 对齐)。
const DefaultExtendedCacheTTL = 1 * time.Hour

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
	// PASR-lite 用此时间戳老化 (5min 默认无命中即回收, ExtendedCacheTTL 标
	// 记的段则用 1h)。
	LastReadAt atomic.Int64

	// LastWriteAt 最近一次 cache_creation_input_tokens > 0 的 unix nanos。
	// 弱信号: 段成员"刚开始"积累 cache, 未必稳定。
	LastWriteAt atomic.Int64

	// ExtendedCacheTTL 段独立的有效 TTL (cache-aware A1)。
	// 单位: nanoseconds (匹配 time.Duration).
	// 0 → 用 SegmentTable.maxAge (默认 5min, 对齐 Anthropic default cache);
	// 非 0 → extended cache 客户场景用 (例 Anthropic extended 1h),
	// EvictExpired 单独判每段是否过期。
	// caller 在段创建后 (或 first cache_creation observation 命中) 调
	// SetExtendedCacheTTL 写入。
	ExtendedCacheTTL atomic.Int64

	// MissCount per-member 连续 cache miss 计数 (cache-aware A3)。
	// observe(cache_creation==0 && cache_read==0) → RecordMiss(idx) 累 1;
	// observe(cache_read>0) → ResetMissCount(idx) 归 0; 达到 PASRDemoteThreshold
	// (默认 2) 时 caller (pasr_feedback.handle) 调 Demote(idx) 清 HasCache bit。
	// 设计意图: 段成员标 hasCache=true 后, 若实际 vendor cache 已掉, 连续
	// miss 应 demote 让其他成员获得机会 (Owner 关切 #3, A3 atom)。
	MissCount [PASRSegmentSize]atomic.Uint32

	// CreatedAt 段创建 unix nanos (用于 ops 调试 + 软迁移过渡判断)。
	CreatedAt int64
}

// PASRDemoteThreshold cache-aware A3: 连续 miss 达此阈值时 demote 段成员。
//   - 1 太敏感: 单次抖动就 demote
//   - 3 太钝: 浪费 vendor cache 机会
//   - 2 平衡 (Owner delegated 拍板)
const PASRDemoteThreshold uint32 = 2

// RecordMiss 段成员 idx 收到一次 cache miss 信号, 返新计数值。
// caller (pasr_feedback) 据返值与 PASRDemoteThreshold 比, 决定是否 Demote。
// idx 越界 → no-op + 返 0.
func (s *PrefixSegment) RecordMiss(idx int) uint32 {
	if idx < 0 || idx >= PASRSegmentSize {
		return 0
	}
	return s.MissCount[idx].Add(1)
}

// ResetMissCount 段成员 idx 收到 cache_read > 0 → 归零 miss 计数 (cache 又
// hot 了, 之前积累的 miss 序列作废, 重新观察)。 idx 越界 → no-op。
func (s *PrefixSegment) ResetMissCount(idx int) {
	if idx < 0 || idx >= PASRSegmentSize {
		return
	}
	s.MissCount[idx].Store(0)
}

// Demote 清 HasCacheBitmap[idx] + 重置 MissCount[idx] (A3 demote 路径)。
// 触发场景: 段成员连续 miss 达阈值 → vendor cache 实际已掉, 撤销
// hasCache=true 标记, 让 ranking 不再为它加 locality 分。
// 段成员要重新通过 cache_creation observation 才能再 set hasCache。
// idx 越界 → no-op。
func (s *PrefixSegment) Demote(idx int) {
	if idx < 0 || idx >= PASRSegmentSize {
		return
	}
	mask := uint32(1) << idx
	for {
		old := s.HasCacheBitmap.Load()
		new := old &^ mask // clear bit
		if old == new || s.HasCacheBitmap.CompareAndSwap(old, new) {
			break
		}
	}
	s.MissCount[idx].Store(0)
}

// SetExtendedCacheTTL 标记本段使用 extended cache TTL (1h 默认)。
// 0 / 负值 → 还原到 SegmentTable 默认 maxAge (5min)。
// hot path 安全 (atomic store)。
func (s *PrefixSegment) SetExtendedCacheTTL(ttl time.Duration) {
	if ttl <= 0 {
		s.ExtendedCacheTTL.Store(0)
		return
	}
	s.ExtendedCacheTTL.Store(int64(ttl))
}

// effectiveMaxAge 段独立有效 TTL: ExtendedCacheTTL 非 0 用它, 否则用
// SegmentTable 的 maxAge 默认。 EvictExpired 调用本函数判每段。
func (s *PrefixSegment) effectiveMaxAge(tableDefault time.Duration) time.Duration {
	ext := s.ExtendedCacheTTL.Load()
	if ext > 0 {
		return time.Duration(ext)
	}
	return tableDefault
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
// M5b: 加 tenantID 参数防跨租户共段。
func (t *SegmentTable) Lookup(tenantID int64, prefixHash []byte) *PrefixSegment {
	key := segmentKey(tenantID, prefixHash)
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
// M5b: 加 tenantID 参数防跨租户共段。
func (t *SegmentTable) LookupOrCreate(tenantID int64, prefixHash []byte, ring *AccountRing) *PrefixSegment {
	key := segmentKey(tenantID, prefixHash)

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

// EvictExpired 老化清理: 删除 LastReadAt + 段独立 effectiveMaxAge 已过期的段。
// 返回被 evict 的段数。
//
// 调用方: 1min ticker goroutine (A5 atomic 实现, A1 cache-aware 缩到 1min)。
//
// cache-aware A1: 段不再共享单一 cutoff — ExtendedCacheTTL 非 0 的段用 1h
// 等独立 TTL, 默认段用 5min。 不能再"扫到首个活段就 break", 必须遍历整条
// LRU 链。 100k 段上限 + 1min ticker = ~100ms 一次扫描, 可接受。
func (t *SegmentTable) EvictExpired(now time.Time) int {
	nowNs := now.UnixNano()
	t.mu.Lock()
	defer t.mu.Unlock()

	count := 0
	// 全 LRU 链遍历 (从 back 向 front), 每段独立判 effective TTL。
	elem := t.lruOrder.Back()
	for elem != nil {
		prev := elem.Prev()
		key, ok := elem.Value.(string)
		if !ok {
			t.lruOrder.Remove(elem)
			elem = prev
			continue
		}
		entry, exists := t.segments[key]
		if !exists {
			t.lruOrder.Remove(elem)
			elem = prev
			continue
		}
		seg := entry.seg
		// 段独立 effective TTL: ExtendedCacheTTL 非 0 用它, 否则 t.maxAge。
		ttl := seg.effectiveMaxAge(t.maxAge)
		if (nowNs - seg.LastReadAt.Load()) < int64(ttl) {
			elem = prev
			continue // 段在自己的 TTL 内还活着, 跳过
		}
		t.lruOrder.Remove(elem)
		delete(t.segments, key)
		count++
		elem = prev
	}
	return count
}

// Delete 单个段删除 (rebalance / 测试用)。
// M5b: 加 tenantID 参数防跨租户共段。
func (t *SegmentTable) Delete(tenantID int64, prefixHash []byte) bool {
	key := segmentKey(tenantID, prefixHash)
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

// segmentKey 把 (tenant_id, prefix_hash) 转成 string map key (M5b: 加 tenant
// 维度防跨租户共段; M5c: 加 mode tag 防 raw 与 hash 同段碰撞)。
//
// 编码: 8 字节 big-endian tenant_id + 1 字节 mode tag + prefix_hash (或 sha256
// 截前 16B 控长度)。 mode tag 0x01 = raw, 0x02 = hash; 防止某 16B 短 prefix
// 与长 prefix 的 sha256 截断结果偶然相同 (理论 collision space 2^128, 实际
// ≈0 但 design smell)。 tenant_id 在前是为了字典序按 tenant 聚类, 利于
// LRU evict 时 cache locality。
//
// 退化路径: tenant_id == 0 时仍编码 0 byte 头, 段表内部 (0, prefix) 与
// (1, prefix) 自然区分; caller 上游应保证 production 路径 tenant_id != 0
// (admin / system 流量也分配 tenant_id, 不应漏)。
const (
	segmentKeyModeRaw  byte = 0x01 // ≤32B prefix, 直接拼
	segmentKeyModeHash byte = 0x02 // >32B prefix, sha256 截 16B
)

func segmentKey(tenantID int64, prefixHash []byte) string {
	var head [9]byte // 8B tenant_id + 1B mode tag
	binary.BigEndian.PutUint64(head[:8], uint64(tenantID))
	if len(prefixHash) <= 32 {
		head[8] = segmentKeyModeRaw
		out := make([]byte, 0, 9+len(prefixHash))
		out = append(out, head[:]...)
		out = append(out, prefixHash...)
		return string(out)
	}
	// 长 prefix (> 32B) 哈希到 sha256 前 16B 当 key 后段, 控制 map 内存占用。
	head[8] = segmentKeyModeHash
	h := sha256.Sum256(prefixHash)
	out := make([]byte, 0, 9+16)
	out = append(out, head[:]...)
	out = append(out, h[:16]...)
	return string(out)
}
