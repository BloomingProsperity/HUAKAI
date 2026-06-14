// Package ssrfpolicy parses operator-controlled passthrough SSRF policy.
package ssrfpolicy

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
)

const (
	PortAllowlistEnv       = "HUAKAI_PASSTHROUGH_PORT_ALLOWLIST"
	DomainDenylistEnv      = "HUAKAI_PASSTHROUGH_DOMAIN_DENYLIST"
	DomainAllowlistEnv     = "HUAKAI_PASSTHROUGH_DOMAIN_ALLOWLIST"
	AllowPrivateIPHostsEnv = "HUAKAI_PASSTHROUGH_ALLOW_PRIVATE_IP_HOSTS"
	// PrivateIPsEnabledEnv is the master kill-switch for the private-IP passthrough
	// escape hatch (SEC-084). Unset or true keeps the per-host allowlist
	// (AllowPrivateIPHostsEnv) behaviour unchanged; an explicit false force-denies
	// every private-IP host regardless of the allowlist — an emergency lockdown
	// that can only tighten policy, never widen it.
	PrivateIPsEnabledEnv = "HUAKAI_PASSTHROUGH_PRIVATE_IPS_ENABLED"
)

type Policy struct {
	portAllowlist      []portRange
	domainDenylist     []hostPattern
	domainAllowlist    []hostPattern
	allowPrivateIPHost map[string]struct{}
	// privateIPsDisabled is the master kill-switch (SEC-084). Its zero value is
	// false (not disabled), so a zero Policy keeps the prior fail-closed-by-empty
	// allowlist behaviour.
	privateIPsDisabled bool
}

type portRange struct {
	start int
	end   int
}

type hostPattern struct {
	host     string
	wildcard bool
}

type envCache struct {
	once   sync.Once
	policy Policy
	err    error
}

var defaultEnvCache envCache

func LoadFromEnv() (Policy, error) {
	defaultEnvCache.once.Do(func() {
		defaultEnvCache.policy, defaultEnvCache.err = Parse(
			os.Getenv(PortAllowlistEnv),
			os.Getenv(DomainDenylistEnv),
			os.Getenv(DomainAllowlistEnv),
			os.Getenv(AllowPrivateIPHostsEnv),
			os.Getenv(PrivateIPsEnabledEnv),
		)
	})
	return defaultEnvCache.policy, defaultEnvCache.err
}

func ResetForTesting() {
	defaultEnvCache = envCache{}
}

func Parse(portAllowlist, domainDenylist, domainAllowlist, allowPrivateIPHosts, privateIPsEnabled string) (Policy, error) {
	ports, err := parsePortRanges(portAllowlist)
	if err != nil {
		return Policy{}, fmt.Errorf("%s: %w", PortAllowlistEnv, err)
	}
	deny, err := parseHostPatterns(domainDenylist)
	if err != nil {
		return Policy{}, fmt.Errorf("%s: %w", DomainDenylistEnv, err)
	}
	allow, err := parseHostPatterns(domainAllowlist)
	if err != nil {
		return Policy{}, fmt.Errorf("%s: %w", DomainAllowlistEnv, err)
	}
	privateHosts, err := parseExplicitHosts(allowPrivateIPHosts)
	if err != nil {
		return Policy{}, fmt.Errorf("%s: %w", AllowPrivateIPHostsEnv, err)
	}
	disabled, err := parsePrivateIPsDisabled(privateIPsEnabled)
	if err != nil {
		return Policy{}, fmt.Errorf("%s: %w", PrivateIPsEnabledEnv, err)
	}
	return Policy{
		portAllowlist:      ports,
		domainDenylist:     deny,
		domainAllowlist:    allow,
		allowPrivateIPHost: privateHosts,
		privateIPsDisabled: disabled,
	}, nil
}

// parsePrivateIPsDisabled reads the master kill-switch. Empty defaults to enabled
// (not disabled) so the private-IP escape hatch keeps its prior behaviour; an
// explicit false disables it. The env names the ALLOW state, so disabled = !allow.
func parsePrivateIPsDisabled(raw string) (bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false, nil
	}
	allow, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("invalid boolean")
	}
	return !allow, nil
}

func (p Policy) AllowsPort(port int) bool {
	if len(p.portAllowlist) == 0 {
		return true
	}
	for _, r := range p.portAllowlist {
		if port >= r.start && port <= r.end {
			return true
		}
	}
	return false
}

func (p Policy) AllowsHost(host string) bool {
	host = normalizeHost(host)
	if host == "" {
		return false
	}
	if matchesAny(host, p.domainDenylist) {
		return false
	}
	if len(p.domainAllowlist) == 0 {
		return true
	}
	return matchesAny(host, p.domainAllowlist)
}

func (p Policy) AllowsPrivateIPHost(host string) bool {
	// SEC-084 master kill-switch: when the escape hatch is disabled, deny every
	// private-IP host regardless of the per-host allowlist.
	if p.privateIPsDisabled {
		return false
	}
	host = normalizeHost(host)
	if host == "" || len(p.allowPrivateIPHost) == 0 {
		return false
	}
	_, ok := p.allowPrivateIPHost[host]
	return ok
}

func parsePortRanges(raw string) ([]portRange, error) {
	parts := csv(raw)
	if len(parts) == 0 {
		return nil, nil
	}
	ranges := make([]portRange, 0, len(parts))
	for _, part := range parts {
		if strings.Contains(part, "-") {
			bounds := strings.Split(part, "-")
			if len(bounds) != 2 {
				return nil, fmt.Errorf("invalid port range")
			}
			start, err := parsePort(bounds[0])
			if err != nil {
				return nil, err
			}
			end, err := parsePort(bounds[1])
			if err != nil {
				return nil, err
			}
			if start > end {
				return nil, fmt.Errorf("invalid descending port range")
			}
			ranges = append(ranges, portRange{start: start, end: end})
			continue
		}
		port, err := parsePort(part)
		if err != nil {
			return nil, err
		}
		ranges = append(ranges, portRange{start: port, end: port})
	}
	return ranges, nil
}

func parsePort(raw string) (int, error) {
	port, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || port <= 0 || port > 65535 {
		return 0, fmt.Errorf("invalid port")
	}
	return port, nil
}

func parseHostPatterns(raw string) ([]hostPattern, error) {
	parts := csv(raw)
	if len(parts) == 0 {
		return nil, nil
	}
	patterns := make([]hostPattern, 0, len(parts))
	for _, part := range parts {
		pattern, err := parseHostPattern(part)
		if err != nil {
			return nil, err
		}
		patterns = append(patterns, pattern)
	}
	return patterns, nil
}

func parseHostPattern(raw string) (hostPattern, error) {
	value := normalizeHost(raw)
	if value == "" || strings.Contains(value, "*") && !strings.HasPrefix(value, "*.") {
		return hostPattern{}, fmt.Errorf("invalid host pattern")
	}
	if strings.HasPrefix(value, "*.") {
		host := strings.TrimPrefix(value, "*.")
		if host == "" || strings.Contains(host, "*") || invalidHostToken(host) {
			return hostPattern{}, fmt.Errorf("invalid host pattern")
		}
		return hostPattern{host: host, wildcard: true}, nil
	}
	if invalidHostToken(value) {
		return hostPattern{}, fmt.Errorf("invalid host pattern")
	}
	return hostPattern{host: value}, nil
}

func parseExplicitHosts(raw string) (map[string]struct{}, error) {
	parts := csv(raw)
	if len(parts) == 0 {
		return nil, nil
	}
	hosts := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		host := normalizeHost(part)
		if host == "" || strings.Contains(host, "*") || invalidHostToken(host) {
			return nil, fmt.Errorf("invalid explicit host")
		}
		hosts[host] = struct{}{}
	}
	return hosts, nil
}

func matchesAny(host string, patterns []hostPattern) bool {
	for _, pattern := range patterns {
		if pattern.matches(host) {
			return true
		}
	}
	return false
}

func (p hostPattern) matches(host string) bool {
	if p.wildcard {
		return strings.HasSuffix(host, "."+p.host)
	}
	return host == p.host
}

func csv(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func normalizeHost(host string) string {
	return strings.ToLower(strings.TrimSpace(host))
}

func invalidHostToken(host string) bool {
	if host == "" || strings.HasSuffix(host, ".") {
		return true
	}
	for i := 0; i < len(host); i++ {
		if host[i] <= ' ' || host[i] == 0x7f || host[i] >= 0x80 {
			return true
		}
	}
	return false
}
