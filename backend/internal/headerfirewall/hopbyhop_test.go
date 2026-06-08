package headerfirewall

import (
	"net/http"
	"testing"
)

// TestStripHopByHopRequestHeaders guards SEC-098: hop-by-hop request headers
// (and any header named in Connection) must be removed before forwarding, while
// ordinary end-to-end headers are preserved. MUTATION: skip the h.Del in the
// hopByHopRequestHeaders loop -> Keep-Alive/Upgrade survive -> this test fails.
func TestStripHopByHopRequestHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Keep-Alive", "timeout=5")
	h.Set("Upgrade", "websocket")
	h.Set("Transfer-Encoding", "chunked")
	h.Set("Connection", "X-Custom-Hop")
	h.Set("X-Custom-Hop", "drop-me")
	h.Set("Authorization", "Bearer keep")
	h.Set("Content-Type", "application/json")

	StripHopByHopRequestHeaders(h)

	for _, name := range []string{"Keep-Alive", "Upgrade", "Transfer-Encoding", "Connection", "X-Custom-Hop"} {
		if got := h.Get(name); got != "" {
			t.Fatalf("hop-by-hop header %q=%q want stripped", name, got)
		}
	}
	if got := h.Get("Authorization"); got != "Bearer keep" {
		t.Fatalf("Authorization=%q want preserved (end-to-end header wrongly stripped)", got)
	}
	if got := h.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type=%q want preserved", got)
	}
}
