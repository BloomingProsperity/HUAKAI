package upstreamcontract

import "strings"

// 识别当前官方合同需要特殊请求门的模型族。只看公开模型 ID 前缀，
// 不绑定某个渠道或账号模式。

func normalizeModelID(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}

func modelHasPrefix(model string, prefixes ...string) bool {
	id := normalizeModelID(model)
	if id == "" {
		return false
	}
	for _, prefix := range prefixes {
		if id == prefix || strings.HasPrefix(id, prefix+"-") || strings.HasPrefix(id, prefix+"@") {
			return true
		}
	}
	return false
}

func isGPT6Astra(model string) bool {
	return modelHasPrefix(model, "gpt-6-astra")
}

func isClaudeFableAlwaysOn(model string) bool {
	return modelHasPrefix(model, "claude-fable-5", "claude-mythos-5")
}
