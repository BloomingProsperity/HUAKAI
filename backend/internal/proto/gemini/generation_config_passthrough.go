package gemini

import (
	"bytes"
	"encoding/json"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// preserveGeminiGenerationConfig 保存通用合同没有字段承载的 Gemini 原生控制项。
// 已进入规范字段的参数必须删除，避免出站合并时出现两个互相冲突的事实源。
func preserveGeminiGenerationConfig(raw []byte, env *proto.HCSF) {
	if env == nil {
		return
	}
	var request struct {
		GenerationConfig json.RawMessage `json:"generationConfig"`
	}
	if json.Unmarshal(raw, &request) != nil || len(bytes.TrimSpace(request.GenerationConfig)) == 0 {
		return
	}
	var config map[string]json.RawMessage
	if json.Unmarshal(request.GenerationConfig, &config) != nil {
		return
	}
	for _, key := range []string{
		"temperature", "topP", "maxOutputTokens", "stopSequences",
		"responseMimeType", "responseSchema",
	} {
		delete(config, key)
	}
	if len(config) == 0 {
		return
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return
	}
	if env.RequestControls.NativeOptions == nil {
		env.RequestControls.NativeOptions = make(map[string]json.RawMessage)
	}
	env.RequestControls.NativeOptions["gemini_messages"] = encoded
}
