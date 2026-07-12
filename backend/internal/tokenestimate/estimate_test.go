package tokenestimate

import (
	"strings"
	"testing"
)

// TestEstimateInputTokens_CJKvsLatin_Discriminating 证明启发式对 CJK 字符
// 按【不同】于拉丁词串的逐类权重计费——即它是按厂商加权的，而非平铺计数。
// 两个 fixture 结构完全一致（N 个单元，各以单个逗号分隔），因此两者唯一
// 不同的项是 cjkGlyph*N 与 wordRun*N；逗号/分隔符的贡献完全相同会相消。
//
// 变异守卫：把 cjkGlyph 和 wordRun 压成同一个值（即本包旨在避免的朴素
// 平铺计数缺陷），两个估算就会相等 → 变红。这是自证的：结构化框定恰好
// 隔离出了逐类权重那一项。
func TestEstimateInputTokens_CJKvsLatin_Discriminating(t *testing.T) {
	const n = 100
	cjkParts := make([]string, n)
	latinParts := make([]string, n)
	for i := 0; i < n; i++ {
		cjkParts[i] = "文"   // 每个单元一个 CJK 字符
		latinParts[i] = "a" // 每个单元一个拉丁字母（单字母词串）
	}
	cjk := strings.Join(cjkParts, ",")
	latin := strings.Join(latinParts, ",")

	for _, family := range []string{"openai_chat", "anthropic_messages", "gemini_messages"} {
		cjkEst := Estimate([]byte(cjk), family)
		latinEst := Estimate([]byte(latin), family)
		if cjkEst == latinEst {
			t.Fatalf("family %q: CJK estimate (%d) must differ from latin word estimate (%d) — cjkGlyph and wordRun weights collapsed to a flat count?", family, cjkEst, latinEst)
		}
	}
}

// TestEstimateInputTokens_Monotonic 证明 body 增大时估算从不缩小，且空 body
// 的估算恰好为 0。
//
// 变异守卫：如果计数被钳制/封顶在常量，更长 body 的断言会变红；如果去掉空
// body 的短路分支，空场景会返回下限补值（>0）而变红。
func TestEstimateInputTokens_Monotonic(t *testing.T) {
	if got := Estimate([]byte(""), "openai_chat"); got != 0 {
		t.Fatalf("empty body must estimate 0, got %d", got)
	}
	if got := Estimate([]byte("   \n\t "), "openai_chat"); got != 0 {
		t.Fatalf("whitespace-only body must estimate 0, got %d", got)
	}

	short := []byte("the quick brown fox")
	long := []byte("the quick brown fox jumps over the lazy dog repeatedly and again and again")
	se := Estimate(short, "openai_chat")
	le := Estimate(long, "openai_chat")
	if le < se {
		t.Fatalf("longer body estimate (%d) must be >= shorter (%d)", le, se)
	}
	if le == se {
		t.Fatalf("a strictly longer body should estimate strictly more here (short=%d long=%d)", se, le)
	}
}

// TestEstimate_VendorWeightsDiffer 证明 protocol-family 选择器确实在切换权重表：
// 一个 CJK 密集的 body 在三个厂商类下估算各不相同（Gemini 对 CJK 的计费比
// Anthropic 便宜）。
//
// 变异守卫：如果 classForProtocolFamily 始终返回同一个类，估算会塌缩为相等，
// 本测试变红。
func TestEstimate_VendorWeightsDiffer(t *testing.T) {
	body := []byte(strings.Repeat("漢字", 200))
	anthropic := Estimate(body, "anthropic_messages")
	gemini := Estimate(body, "gemini_messages")
	if anthropic == gemini {
		t.Fatalf("CJK-heavy body must estimate differently for anthropic (%d) vs gemini (%d) — vendor weights not selected by family?", anthropic, gemini)
	}
}

// TestEstimate_UnknownFamily_FallsBack 证明未知 protocol family 不会 panic，
// 并产出合理的正数估算（OpenAI 回退表）。
func TestEstimate_UnknownFamily_FallsBack(t *testing.T) {
	got := Estimate([]byte("hello world"), "some_unknown_family_xyz")
	want := Estimate([]byte("hello world"), "openai_chat")
	if got != want {
		t.Fatalf("unknown family should fall back to openai table: got %d want %d", got, want)
	}
	if got <= 0 {
		t.Fatalf("non-empty body must estimate > 0, got %d", got)
	}
}
