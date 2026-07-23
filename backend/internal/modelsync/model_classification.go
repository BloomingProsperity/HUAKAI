package modelsync

import "strings"

// classifyOpenAIModel 把模型目录项归到公开 API 能力；未知名称保留为空能力，
// 交给模型发现流程人工确认，不能因为本地词表落后而静默丢掉上游新模型。
func classifyOpenAIModel(id string) []string {
	lower := strings.ToLower(strings.TrimSpace(id))
	switch {
	case lower == "":
		return nil
	case strings.Contains(lower, "gpt-image"), strings.Contains(lower, "dall-e"):
		return []string{"image_output", "images"}
	case strings.HasPrefix(lower, "sora"):
		return []string{"video"}
	case strings.Contains(lower, "embedding"):
		return []string{"embeddings"}
	case strings.Contains(lower, "rerank"):
		return []string{"rerank"}
	case strings.Contains(lower, "moderation"):
		return []string{"moderation"}
	case strings.Contains(lower, "whisper"), strings.Contains(lower, "transcribe"):
		return []string{"audio", "audio_transcription"}
	case strings.Contains(lower, "tts"), strings.Contains(lower, "speech"):
		return []string{"audio", "audio_speech"}
	case strings.Contains(lower, "realtime"):
		return []string{"audio", "live_session"}
	case strings.Contains(lower, "audio"):
		return []string{"audio", "audio_input", "chat"}
	case strings.HasPrefix(lower, "ft:gpt-"):
		return []string{"chat"}
	}
	for _, prefix := range []string{"gpt-", "o1", "o3", "o4", "chatgpt-"} {
		if strings.HasPrefix(lower, prefix) {
			return []string{"chat", "responses"}
		}
	}
	return nil
}

// ClassifyOpenAIModel 供账号级目录发现复用同一份公开能力归类，避免全局目录与
// 单账号目录对同一模型给出不同口径。
func ClassifyOpenAIModel(id string) []string {
	return append([]string(nil), classifyOpenAIModel(id)...)
}

// ClassifyOpenAICompatibleModel 给未接专门分类器的 openai-compatible 车道按模型名派生
// 媒体能力:嵌入/重排模型名字带稳定词根,不派生则媒体能力门(模型注册表 fail-closed)会把
// 它们判成"不支持"。非媒体名回落 chat,与该车道历史默认一致。
func ClassifyOpenAICompatibleModel(id string) []string {
	lower := strings.ToLower(strings.TrimSpace(id))
	switch {
	case lower == "":
		return nil
	case strings.Contains(lower, "embedding"), strings.Contains(lower, "embed-"):
		return []string{"embeddings"}
	case strings.Contains(lower, "rerank"):
		return []string{"rerank"}
	default:
		return []string{"chat"}
	}
}

func classifyGrokModel(id string) []string {
	lower := strings.ToLower(strings.TrimSpace(id))
	switch {
	case lower == "":
		return nil
	case strings.Contains(lower, "imagine-video"), strings.Contains(lower, "video"):
		return []string{"video"}
	case strings.Contains(lower, "imagine-image"), strings.Contains(lower, "-image-"):
		return []string{"image_output", "images"}
	case strings.Contains(lower, "multi-agent"):
		return []string{"responses", "tools"}
	case strings.HasPrefix(lower, "grok-"):
		capabilities := []string{"chat", "responses", "tools"}
		if strings.Contains(lower, "vision") {
			capabilities = append(capabilities, "vision")
		}
		return capabilities
	default:
		return nil
	}
}

// ClassifyGrokModel 供账号级目录发现复用全局目录的能力归类。
func ClassifyGrokModel(id string) []string {
	return append([]string(nil), classifyGrokModel(id)...)
}
