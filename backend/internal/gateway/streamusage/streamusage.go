// Package streamusage 为官方 OpenAI 兼容族的流式出站强制 stream_options.include_usage,
// 确保上游返回权威 usage,避免估算计费(B1)。
package streamusage

import "encoding/json"

// officialFamilies 是官方 OpenAI 兼容族(响应 SSE 同 OpenAI、
// 支持 stream_options.include_usage 在流末返回权威 usage)。**不含**反转 session 族
// (copilot/cursor/antigravity/kiro/windsurf——它们要请求真实性,不能注入客户端没发的字段),
// 也不含非 OpenAI 兼容 SSE 的 dify_chat / ollama_native。
var officialFamilies = map[string]struct{}{
	"openai_chat": {}, "deepseek_chat": {}, "mistral_chat": {}, "groqcloud_chat": {},
	"together_chat": {}, "perplexity_chat": {}, "fireworks_chat": {}, "kimi_chat": {},
	"qwen_chat": {}, "glm_chat": {}, "yi_chat": {}, "baichuan_chat": {}, "doubao_chat": {},
	"ernie_chat": {}, "step_chat": {}, "hunyuan_chat": {}, "minimax_chat": {}, "cohere_chat": {},
	"ollama_chat": {},
}

// maybeInjectStreamUsage 对官方 OpenAI 兼容族的流式出站强制 stream_options.include_usage=true
// (保留客户端其它 stream_options 字段)。仅当 body 顶层 stream==true 且已是对象时注入;
// 已为 true 则不动。解析失败 fail-open 原样返回(B1)。
func Inject(protocolFamily string, body []byte) []byte {
	if _, ok := officialFamilies[protocolFamily]; !ok || len(body) == 0 {
		return body
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil || root == nil {
		return body
	}
	var stream bool
	if json.Unmarshal(root["stream"], &stream) != nil || !stream {
		return body
	}
	opts := map[string]json.RawMessage{}
	if raw, ok := root["stream_options"]; ok {
		if json.Unmarshal(raw, &opts) != nil || opts == nil {
			opts = map[string]json.RawMessage{}
		}
	}
	var included bool
	if json.Unmarshal(opts["include_usage"], &included) == nil && included {
		return body // 客户端已请求权威 usage,不动
	}
	opts["include_usage"] = json.RawMessage("true")
	optsRaw, err := json.Marshal(opts)
	if err != nil {
		return body
	}
	root["stream_options"] = optsRaw
	out, err := json.Marshal(root)
	if err != nil {
		return body
	}
	return out
}
