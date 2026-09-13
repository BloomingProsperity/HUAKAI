package moderation

// 入站协议标识只服务本包抽取与入口接线，不复述外部项目名称或目录结构。
const (
	ProtocolOpenAIChat            = "openai_chat"
	ProtocolAnthropicMessages     = "anthropic_messages"
	ProtocolOpenAIResponses       = "openai_responses"
	ProtocolGemini                = "gemini"
	ProtocolOpenAICompletions     = "openai_completions"
	ProtocolOpenAIEmbeddings      = "openai_embeddings"
	ProtocolOpenAIRerank          = "openai_rerank"
	ProtocolOpenAIImages          = "openai_images"
	ProtocolOpenAIAudioSpeech     = "openai_audio_speech"
	ProtocolOpenAIAudioTranscript = "openai_audio_transcription"
	ProtocolOpenAIVideo           = "openai_video"
	ProtocolMediaTask             = "media_task"
)

func registeredProtocol(protocol string) bool {
	switch protocol {
	case ProtocolOpenAIChat, ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolGemini,
		ProtocolOpenAICompletions, ProtocolOpenAIEmbeddings, ProtocolOpenAIRerank, ProtocolOpenAIImages,
		ProtocolOpenAIAudioSpeech, ProtocolOpenAIAudioTranscript, ProtocolOpenAIVideo, ProtocolMediaTask:
		return true
	default:
		return false
	}
}

// protocolAllowsEmptyText 表示该协议已登记，但本切只扫用户可控文本；
// 无文本（如图片变体、无提示转写）视为已进闸而非未知协议。
func protocolAllowsEmptyText(protocol string) bool {
	switch protocol {
	case ProtocolOpenAIImages, ProtocolOpenAIAudioTranscript, ProtocolMediaTask:
		return true
	default:
		return false
	}
}
