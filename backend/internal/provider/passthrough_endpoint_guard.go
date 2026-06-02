package provider

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

var ErrUnsafePassthroughEndpoint = errors.New("provider: unsafe upstream passthrough endpoint")

func safePassthroughBaseURL(raw string) (*url.URL, error) {
	if hasControlOrSpace(raw) {
		return nil, passthroughEndpointBlocked("control character or whitespace")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, passthroughEndpointBlocked("invalid URL")
	}
	if _, err := validatePassthroughEndpointURL(u); err != nil {
		return nil, err
	}
	return u, nil
}

func validatePassthroughEndpointURL(u *url.URL) (string, error) {
	if u == nil {
		return "", passthroughEndpointBlocked("invalid URL")
	}
	if u.Scheme != "https" {
		return "", passthroughEndpointBlocked("scheme must be https")
	}
	if u.User != nil {
		return "", passthroughEndpointBlocked("userinfo is not allowed")
	}
	if u.Fragment != "" {
		return "", passthroughEndpointBlocked("fragment is not allowed")
	}
	if strings.Contains(u.Host, "%") {
		return "", passthroughEndpointBlocked("encoded host is not allowed")
	}
	if strings.HasSuffix(u.Host, ":") {
		return "", passthroughEndpointBlocked("invalid port")
	}
	if port := u.Port(); port != "" && !validEndpointPort(port) {
		return "", passthroughEndpointBlocked("invalid port")
	}
	return validatePassthroughHost(u.Hostname())
}

func validatePassthroughHost(raw string) (string, error) {
	host := strings.ToLower(raw)
	if host == "" {
		return "", passthroughEndpointBlocked("empty host")
	}
	if strings.Contains(host, "%") {
		return "", passthroughEndpointBlocked("encoded host is not allowed")
	}
	if host[len(host)-1] == '.' {
		return "", passthroughEndpointBlocked("trailing-dot host")
	}
	if hasNonASCII(host) {
		return "", passthroughEndpointBlocked("non-ascii host")
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if !publicPassthroughIP(addr) {
			return "", passthroughEndpointBlocked("blocked IP")
		}
		return host, nil
	}
	if blockedPassthroughHost(host) || numericObfuscatedHost(host) {
		return "", passthroughEndpointBlocked("blocked host")
	}
	return host, nil
}

// UsesCustomPassthroughEndpoint reports whether cred carries a tenant-supplied
// upstream base URL whose request target must be guarded before Do().
func UsesCustomPassthroughEndpoint(cred Credential) bool {
	if cred.Type != CredentialTypeUpstreamPassthrough {
		return false
	}
	for _, key := range []string{"base_url", "endpoint_api", "copilot_endpoint_api"} {
		if strings.TrimSpace(cred.Extra[key]) != "" {
			return true
		}
	}
	return false
}

var passthroughEndpointLookupNetIP = net.DefaultResolver.LookupNetIP

// SwapPassthroughEndpointLookupForTesting replaces the endpoint DNS lookup hook.
// It is exported for cross-package dispatcher tests; production code must not
// call it.
func SwapPassthroughEndpointLookupForTesting(fn func(context.Context, string, string) ([]netip.Addr, error)) func() {
	original := passthroughEndpointLookupNetIP
	passthroughEndpointLookupNetIP = fn
	return func() { passthroughEndpointLookupNetIP = original }
}

// ValidatePassthroughEndpointTarget resolves a tenant-supplied passthrough
// request target immediately before outbound I/O. EndpointForCredential stays
// pure/static so adapters do not perform DNS, while the dispatcher can still
// fail closed on hostname aliases that resolve to loopback, private, link-local,
// metadata, or other special-use addresses.
func ValidatePassthroughEndpointTarget(ctx context.Context, endpoint *url.URL) error {
	host, err := validatePassthroughEndpointURL(endpoint)
	if err != nil {
		return err
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return nil
	}
	addrs, err := passthroughEndpointLookupNetIP(ctx, "ip", host)
	if err != nil || len(addrs) == 0 {
		return passthroughEndpointBlocked("host resolution failed")
	}
	for _, addr := range addrs {
		if !publicPassthroughIP(addr) {
			return passthroughEndpointBlocked("blocked resolved IP")
		}
	}
	return nil
}

// WrapPassthroughEndpointTransport returns a transport whose actual direct dial
// uses the same public-IP policy as ValidatePassthroughEndpointTarget. It must be
// applied after account-proxy resolution; proxied or custom RoundTrippers cannot
// bind HUAKAI's DNS decision to the target dial, so they fail closed here.
func WrapPassthroughEndpointTransport(rt http.RoundTripper) (http.RoundTripper, error) {
	if rt == nil {
		rt = http.DefaultTransport
	}
	base, ok := rt.(*http.Transport)
	if !ok {
		return nil, passthroughEndpointBlocked("unsupported transport")
	}
	if base.Proxy != nil {
		return nil, passthroughEndpointBlocked("proxy transport is not allowed")
	}
	clone := base.Clone()
	dial := clone.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	clone.Proxy = nil
	clone.DialContext = passthroughGuardedDialContext(dial)
	clone.DialTLS = nil
	clone.DialTLSContext = nil
	return clone, nil
}

func passthroughGuardedDialContext(base func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		dialAddresses, err := resolvePublicPassthroughDialAddresses(ctx, address)
		if err != nil {
			return nil, err
		}
		var lastDialErr error
		for _, dialAddress := range dialAddresses {
			conn, err := base(ctx, network, dialAddress)
			if err != nil {
				lastDialErr = err
				continue
			}
			if !publicPassthroughNetAddr(conn.RemoteAddr()) {
				_ = conn.Close()
				return nil, passthroughEndpointBlocked("blocked remote IP")
			}
			return conn, nil
		}
		if lastDialErr != nil {
			return nil, lastDialErr
		}
		return nil, passthroughEndpointBlocked("blocked resolved IP")
	}
}

func resolvePublicPassthroughDialAddresses(ctx context.Context, address string) ([]string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, passthroughEndpointBlocked("invalid dial address")
	}
	host, err = validatePassthroughHost(host)
	if err != nil {
		return nil, err
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return []string{net.JoinHostPort(addr.String(), port)}, nil
	}
	addrs, err := passthroughEndpointLookupNetIP(ctx, "ip", host)
	if err != nil || len(addrs) == 0 {
		return nil, passthroughEndpointBlocked("host resolution failed")
	}
	dialAddresses := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		if !publicPassthroughIP(addr) {
			return nil, passthroughEndpointBlocked("blocked resolved IP")
		}
		dialAddresses = append(dialAddresses, net.JoinHostPort(addr.String(), port))
	}
	return dialAddresses, nil
}

func publicPassthroughNetAddr(addr net.Addr) bool {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		return false
	}
	netipAddr, ok := netip.AddrFromSlice(tcp.IP)
	if !ok {
		return false
	}
	return publicPassthroughIP(netipAddr)
}

func passthroughEndpointBlocked(reason string) error {
	return fmt.Errorf("%w: %s", ErrUnsafePassthroughEndpoint, reason)
}

func hasControlOrSpace(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] <= ' ' || s[i] == 0x7f {
			return true
		}
	}
	return false
}

func hasNonASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return true
		}
	}
	return false
}

func validEndpointPort(raw string) bool {
	port, err := strconv.Atoi(raw)
	return err == nil && port > 0 && port <= 65535
}

func blockedPassthroughHost(host string) bool {
	switch host {
	case "localhost", "metadata", "metadata.google.internal", "169.254.169.254":
		return true
	}
	for _, suffix := range []string{".localhost", ".local", ".internal", ".lan", ".home", ".corp", ".intranet"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

func numericObfuscatedHost(host string) bool {
	labels := strings.Split(host, ".")
	if len(labels) == 0 || len(labels) > 4 {
		return false
	}
	for _, label := range labels {
		if label == "" {
			return false
		}
		switch {
		case strings.HasPrefix(label, "0x") || strings.HasPrefix(label, "0X"):
			if _, err := strconv.ParseUint(label[2:], 16, 32); err != nil {
				return false
			}
		case len(label) > 1 && label[0] == '0':
			if _, err := strconv.ParseUint(label, 8, 32); err != nil {
				return false
			}
		default:
			if _, err := strconv.ParseUint(label, 10, 32); err != nil {
				return false
			}
		}
	}
	return true
}

func publicPassthroughIP(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.IsLoopback() || addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsMulticast() || addr.IsUnspecified() || !addr.IsGlobalUnicast() {
		return false
	}
	for _, prefix := range passthroughSpecialUseDenyPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

var passthroughSpecialUseDenyPrefixes = []netip.Prefix{
	mustPassthroughPrefix("0.0.0.0/8"),
	mustPassthroughPrefix("100.64.0.0/10"),
	mustPassthroughPrefix("192.0.0.0/24"),
	mustPassthroughPrefix("192.0.2.0/24"),
	mustPassthroughPrefix("192.88.99.0/24"),
	mustPassthroughPrefix("198.18.0.0/15"),
	mustPassthroughPrefix("198.51.100.0/24"),
	mustPassthroughPrefix("203.0.113.0/24"),
	mustPassthroughPrefix("240.0.0.0/4"),
	mustPassthroughPrefix("255.255.255.255/32"),
	mustPassthroughPrefix("::/96"),
	mustPassthroughPrefix("64:ff9b::/96"),
	mustPassthroughPrefix("64:ff9b:1::/48"),
	mustPassthroughPrefix("100::/64"),
	mustPassthroughPrefix("2001::/23"),
	mustPassthroughPrefix("2001:db8::/32"),
	mustPassthroughPrefix("2002::/16"),
	mustPassthroughPrefix("3fff::/20"),
	mustPassthroughPrefix("5f00::/16"),
}

func mustPassthroughPrefix(raw string) netip.Prefix {
	prefix, err := netip.ParsePrefix(raw)
	if err != nil {
		panic(err)
	}
	return prefix
}
