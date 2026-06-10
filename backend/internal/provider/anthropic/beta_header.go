// 出站 Anthropic-Beta 合成(DM-03):凭据配置 + 客户端请求 token 合并。
package anthropic

import "strings"

// oauthInboundBetaAllowlist 是 OAuth/session 池账号出口允许透传的客户端
// beta token 白名单。池账号带 Claude Code 设备指纹(DEVPIN-01/02),透传
// 任意客户端 token 会制造真实 Claude Code 不会发的 beta 组合=指纹异常,
// 反封禁姿态下必须收窄;API-key 直连是租户自有账号,不设此限(语法校验
// 已在 provider.ParseInboundBetaTokens 完成)。
var oauthInboundBetaAllowlist = map[string]struct{}{
	"claude-code-20250219":                   {},
	"oauth-2025-04-20":                       {},
	"interleaved-thinking-2025-05-14":        {},
	"fine-grained-tool-streaming-2025-05-14": {},
	"context-management-2025-06-27":          {},
	"context-1m-2025-08-07":                  {},
	"output-128k-2025-02-19":                 {},
	"token-efficient-tools-2025-02-19":       {},
	"computer-use-2025-01-24":                {},
	"prompt-caching-2024-07-31":              {},
	"extended-cache-ttl-2025-04-11":          {},
	"memory-2025-08-04":                      {},
}

func oauthBetaAllowed(tok string) bool {
	_, ok := oauthInboundBetaAllowlist[tok]
	return ok
}

// outboundBetaHeader 合成出站 Anthropic-Beta 值:凭据配置的 token 永远在前
// 且原样保留;客户端 token 经 allow 过滤(nil=全放行)后去重追加。无客户端
// token 时逐字节返回凭据原值——既有流量出站零变化。
func outboundBetaHeader(credBetas string, inbound []string, allow func(string) bool) string {
	kept := inbound[:0:0]
	for _, tok := range inbound {
		if allow == nil || allow(tok) {
			kept = append(kept, tok)
		}
	}
	if len(kept) == 0 {
		return credBetas
	}
	seen := map[string]struct{}{}
	var parts []string
	for _, raw := range strings.Split(credBetas, ",") {
		tok := strings.TrimSpace(raw)
		if tok == "" {
			continue
		}
		parts = append(parts, tok)
		seen[strings.ToLower(tok)] = struct{}{}
	}
	for _, tok := range kept {
		if _, dup := seen[tok]; dup {
			continue
		}
		seen[tok] = struct{}{}
		parts = append(parts, tok)
	}
	return strings.Join(parts, ",")
}
