package sessioncap

import (
	"sync"
	"testing"
	"time"
)

// newRegistryWithClock 用一个受控时钟构造一个 Registry。
func newRegistryWithClock(idleTTL time.Duration, now func() time.Time) *Registry {
	r := NewRegistry(idleTTL)
	r.now = now
	return r
}

func TestWouldExceed_NewSessionOverCap(t *testing.T) {
	r := NewRegistry(5 * time.Minute)
	r.Register(1, "sess-a")
	r.Register(1, "sess-b")
	r.Register(1, "sess-c")

	if !r.WouldExceed(1, "sess-d", 3) {
		t.Fatal("expected WouldExceed=true for new session when at cap")
	}
}

func TestWouldExceed_ExistingSessionAllowed(t *testing.T) {
	r := NewRegistry(5 * time.Minute)
	r.Register(1, "sess-a")
	r.Register(1, "sess-b")
	r.Register(1, "sess-c")

	if r.WouldExceed(1, "sess-c", 3) {
		t.Fatal("expected WouldExceed=false for existing session (re-bind must be allowed)")
	}
}

func TestWouldExceed_MaxZeroDefaultSafety(t *testing.T) {
	r := NewRegistry(5 * time.Minute)
	for i := 0; i < 99; i++ {
		r.Register(1, string(rune('a'+i%26))+string(rune('A'+i/26%26)))
	}
	if r.WouldExceed(1, "new-sess", 0) {
		t.Fatal("expected WouldExceed=false when max=0 (unlimited)")
	}
}

func TestWouldExceed_EmptyRegistryFailOpen(t *testing.T) {
	r := NewRegistry(5 * time.Minute)
	if r.WouldExceed(1, "new-sess", 3) {
		t.Fatal("expected WouldExceed=false when registry has no sessions for account")
	}
}

func TestWouldExceed_UnderCap(t *testing.T) {
	r := NewRegistry(5 * time.Minute)
	r.Register(1, "sess-a")
	r.Register(1, "sess-b")

	if r.WouldExceed(1, "sess-new", 3) {
		t.Fatal("expected WouldExceed=false when under cap")
	}
}

func TestWouldExceed_TTLExpiry(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := newRegistryWithClock(5*time.Minute, func() time.Time { return now })

	r.Register(1, "old-a")
	r.Register(1, "old-b")
	r.Register(1, "old-c")

	now = now.Add(6 * time.Minute)

	if r.WouldExceed(1, "sess-new", 3) {
		t.Fatal("expected WouldExceed=false after TTL expiry of all sessions")
	}
}

func TestRegister_Idempotent(t *testing.T) {
	r := NewRegistry(5 * time.Minute)
	r.Register(1, "sess-a")
	r.Register(1, "sess-a")
	r.Register(1, "sess-a")

	if !r.WouldExceed(1, "sess-b", 1) {
		t.Fatal("expected WouldExceed=true: account at cap with 1 session")
	}
	if r.WouldExceed(1, "sess-a", 1) {
		t.Fatal("expected WouldExceed=false: existing session re-bind allowed")
	}
}

func TestConcurrency_RaceDetector(t *testing.T) {
	r := NewRegistry(5 * time.Minute)
	const goroutines = 50
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				sess := string(rune('a'+(g*iterations+i)%26)) + string(rune('A'+(g*iterations+i)/26%26))
				r.Register(int64(g%5), sess)
				_ = r.WouldExceed(int64(g%5), sess, 10)
			}
		}()
	}
	wg.Wait()
}
