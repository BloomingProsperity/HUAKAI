package anthropic

import "testing"

// DEVPIN-01:按账号固定设备 profile(反封禁 anti-clustering)。
// 相比 CLIProxyAPI 的 claude_device_profile.go 持平甚至更优(用确定性派生取代
// TTL 缓存;软件版本钉死在真实 baseline 下限上——绝不臆造)。

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
	// MUTATION GUARD:把按账号的哈希塌缩成一个常量索引会让所有账号共享同一个
	// 平台 -> 这条区分性断言变红,即 anti-clustering 指纹消失(退回到原始
	// bug:1000 个账号发出逐字节相同的指纹)。
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
		// 平台必须是白名单里真实的元组(不会出现 MacOS+x86 这种荒唐组合)
		if !allowed[p.os+"/"+p.arch] {
			t.Fatalf("accountID %d incoherent platform %s/%s", id, p.os, p.arch)
		}
		// 软件版本绝不臆造:钉死在已知真实的 baseline 下限上,这样捏造出来的
		// claude-cli 版本永远不会成为破绽。
		if p.userAgent != base.userAgent || p.packageVersion != base.packageVersion || p.runtimeVersion != base.runtimeVersion {
			t.Fatalf("accountID %d altered software fingerprint: %+v (baseline %+v)", id, p, base)
		}
	}
}
