// Package officialclient 判定某 vendor 的账号是否只接受其官方客户端:Anthropic 账号
// 要求 Claude Code、OpenAI 账号要求 Codex CLI,非官方客户端拒。客户端身份由 clientid
// 检测,本包据其结果 + vendor + 是否要求官方客户端,判 allow/reject。
//
// 用法:请求鉴权/选号阶段用 clientid.Detect 得到客户端身份,调 GateDecision 判定,
// 返回 reject=true 即拒(上层 403)。
package officialclient

import (
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/clientid"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

// 判定原因常量(供审计日志与 403 错误信息渲染)。
const (
	ReasonNoRestriction     = "no_restriction"                // 未开门控,放行
	ReasonOfficialClientOK  = "official_client_ok"            // 官方客户端,放行
	ReasonNonOfficialReject = "non_official_client_rejected"  // 非官方客户端,拒
	ReasonVendorNoOfficial  = "vendor_has_no_official_client" // vendor 无官方客户端映射
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

// reverseAuthModes 是反转/订阅号(OAuth/session 类凭据)的账号类型集合;这类账号要求
// 对应官方客户端。官方 API key / 云凭据类(api_key/aistudio_api_key/bedrock/vertex_*/
// azure)不在此集合、不设限。取值为 credentialstore.AuthMode*。
var reverseAuthModes = map[string]struct{}{
	credentialstore.AuthModeClaudeAIOAuth: {},
	credentialstore.AuthModeClaudeCode:    {},
	credentialstore.AuthModeChatGPTOAuth:  {},
	credentialstore.AuthModeCodexCLIOAuth: {},
	credentialstore.AuthModeCodexWebOAuth: {},
	credentialstore.AuthModeCodeAssist:    {},
	credentialstore.AuthModeGoogleOne:     {},
	credentialstore.AuthModeAntigravity:   {},
	credentialstore.AuthModeCopilotOAuth:  {},
	credentialstore.AuthModeXAIOAuth:      {},
	credentialstore.AuthModeKimiOAuth:     {},
	credentialstore.AuthModeOAuth:         {},
	credentialstore.AuthModeRefreshToken:  {},
}

// IsReverseAccountType 报告账号类型是否为反转/订阅号(OAuth/session 类,要求官方客户端)。
// accountType 取值为 provider.AccountInfo.AccountType(= credentialstore.AuthMode*);
// 官方 API key / 云凭据类及未知/空值返回 false(不设限)。
func IsReverseAccountType(accountType string) bool {
	_, ok := reverseAuthModes[strings.ToLower(strings.TrimSpace(accountType))]
	return ok
}

// GateDecision 报告某账号类型 + vendor 下、已检测出 clientIdentity 的请求是否应被拒。
// 反转/订阅号且该 vendor 有官方客户端映射时,非官方客户端 → reject;非反转号、或该 vendor
// 无官方客户端映射,均不拒。返回 (reject, reason)。
func GateDecision(accountType, vendor string, clientIdentity clientid.Identity) (bool, string) {
	if !IsReverseAccountType(accountType) {
		return false, ReasonNoRestriction
	}
	required, has := RequiredIdentity(vendor)
	if !has {
		return false, ReasonVendorNoOfficial
	}
	if clientIdentity == required {
		return false, ReasonOfficialClientOK
	}
	return true, ReasonNonOfficialReject
}
