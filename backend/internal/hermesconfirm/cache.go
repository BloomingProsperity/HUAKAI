// Package hermesconfirm holds the in-process single-use correlation-id store
// (Cache) backing Hermes 的 mutating-tool 的 dry-run→confirm 安全原语(L2)。
//
// 它从 internal/hermeshttp 提取到独立共享包,**纯重构、行为零变**:这样 operator 确认侧
// (hermeshttp)与未来 LLM 提议侧(hermeschat,Phase B)能注入**同一个 Cache 实例**——
// hermeshttp 单向 import hermeschat,故确认/提议共用的类型必须落在两者都能 import 的中立包,
// 否则会构成 import 环。逻辑与原 confirmCache 逐字保留(单次消费 + 六元组绑定 + 5 分钟 TTL +
// crypto 随机 id),仅大小写导出 + 改包名。
package hermesconfirm

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// ConfirmTTL is the window within which a dry-run preview's correlation_id can
// be re-submitted with confirm=true to actually execute. After this, the
// correlation is stale and a confirm=true request is rejected (400) — never
// executed. Kept short so a leaked/observed correlation_id has a small blast
// window.
const ConfirmTTL = 5 * time.Minute

// PendingConfirmation is one outstanding dry-run preview awaiting confirmation.
// It pins the EXACT tool + tenant + actor + target the preview was computed for,
// so a confirm cannot be redirected to a different tool/tenant/target than the
// one the operator previewed (a confirm with mismatched fields is rejected).
type PendingConfirmation struct {
	ToolName  string
	TenantID  int64
	ActorID   int64
	TokenID   int64
	TargetID  int64
	ExpiresAt time.Time
}

// Cache is the in-process single-use correlation-id store backing L2
// (dry-run-first + confirmation). A correlation_id is issued on a dry-run and
// CONSUMED (deleted) the moment it is taken for execution, so it can drive at
// most one mutation (a re-used correlation_id finds nothing and is rejected).
//
// In-process is intentional for this wave: the confirm must land on the SAME
// replica that issued the preview. A multi-replica operator UI re-issues the
// dry-run if it hits a different replica (the preview is cheap + read-only); a
// shared cache is a documented follow-up, not a safety hole (a missing
// correlation_id always fails closed → 400, never executes).
type Cache struct {
	mu      sync.Mutex
	entries map[string]PendingConfirmation
	now     func() time.Time
}

// NewCache constructs an empty Cache backed by the real clock.
func NewCache() *Cache {
	return &Cache{
		entries: make(map[string]PendingConfirmation),
		now:     time.Now,
	}
}

// Issue stores a pending confirmation and returns a fresh, unguessable
// correlation_id. The id is 128 bits of crypto-random hex so it cannot be
// predicted or enumerated by a caller who never saw the preview.
func (c *Cache) Issue(p PendingConfirmation) (string, error) {
	id, err := randomCorrelationID()
	if err != nil {
		return "", err
	}
	p.ExpiresAt = c.now().Add(ConfirmTTL)
	c.mu.Lock()
	c.entries[id] = p
	// Opportunistically evict expired entries so the map cannot grow unbounded
	// from previews that are never confirmed.
	c.evictExpiredLocked()
	c.mu.Unlock()
	return id, nil
}

// Consume atomically looks up AND removes the correlation_id (single-use). It
// returns (entry, true) only when the id exists, is unexpired, and matches the
// supplied tool/tenant/actor binding; otherwise (zero, false). Because the
// delete happens under the same lock as the lookup, two concurrent confirms on
// the same id can never both succeed — at most one consumes it.
func (c *Cache) Consume(id, toolName string, tenantID, actorID, tokenID int64) (PendingConfirmation, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[id]
	if !ok {
		return PendingConfirmation{}, false
	}
	// Remove first so even a mismatched/expired hit is single-use (a guessed id
	// cannot be probed repeatedly).
	delete(c.entries, id)
	if c.now().After(entry.ExpiresAt) {
		return PendingConfirmation{}, false
	}
	// Bind the confirm to the EXACT operator that previewed: tool + tenant +
	// actor-user + the operator's admin TokenID. Without the TokenID check a
	// different operator (distinct admin token) acting in the same tenant-user
	// context could consume another operator's preview and execute the mutation.
	if entry.ToolName != toolName || entry.TenantID != tenantID || entry.ActorID != actorID || entry.TokenID != tokenID {
		return PendingConfirmation{}, false
	}
	return entry, true
}

func (c *Cache) evictExpiredLocked() {
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
