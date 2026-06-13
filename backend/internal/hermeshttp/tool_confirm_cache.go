package hermeshttp

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// confirmTTL is the window within which a dry-run preview's correlation_id can
// be re-submitted with confirm=true to actually execute. After this, the
// correlation is stale and a confirm=true request is rejected (400) — never
// executed. Kept short so a leaked/observed correlation_id has a small blast
// window.
const confirmTTL = 5 * time.Minute

// pendingConfirmation is one outstanding dry-run preview awaiting confirmation.
// It pins the EXACT tool + tenant + actor + target the preview was computed for,
// so a confirm cannot be redirected to a different tool/tenant/target than the
// one the operator previewed (a confirm with mismatched fields is rejected).
type pendingConfirmation struct {
	ToolName  string
	TenantID  int64
	ActorID   int64
	TokenID   int64
	TargetID  int64
	ExpiresAt time.Time
}

// confirmCache is the in-process single-use correlation-id store backing L2
// (dry-run-first + confirmation). A correlation_id is issued on a dry-run and
// CONSUMED (deleted) the moment it is taken for execution, so it can drive at
// most one mutation (a re-used correlation_id finds nothing and is rejected).
//
// In-process is intentional for this wave: the confirm must land on the SAME
// replica that issued the preview. A multi-replica operator UI re-issues the
// dry-run if it hits a different replica (the preview is cheap + read-only); a
// shared cache is a documented follow-up, not a safety hole (a missing
// correlation_id always fails closed → 400, never executes).
type confirmCache struct {
	mu      sync.Mutex
	entries map[string]pendingConfirmation
	now     func() time.Time
}

func newConfirmCache() *confirmCache {
	return &confirmCache{
		entries: make(map[string]pendingConfirmation),
		now:     time.Now,
	}
}

// issue stores a pending confirmation and returns a fresh, unguessable
// correlation_id. The id is 128 bits of crypto-random hex so it cannot be
// predicted or enumerated by a caller who never saw the preview.
func (c *confirmCache) issue(p pendingConfirmation) (string, error) {
	id, err := randomCorrelationID()
	if err != nil {
		return "", err
	}
	p.ExpiresAt = c.now().Add(confirmTTL)
	c.mu.Lock()
	c.entries[id] = p
	// Opportunistically evict expired entries so the map cannot grow unbounded
	// from previews that are never confirmed.
	c.evictExpiredLocked()
	c.mu.Unlock()
	return id, nil
}

// consume atomically looks up AND removes the correlation_id (single-use). It
// returns (entry, true) only when the id exists, is unexpired, and matches the
// supplied tool/tenant/actor binding; otherwise (zero, false). Because the
// delete happens under the same lock as the lookup, two concurrent confirms on
// the same id can never both succeed — at most one consumes it.
func (c *confirmCache) consume(id, toolName string, tenantID, actorID int64) (pendingConfirmation, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[id]
	if !ok {
		return pendingConfirmation{}, false
	}
	// Remove first so even a mismatched/expired hit is single-use (a guessed id
	// cannot be probed repeatedly).
	delete(c.entries, id)
	if c.now().After(entry.ExpiresAt) {
		return pendingConfirmation{}, false
	}
	if entry.ToolName != toolName || entry.TenantID != tenantID || entry.ActorID != actorID {
		return pendingConfirmation{}, false
	}
	return entry, true
}

func (c *confirmCache) evictExpiredLocked() {
	now := c.now()
	for id, e := range c.entries {
		if now.After(e.ExpiresAt) {
			delete(c.entries, id)
		}
	}
}

func randomCorrelationID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "hmc_" + hex.EncodeToString(buf), nil
}
