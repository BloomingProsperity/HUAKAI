package main

import (
	"net"
	"net/http"
	"strings"

	"go.uber.org/zap"
)

const (
	internalPathPrefix    = "/internal/"
	internalAllowCIDRsEnv = "HUAKAI_HERMES_INTERNAL_EXTRA_ALLOW_CIDRS"
)

// parseInternalAllowCIDRs parses a comma-separated list of extra CIDRs that may
// reach the /internal/* routes, on top of the always-allowed loopback + private
// (RFC1918) + link-local ranges. Unparseable entries are skipped (a bad CIDR is
// simply not added). Empty input -> nil.
func parseInternalAllowCIDRs(raw string) []*net.IPNet {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []*net.IPNet
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ipnet, err := net.ParseCIDR(part); err == nil && ipnet != nil {
			out = append(out, ipnet)
		}
	}
	return out
}

// internalSourceGate rejects requests to /internal/* — the Hermes control plane
// (runner bootstrap/refresh/keys, the read-only tool-execute callback, and the
// internal OpenAI egress) — unless the TRUE socket peer is on a trusted network:
// loopback, RFC1918 private, link-local, or an operator-configured extra CIDR.
// This adds a network-origin barrier so the internal control plane is not
// reachable from the public internet on the shared listener; the app-layer
// internal_token / runner HMAC is no longer the SOLE gate (audit B2).
//
// It MUST be installed BEFORE middleware.RealIP. RealIP overwrites RemoteAddr
// from client-supplied X-Forwarded-For/X-Real-IP with no trusted-proxy check, so
// a gate reading the post-RealIP RemoteAddr would let an attacker spoof a
// loopback source by sending `X-Forwarded-For: 127.0.0.1`. Running first, the
// gate judges the genuine TCP peer (r.RemoteAddr), which cannot be forged
// off-box — the same ordering rationale the per-IP rate limiter uses.
//
// Non-/internal/ paths pass through untouched. A rejected request gets 404 (the
// internal routes are invisible to untrusted sources) plus a WARN log of the
// real peer for attack visibility / misconfiguration diagnosis.
func internalSourceGate(extra []*net.IPNet, logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, internalPathPrefix) {
				next.ServeHTTP(w, r)
				return
			}
			if internalSourceAllowed(r.RemoteAddr, extra) {
				next.ServeHTTP(w, r)
				return
			}
			if logger != nil {
				logger.Warn("rejected /internal/* request from untrusted source",
					zap.String("remote_addr", r.RemoteAddr), zap.String("path", r.URL.Path))
			}
			http.NotFound(w, r)
		})
	}
}

// internalSourceAllowed reports whether a socket peer address (host:port form, as
// in http.Request.RemoteAddr) may reach the internal routes. An unparseable peer
// fails closed.
func internalSourceAllowed(remoteAddr string, extra []*net.IPNet) bool {
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return true
	}
	for _, n := range extra {
		if n != nil && n.Contains(ip) {
			return true
		}
	}
	return false
}
