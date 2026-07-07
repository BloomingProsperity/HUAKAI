// Package officialclient 判定某 vendor 的账号是否强制官方客户端入站:Owner 决策
// (2026-07-07)后仅 Anthropic/Claude 账号强制 Claude Code;OpenAI/codex/chatgpt
// OAuth 账号默认放开,可由标准客户端经翻译层使用。客户端身份由 clientid 检测,
// 本包据其结果 + vendor + 是否强制官方客户端,判 allow/reject。
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
	ReasonVendorNotEnforced = "vendor_official_client_not_enforced"
)

// RequiredIdentity 返回某 vendor 对应的官方客户端身份;ok=false 表示该 vendor 无对应
// 官方客户端映射。当前覆盖 Anthropic(Claude Code)与 OpenAI(Codex CLI)。注意:
// 该映射也供出站身份改写使用,不等同于入站强制策略。
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

// vendorEnforcesOfficialClient 判定某 vendor 是否强制官方客户端入站。
// Owner 决策(2026-07-07):仅 Anthropic/claude 账号强制官方客户端(Claude Code);
// OpenAI/codex/chatgpt 账号默认放开——标准 chat/Responses/messages 客户端经翻译层即可用,
// 出站仍由 mimicryidentity 伪装成 Codex CLI,账号侧仍官方样貌。
func vendorEnforcesOfficialClient(vendor string) bool {
	switch strings.ToLower(strings.TrimSpace(vendor)) {
	case "anthropic", "claude":
		return true
	default:
		return false
	}
}

// reverseAuthModes 是反转/订阅号(OAuth/session 类凭据)的账号类型集合;这类账号会参与
// 入站官方客户端门的候选判定,也供出站身份改写限定 scope。官方 API key / 云凭据类
// (api_key/aistudio_api_key/bedrock/vertex_*/azure)不在此集合。取值为
// credentialstore.AuthMode*。
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

// IsReverseAccountType 报告账号类型是否为反转/订阅号(OAuth/session 类)。
// accountType 取值为 provider.AccountInfo.AccountType(= credentialstore.AuthMode*);
// 官方 API key / 云凭据类及未知/空值返回 false。
func IsReverseAccountType(accountType string) bool {
	_, ok := reverseAuthModes[strings.ToLower(strings.TrimSpace(accountType))]
	return ok
}

// GateDecision 报告某账号类型 + vendor 下、已检测出 clientIdentity 的请求是否应被拒。
// 仅反转/订阅号 + vendor 默认强制官方客户端入站或账号级 forceOfficialClient + 身份
// 非官方时拒;非反转号不拒。forceOfficialClient 只扩大已有官方客户端映射 vendor 的
// 入站门控,不越过反转账号前置条件;无官方客户端映射仍 fail-open。返回 (reject, reason)。
func GateDecision(accountType, vendor string, clientIdentity clientid.Identity, forceOfficialClient bool) (bool, string) {
	if !IsReverseAccountType(accountType) {
		return false, ReasonNoRestriction
	}
	if !vendorEnforcesOfficialClient(vendor) && !forceOfficialClient {
		return false, ReasonVendorNotEnforced
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
