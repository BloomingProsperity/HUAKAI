// Package clientip resolves the real client IP of an inbound HTTP request behind a
// reverse proxy / CDN / load balancer, fail-closed against X-Forwarded-For spoofing.
//
// S2-109: IP-sensitive security paths (burst-limit keying, login anomaly evidence,
// voucher redeem source) previously read net/http.Request.RemoteAddr only. Behind a
// shared ingress (CDN/LB) every user collapses to the same RemoteAddr, causing
// false-positive burst blocks and useless anomaly evidence. The naive fix — trusting
// X-Forwarded-For — is itself a vulnerability, because any client can forge that
// header. This resolver honors forwarded headers ONLY when the immediate socket peer
// (and each successive upstream hop) is inside an operator-configured trusted-proxy
// CIDR allowlist; otherwise it returns the socket peer. With no trusted proxies
// configured it always returns RemoteAddr — the safe default for direct-exposure or
// single-tenant deployments.
package clientip

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Resolver maps an HTTP request to its best-effort real client IP. The zero value and
// a nil pointer are valid and behave as "no trusted proxies" (RemoteAddr only).
type Resolver struct {
	trusted []netip.Prefix
}

// NewResolver builds a Resolver from CIDR or bare-IP strings (e.g. "10.0.0.0/8",
// "192.168.1.1", "2001:db8::/32"). Blank entries are skipped; a bare IP is treated as
// a single-host prefix. An unparseable entry returns an error so a misconfigured
// allowlist fails loudly at boot rather than silently trusting nothing — or, worse,
// being mistaken for "trust everything".
func NewResolver(cidrs []string) (*Resolver, error) {
	var trusted []netip.Prefix
	for _, raw := range cidrs {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(s); err == nil {
			trusted = append(trusted, prefix.Masked())
			continue
		}
		addr, err := netip.ParseAddr(s)
		if err != nil {
			return nil, fmt.Errorf("clientip: invalid trusted proxy %q: %w", raw, err)
		}
		addr = addr.Unmap()
		trusted = append(trusted, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return &Resolver{trusted: trusted}, nil
}

// ClientIP returns the resolved client IP as a string. It is nil-safe: a nil Resolver,
// or one with no trusted proxies, returns the socket peer (RemoteAddr host) and ignores
// every forwarded header.
func (r *Resolver) ClientIP(req *http.Request) string {
	if req == nil {
		return ""
	}
	peer := remoteHost(req.RemoteAddr)
	if r == nil || len(r.trusted) == 0 {
		return peer
	}
	peerAddr, err := netip.ParseAddr(peer)
	if err != nil || !r.isTrusted(peerAddr) {
		// The immediate peer is not a configured trusted proxy, so any forwarded
		// header it presents is attacker-controlled. Trust only the socket address.
		return peer
	}
	// Peer is a trusted proxy: X-Forwarded-For was appended by trusted hops. Walk it
	// right-to-left, skipping trusted hops; the first (rightmost) untrusted address is
	// the real client as seen by our outermost trusted proxy. Leftmost entries are
	// client-spoofable, so they are never trusted on their own.
	//
	// A request may carry X-Forwarded-For as MULTIPLE header fields (RFC 7230 §3.2.2:
	// repeated fields are equivalent to a single comma-joined field, preserving order).
	// http.Header.Get returns only the first field — which can be the client's spoofed
	// line, with our trusted proxy's real value in a later field. Join every field in
	// order so the rightmost token is always the one our trusted proxy actually appended.
	xffValues := req.Header.Values("X-Forwarded-For")
	if len(xffValues) == 0 {
		return peer
	}
	parts := strings.Split(strings.Join(xffValues, ","), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		hop := strings.TrimSpace(parts[i])
		if hop == "" {
			continue
		}
		addr, err := netip.ParseAddr(hop)
		if err != nil {
			// A malformed hop means we can no longer trust anything to its left; stop
			// at the nearest trusted boundary rather than return a spoofable value.
			return peer
		}
		addr = addr.Unmap()
		if r.isTrusted(addr) {
			continue
		}
		return addr.String()
	}
	// Every hop in the chain is itself a trusted proxy — no distinct client to report.
	return peer
}

func (r *Resolver) isTrusted(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	for _, prefix := range r.trusted {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func remoteHost(remoteAddr string) string {
	if remoteAddr == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return remoteAddr
}
