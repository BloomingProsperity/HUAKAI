package codexclientaccess

import "strings"

// officialCodexOriginatorSet 是 HUAKAI 认可的官方 Codex 客户端 originator 精确集合。
// 用精确等值匹配(而非「含 codex」宽松兜底)防 evil-codex_ 之类伪造绕过。
var officialCodexOriginatorSet = map[string]struct{}{
	"codex_cli_rs":          {},
	"codex-tui":             {},
	"codex_vscode":          {},
	"codex_vscode_copilot":  {},
	"codex_app":             {},
	"codex_chatgpt_desktop": {},
	"codex_atlas":           {},
	"codex_exec":            {},
	"codex_sdk_ts":          {},
}

// officialCodexUserAgentPrefixes 是官方 Codex 客户端家族 UA 前缀集(strict 只做 HasPrefix,
// 不退化为 Contains 子串兜底,收窄「浏览器前缀 + 中段 codex token」类伪造面)。
var officialCodexUserAgentPrefixes = []string{
	"codex_cli_rs/",
	"codex-tui/",
	"codex_vscode/",
	"codex_vscode_copilot/",
	"codex_app/",
	"codex_chatgpt_desktop/",
	"codex_atlas/",
	"codex_exec/",
	"codex_sdk_ts/",
}

// IsCodexVendor 判断账号平台是否属于 Codex 全局加固层覆盖的 vendor。
func IsCodexVendor(vendor string) bool {
	switch normalizeClientAccessText(vendor) {
	case "openai", "codex", "chatgpt":
		return true
	default:
		return false
	}
}

// IsOfficialCodexUserAgent 判断请求 UA 是否来自 HUAKAI 认可的官方 Codex 客户端形态。
// 匹配层级(高到低):UA strict 前缀集 → `codex ` 空格家族前缀(保留空格,避免退化成裸 codex)
// → UA 尾部 `(name; version)` 括号组的 name 交官方 originator 判定(codex-rs 把 clientInfo.name
// 写入 UA 尾部,可借此恢复被 originator override 的真实客户端)。
func IsOfficialCodexUserAgent(ua string) bool {
	normalized := normalizeClientAccessText(ua)
	if normalized == "" {
		return false
	}
	for _, prefix := range officialCodexUserAgentPrefixes {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	if strings.HasPrefix(normalized, "codex ") {
		return true
	}
	if name, ok := lastUserAgentParenName(normalized); ok {
		return IsOfficialCodexOriginator(name)
	}
	return false
}

// IsOfficialCodexOriginator 判断 originator 是否是 HUAKAI 认可的官方 Codex 客户端标识。
// 精确集合匹配 + `codex ` 空格家族前缀;不用「含 codex」宽松兜底(避免伪造绕过)。
func IsOfficialCodexOriginator(originator string) bool {
	normalized := normalizeClientAccessText(originator)
	if normalized == "" {
		return false
	}
	if _, ok := officialCodexOriginatorSet[normalized]; ok {
		return true
	}
	return strings.HasPrefix(normalized, "codex ")
}

func normalizeClientAccessText(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// lastUserAgentParenName 提取归一化 UA 尾部最后一个 `(...)` 括号组内 `;` 前的 name 段。
// 入参应为已归一化(小写 + 去首尾空格)的 UA;要求括号组落在 UA 末尾,无法解析返回 false。
func lastUserAgentParenName(ua string) (string, bool) {
	end := strings.LastIndex(ua, ")")
	if end != len(ua)-1 {
		return "", false
	}
	start := strings.LastIndex(ua[:end], "(")
	if start < 0 {
		return "", false
	}
	body := strings.TrimSpace(ua[start+1 : end])
	if body == "" {
		return "", false
	}
	name := body
	if idx := strings.Index(body, ";"); idx >= 0 {
		name = body[:idx]
	}
	name = normalizeClientAccessText(name)
	if name == "" {
		return "", false
	}
	return name, true
}

// matchClientEntry 遍历白名单条目,命中即回传该条(供读取 SkipEngineFingerprint 等条目级配置)。
func matchClientEntry(ua, originator string, entries []AllowedClientEntry) (AllowedClientEntry, bool) {
	for _, entry := range entries {
		if isAllowedClientMatch(ua, originator, entry) {
			return entry, true
		}
	}
	return AllowedClientEntry{}, false
}

// isAllowedClientMatch 白名单单条双因子 AND:originator 精确等值 + 每个 UAContains marker 都命中。
// UAContains 为空或含空白 marker → 安全失败(false),避免双因子退化成仅凭可伪造 originator 的单因子。
func isAllowedClientMatch(ua, originator string, entry AllowedClientEntry) bool {
	entryOriginator := normalizeClientAccessText(entry.Originator)
	if entryOriginator == "" || normalizeClientAccessText(originator) != entryOriginator {
		return false
	}
	if len(entry.UAContains) == 0 {
		return false
	}

	normalizedUA := normalizeClientAccessText(ua)
	for _, marker := range entry.UAContains {
		normalizedMarker := normalizeClientAccessText(marker)
		if normalizedMarker == "" {
			return false
		}
		if !strings.Contains(normalizedUA, normalizedMarker) {
			return false
		}
	}
	return true
}

// matchDenyEntries 遍历黑名单条目(OR),任一条命中即拒。
func matchDenyEntries(ua, originator string, entries []AllowedClientEntry) bool {
	for _, entry := range entries {
		if isDeniedClientMatch(ua, originator, entry) {
			return true
		}
	}
	return false
}

// isDeniedClientMatch 黑名单单条 OR 宽 deny:originator 精确命中,或任一非空 UAContains marker 命中即拒。
// 全空字段(originator 与 ua_contains 均空)→ 不命中。与白名单 AND 非对称:deny 宽、allow 严。
func isDeniedClientMatch(ua, originator string, entry AllowedClientEntry) bool {
	entryOriginator := normalizeClientAccessText(entry.Originator)
	if entryOriginator != "" && normalizeClientAccessText(originator) == entryOriginator {
		return true
	}

	normalizedUA := normalizeClientAccessText(ua)
	for _, marker := range entry.UAContains {
		normalizedMarker := normalizeClientAccessText(marker)
		if normalizedMarker == "" {
			continue
		}
		if strings.Contains(normalizedUA, normalizedMarker) {
			return true
		}
	}
	return false
}
