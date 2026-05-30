package clientip

import (
	"net/http"
	"testing"
)

func req(remoteAddr, xff string) *http.Request {
	r := &http.Request{RemoteAddr: remoteAddr, Header: http.Header{}}
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

func mustResolver(t *testing.T, cidrs ...string) *Resolver {
	t.Helper()
	r, err := NewResolver(cidrs)
	if err != nil {
		t.Fatalf("NewResolver(%v): %v", cidrs, err)
	}
	return r
}

// TestResolverClientIP is the S2-109 security core: forwarded headers are honored ONLY
// when the immediate peer is a configured trusted proxy, and the real client is taken
// as the RIGHTMOST untrusted hop (the address our outermost trusted proxy actually
// observed) — never a client-controlled leftmost entry.
//
// Mutation checks (each row that would flip):
//   - drop the `len(r.trusted)==0 -> peer` guard: no_trusted_config / spoof_no_config flip.
//   - drop the `!isTrusted(peer) -> peer` guard: untrusted_peer_spoof returns the forged
//     client instead of the socket peer (the headline vuln) → red.
//   - walk X-Forwarded-For left-to-right instead of right-to-left: multi_hop_spoof returns
//     the forged leftmost 1.1.1.1 instead of the real 198.51.100.9 → red.
func TestResolverClientIP(t *testing.T) {
	trusted := mustResolver(t, "10.0.0.0/8", "2001:db8::/32")

	cases := []struct {
		name     string
		resolver *Resolver
		remote   string
		xff      string
		want     string
	}{
		// No trusted proxies configured: forwarded headers are ignored entirely.
		{"no_trusted_config", mustResolver(t), "203.0.113.7:443", "198.51.100.9", "203.0.113.7"},
		{"spoof_no_config", mustResolver(t), "203.0.113.7:443", "1.1.1.1", "203.0.113.7"},
		{"nil_resolver", nil, "203.0.113.7:443", "1.1.1.1", "203.0.113.7"},

		// Untrusted immediate peer: its forwarded header is attacker-controlled → ignored.
		{"untrusted_peer_spoof", trusted, "203.0.113.7:443", "198.51.100.9", "203.0.113.7"},

		// Trusted peer: honor the forwarded chain.
		{"trusted_peer_single_client", trusted, "10.1.2.3:5000", "198.51.100.9", "198.51.100.9"},
		{"trusted_peer_no_xff", trusted, "10.1.2.3:5000", "", "10.1.2.3"},
		{"all_hops_trusted", trusted, "10.1.2.3:5000", "10.9.9.9, 10.8.8.8", "10.1.2.3"},

		// Anti-spoof: a client that forges a leftmost XFF entry. Real chain as appended by
		// our proxies is: [forged 1.1.1.1], realclient 198.51.100.9, trusted hop 10.9.9.9.
		// Rightmost-untrusted walk returns the real client, never the forgery.
		{"multi_hop_spoof", trusted, "10.1.2.3:5000", "1.1.1.1, 198.51.100.9, 10.9.9.9", "198.51.100.9"},

		// Malformed rightmost hop (appended by our trusted proxy) → cannot trust the chain,
		// fall back to the trusted peer rather than a spoofable value.
		{"malformed_rightmost_hop", trusted, "10.1.2.3:5000", "198.51.100.9, garbage", "10.1.2.3"},

		// IPv6 trusted peer + untrusted IPv6 client.
		{"ipv6_trusted_peer", trusted, "[2001:db8::1]:5000", "2606:4700::1234", "2606:4700::1234"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.resolver.ClientIP(req(tc.remote, tc.xff)); got != tc.want {
				t.Fatalf("ClientIP(remote=%q xff=%q)=%q want %q", tc.remote, tc.xff, got, tc.want)
			}
		})
	}
}

// TestResolverClientIPRepeatedXFFHeaders guards the repeated-header case: a request may carry
// X-Forwarded-For as MULTIPLE header fields (RFC 7230 §3.2.2). A client can spoof the first
// field; our trusted proxy appends the real client in a later field. The resolver must join all
// fields in order before the right-to-left walk — http.Header.Get reads only the first.
//
// Mutation: revert to Header.Get("X-Forwarded-For") → only "1.1.1.1" is parsed and returned → red.
func TestResolverClientIPRepeatedXFFHeaders(t *testing.T) {
	trusted := mustResolver(t, "10.0.0.0/8")
	r := &http.Request{RemoteAddr: "10.1.2.3:5000", Header: http.Header{}}
	r.Header.Add("X-Forwarded-For", "1.1.1.1")      // client-supplied spoof (first field)
	r.Header.Add("X-Forwarded-For", "198.51.100.9") // appended by our trusted proxy (later field)
	if got := trusted.ClientIP(r); got != "198.51.100.9" {
		t.Fatalf("repeated XFF: got %q want 198.51.100.9 (must join all header fields, not just the first)", got)
	}
}

// TestResolverBareIPTrust proves a bare-IP allowlist entry trusts exactly that host (/32),
// not its neighbours. Mutation: widen the bare-IP prefix → the .4 peer would wrongly honor XFF.
func TestResolverBareIPTrust(t *testing.T) {
	r := mustResolver(t, "10.1.2.3")
	if got := r.ClientIP(req("10.1.2.3:5000", "198.51.100.9")); got != "198.51.100.9" {
		t.Fatalf("trusted bare IP peer: got %q want forwarded client", got)
	}
	if got := r.ClientIP(req("10.1.2.4:5000", "198.51.100.9")); got != "10.1.2.4" {
		t.Fatalf("neighbour of bare IP must be untrusted: got %q want socket peer", got)
	}
}

// TestNewResolverRejectsMalformedCIDR proves a bad allowlist entry fails loudly at boot
// rather than being silently dropped (which could read as "trust nothing" or be confused
// with "trust everything"). Mutation: swallow the parse error → this returns nil err → red.
func TestNewResolverRejectsMalformedCIDR(t *testing.T) {
	if _, err := NewResolver([]string{"10.0.0.0/8", "not-an-ip"}); err == nil {
		t.Fatal("NewResolver must reject a malformed trusted-proxy entry")
	}
	// Blank/whitespace entries are skipped, not errors.
	if _, err := NewResolver([]string{"", "  "}); err != nil {
		t.Fatalf("blank entries must be skipped, got %v", err)
	}
}
