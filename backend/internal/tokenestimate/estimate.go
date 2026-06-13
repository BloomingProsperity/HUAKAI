// Package tokenestimate provides a fast, dependency-free heuristic that
// approximates the number of input tokens a request body will consume on a
// given upstream vendor, without invoking a real tokenizer.
//
// The heuristic walks the body rune-by-rune, classifies each rune into a small
// number of character classes (CJK glyphs, latin word runs, digit runs,
// whitespace/newlines, emoji, and several punctuation buckets), and accumulates
// a per-class weight. Different vendors tokenize the same text differently —
// CJK glyphs are cheap on some tokenizers and expensive on others, latin words
// rarely map 1:1 to tokens — so the weight table is selected from the request's
// protocol family. The result is deliberately an estimate: it is consumed by a
// pre-dispatch routing gate that can only make a routing decision slightly more
// or less conservative, never a billing or correctness decision.
package tokenestimate

import (
	"math"
	"strings"
	"unicode"
)

// classWeights holds the per-character-class multipliers used while scanning a
// body. Values are intentionally fractional: a latin word of several letters
// contributes roughly one weight unit, a CJK glyph contributes its own unit,
// and whitespace contributes very little.
type classWeights struct {
	wordRun    float64 // one latin alphabetic run (a "word")
	numberRun  float64 // one contiguous digit run
	cjkGlyph   float64 // each individual CJK glyph
	punct      float64 // ordinary punctuation rune
	mathPunct  float64 // mathematical operators/symbols
	pathPunct  float64 // url/path delimiters (/ : ? & = # %)
	atSign     float64 // '@' (tends to split words)
	emoji      float64 // each emoji / pictograph rune
	newline    float64 // newline or tab
	space      float64 // ordinary space
	floor      int     // additive baseline once any content is present
}

// vendorClass groups protocol families that share tokenizer behaviour.
type vendorClass int

const (
	vendorOpenAI vendorClass = iota
	vendorAnthropic
	vendorGemini
)

// weightsFor returns the weight table for a vendor class. The numbers below are
// hand-tuned approximations chosen so that (a) longer text always estimates
// higher, and (b) CJK-heavy text estimates differently from latin-word text on
// every vendor — the two properties the routing gate relies on. They are not
// copied from any reference table; they are independent round-number choices.
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
	default: // vendorOpenAI and any unknown family fall back here
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
