package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
)

func runGate(t *testing.T, gate func(http.Handler) http.Handler, remoteAddr, path string, headers map[string]string) (reached bool, status int) {
	t.Helper()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = remoteAddr
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	gate(next).ServeHTTP(rec, req)
	return reached, rec.Code
}

// TestInternalSourceGate_RejectsPublicAllowsTrusted: /internal/* is reachable
// from loopback / RFC1918 / link-local peers but NOT from a public-internet peer;
// non-/internal paths are never gated. MUTATION: drop the IsLoopback/IsPrivate
// allow -> trusted peers get 404; drop the prefix check -> public /v1 gets 404.
func TestInternalSourceGate_RejectsPublicAllowsTrusted(t *testing.T) {
	gate := internalSourceGate(nil, nil)
	cases := []struct {
		name, remote, path string
		wantReached        bool
	}{
		{"public peer to /internal -> rejected", "203.0.113.7:51000", "/internal/keys", false},
		{"loopback to /internal -> allowed", "127.0.0.1:51000", "/internal/keys", true},
		{"ipv6 loopback to /internal -> allowed", "[::1]:51000", "/internal/hermes/tool-execute", true},
		{"rfc1918 private to /internal -> allowed", "10.4.5.6:443", "/internal/hermes/tool-execute", true},
		{"private 172.16 to /internal -> allowed", "172.16.9.9:443", "/internal/runner/bootstrap", true},
		{"public peer to /v1 (non-internal) -> untouched", "203.0.113.7:51000", "/v1/chat/completions", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached, status := runGate(t, gate, tc.remote, tc.path, nil)
			if reached != tc.wantReached {
				t.Fatalf("reached=%v want=%v (status=%d)", reached, tc.wantReached, status)
			}
			if !tc.wantReached && status != http.StatusNotFound {
				t.Fatalf("rejected request must be 404 (invisible), got %d", status)
			}
		})
	}
}

// TestInternalSourceGate_IgnoresXForwardedForSpoof is the security tripwire: a
// public peer that sets `X-Forwarded-For: 127.0.0.1` must STILL be rejected — the
// gate judges the real socket RemoteAddr, never the spoofable header. MUTATION:
// make the gate read X-Forwarded-For instead of RemoteAddr -> this passes -> RED.
func TestInternalSourceGate_IgnoresXForwardedForSpoof(t *testing.T) {
	gate := internalSourceGate(nil, nil)
	reached, status := runGate(t, gate, "203.0.113.7:51000", "/internal/keys",
		map[string]string{"X-Forwarded-For": "127.0.0.1", "X-Real-IP": "127.0.0.1"})
	if reached {
		t.Fatal("X-Forwarded-For: 127.0.0.1 must NOT bypass the gate from a public socket peer")
	}
	if status != http.StatusNotFound {
		t.Fatalf("spoof attempt must be 404, got %d", status)
	}
}

// TestInternalSourceGate_RunsBeforeRealIP guards the ORDERING invariant: the gate
// must sit BEFORE chi's middleware.RealIP, which rewrites RemoteAddr from
// X-Forwarded-For with no trusted-proxy check. With the correct order
// gate(RealIP(next)), a public peer spoofing XFF=127.0.0.1 is rejected by the
// gate before RealIP can rewrite the address. MUTATION: invert to
// RealIP(gate(next)) -> RealIP rewrites RemoteAddr to 127.0.0.1, the gate then
// sees a loopback peer and admits it -> next reached -> RED.
func TestInternalSourceGate_RunsBeforeRealIP(t *testing.T) {
	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true })

	// Correct order: gate first, then RealIP.
	handler := internalSourceGate(nil, nil)(middleware.RealIP(next))
	req := httptest.NewRequest(http.MethodGet, "/internal/keys", nil)
	req.RemoteAddr = "203.0.113.7:51000" // public socket peer
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if reached {
		t.Fatal("gate must reject the public peer before RealIP rewrites RemoteAddr from X-Forwarded-For")
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

// TestInternalSourceAllowed_ExtraCIDR: an operator-configured extra CIDR widens
// the allow-set; a public IP outside it stays rejected.
func TestInternalSourceAllowed_ExtraCIDR(t *testing.T) {
	_, allowed, _ := net.ParseCIDR("198.51.100.0/24")
	extra := parseInternalAllowCIDRs("198.51.100.0/24")
	if len(extra) != 1 || !extra[0].IP.Equal(allowed.IP) {
		t.Fatalf("parseInternalAllowCIDRs did not parse the CIDR: %+v", extra)
	}
	if !internalSourceAllowed("198.51.100.42:9000", extra) {
		t.Fatal("IP inside the extra CIDR must be allowed")
	}
	if internalSourceAllowed("203.0.113.7:9000", extra) {
		t.Fatal("public IP outside loopback/private/extra must be rejected")
	}
	if internalSourceAllowed("garbage", nil) {
		t.Fatal("unparseable peer must fail closed")
	}
}

// TestInternalSourceAllowed_IPClassificationEdges locks the IP-classification
// edge cases a network gate must get right: an IPv4-mapped IPv6 of a PUBLIC
// address must NOT be admitted (Go unmaps before classifying), while genuine
// IPv6 ULA (fc00::/7) and v4-mapped private/loopback are trusted. A regression
// here is a silent public-internet bypass.
func TestInternalSourceAllowed_IPClassificationEdges(t *testing.T) {
	rejected := []string{
		"[::ffff:203.0.113.7]:9000", // IPv4-mapped IPv6 of a PUBLIC v4 — must stay rejected
		"[2001:db8::1]:9000",        // public IPv6
		"[100.64.0.1]:0",            // CGNAT (not RFC1918) — rejected without an explicit CIDR
	}
	for _, a := range rejected {
		if internalSourceAllowed(a, nil) {
			t.Fatalf("%s must be rejected (public/non-private)", a)
		}
	}
	allowed := []string{
		"[fd12:3456::1]:9000",     // IPv6 ULA (private)
		"[::ffff:10.0.0.5]:9000",  // v4-mapped private
		"[::ffff:127.0.0.1]:9000", // v4-mapped loopback
		"[fe80::1]:9000",          // link-local
	}
	for _, a := range allowed {
		if !internalSourceAllowed(a, nil) {
			t.Fatalf("%s must be allowed (private/loopback/link-local)", a)
		}
	}
}
