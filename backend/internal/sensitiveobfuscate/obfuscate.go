// Package sensitiveobfuscate provides keyword-cloaking for outbound request text.
// It inserts a zero-width space (U+200B) after the first rune of each matched
// sensitive word inside Anthropic Messages request bodies (system + message
// content), making the text pass through upstream keyword classifiers while
// remaining visually identical to humans.
//
// Design rules:
//   - Empty word list -> Obfuscate is identity; input bytes returned unchanged.
//   - Parse error -> input bytes returned unchanged (fail-safe).
//   - Only re-serializes when at least one substitution was made.
//   - Longest-match wins when words overlap (e.g. ["ban","banned"] -> "banned"
//     is matched as one unit, not double-replaced).
//   - Case-insensitive matching; substitution preserves original casing.
package sensitiveobfuscate

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const zwsp = "\u200b"

// Matcher holds a compiled sensitive-word pattern. A zero-value Matcher (or
// one built from an empty word list) is a safe no-op.
type Matcher struct {
	re *regexp.Regexp // nil when no words configured
}

// BuildSensitiveWordMatcher compiles words into a longest-first, case-insensitive
// alternation regex. Blank/whitespace-only words are silently skipped. If no
// valid words remain, the returned Matcher is a no-op (Obfuscate returns input
// unchanged).
func BuildSensitiveWordMatcher(words []string) Matcher {
	var valid []string
	for _, w := range words {
		if t := strings.TrimSpace(w); t != "" {
			valid = append(valid, t)
		}
	}
	if len(valid) == 0 {
		return Matcher{}
	}
	// Sort longest first so longer words take priority over shorter prefixes.
	sort.Slice(valid, func(i, j int) bool {
		return len(valid[i]) > len(valid[j])
	})
	// Build alternation; each word is quoted so no regex meta-chars leak.
	var sb strings.Builder
	sb.WriteString("(?i)(")
	for i, w := range valid {
		if i > 0 {
			sb.WriteByte('|')
		}
		sb.WriteString(regexp.QuoteMeta(w))
	}
	sb.WriteByte(')')
	re := regexp.MustCompile(sb.String())
	return Matcher{re: re}
}

// ObfuscateSensitiveWords walks the Anthropic Messages JSON body and inserts
// U+200B after the first rune of every matched sensitive word in all text
// fields (system string/blocks and messages[].content string/blocks with
// type=="text"). Returns the original body unchanged on empty matcher, no
// match, or parse failure.
func ObfuscateSensitiveWords(body []byte, m Matcher) []byte {
	if m.re == nil {
		return body
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return body
	}
	changed := false

	// --- system field ---
	if sysRaw, ok := root["system"]; ok {
		if newRaw, c := obfuscateFieldRaw(sysRaw, m); c {
			root["system"] = newRaw
			changed = true
		}
	}

	// --- messages field ---
	if msgsRaw, ok := root["messages"]; ok {
		var msgs []json.RawMessage
		if json.Unmarshal(msgsRaw, &msgs) == nil {
			msgChanged := false
			for i, msgRaw := range msgs {
				var msg map[string]json.RawMessage
				if json.Unmarshal(msgRaw, &msg) != nil {
					continue
				}
				contentRaw, ok := msg["content"]
				if !ok {
					continue
				}
				if newRaw, c := obfuscateFieldRaw(contentRaw, m); c {
					msg["content"] = newRaw
					remsg, err := json.Marshal(msg)
					if err != nil {
						continue
					}
					msgs[i] = remsg
					msgChanged = true
				}
			}
			if msgChanged {
				reMsgs, err := json.Marshal(msgs)
				if err == nil {
					root["messages"] = reMsgs
					changed = true
				}
			}
		}
	}

	if !changed {
		return body
	}
	out, err := json.Marshal(root)
	if err != nil {
		return body
	}
	return out
}

// obfuscateFieldRaw handles a raw JSON value that may be:
//   - a JSON string  -> obfuscate directly
//   - a JSON array   -> walk blocks with type=="text"
//
// Returns (newRaw, true) when something changed, (original, false) otherwise.
func obfuscateFieldRaw(raw json.RawMessage, m Matcher) (json.RawMessage, bool) {
	// Try string first.
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if ns, c := obfuscateString(s, m); c {
			enc, err := json.Marshal(ns)
			if err != nil {
				return raw, false
			}
			return enc, true
		}
		return raw, false
	}
	// Try array of content blocks.
	var blocks []json.RawMessage
	if json.Unmarshal(raw, &blocks) == nil {
		blockChanged := false
		for i, blkRaw := range blocks {
			var blk map[string]json.RawMessage
			if json.Unmarshal(blkRaw, &blk) != nil {
				continue
			}
			// Only process type=="text" blocks.
			var btype string
			if json.Unmarshal(blk["type"], &btype) != nil || btype != "text" {
				continue
			}
			var text string
			if json.Unmarshal(blk["text"], &text) != nil {
				continue
			}
			if nt, c := obfuscateString(text, m); c {
				enc, err := json.Marshal(nt)
				if err != nil {
					continue
				}
				blk["text"] = enc
				reblk, err := json.Marshal(blk)
				if err != nil {
					continue
				}
				blocks[i] = reblk
				blockChanged = true
			}
		}
		if blockChanged {
			enc, err := json.Marshal(blocks)
			if err != nil {
				return raw, false
			}
			return enc, true
		}
		return raw, false
	}
	return raw, false
}

// obfuscateString inserts U+200B after the first rune of each matched word in s.
// Returns (modified, true) when at least one substitution was made.
func obfuscateString(s string, m Matcher) (string, bool) {
	if m.re == nil {
		return s, false
	}
	changed := false
	result := m.re.ReplaceAllStringFunc(s, func(match string) string {
		// Insert ZWSP after the first rune.
		_, size := utf8.DecodeRuneInString(match)
		changed = true
		return match[:size] + zwsp + match[size:]
	})
	return result, changed
}
