// A07.2: SingleFlight primitive — concurrent dedup for keyed work.
// Spec: docs/specs/upstream-credential-management.md §A07 (Invariant 4) /
// synthesis §1 A07.
//
// Foundation primitive #2 for the A07 three-scope refresh storm controller.
// A07.1 (TokenBucket) supplies the rate budget; this file supplies same-key
// dedup so N concurrent callers don't all execute the keyed function.
// A07.3 (3-scope policy compositor) wires both into the OAuth refresh flow.
//
// No IO, no network, no credential contact: pure concurrent control.
// Synthesis of two parallel-draft lanes (CLAUDE.md #10 + 2026-05-04 directive).
package gateway

import (
	"fmt"
	"sync"
)

// singleFlightCall holds the in-flight or completed state for a Do call.
type singleFlightCall struct {
	wg  sync.WaitGroup
	val any
	err error
}

// SingleFlight collapses concurrent calls with the same key so that fn runs
// at most once; all other callers wait and share the result. Used by the
// A07 refresh storm controller to dedupe per-account refresh attempts.
type SingleFlight struct {
	mu    sync.Mutex
	calls map[string]*singleFlightCall
}

// NewSingleFlight returns an initialized SingleFlight.
func NewSingleFlight() *SingleFlight {
	return &SingleFlight{calls: make(map[string]*singleFlightCall)}
}

// Do executes fn for the given key if no call is in flight. Concurrent callers
// with the same key wait for the single in-flight execution and share its
// result. The shared return value is true when the caller is a follower
// (did not execute fn itself).
//
// If fn panics, the panic is recovered, converted to an error of the form
// "singleflight: function panicked: <value>", and broadcast to all followers
// (so they are never left waiting on a permanently-blocked call).
func (sf *SingleFlight) Do(key string, fn func() (any, error)) (val any, err error, shared bool) {
	sf.mu.Lock()
	if call, ok := sf.calls[key]; ok {
		sf.mu.Unlock()
		call.wg.Wait()
		return call.val, call.err, true
	}

	call := &singleFlightCall{}
	call.wg.Add(1)
	sf.calls[key] = call
	sf.mu.Unlock()

	// Defer-everything pattern: even on panic, val/err are set, the in-flight
	// entry is cleared, and followers are woken via wg.Done. Order matters —
	// set val/err BEFORE wg.Done so followers see the final result.
	defer func() {
		if r := recover(); r != nil {
			val = nil
			err = fmt.Errorf("singleflight: function panicked: %v", r)
		}
		call.val = val
		call.err = err

		sf.mu.Lock()
		// Only delete if the entry is still ours; Forget() may have replaced it.
		if sf.calls[key] == call {
			delete(sf.calls, key)
		}
		sf.mu.Unlock()

		call.wg.Done()
	}()

	val, err = fn()
	return val, err, false
}

// Forget removes key from the in-flight map so the next Do call for this key
// executes fn again rather than joining a stale result. Safe to call while a
// Do is in flight; the in-flight callers still see the original result, but
// new callers will start a fresh execution.
func (sf *SingleFlight) Forget(key string) {
	sf.mu.Lock()
	delete(sf.calls, key)
	sf.mu.Unlock()
}

// InFlight reports whether a call for key is currently in progress.
// Non-blocking.
func (sf *SingleFlight) InFlight(key string) bool {
	sf.mu.Lock()
	_, ok := sf.calls[key]
	sf.mu.Unlock()
	return ok
}
