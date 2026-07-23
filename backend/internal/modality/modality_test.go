package modality

import "testing"

// TestSupports_KeywordMatrix 锁定每个 modality 的能力关键词集合。
// 判别性:改表中任一关键词、或把某 lane 的关键词集合张冠李戴,对应断言转红。
func TestSupports_KeywordMatrix(t *testing.T) {
	cases := []struct {
		name         string
		capabilities []string
		modality     Modality
		want         bool
	}{
		{"image 命中 image_output", []string{"image_output"}, Image, true},
		{"image 命中 images 别名", []string{"images"}, Image, true},
		{"image 不受 chat 能力影响", []string{"chat", "stream", "tools"}, Image, false},
		{"embeddings 命中标准词", []string{"embeddings"}, Embeddings, true},
		{"embeddings 命中 gemini embedContent", []string{"embedContent"}, Embeddings, true},
		{"embeddings 命中 gemini batchEmbedContents", []string{"batchEmbedContents"}, Embeddings, true},
		{"embeddings 拒绝 chat-only 模型", []string{"chat", "responses"}, Embeddings, false},
		{"rerank 命中", []string{"rerank"}, Rerank, true},
		{"rerank 拒绝 embeddings 模型", []string{"embeddings"}, Rerank, false},
		{"countTokens 命中", []string{"countTokens"}, CountTokens, true},
		{"countTokens 拒绝 chat-only", []string{"chat"}, CountTokens, false},
		{"audio_speech 命中", []string{"audio_speech"}, AudioSpeech, true},
		{"audio_speech 拒绝转写能力", []string{"audio_transcription"}, AudioSpeech, false},
		{"audio_transcription 命中", []string{"audio_transcription"}, AudioTranscription, true},
		{"audio_transcription 拒绝合成能力", []string{"audio_speech"}, AudioTranscription, false},
		{"video 命中 video", []string{"video"}, Video, true},
		{"video 命中 video_output", []string{"video_output"}, Video, true},
		{"video 拒绝 image 模型", []string{"image_output", "images"}, Video, false},
		{"未知 modality 恒拒绝", []string{"chat"}, Modality("bogus"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Supports(tc.capabilities, tc.modality); got != tc.want {
				t.Fatalf("Supports(%v, %s) = %v, want %v", tc.capabilities, tc.modality, got, tc.want)
			}
		})
	}
}

// TestSupports_EmptyCapabilitiesFailClosed 锁定空能力声明的 fail-closed 合同:任何 modality
// 对空声明一律拒绝——媒体请求打到能力未知的模型必须显性拒绝,不得放行赌上游兜底。
// 判别性:给任一 modality 恢复"空集合放行"的旁路即转红。
func TestSupports_EmptyCapabilitiesFailClosed(t *testing.T) {
	all := []Modality{Image, Embeddings, Rerank, CountTokens, AudioSpeech, AudioTranscription, Video}
	for _, m := range all {
		if Supports(nil, m) {
			t.Fatalf("Supports(空能力, %s) = true, 应 fail-closed 拒绝", m)
		}
		if Supports([]string{}, m) {
			t.Fatalf("Supports(空切片, %s) = true, 应 fail-closed 拒绝", m)
		}
	}
}
