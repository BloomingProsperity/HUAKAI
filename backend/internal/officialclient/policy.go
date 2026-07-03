// Package officialclient 实现「反转/订阅号只接对应官方客户端」的访问控制策略
// (per-vendor official-client gate)。它是【鉴真门】:校验进入的请求确实来自
// 该 vendor 的官方客户端(Anthropic=Claude Code / OpenAI=Codex CLI / Gemini=
// Gemini CLI),非官方则拒。
//
// 这是【访问控制】,不是伪装:本包只判定 allow/reject,不改写请求、不注入任何
// 伪造身份。进来的必须真是官方客户端(由 clientid 检测),透传其真流量;非官方拒。
//
// 职责边界(与相邻包):
//   - clientid 负责【检测】客户端身份(UA/X-Client 信号 → Identity+confidence),
//     其文档明言"不直接拒绝请求,让 policy 决定";本包就是那个 policy。
//   - 本包不做检测、不做 body 伪装、不持久化。
//
// 用法:上层在请求鉴权/选号阶段,对开了 official-client-only 的反转号池,用
// clientid.Detect 得到身份后调 Allowed 判定;返回 false 即 403 拒。
//
// 参考做法(§16,clean-room 重写不搬码):sub2api 用分组级 ClaudeCodeOnly +
// 账号级 codex_cli_only 做同类 per-vendor 官方客户端门控,非官方拒。
package officialclient

import (
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/clientid"
)

// 判定原因常量(供审计日志与 403 错误信息渲染)。
const (
	ReasonNoRestriction     = "no_restriction"                 // 未开门控,放行
	ReasonOfficialClientOK  = "official_client_ok"             // 官方客户端,放行
	ReasonNonOfficialReject = "non_official_client_rejected"   // 非官方客户端,拒
	ReasonUnknownClient     = "unknown_client_rejected"        // 身份未知,保守拒
	ReasonVendorNoOfficial  = "vendor_has_no_official_client"  // vendor 无官方客户端概念却开了门(配置矛盾),保守拒
)

// RequiredIdentity 返回某 vendor 的反转/订阅号所要求的官方客户端身份。
// ok=false 表示该 vendor 没有「官方客户端」概念(如纯 apikey 聚合厂),不应对其
// 启用 official-client-only。
//
// 当前覆盖 Anthropic(Claude Code)。Codex(OpenAI)/Gemini CLI 待 clientid 扩展
// 对应 Identity 后接入(见包级 TODO:clientid 目前无 Codex/GeminiCLI 身份枚举)。
func RequiredIdentity(vendor string) (clientid.Identity, bool) {
	switch strings.ToLower(strings.TrimSpace(vendor)) {
	case "anthropic", "claude":
		return clientid.IdentityClaudeCode, true
	default:
		return "", false
	}
}

// Allowed 判定一个已检测出 clientIdentity 的请求是否允许访问该 vendor 的账号。
//
//   - officialOnly=false → 恒 allow(不设限,如 apikey 号池)。
//   - officialOnly=true 且 vendor 有官方客户端 → 仅当 clientIdentity 恰为该官方
//     客户端才 allow;非官方与 unknown 一律 reject。
//   - officialOnly=true 但 vendor 无官方客户端概念 → 保守 reject(配置矛盾)。
//
// 返回 (allowed, reason);reason 为上述常量之一,供审计与 403 错误渲染。
func Allowed(clientIdentity clientid.Identity, vendor string, officialOnly bool) (bool, string) {
	if !officialOnly {
		return true, ReasonNoRestriction
	}
	required, has := RequiredIdentity(vendor)
	if !has {
		return false, ReasonVendorNoOfficial
	}
	if clientIdentity == clientid.IdentityUnknown {
		return false, ReasonUnknownClient
	}
	if clientIdentity == required {
		return true, ReasonOfficialClientOK
	}
	return false, ReasonNonOfficialReject
}
