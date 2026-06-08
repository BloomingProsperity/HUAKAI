package provider

import (
	"errors"
	"testing"
)

// PROXY-03: precedence account > tenant-default > direct, with fail-closed at
// EVERY bound-but-unhealthy tier (a configured-but-broken proxy must never
// silently fall through to a lower tier or a direct connection — that would leak
// the account's real egress IP, breaking IP isolation).
func TestChooseProxyTier(t *testing.T) {
	// account bound + healthy -> account tier
	f, use, err := chooseProxyTier(proxyRow{hasProxyBound: true, proxyIsHealthy: true, protocol: "http", host: "acct", port: 1})
	if err != nil || !use || f.host != "acct" {
		t.Fatalf("account healthy: use=%v host=%q err=%v", use, f.host, err)
	}

	// account bound + UNHEALTHY -> fail-closed, even though a healthy default exists
	_, use, err = chooseProxyTier(proxyRow{
		hasProxyBound: true, proxyIsHealthy: false,
		hasDefaultBound: true, defaultIsHealthy: true, dhost: "def",
	})
	if !errors.Is(err, ErrProxyUnhealthy) || use {
		t.Fatalf("account bound-unhealthy MUST fail-closed (not fall to default): use=%v err=%v", use, err)
	}

	// no account proxy + default healthy -> default tier
	f, use, err = chooseProxyTier(proxyRow{hasDefaultBound: true, defaultIsHealthy: true, dprotocol: "http", dhost: "def", dport: 2})
	if err != nil || !use || f.host != "def" {
		t.Fatalf("default healthy: use=%v host=%q err=%v", use, f.host, err)
	}

	// no account proxy + default UNHEALTHY -> fail-closed, NOT direct
	_, use, err = chooseProxyTier(proxyRow{hasDefaultBound: true, defaultIsHealthy: false})
	if !errors.Is(err, ErrProxyUnhealthy) || use {
		t.Fatalf("default bound-unhealthy MUST fail-closed (not direct): use=%v err=%v", use, err)
	}

	// neither -> direct
	_, use, err = chooseProxyTier(proxyRow{})
	if err != nil || use {
		t.Fatalf("no proxy bound -> direct: use=%v err=%v", use, err)
	}

	// account healthy takes precedence over a healthy default
	f, use, _ = chooseProxyTier(proxyRow{
		hasProxyBound: true, proxyIsHealthy: true, host: "acct",
		hasDefaultBound: true, defaultIsHealthy: true, dhost: "def",
	})
	if !use || f.host != "acct" {
		t.Fatalf("account tier must win over tenant-default, got host=%q", f.host)
	}
}
