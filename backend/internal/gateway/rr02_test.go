package gateway

import (
	"testing"
	"time"
)

// RR-02: derive cooldown from the error BODY when no Retry-After header (Codex
// usage_limit_reached only puts resets_at / resets_in_seconds in the body).
func TestRetryAfterFromBody(t *testing.T) {
	now := time.Unix(1000, 0)
	cases := []struct {
		body string
		want int64
	}{
		{`{"error":{"resets_in_seconds":30}}`, 30000},
		{`{"error":{"resets_at":1060}}`, 60000}, // 60s in the future
		{`{"error":{"resets_at":900}}`, 0},      // already past -> 0
		{`{"error":{"message":"nope"}}`, 0},     // no reset fields
		{`not json`, 0},                         // unparseable
		{``, 0},                                 // empty
	}
	for _, c := range cases {
		// MUTATION GUARD: making retryAfterFromBody ignore the body (return 0)
		// collapses every non-zero case -> red.
		if got := retryAfterFromBody([]byte(c.body), now); got != c.want {
			t.Fatalf("retryAfterFromBody(%q)=%d want %d", c.body, got, c.want)
		}
	}
}
