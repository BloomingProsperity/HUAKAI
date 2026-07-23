package dispatcher

import "testing"

// TestMediaEndpointFamilySet 锁定"哪些端点族要求账号 model_allow_list 显式命中"的完整集合。
// 判别性:集合里少一个媒体族(如拼错 gemini_count_tokens、漏 videos)对应子断言转红;
// 错收 chat 族(空清单=无限制的历史语义被破坏)同样转红。
func TestMediaEndpointFamilySet(t *testing.T) {
	media := []string{"images", "videos", "audio", "embeddings", "rerank", "gemini_count_tokens"}
	for _, family := range media {
		if !mediaEndpointFamily(family) {
			t.Fatalf("媒体端点族 %q 必须要求账号清单显式命中", family)
		}
	}
	nonMedia := []string{"", "chat", "completions", "responses", "gemini_generate_content", "media_tasks", "messages"}
	for _, family := range nonMedia {
		if mediaEndpointFamily(family) {
			t.Fatalf("非媒体端点族 %q 不得要求清单显式命中(空清单=无限制语义)", family)
		}
	}
}
