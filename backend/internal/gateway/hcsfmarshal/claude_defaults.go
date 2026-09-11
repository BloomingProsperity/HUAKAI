package hcsfmarshal

import "github.com/BloomingProsperity/HUAKAI/internal/proto"

// claudeTranslatedMaxTokens 是跨协议打到 Claude 族且请求未给上限时的 HUAKAI 保守值。
// 只用于 HCSF 翻译路径，不进入官方直发，也不是按模型抄来的厂商表。
const claudeTranslatedMaxTokens = 4096

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
	body["max_tokens"] = claudeTranslatedMaxTokens
	addMarshalLossRaw(env, family, proto.CapabilityText, "", "translated Claude request lacked max_tokens; applied HUAKAI conservative default", "claude_max_tokens_translated_default")
}
