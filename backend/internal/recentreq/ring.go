// Package recentreq implements MGMT-RECENTREQ-01: per-provider-account
// recent-request observability for incident triage.
//
// An in-memory ring buffer (cap 64) of request outcomes is maintained per
// account. Callers record outcomes at dispatch time; the admin health endpoint
// exposes a Summary for each account.
//
// Per-process caveat: each gateway instance tracks its own requests. In a
// multi-instance deployment the summary reflects only the traffic handled by
// this process. This is consistent with sessioncap.Registry and is safe given
// the fail-open, observability-only nature of this feature.
package recentreq

import (
	"sync"
	"time"
)

// ringCap is the number of entries retained per account.
const ringCap = 64

// entry is one recorded request outcome.
type entry struct {
	at time.Time
	ok bool
}

// ringBuf is a fixed-size circular buffer of entries.
type ringBuf struct {
	entries [ringCap]entry
	head    int // next write position
	count   int // total entries filled, capped at ringCap
}

func (b *ringBuf) record(at time.Time, ok bool) {
	b.entries[b.head%ringCap] = entry{at: at, ok: ok}
	b.head++
	if b.count < ringCap {
		b.count++
	}
}

// Summary holds aggregated counts for one account's ring.
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

// Ring is a thread-safe in-memory store of recent request outcomes per
// provider account. A nil *Ring is safe to use; all operations are no-ops.
// Zero value is NOT usable; use NewRing.
type Ring struct {
	mu      sync.RWMutex
	buffers map[int64]*ringBuf
}

// NewRing constructs a ready-to-use Ring.
func NewRing() *Ring {
	return &Ring{
		buffers: make(map[int64]*ringBuf),
	}
}

// Record appends one outcome for accountID. Evicts the oldest entry when the
// ring is full. Safe to call on a nil *Ring (no-op).
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

// Summary returns aggregated counts for accountID. Returns an empty Summary
// when the ring is nil or no data has been recorded for the account.
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
