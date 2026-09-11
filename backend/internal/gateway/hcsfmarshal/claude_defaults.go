package hcsfmarshal

import "github.com/BloomingProsperity/HUAKAI/internal/proto"

// claudeTranslatedMaxTokensFallback 是目录未登记正输出上限时的翻译兜底。
// 只用于 HCSF 翻译路径，不进入官方直发。
const claudeTranslatedMaxTokensFallback = 4096

func translatedClaudeMaxTokens(env *proto.HCSF) int {
	if env != nil {
		if n := env.RequestMeta.CatalogMaxOutputTokens; n != nil && *n > 0 {
			return *n
		}
	}
	return claudeTranslatedMaxTokensFallback
}

func applyClaudeTranslatedMaxTokens(body map[string]any, env *proto.HCSF, family string) {
	if family != "anthropic_messages" || body == nil || env == nil {
		return
	}
	if _, exists := body["max_tokens"]; exists {
		return
	}
	if env.RequestControls.MaxTokens != nil {
		return
	}
	body["max_tokens"] = translatedClaudeMaxTokens(env)
	addMarshalLossRaw(env, family, proto.CapabilityText, "", "translated Claude request lacked max_tokens; applied catalog limit or HUAKAI fallback", "claude_max_tokens_translated_default")
}
