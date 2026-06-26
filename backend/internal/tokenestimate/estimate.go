// 包 tokenestimate 提供一个快速、无依赖的启发式算法，在不调用真实 tokenizer
// 的前提下，近似估算一个请求 body 在指定上游 vendor 上会消耗的 input token 数。
//
// 该启发式逐个 rune 扫描 body，把每个 rune 归入少数几个字符类（CJK 字形、latin
// 单词串、数字串、空白/换行、emoji 以及若干标点桶），并按类累加权重。不同 vendor
// 对同一段文本的分词方式不同——CJK 字形在某些 tokenizer 上便宜、在另一些上昂贵，
// latin 单词很少与 token 一一对应——所以权重表按请求的 protocol family 选取。
// 结果刻意只是个估计值：它被一个 pre-dispatch 路由闸门消费，该闸门至多只能让路由
// 决策稍微更保守或更激进，绝不参与计费或正确性决策。
package tokenestimate

import (
	"math"
	"strings"
	"unicode"
)

// classWeights 保存扫描 body 时各字符类的乘数。这些值刻意取小数：由若干字母组成
// 的一个 latin 单词大致贡献一个权重单位，一个 CJK 字形贡献自己的一个单位，空白
// 贡献极少。
type classWeights struct {
	wordRun    float64 // 一个 latin 字母串（一个"单词"）
	numberRun  float64 // 一个连续数字串
	cjkGlyph   float64 // 每个单独的 CJK 字形
	punct      float64 // 普通标点 rune
	mathPunct  float64 // 数学运算符/符号
	pathPunct  float64 // url/path 分隔符 (/ : ? & = # %)
	atSign     float64 // '@'（倾向于拆分单词）
	emoji      float64 // 每个 emoji / 象形文字 rune
	newline    float64 // 换行或 tab
	space      float64 // 普通空格
	floor      int     // 一旦有内容就加上的基线增量
}

// vendorClass 把分词行为相同的 protocol family 归为一类。
type vendorClass int

const (
	vendorOpenAI vendorClass = iota
	vendorAnthropic
	vendorGemini
)

// weightsFor 返回某个 vendor class 的权重表。下面这些数字是手工调出的近似值，
// 选取它们是为了让 (a) 文本越长估值总是越高，且 (b) 在每家 vendor 上 CJK 密集文本
// 的估值都与 latin 单词文本不同——这正是路由闸门依赖的两个性质。它们不是从任何
// 参考表抄来的，而是独立选定的整齐数值。
func weightsFor(v vendorClass) classWeights {
	switch v {
	case vendorAnthropic:
		return classWeights{
			wordRun: 1.15, numberRun: 1.6, cjkGlyph: 1.2, punct: 0.4,
			mathPunct: 2.0, pathPunct: 1.25, atSign: 2.0, emoji: 2.5,
			newline: 0.9, space: 0.4, floor: 1,
		}
	case vendorGemini:
		return classWeights{
			wordRun: 1.15, numberRun: 2.5, cjkGlyph: 0.7, punct: 0.4,
			mathPunct: 1.1, pathPunct: 1.2, atSign: 2.5, emoji: 1.1,
			newline: 1.1, space: 0.2, floor: 1,
		}
	default: // vendorOpenAI 及任何未知 family 落到这里
		return classWeights{
			wordRun: 1.05, numberRun: 1.5, cjkGlyph: 0.85, punct: 0.4,
			mathPunct: 2.0, pathPunct: 1.0, atSign: 2.0, emoji: 2.0,
			newline: 0.5, space: 0.4, floor: 1,
		}
	}
}

// classForProtocolFamily maps a HUAKAI protocol-family literal (the same values
// produced by registry resolution, e.g. "anthropic_messages", "openai_chat",
// "gemini_messages") onto a tokenizer vendor class. Unknown families fall back
// to the OpenAI-style table, matching the gate's fail-open philosophy.
func classForProtocolFamily(family string) vendorClass {
	switch {
	case strings.HasPrefix(family, "anthropic"), strings.HasPrefix(family, "claude"):
		return vendorAnthropic
	case strings.HasPrefix(family, "gemini"):
		return vendorGemini
	default:
		return vendorOpenAI
	}
}

// Estimate approximates the input-token count of body for the given protocol
// family. An empty body estimates 0. The estimate is monotonic in body length
// (appending content never lowers the estimate) and is vendor-weighted, so a
// run of CJK glyphs estimates differently from an equal-length run of latin
// words.
func Estimate(body []byte, protocolFamily string) int {
	return EstimateString(string(body), protocolFamily)
}

// EstimateString is Estimate over a string. It is the scanning core; Estimate
// is a thin []byte adapter.
func EstimateString(text, protocolFamily string) int {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0
	}
	w := weightsFor(classForProtocolFamily(protocolFamily))

	const (
		runNone = iota
		runWord
		runNumber
	)
	current := runNone
	var total float64

	for _, r := range text {
		switch {
		case unicode.IsSpace(r):
			current = runNone
			if r == '\n' || r == '\t' || r == '\r' {
				total += w.newline
			} else {
				total += w.space
			}
		case isCJKGlyph(r):
			current = runNone
			total += w.cjkGlyph
		case isPictograph(r):
			current = runNone
			total += w.emoji
		case unicode.IsLetter(r):
			// A latin/alphabetic run is charged once at its start; interior
			// letters of the same run are free, modelling subword merging.
			if current != runWord {
				total += w.wordRun
				current = runWord
			}
		case unicode.IsDigit(r):
			if current != runNumber {
				total += w.numberRun
				current = runNumber
			}
		default:
			current = runNone
			switch {
			case isMathSymbol(r):
				total += w.mathPunct
			case r == '@':
				total += w.atSign
			case isPathDelim(r):
				total += w.pathPunct
			default:
				total += w.punct
			}
		}
	}

	return int(math.Ceil(total)) + w.floor
}

// isCJKGlyph reports whether r is a Chinese/Japanese/Korean (or related) glyph
// that most tokenizers charge per-character.
func isCJKGlyph(r rune) bool {
	switch {
	case r >= 0x4E00 && r <= 0x9FFF: // CJK Unified Ideographs
		return true
	case r >= 0x3400 && r <= 0x4DBF: // CJK Extension A
		return true
	case r >= 0x3040 && r <= 0x30FF: // Hiragana + Katakana
		return true
	case r >= 0xAC00 && r <= 0xD7AF: // Hangul syllables
		return true
	case r >= 0xF900 && r <= 0xFAFF: // CJK compatibility ideographs
		return true
	default:
		return false
	}
}

// isPictograph reports whether r sits in one of the common emoji / pictograph
// blocks.
func isPictograph(r rune) bool {
	switch {
	case r >= 0x1F300 && r <= 0x1FAFF:
		return true
	case r >= 0x2600 && r <= 0x27BF:
		return true
	case r >= 0x1F000 && r <= 0x1F2FF:
		return true
	default:
		return false
	}
}

// isMathSymbol reports whether r is a mathematical operator/symbol that
// tokenizers tend to charge more for than ordinary punctuation.
func isMathSymbol(r rune) bool {
	if unicode.Is(unicode.Sm, r) {
		return true
	}
	switch r {
	case '∑', '∫', '∂', '√', '∞', '≈', '≠', '≤', '≥', '±', '×', '÷':
		return true
	default:
		return false
	}
}

// isPathDelim reports whether r is a url/path delimiter that common tokenizers
// handle cheaply.
func isPathDelim(r rune) bool {
	switch r {
	case '/', ':', '?', '&', '=', '#', '%':
		return true
	default:
		return false
	}
}
