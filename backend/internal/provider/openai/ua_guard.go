package openai

import "strings"

// 反封禁(SUB2-01)：出站 User-Agent 浏览器指纹清洗。
//
// Codex session/OAuth 出口若把浏览器型 UA(Mozilla/...)透传给 OpenAI/Codex
// 上游,Cloudflare/风控会立刻识别为"非官方客户端"。官方 Codex CLI 的 UA 从不
// 以 Mozilla/ 开头,所以浏览器型 UA 出现即异常,必须改写回 Codex CLI 风格。
// 仅作用于 session/oauth 路(CodexSessionAdapter 本就拒绝 apikey),符合 Owner
// 「官方 API-key 路不打指纹」原则。ON per Owner 2026-06-08 anti-ban directive.

// browserUAPrefixes 是浏览器 UA 的判别前缀(全部小写)。真实浏览器 UA 一律以
// Mozilla/ 开头;Opera 旧版以 opera/ 开头。官方 CLI/SDK UA 均不命中。
var browserUAPrefixes = []string{"mozilla/", "opera/"}

// isBrowserUserAgent 报告 ua 是否为浏览器型(应被清洗)。空串 / CLI UA 返回 false。
func isBrowserUserAgent(ua string) bool {
	lc := strings.ToLower(strings.TrimSpace(ua))
	for _, p := range browserUAPrefixes {
		if strings.HasPrefix(lc, p) {
			return true
		}
	}
	return false
}
