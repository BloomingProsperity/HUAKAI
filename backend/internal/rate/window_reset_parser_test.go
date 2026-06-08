package rate

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestParseMultiWindowReset(t *testing.T) {
	// MUTATION: ignoring the 5h reset headers must make until/reason wrong.
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	reset5h := now.Add(30 * time.Minute)
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-reset", strconv.FormatInt(reset5h.Unix(), 10))
	headers.Set("anthropic-ratelimit-unified-5h-surpassed-threshold", "true")

	until, reason, ok := ParseMultiWindowReset(headers, now)
	if !ok {
		t.Fatal("ParseMultiWindowReset ok=false, want true")
	}
	if !until.Equal(reset5h) {
		t.Fatalf("until=%s want %s", until, reset5h)
	}
	if reason != ReasonRateLimit5h {
		t.Fatalf("reason=%s want %s", reason, ReasonRateLimit5h)
	}

	// GUARD: no exceeded-window marker means the parser must not refine cooldown.
	guardHeaders := http.Header{}
	guardHeaders.Set("anthropic-ratelimit-unified-5h-reset", strconv.FormatInt(reset5h.Unix(), 10))
	until, reason, ok = ParseMultiWindowReset(guardHeaders, now)
	if ok || !until.IsZero() || reason != "" {
		t.Fatalf("headers without exceeded marker parsed as (%s,%s,%v), want zero/false", until, reason, ok)
	}
}
