package anthropic

import (
	"hash/fnv"
	"strconv"
)

// Per-account Claude Code device-profile pinning (anti-clustering / 反封禁).
//
// Background: applyClaudeDeviceProfile previously stamped ONE hardcoded
// (User-Agent + X-Stainless-*) tuple on every account's egress, so N pooled
// accounts presented a byte-identical fingerprint — a trivial clustering signal
// for upstream WAFs. CLIProxyAPI (internal/runtime/executor/helps/
// claude_device_profile.go) solves this with a per-auth 7-day cache + version-
// floor monotonic upgrade seeded from each client's real inbound UA.
//
// HUAKAI's adapter does not receive the inbound client UA at this layer, so we
// take a parity-OR-BETTER approach: DETERMINISTICALLY derive a stable, per-
// account-distinct platform tuple from the account id. This is stronger than a
// TTL cache for our case — there is no cold-start window where every account
// shares the baseline, no eviction, and (critically) we NEVER invent a non-
// existent claude-cli version: the UA / package / runtime stay pinned to the
// known-real baseline floor, so a fabricated version can never become a tell.
// Only the OS/Arch platform tuple (all real Claude Code targets) varies, giving
// per-account distinctness while each account stays byte-stable over time.
//
// ON per Owner 2026-06-08「必须开着」. AccountID<=0 (e.g. the official API-key
// path with no pooled account) falls back to the exact baseline, so behavior
// there is unchanged.

// claudeDeviceProfile is one resolved Claude Code device fingerprint.
type claudeDeviceProfile struct {
	userAgent      string
	packageVersion string
	runtimeVersion string
	os             string
	arch           string
}

// baselineClaudeDeviceProfile is the single known-real Claude Code fingerprint
// (matches CLIProxyAPI defaults). It is the version FLOOR: per-account variation
// never drops below it and never alters the software version, only the platform.
func baselineClaudeDeviceProfile() claudeDeviceProfile {
	return claudeDeviceProfile{
		userAgent:      claudeCodeUserAgent,
		packageVersion: claudeStainlessPackageVersion,
		runtimeVersion: claudeStainlessRuntimeVersion,
		os:             claudeStainlessOS,
		arch:           claudeStainlessArch,
	}
}

// claudeDevicePlatforms is the allowlist of REAL Claude Code (Stainless SDK)
// OS/Arch tuples (values match CPA mapStainlessOS/mapStainlessArch). Index 0 is
// the baseline platform. Each entry is internally coherent — no MacOS+x86
// nonsense — so a derived profile is always a plausible real machine.
var claudeDevicePlatforms = [][2]string{
	{claudeStainlessOS, claudeStainlessArch}, // MacOS / arm64 (baseline)
	{"MacOS", "x64"},
	{"Linux", "x64"},
	{"Windows", "x64"},
}

// resolveAccountDeviceProfile returns a deterministic, per-account-stable device
// profile. The same accountID always yields the same profile (byte-stable over
// time); distinct accountIDs generally differ in their OS/Arch platform
// (anti-clustering). accountID<=0 returns the exact baseline unchanged.
func resolveAccountDeviceProfile(accountID int64) claudeDeviceProfile {
	p := baselineClaudeDeviceProfile()
	if accountID <= 0 {
		return p
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(strconv.FormatInt(accountID, 10)))
	plat := claudeDevicePlatforms[h.Sum32()%uint32(len(claudeDevicePlatforms))]
	p.os = plat[0]
	p.arch = plat[1]
	return p
}
