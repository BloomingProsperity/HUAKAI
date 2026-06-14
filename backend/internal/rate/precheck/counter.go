// Package precheck holds the in-memory RPM/TPM budget tracker used by the
// proactive rate pre-check selection gate (ROUTE-121). It lets the router skip
// an upstream account that is about to exceed its per-minute request or token
// budget, so the platform avoids provoking a user-visible 429 instead of merely
// reacting to one after the fact.
//
// The tracker is a fixed-window counter keyed by upstream account id with two
// independent windows: requests-per-minute and tokens-per-minute. A zero limit
// means "unlimited" (the account is never blocked on that dimension), so an
// account with no configured budget keeps its current behaviour. A nil *Counter
// is safe to use: Check allows and Record is a no-op, which keeps the gate
// fail-open when the tracker is not wired.
package precheck

import (
	"sync"
	"time"
)

// DefaultWindow is the budget window length when New is given a non-positive one.
const DefaultWindow = time.Minute

// Limits is one account's per-window budget. A zero (or negative) value on a
// dimension means that dimension is unlimited.
type Limits struct {
	RPM int64
	TPM int64
}

func (l Limits) rpmLimited() bool { return l.RPM > 0 }
func (l Limits) tpmLimited() bool { return l.TPM > 0 }

// Dimension names the budget a Decision tripped on.
type Dimension string

const (
	// DimensionNone means the request fits the budget.
	DimensionNone Dimension = ""
	// DimensionRPM means the requests-per-minute budget is full.
	DimensionRPM Dimension = "rpm"
	// DimensionTPM means the tokens-per-minute budget is full.
	DimensionTPM Dimension = "tpm"
)

// Decision is the outcome of a budget pre-check for a single account.
type Decision struct {
	Allowed   bool
	Dimension Dimension
}

// Counter is a concurrency-safe fixed-window RPM/TPM budget tracker.
type Counter struct {
	window time.Duration
	now    func() time.Time

	mu   sync.Mutex
	reqs map[int64]*windowCount
	toks map[int64]*windowCount
}

type windowCount struct {
	start time.Time
	count int64
}

// New returns a Counter using the given window length and clock. A non-positive
// window falls back to DefaultWindow; a nil clock falls back to time.Now.
func New(window time.Duration, now func() time.Time) *Counter {
	if window <= 0 {
		window = DefaultWindow
	}
	if now == nil {
		now = time.Now
	}
	return &Counter{
		window: window,
		now:    now,
		reqs:   make(map[int64]*windowCount),
		toks:   make(map[int64]*windowCount),
	}
}

// Check reports whether one more request of estTokens estimated tokens would
// fit account accountID's budget, WITHOUT consuming any of it. The caller that
// actually dispatches the request must follow up with Record. Check on a nil
// Counter, an account id <= 0, or fully-unlimited limits always allows.
func (c *Counter) Check(accountID int64, lim Limits, estTokens int64) Decision {
	if c == nil || accountID <= 0 || (!lim.rpmLimited() && !lim.tpmLimited()) {
		return Decision{Allowed: true, Dimension: DimensionNone}
	}
	if estTokens < 0 {
		estTokens = 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if lim.rpmLimited() {
		if c.live(c.reqs, accountID, now).count+1 > lim.RPM {
			return Decision{Allowed: false, Dimension: DimensionRPM}
		}
	}
	if lim.tpmLimited() {
		if c.live(c.toks, accountID, now).count+estTokens > lim.TPM {
			return Decision{Allowed: false, Dimension: DimensionTPM}
		}
	}
	return Decision{Allowed: true, Dimension: DimensionNone}
}

// Record consumes one request and tokens worth of budget for accountID in the
// current window. It is a no-op on a nil Counter or an account id <= 0.
func (c *Counter) Record(accountID int64, tokens int64) {
	if c == nil || accountID <= 0 {
		return
	}
	if tokens < 0 {
		tokens = 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.live(c.reqs, accountID, now).count++
	c.live(c.toks, accountID, now).count += tokens
}

// live returns the current window bucket for accountID, resetting it when the
// clock has crossed into a new fixed window. Callers must hold c.mu.
func (c *Counter) live(m map[int64]*windowCount, accountID int64, now time.Time) *windowCount {
	start := now.Truncate(c.window)
	wc := m[accountID]
	if wc == nil || wc.start.Before(start) {
		wc = &windowCount{start: start}
		m[accountID] = wc
	}
	return wc
}
