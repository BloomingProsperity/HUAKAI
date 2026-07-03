// Package officialclient 判定某 vendor 的账号是否只接受其官方客户端:Anthropic 账号
// 要求 Claude Code、OpenAI 账号要求 Codex CLI,非官方客户端拒。客户端身份由 clientid
// 检测,本包据其结果 + vendor + 是否要求官方客户端,判 allow/reject。
//
// 用法:请求鉴权/选号阶段用 clientid.Detect 得到客户端身份,调 Allowed 判定,
// 返回 false 即拒(上层 403)。
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
	ReasonVendorNoOfficial  = "vendor_has_no_official_client"  // vendor 无官方客户端映射,拒
)

// RequiredIdentity 返回某 vendor 要求的官方客户端身份;ok=false 表示该 vendor 无对应
// 官方客户端映射。当前覆盖 Anthropic(Claude Code)与 OpenAI(Codex CLI)。
func RequiredIdentity(vendor string) (clientid.Identity, bool) {
	switch strings.ToLower(strings.TrimSpace(vendor)) {
	case "anthropic", "claude":
		return clientid.IdentityClaudeCode, true
	case "openai", "codex", "chatgpt":
		return clientid.IdentityCodexCLI, true
	default:
		return "", false
	}
}

// Allowed 判定已检测出 clientIdentity 的请求是否允许访问该 vendor 的账号。
//   - officialOnly=false → 放行(任意客户端)。
//   - officialOnly=true 且 vendor 有官方客户端 → 仅该官方客户端放行,非官方与 unknown 拒。
//   - officialOnly=true 但 vendor 无官方客户端映射 → 拒。
// 返回 (allowed, reason),reason 为下列常量之一。
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
