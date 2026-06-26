package anthropic

import (
	"hash/fnv"
	"strconv"
)

// 按账号固定 Claude Code 设备指纹(anti-clustering / 反封禁)。
//
// 背景:applyClaudeDeviceProfile 之前给每个账号的出口流量都打上同一组硬编码的
//(User-Agent + X-Stainless-*)元组,于是 N 个池账号呈现出逐字节相同的指纹——
// 对上游 WAF 来说这是一个极易识别的聚类信号。CLIProxyAPI(internal/runtime/
// executor/helps/claude_device_profile.go)通过 per-auth 的 7 天缓存 + 以每个
// 客户端真实入站 UA 为种子的版本下限单调升级来解决此问题。
//
// HUAKAI 的 adapter 在这一层拿不到入站客户端 UA,所以我们采取一种持平甚至更优的
// 做法:从 account id 确定性地派生出一个稳定、按账号区分的平台元组。对我们这个
// 场景而言它比 TTL 缓存更强——既不存在所有账号共享 baseline 的冷启动窗口,也没有
// 驱逐,而且(关键)我们绝不臆造一个不存在的 claude-cli 版本:UA / package /
// runtime 始终钉死在已知真实的 baseline 下限上,这样捏造出来的版本永远不会成为
// 破绽。只有 OS/Arch 平台元组(全部是真实 Claude Code 的目标平台)会变化,从而既
// 保证按账号区分,又让每个账号随时间逐字节稳定。
//
// 按 Owner 2026-06-08「必须开着」默认开启。AccountID<=0(如无池账号的官方
// API-key 路径)回退到精确的 baseline,因此那条路径行为不变。

// claudeDeviceProfile 是一份解析出的 Claude Code 设备指纹。
type claudeDeviceProfile struct {
	userAgent      string
	packageVersion string
	runtimeVersion string
	os             string
	arch           string
}

// baselineClaudeDeviceProfile 是唯一一份已知真实的 Claude Code 指纹(与
// CLIProxyAPI 默认值一致)。它是版本下限:按账号的变化永远不会低于它,也永远
// 不改动软件版本,只改动平台。
func baselineClaudeDeviceProfile() claudeDeviceProfile {
	return claudeDeviceProfile{
		userAgent:      claudeCodeUserAgent,
		packageVersion: claudeStainlessPackageVersion,
		runtimeVersion: claudeStainlessRuntimeVersion,
		os:             claudeStainlessOS,
		arch:           claudeStainlessArch,
	}
}

// claudeDevicePlatforms 是真实 Claude Code(Stainless SDK)OS/Arch 元组的白名单
//(取值与 CPA 的 mapStainlessOS/mapStainlessArch 一致)。索引 0 是 baseline 平台。
// 每一项内部自洽——不会出现 MacOS+x86 这种荒唐组合——所以派生出的 profile 始终
// 是一台貌似真实的机器。
var claudeDevicePlatforms = [][2]string{
	{claudeStainlessOS, claudeStainlessArch}, // MacOS / arm64(baseline)
	{"MacOS", "x64"},
	{"Linux", "x64"},
	{"Windows", "x64"},
}

// resolveAccountDeviceProfile 返回一个确定性的、按账号稳定的设备 profile。同一个
// accountID 始终产出同一个 profile(随时间逐字节稳定);不同的 accountID 一般在
// OS/Arch 平台上有所区别(anti-clustering)。accountID<=0 时原样返回精确的 baseline。
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
