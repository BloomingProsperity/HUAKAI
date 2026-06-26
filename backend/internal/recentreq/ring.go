// Package recentreq 实现 MGMT-RECENTREQ-01: 按 provider 账号维度的近期请求
// 可观测性, 用于事故定级排查。
//
// 每个账号维护一个内存 ring buffer(容量 64)记录请求结果。调用方在 dispatch
// 时记录结果; admin 健康端点为每个账号暴露一个 Summary。
//
// 进程级注意点: 每个 gateway 实例只跟踪自己的请求。在多实例部署中, 该汇总只
// 反映本进程处理的流量。这与 sessioncap.Registry 一致, 鉴于此特性是 fail-open
// 且仅用于可观测性, 这样做是安全的。
package recentreq

import (
	"sync"
	"time"
)

// ringCap 是每个账号保留的 entry 数量。
const ringCap = 64

// entry 是一条记录下来的请求结果。
type entry struct {
	at time.Time
	ok bool
}

// ringBuf 是一个由 entry 组成的固定大小环形缓冲区。
type ringBuf struct {
	entries [ringCap]entry
	head    int // 下一个写入位置
	count   int // 已填充的 entry 总数, 上限为 ringCap
}

func (b *ringBuf) record(at time.Time, ok bool) {
	b.entries[b.head%ringCap] = entry{at: at, ok: ok}
	b.head++
	if b.count < ringCap {
		b.count++
	}
}

// Summary 保存某个账号 ring 的聚合计数。
type Summary struct {
	Total   int
	Success int
	Failure int
	LastAt  time.Time
}

func (b *ringBuf) summary() Summary {
	var s Summary
	s.Total = b.count
	start := b.head - b.count
	for i := 0; i < b.count; i++ {
		e := b.entries[(start+i)%ringCap]
		if e.ok {
			s.Success++
		} else {
			s.Failure++
		}
		if e.at.After(s.LastAt) {
			s.LastAt = e.at
		}
	}
	return s
}

// Ring 是一个线程安全的内存 store, 按 provider 账号保存近期请求结果。
// nil 的 *Ring 可安全使用; 所有操作都是 no-op。
// 零值不可用; 请使用 NewRing。
type Ring struct {
	mu      sync.RWMutex
	buffers map[int64]*ringBuf
}

// NewRing 构造一个可直接使用的 Ring。
func NewRing() *Ring {
	return &Ring{
		buffers: make(map[int64]*ringBuf),
	}
}

// Record 为 accountID 追加一条结果。ring 满时淘汰最旧的 entry。
// 在 nil 的 *Ring 上调用是安全的(no-op)。
func (r *Ring) Record(accountID int64, ok bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.buffers[accountID]
	if b == nil {
		b = &ringBuf{}
		r.buffers[accountID] = b
	}
	b.record(time.Now(), ok)
}

// Summary 返回 accountID 的聚合计数。当 ring 为 nil 或该账号尚无数据时,
// 返回空的 Summary。
func (r *Ring) Summary(accountID int64) Summary {
	if r == nil {
		return Summary{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	b := r.buffers[accountID]
	if b == nil {
		return Summary{}
	}
	return b.summary()
}
