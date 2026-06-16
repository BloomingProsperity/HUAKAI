package provider

import (
	"errors"
	"testing"
)

// PROXY-03: precedence account > tenant-default > direct, with fail-closed at
// EVERY bound-but-unhealthy tier (a configured-but-broken proxy must never
// silently fall through to a lower tier or a direct connection — that would leak
// the account's real egress IP, breaking IP isolation).
//
// directFallbackAllowed=false here = platform kill-switch OFF, so the default
// fail-closed behavior must hold regardless of fallback_mode.
func TestChooseProxyTier(t *testing.T) {
	// account bound + healthy -> account tier
	f, use, err := chooseProxyTier(proxyRow{hasProxyBound: true, proxyIsHealthy: true, protocol: "http", host: "acct", port: 1}, false)
	if err != nil || !use || f.host != "acct" {
		t.Fatalf("account healthy: use=%v host=%q err=%v", use, f.host, err)
	}

	// account bound + UNHEALTHY -> fail-closed, even though a healthy default exists
	_, use, err = chooseProxyTier(proxyRow{
		hasProxyBound: true, proxyIsHealthy: false,
		hasDefaultBound: true, defaultIsHealthy: true, dhost: "def",
	}, false)
	if !errors.Is(err, ErrProxyUnhealthy) || use {
		t.Fatalf("account bound-unhealthy MUST fail-closed (not fall to default): use=%v err=%v", use, err)
	}

	// no account proxy + default healthy -> default tier
	f, use, err = chooseProxyTier(proxyRow{hasDefaultBound: true, defaultIsHealthy: true, dprotocol: "http", dhost: "def", dport: 2}, false)
	if err != nil || !use || f.host != "def" {
		t.Fatalf("default healthy: use=%v host=%q err=%v", use, f.host, err)
	}

	// no account proxy + default UNHEALTHY -> fail-closed, NOT direct
	_, use, err = chooseProxyTier(proxyRow{hasDefaultBound: true, defaultIsHealthy: false}, false)
	if !errors.Is(err, ErrProxyUnhealthy) || use {
		t.Fatalf("default bound-unhealthy MUST fail-closed (not direct): use=%v err=%v", use, err)
	}

	// neither -> direct
	_, use, err = chooseProxyTier(proxyRow{}, false)
	if err != nil || use {
		t.Fatalf("no proxy bound -> direct: use=%v err=%v", use, err)
	}

	// account healthy takes precedence over a healthy default
	f, use, _ = chooseProxyTier(proxyRow{
		hasProxyBound: true, proxyIsHealthy: true, host: "acct",
		hasDefaultBound: true, defaultIsHealthy: true, dhost: "def",
	}, false)
	if !use || f.host != "acct" {
		t.Fatalf("account tier must win over tenant-default, got host=%q", f.host)
	}
}

// fallback_mode='direct' is a DOUBLE gate: it only egresses direct when BOTH the
// per-proxy fallback_mode is 'direct' AND the platform kill-switch is ON. Any
// single gate off must keep fail-closed (reject), so the gateway IP never leaks.
// Each row is bound-but-unhealthy so the fallback branch is exercised.
func TestChooseProxyTier_DirectFallbackDoubleGate(t *testing.T) {
	unhealthyAccount := func(mode string) proxyRow {
		return proxyRow{hasProxyBound: true, proxyIsHealthy: false, fallbackMode: mode}
	}

	// direct + kill-switch ON -> direct egress (use=false, err=nil)
	_, use, err := chooseProxyTier(unhealthyAccount("direct"), true)
	if use || err != nil {
		t.Fatalf("direct + switch ON must egress direct (use=false,err=nil): use=%v err=%v", use, err)
	}

	// direct + kill-switch OFF -> reject (platform gate closed)
	_, use, err = chooseProxyTier(unhealthyAccount("direct"), false)
	if !errors.Is(err, ErrProxyUnhealthy) || use {
		t.Fatalf("direct + switch OFF MUST reject: use=%v err=%v", use, err)
	}

	// reject mode + kill-switch ON -> reject (per-proxy gate closed)
	_, use, err = chooseProxyTier(unhealthyAccount("reject"), true)
	if !errors.Is(err, ErrProxyUnhealthy) || use {
		t.Fatalf("reject mode + switch ON MUST reject: use=%v err=%v", use, err)
	}

	// empty/default mode + kill-switch ON -> reject (default is fail-closed)
	_, use, err = chooseProxyTier(unhealthyAccount(""), true)
	if !errors.Is(err, ErrProxyUnhealthy) || use {
		t.Fatalf("default mode + switch ON MUST reject: use=%v err=%v", use, err)
	}

	// tenant-default tier honors its own fallback_mode: direct + switch ON -> direct
	_, use, err = chooseProxyTier(proxyRow{hasDefaultBound: true, defaultIsHealthy: false, dFallbackMode: "direct"}, true)
	if use || err != nil {
		t.Fatalf("tenant-default direct + switch ON must egress direct: use=%v err=%v", use, err)
	}
}
