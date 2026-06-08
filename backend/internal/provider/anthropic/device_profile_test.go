package anthropic

import "testing"

// DEVPIN-01: per-account device-profile pinning (反封禁 anti-clustering).
// Parity-or-better vs CLIProxyAPI claude_device_profile.go (deterministic
// derivation replaces the TTL cache; software version pinned to the real
// baseline floor — never invented).

func TestResolveAccountDeviceProfile_BaselineFallback(t *testing.T) {
	base := baselineClaudeDeviceProfile()
	for _, id := range []int64{0, -1, -999} {
		if got := resolveAccountDeviceProfile(id); got != base {
			t.Fatalf("accountID %d: expected baseline %+v, got %+v", id, base, got)
		}
	}
}

func TestResolveAccountDeviceProfile_Deterministic(t *testing.T) {
	for _, id := range []int64{1, 42, 100000} {
		a := resolveAccountDeviceProfile(id)
		b := resolveAccountDeviceProfile(id)
		if a != b {
			t.Fatalf("accountID %d not deterministic: %+v vs %+v", id, a, b)
		}
	}
}

func TestResolveAccountDeviceProfile_PerAccountDistinct(t *testing.T) {
	// MUTATION GUARD: collapsing the per-account hash to a constant index makes
	// every account share one platform -> this distinctness assertion goes red,
	// i.e. the anti-clustering fingerprint is gone (back to the original bug
	// where 1000 accounts emit a byte-identical fingerprint).
	seen := map[string]bool{}
	for id := int64(1); id <= 64; id++ {
		p := resolveAccountDeviceProfile(id)
		seen[p.os+"/"+p.arch] = true
	}
	if len(seen) < 2 {
		t.Fatalf("expected >1 distinct platform across accounts (anti-clustering), got %d: %v", len(seen), seen)
	}
}

func TestResolveAccountDeviceProfile_CoherentAndNeverInventsVersion(t *testing.T) {
	base := baselineClaudeDeviceProfile()
	allowed := map[string]bool{}
	for _, pl := range claudeDevicePlatforms {
		allowed[pl[0]+"/"+pl[1]] = true
	}
	for id := int64(1); id <= 200; id++ {
		p := resolveAccountDeviceProfile(id)
		// platform must be a real allowlisted tuple (no MacOS+x86 nonsense)
		if !allowed[p.os+"/"+p.arch] {
			t.Fatalf("accountID %d incoherent platform %s/%s", id, p.os, p.arch)
		}
		// software version is NEVER invented: pinned to the known-real baseline
		// floor so a fabricated claude-cli version can never become a tell.
		if p.userAgent != base.userAgent || p.packageVersion != base.packageVersion || p.runtimeVersion != base.runtimeVersion {
			t.Fatalf("accountID %d altered software fingerprint: %+v (baseline %+v)", id, p, base)
		}
	}
}
