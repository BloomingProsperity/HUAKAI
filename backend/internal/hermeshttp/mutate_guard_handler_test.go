package hermeshttp

import (
	"net/http"
	"sync"
	"testing"
	"time"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops/mutateguard"
)

// This file exercises the S2 (c) handler-side per-operator-token rate limiter:
// it counts only REAL confirmed executes (previews/denials do not), it is keyed
// per operator TOKEN (not per tenant), and with NO knobs set the whole mutating
// path is byte-for-byte legacy behavior. The fixed clock makes the window
// deterministic. Fakes (fakeMutator / mutatingRegistry / mutateCounters /
// buildMutateHandler / mutateRequest / operator / decodeBody) are reused from
// tools_mutate_handler_test.go + tools_handler_test.go in this package.

// fixedClock returns a Now func that advances only when the test ticks it, so the
// sliding window does not drift during the test.
type fixedClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fixedClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// operatorTok builds an operator identity with a specific admin token id so a
// test can drive two DIFFERENT operator tokens within the SAME tenant.
func operatorTok(tenant, tokenID int64) (sessionauth.Identity, adminActor) {
	ident, actor := operator(tenant)
	actor.TokenID = tokenID
	return ident, actor
}

// confirmOnce runs a full preview+confirm for account_pause and returns the
// confirm response recorder.
func confirmOnce(t *testing.T, h handler, ident sessionauth.Identity, actor adminActor) (status int) {
	t.Helper()
	preview := mutateRequest(h, ident, actor, `{"tool_name":"account_pause","args":{"account_id":5}}`)
	corr, ok := decodeBody(t, preview)["correlation_id"].(string)
	if !ok {
		t.Fatalf("no correlation_id in preview body=%s", preview.Body.String())
	}
	confirm := mutateRequest(h, ident, actor, `{"tool_name":"account_pause","args":{"account_id":5},"confirm":true,"correlation_id":"`+corr+`"}`)
	return confirm.Code
}

// --- Test 6: per-token rate limit, per-token NOT per-tenant ------------------

func TestS2_RateLimitPerTokenNotPerTenant(t *testing.T) {
	// Regression (S2 c, DISCRIMINATING): PER_TOKEN=2 with an injected clock. Token A
	// gets two confirms (both pass) and a third confirm -> 429. Token B (SAME
	// tenant, different token) gets its FIRST confirm -> passes, proving the budget
	// is per operator TOKEN, not per tenant.
	//
	// Mutation check (run + RED confirmed, then restored):
	//   - key the limiter on tenant id instead of actor.TokenID -> token B's first
	//     confirm is throttled by token A's budget -> the `bFirst==200` guard RED;
	//   - delete the limiter check in confirmMutation -> token A's THIRD confirm
	//     passes (200) -> the `aThird==429` guard RED.
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	c := &mutateCounters{}
	h := buildMutateHandler(mutatingRegistry(c), &fakeToolCalls{}, &fakeMutator{})
	h.mutateRateLimiter = mutateguard.NewRateLimiter(2, time.Minute, 0, clock.Now)

	identA, actorA := operatorTok(7, 1001)
	identB, actorB := operatorTok(7, 2002) // same tenant 7, different token

	if s := confirmOnce(t, h, identA, actorA); s != http.StatusOK {
		t.Fatalf("token A confirm #1 status=%d want 200", s)
	}
	if s := confirmOnce(t, h, identA, actorA); s != http.StatusOK {
		t.Fatalf("token A confirm #2 status=%d want 200", s)
	}
	aThird := confirmOnce(t, h, identA, actorA)
	if aThird != http.StatusTooManyRequests {
		t.Fatalf("token A confirm #3 status=%d want 429 (PER_TOKEN=2 exhausted)", aThird)
	}
	bFirst := confirmOnce(t, h, identB, actorB)
	if bFirst != http.StatusOK {
		t.Fatalf("token B confirm #1 status=%d want 200 — budget is per-token, not per-tenant", bFirst)
	}
}

// --- Test 7: rate limit counts only real executes, not previews -------------

func TestS2_RateLimitCountsOnlyConfirmedNotPreviews(t *testing.T) {
	// Regression (S2 c, DISCRIMINATING): with PER_TOKEN=2, five dry-run PREVIEWS
	// must NOT burn budget; the two subsequent CONFIRMS both pass. The limiter is
	// checked AFTER the confirm-id consume, so previews never reach it.
	//
	// Mutation check (run + RED confirmed, then restored): move the limiter check
	// ABOVE the `if !req.Confirm { previewMutation } ` branch (e.g. into
	// executeMutatingTool before the confirm split, or before the consume) so
	// previews burn budget; then 5 previews exhaust the budget of 2 and the first
	// confirm returns 429 -> the `c1==200` guard goes RED.
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	c := &mutateCounters{}
	h := buildMutateHandler(mutatingRegistry(c), &fakeToolCalls{}, &fakeMutator{})
	h.mutateRateLimiter = mutateguard.NewRateLimiter(2, time.Minute, 0, clock.Now)

	ident, actor := operatorTok(7, 3003)

	// 5 previews (dry-run, confirm=false) — these must NOT count.
	for i := 0; i < 5; i++ {
		preview := mutateRequest(h, ident, actor, `{"tool_name":"account_pause","args":{"account_id":5}}`)
		if preview.Code != http.StatusOK || decodeBody(t, preview)["dry_run"] != true {
			t.Fatalf("preview #%d did not return a dry-run: status=%d body=%s", i, preview.Code, preview.Body.String())
		}
	}

	if c1 := confirmOnce(t, h, ident, actor); c1 != http.StatusOK {
		t.Fatalf("confirm #1 after 5 previews status=%d want 200 — previews must not burn budget", c1)
	}
	if c2 := confirmOnce(t, h, ident, actor); c2 != http.StatusOK {
		t.Fatalf("confirm #2 after 5 previews status=%d want 200 (budget=2)", c2)
	}
}

// --- Test 8: all knobs unset == byte-for-byte legacy behavior ----------------

func TestS2_AllKnobsUnsetIsLegacyBehavior(t *testing.T) {
	// Regression (S2, DEFAULT-CONSERVATIVE / DISCRIMINATING): with NO guard wired —
	// handler built by buildMutateHandler (no rate limiter) over an orchestrator
	// with NO concurrency cap, NO tx deadline — the mutating path behaves exactly
	// like the legacy path: many confirms in a row all succeed (no 429 busy / no
	// rate-limit rejection), and no SET LOCAL statement_timeout is issued.
	//
	// Mutation check (run + RED confirmed, then restored): make any guard's default
	// non-disable when its sentinel is unset — e.g. have NewMutateOrchestrator
	// default txDeadline to 90s even with no WithTxDeadline option, or have
	// NewRateLimiter treat limit<=0 as "default 30" instead of disabled. Then a
	// legacy deployment starts issuing statement_timeout / 429s and these
	// all-success guards go RED.

	// Handler with no rate limiter -> no 429 across many confirms; the
	// orchestrator's own legacy behavior (no statement_timeout, no ErrMutateBusy
	// with no options) is proven in internal/hermesops/mutate_guard_test.go.
	c := &mutateCounters{}
	h := buildMutateHandler(mutatingRegistry(c), &fakeToolCalls{}, &fakeMutator{})
	// h.mutateRateLimiter left nil (disabled sentinel).
	ident, actor := operator(7)
	for i := 0; i < 20; i++ {
		if s := confirmOnce(t, h, ident, actor); s != http.StatusOK {
			t.Fatalf("confirm #%d status=%d want 200 — unset rate knob must be unbounded (legacy)", i, s)
		}
	}
	if c.mutates != 20 {
		t.Fatalf("mutates=%d want 20 — every confirm should have mutated with no guard", c.mutates)
	}
}
