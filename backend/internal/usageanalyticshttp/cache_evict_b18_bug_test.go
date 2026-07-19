package usageanalyticshttp

import (
	"fmt"
	"testing"
	"time"
)

// TestGetOrLoadEvictsExpiredEntriesB18 pins the correct behavior for bug B18:
// the snapshot cache must evict expired entries so that a client varying the
// cache key (e.g. the leaderboard/performance `window` label) cannot drive
// unbounded memory growth. Distinct keys whose TTL has elapsed must not remain
// resident in state.entries forever — under the buggy code an expired entry is
// only ever removed if the identical key happens to be queried again, so a
// monotonically varying window accumulates entries with no ceiling.
func TestGetOrLoadEvictsExpiredEntriesB18(t *testing.T) {
	loader := func() (any, error) { return "v", nil }

	// Simulate a client hitting the endpoint with many distinct window labels,
	// each producing a distinct cache key with a short TTL that then expires.
	const seeded = 200
	keys := make([]string, seeded)
	for i := 0; i < seeded; i++ {
		keys[i] = fmt.Sprintf("b18-expired-window=%dms", i)
		if _, _, err := GetOrLoad(keys[i], time.Millisecond, loader); err != nil {
			t.Fatalf("seed load %d err=%v", i, err)
		}
	}

	// Let every seeded entry's TTL elapse.
	time.Sleep(30 * time.Millisecond)

	// Further activity from the same client (a new distinct window). A correct
	// cache takes this opportunity to reclaim the expired entries.
	if _, _, err := GetOrLoad("b18-fresh-window", time.Minute, loader); err != nil {
		t.Fatalf("fresh load err=%v", err)
	}

	state.mu.Lock()
	remaining := 0
	for _, k := range keys {
		if _, ok := state.entries[k]; ok {
			remaining++
		}
	}
	state.mu.Unlock()

	if remaining != 0 {
		t.Fatalf("expired cache entries retained: %d of %d expired keys still resident; "+
			"cache never evicts -> unbounded memory growth as the window label varies", remaining, seeded)
	}
}
