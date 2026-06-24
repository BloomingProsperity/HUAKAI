package headerfirewall

import (
	"net/http"
	"testing"
)

// Egress anti-detection hygiene for HUAKAI outbound relay requests.
// MUTATION coverage:
//   - making NormalizeEgressRequestHeaders a no-op leaves proxy-leak headers in the
//     outbound request → the strip assertions go red.
//   - over-stripping (e.g. deleting Authorization/X-Api-Key) → the preserve assertions go red.
func TestNormalizeEgressRequestHeadersStripsProxyLeaks(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer secret")
	h.Set("X-Api-Key", "k")
	h.Set("Anthropic-Version", "2023-06-01")
	h.Set("Content-Type", "application/json")
	h.Set("X-Forwarded-For", "203.0.113.7")
	h.Set("X-Real-Ip", "203.0.113.7")
	h.Set("Via", "1.1 vegur")
	h.Set("Cf-Connecting-Ip", "203.0.113.7")
	h.Set("True-Client-Ip", "203.0.113.7")
	h.Set("Cf-Ray", "8aabbccdd")
	h.Set("Forwarded", "for=203.0.113.7")
	h.Set("X-Forwarded-Proto", "https")

	NormalizeEgressRequestHeaders(h)

	for _, leak := range []string{
		"X-Forwarded-For", "X-Real-Ip", "Via", "Cf-Connecting-Ip",
		"True-Client-Ip", "Cf-Ray", "Forwarded", "X-Forwarded-Proto",
	} {
		if h.Get(leak) != "" {
			t.Fatalf("egress leak header %q not stripped (got %q) — upstream WAF can fingerprint us as a relay", leak, h.Get(leak))
		}
	}
	if h.Get("Authorization") != "Bearer secret" || h.Get("X-Api-Key") != "k" ||
		h.Get("Anthropic-Version") != "2023-06-01" || h.Get("Content-Type") != "application/json" {
		t.Fatalf("legit/auth headers wrongly stripped: %+v", h)
	}
}
