package realtokenizer

import (
	"fmt"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/tokencheck"
	"github.com/tiktoken-go/tokenizer"
)

// OpenAI 系模型必须用 tiktoken 而不是按字节/字符的启发式算法来对其纯文本 JSON 叶子节点计数。
// 期望值是独立地用该库重新算出来的,因此断言能自证、不是脆弱的魔数。
// 变异:让 codecForModel 对 gpt 系模型返回 (nil,false)(从而使 InputTokens 回退到启发式),
// 则 got != want -> 变红。
func TestInputTokens_OpenAIModelUsesTiktoken(t *testing.T) {
	text := "The quick brown fox jumps over the lazy dog, repeatedly and at some length."
	body := []byte(fmt.Sprintf(`{"model":"gpt-4o","messages":[{"role":"user","content":%q}]}`, text))

	codec, err := tokenizer.ForModel("gpt-4o")
	if err != nil {
		t.Fatalf("ForModel gpt-4o: %v", err)
	}
	// JSON 遍历会对每个字符串 VALUE 叶子节点计数:模型名、role、以及 content。
	// 键(key)不计入。
	want := 0
	for _, leaf := range []string{"gpt-4o", "user", text} {
		n, err := codec.Count(leaf)
		if err != nil {
			t.Fatalf("Count(%q): %v", leaf, err)
		}
		want += n
	}

	got := InputTokens("gpt-4o", body)
	if got != want {
		t.Fatalf("InputTokens(gpt-4o)=%d; want tiktoken sum %d", got, want)
	}
	// 而且它必须与启发式结果不同——否则该测试就无法把真实分词器和回退算法区分开。
	if heur := tokencheck.EstimateRequestInputTokens(body); got == heur {
		t.Fatalf("tiktoken count %d coincidentally equals heuristic %d; pick a more discriminating fixture", got, heur)
	}
}

// 非 OpenAI 模型没有 tiktoken 编码器,因此 InputTokens 必须精确地使用共享的启发式算法。
// 变异:如果某个 Claude 模型被错误地解析出了 codec,结果就会偏离启发式 -> 变红。
func TestInputTokens_NonOpenAIFallsBackToHeuristic(t *testing.T) {
	body := []byte(`{"model":"claude-3-5-sonnet-20241022","messages":[{"role":"user","content":"hello there"}]}`)

	if _, ok := textCounter("claude-3-5-sonnet-20241022"); ok {
		t.Fatal("claude model must not resolve a tiktoken codec")
	}
	got := InputTokens("claude-3-5-sonnet-20241022", body)
	want := tokencheck.EstimateRequestInputTokens(body)
	if got != want {
		t.Fatalf("non-OpenAI InputTokens=%d; want heuristic %d", got, want)
	}
}

// 多模态请求绝不能把 base64 大块喂给分词器:该大块会被 tokencheck 截断,因此即便是
// OpenAI 模型,其估算也会被限定在远低于原始 byte/4 数值的范围内。
// 变异:让大块经过计数器(去掉截断),则估算会朝 len(blob)/4 暴涨 -> 变红。
func TestInputTokens_BlobIsCappedNotTokenized(t *testing.T) {
	blob := "data:image/png;base64," + strings.Repeat("A", 40000) // ~40KB;byte/4 ~= 10000
	body := []byte(fmt.Sprintf(`{"model":"gpt-4o","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":%q}}]}]}`, blob))

	got := InputTokens("gpt-4o", body)
	if got >= len(blob)/4 {
		t.Fatalf("blob estimate=%d not capped (raw byte/4=%d)", got, len(blob)/4)
	}
	if got > 4000 {
		t.Fatalf("blob estimate=%d unexpectedly large; cap not applied", got)
	}
}

// 变异:翻转 parseEnabled 的任一分支,这些用例中就会有一个变红。
func TestParseEnabled(t *testing.T) {
	cases := map[string]bool{"": true, "true": true, "1": true, "false": false, "0": false, "garbage": true}
	for raw, want := range cases {
		if got := parseEnabled(raw); got != want {
			t.Fatalf("parseEnabled(%q)=%v want %v", raw, got, want)
		}
	}
}

// codec 缓存必须返回稳定的结果(包括否定结果),这样热路径上每个请求都不必重新解析。
// 变异:不存储该条目,则第二次查找会重新跑一遍 ForModel(此处结果仍正确,但缓存的存在
// 才是要守护的不变量)——断言缓存到的值完全一致。
func TestCodecForModel_Caches(t *testing.T) {
	c1, ok1 := codecForModel("gpt-4o")
	c2, ok2 := codecForModel("gpt-4o")
	if !ok1 || !ok2 || c1 != c2 {
		t.Fatalf("codec cache not stable: ok1=%v ok2=%v same=%v", ok1, ok2, c1 == c2)
	}
	if _, ok := codecForModel(""); ok {
		t.Fatal("empty model must not resolve a codec")
	}
}
