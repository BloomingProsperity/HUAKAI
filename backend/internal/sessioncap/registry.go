// Package sessioncap implements SUB2-EGRESS-02: per-account concurrent session
// cap. An in-memory Registry tracks distinct active sessionHash values per
// account; a pool gate rejects a new session when the account is already at
// its configured cap.
//
// Safety design:
//   - Opt-in via max_sessions > 0 on provider_accounts (0 = unlimited = today's
//     behavior, a TRUE no-op).
//   - Fail-open: nil registry, or any error -> account stays eligible.
//   - Existing session always allowed: WouldExceed excludes the current
//     sessionHash from the count, so a re-binding request is never rejected.
//   - Per-process only: each gateway instance tracks its own sessions.
//     fail-open + opt-in make this safe in a multi-instance deployment.
package sessioncap

import (
	"sync"
	"time"
)

// DefaultIdleTTL is the duration after which an idle session entry expires.
const DefaultIdleTTL = 5 * time.Minute

// entry holds the last-seen timestamp for one session.
type entry struct {
	lastSeen time.Time
}

// Registry is a thread-safe in-memory store of active sessions per account.
// Zero value is NOT usable; use NewRegistry.
type Registry struct {
	mu sync.RWMutex
	// sessions maps accountID -> sessionHash -> entry
	sessions map[int64]map[string]entry
	idleTTL  time.Duration
	now      func() time.Time
}

// NewRegistry constructs a Registry with the given idle TTL.
// idleTTL <= 0 uses DefaultIdleTTL.
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

// Register adds or refreshes the session for (accountID, sessionHash).
// Idempotent: calling multiple times for the same pair only updates lastSeen.
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

// WouldExceed reports whether adding sessionHash as a NEW session would cause
// the account to exceed max concurrent sessions.
//
// The current sessionHash is excluded from the count -- a session already
// active on this account (or re-binding via stickiness) is never rejected.
// Only a genuinely new session when the account already has max distinct
// others is rejected.
//
// TTL expiry is applied lazily during this call (expired entries removed).
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
			// lazy TTL expiry
			delete(m, hash)
			continue
		}
		if hash == sessionHash {
			// existing session: exclude from cap count
			continue
		}
		count++
	}
	return count >= max
}
