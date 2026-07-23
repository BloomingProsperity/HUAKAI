// Package modality 是"某个模型能否服务某类媒体请求"的唯一判定原语。
//
// 判定只看请求模型在注册表里的能力声明(model_registry_capabilities,经
// registry.Resolved.Capabilities 投影),与账号无关——modality 是模型属性,不是账号属性。
// 各媒体 lane(图片/embeddings/rerank/countTokens/audio/video)统一调 Supports,避免同一
// 模型在不同 lane 给出不同口径;新增一种 modality = 本表加一行,无需再抄判定骨架。
//
// fail-closed 合同:模型能力声明为空(未探测/未登记)一律判不支持。媒体请求打到能力
// 未知的模型,正确出口是"注册表能力未配置"的显性拒绝,不是放行后由上游 4xx 兜底——
// 上游"不支持"多为终态错误,既不换号也说不清根因。发现/上架管道会为各车道模型写入
// 能力声明;手工建的模型须同时登记能力才对媒体端点可用。
package modality

// Modality 是一类专门媒体请求端点对应的能力域。
type Modality string

const (
	Image              Modality = "image"
	Embeddings         Modality = "embeddings"
	Rerank             Modality = "rerank"
	CountTokens        Modality = "count_tokens"
	AudioSpeech        Modality = "audio_speech"
	AudioTranscription Modality = "audio_transcription"
	Video              Modality = "video"
)

// keywords 是 modality→注册表能力关键词的唯一真相表:模型声明了对应集合中任一能力
// 即支持该 modality。集合覆盖各车道词表(openai 系/gemini generateContent 词表/兼容别名)。
var keywords = map[Modality][]string{
	Image:              {"image_output", "images"},
	Embeddings:         {"embeddings", "embedContent", "batchEmbedContents"},
	Rerank:             {"rerank"},
	CountTokens:        {"countTokens"},
	AudioSpeech:        {"audio_speech"},
	AudioTranscription: {"audio_transcription"},
	Video:              {"video", "video_output"},
}

// Supports 判定拥有 capabilities 能力声明的模型能否服务 m 类请求。
// capabilities 取自请求模型的注册表能力(registry.Resolved.Capabilities),非账号能力;
// 空声明按包合同 fail-closed。
func Supports(capabilities []string, m Modality) bool {
	want, ok := keywords[m]
	if !ok {
		return false
	}
	for _, capability := range capabilities {
		for _, keyword := range want {
			if capability == keyword {
				return true
			}
		}
	}
	return false
}
