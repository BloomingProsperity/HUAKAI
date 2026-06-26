package tokencheck

import (
	"fmt"
	"strings"
	"testing"
)

func TestEstimateRequestInputTokensTextOnly(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hello world"}]}`)
	// 字符串叶子:"m"=1,"user"=1,"hello world"(10 个非空白 ascii/4→3)=3;键名不计。
	// MUTATION: 键名计入估算 → 总数膨胀(model/messages/role/content 全算进去)→ RED;
	// 改用 len(body)/4 → 16+ → RED。
	if got := EstimateRequestInputTokens(body); got != 5 {
		t.Fatalf("EstimateRequestInputTokens=%d want 5", got)
	}
}

func TestEstimateRequestInputTokensCJK(t *testing.T) {
	body := []byte(`{"contents":[{"parts":[{"text":"你好世界"}]}]}`)
	// 4 个 CJK / 1.5 → ceil(2.67) = 3(gemini 原生 contents 形态同样覆盖)。
	// MUTATION: 估算器只认 messages 键(协议耦合)→ gemini 形态回 1(floor)→ RED。
	if got := EstimateRequestInputTokens(body); got != 3 {
		t.Fatalf("EstimateRequestInputTokens=%d want 3", got)
	}
}

func TestEstimateRequestInputTokensCapsBase64Blob(t *testing.T) {
	blob := "data:image/png;base64," + strings.Repeat("iVBORw0KGgoAAAANSUhEUg", 400) // ~8.8KB
	body := []byte(fmt.Sprintf(`{"messages":[{"role":"user","content":[{"type":"text","text":"describe"},{"type":"image_url","image_url":{"url":"%s"}}]}]}`, blob))
	got := EstimateRequestInputTokens(body)
	// MUTATION: 去掉 blobTokenCap 封顶,8.8KB blob 折 ~2200 token → 上界断言 RED;
	// 退回 len(body)/4 整体字节估算 → 同样 RED。多收钱回归(评审 F-1)由此锁死。
	upper := blobTokenCap + 64 // blob 封顶 + 周边少量文本叶子
	if got > upper {
		t.Fatalf("EstimateRequestInputTokens=%d want <= %d (blob must be capped)", got, upper)
	}
	if got < blobTokenCap {
		t.Fatalf("EstimateRequestInputTokens=%d want >= %d (capped blob still billable)", got, blobTokenCap)
	}
}

func TestEstimateRequestInputTokensCapsNewlineWrappedBase64(t *testing.T) {
	// 标准 76 列 MIME 包裹 base64(Anthropic source.data / Gemini inlineData.data
	// 真实客户端形态)。MUTATION: blob 判定不容忍 \n(采样窗内必有换行)→ 走
	// estimateText 无封顶,~48KB 折 ~12000 token 终局超收 → 上界断言 RED。
	line := strings.Repeat("iVBORw0KGgoAAAANSUhEUgAA", 3) + "\n" // 72 列 + 换行
	blob := strings.Repeat(line, 660)                            // ~48KB
	body := []byte(fmt.Sprintf(`{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":%q}}]}]}`, blob))
	got := EstimateRequestInputTokens(body)
	upper := blobTokenCap + 64
	if got > upper {
		t.Fatalf("EstimateRequestInputTokens=%d want <= %d (wrapped base64 must be capped)", got, upper)
	}
}

func TestEstimateRequestInputTokensDoesNotCapLongProse(t *testing.T) {
	// 12KB 含空格英文长文是真实计费输入,绝不可按 blob 封顶。
	// MUTATION: blob 字符集容忍空格 → 长文被封顶 1536 → 下界断言 RED(系统性少收)。
	prose := strings.TrimSpace(strings.Repeat("the quick brown fox jumps over the lazy dog ", 280)) // ~12.3KB
	body := []byte(fmt.Sprintf(`{"messages":[{"role":"user","content":%q}]}`, prose))
	got := EstimateRequestInputTokens(body)
	if got <= blobTokenCap {
		t.Fatalf("EstimateRequestInputTokens=%d want > %d (real prose must not be capped)", got, blobTokenCap)
	}
}

func TestEstimateRequestInputTokensMalformedJSONFallsBackToBytes(t *testing.T) {
	body := []byte("not json at all {{{")
	want := (len("not json at all {{{") + 3) / 4
	if got := EstimateRequestInputTokens(body); got != want {
		t.Fatalf("EstimateRequestInputTokens=%d want %d", got, want)
	}
}

func TestEstimateRequestInputTokensFloorsAtOne(t *testing.T) {
	for _, body := range []string{"", `{"a":null}`, `{}`} {
		if got := EstimateRequestInputTokens([]byte(body)); got != 1 {
			t.Fatalf("EstimateRequestInputTokens(%q)=%d want 1", body, got)
		}
	}
}

// EstimateRequestInputTokensWith 必须让纯文本叶子走注入的计数器,同时仍用内置
// 启发式给 base64/二进制大块封顶(绝不能把 1MB 的 data-URI 交给真实 tokenizer)。
// MUTATION:若让 estimateStringLeaf 也把大块送进 textCounter,那个哨兵计数器
//(每次调用返回一个巨大的固定值)就会把大块的贡献撑爆 -> 上界断言转红。
func TestEstimateRequestInputTokensWith_InjectedCounterButBlobsCapped(t *testing.T) {
	const perText = 100000
	textOnly := []byte(`{"a":"hello","b":"world"}`)
	// 两个文本叶子 -> 经注入计数器得到 2 * perText。
	if got := EstimateRequestInputTokensWith(textOnly, func(string) int { return perText }); got != 2*perText {
		t.Fatalf("injected counter not used for text leaves: got %d want %d", got, 2*perText)
	}

	blob := "data:image/png;base64," + strings.Repeat("A", 40000)
	body := []byte(fmt.Sprintf(`{"img":%q}`, blob))
	got := EstimateRequestInputTokensWith(body, func(string) int { return perText })
	if got >= perText {
		t.Fatalf("blob leaf was routed through the injected counter (got %d); it must be capped", got)
	}
	if got > blobTokenCap {
		t.Fatalf("blob estimate %d exceeds cap %d", got, blobTokenCap)
	}

	// nil 计数器的行为必须与默认启发式入口完全一致。
	if EstimateRequestInputTokensWith(textOnly, nil) != EstimateRequestInputTokens(textOnly) {
		t.Fatal("nil counter must fall back to the default heuristic")
	}
}
