// Package sensitiveobfuscate 为出站请求文本提供关键词隐匿能力。
// 它在 Anthropic Messages 请求体（system + message content）中每个匹配到的
// 敏感词的首个 rune 之后插入一个零宽空格（U+200B），使文本能够绕过上游的
// 关键词分类器，同时对人类肉眼而言保持完全一致。
//
// 设计规则：
//   - 空词表 -> Obfuscate 为恒等操作；原样返回输入字节。
//   - 解析出错 -> 原样返回输入字节（fail-safe）。
//   - 仅在至少做了一次替换时才重新序列化。
//   - 词条重叠时最长匹配优先（例如 ["ban","banned"] -> "banned"
//     会作为一个整体匹配，而不会被重复替换）。
//   - 大小写不敏感匹配；替换保留原始大小写。
package sensitiveobfuscate

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const zwsp = "\u200b"

// Matcher 持有一个已编译的敏感词模式。零值 Matcher（或由空词表
// 构建出的 Matcher）是安全的空操作。
type Matcher struct {
	re *regexp.Regexp // 未配置任何词条时为 nil
}

// BuildSensitiveWordMatcher 将词条编译为一个最长优先、大小写不敏感的
// 多选分支正则。空白/纯空白词条会被静默跳过。若没有任何有效词条剩下，
// 返回的 Matcher 即为空操作（Obfuscate 原样返回输入）。
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
	// 最长优先排序，使更长的词条优先于更短的前缀。
	sort.Slice(valid, func(i, j int) bool {
		return len(valid[i]) > len(valid[j])
	})
	// 构建多选分支；每个词条都被转义，使正则元字符不会泄漏出去。
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

// ObfuscateSensitiveWords 遍历 Anthropic Messages JSON 请求体，在所有文本
// 字段（system 字符串/blocks，以及 messages[].content 字符串/type=="text"
// 的 blocks）中每个匹配到的敏感词首个 rune 之后插入 U+200B。当 matcher 为空、
// 无匹配或解析失败时，原样返回请求体。
func ObfuscateSensitiveWords(body []byte, m Matcher) []byte {
	if m.re == nil {
		return body
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return body
	}
	changed := false

	// --- system 字段 ---
	if sysRaw, ok := root["system"]; ok {
		if newRaw, c := obfuscateFieldRaw(sysRaw, m); c {
			root["system"] = newRaw
			changed = true
		}
	}

	// --- messages 字段 ---
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

// obfuscateFieldRaw 处理一个可能为以下形态的原始 JSON 值：
//   - JSON 字符串  -> 直接做隐匿处理
//   - JSON 数组    -> 遍历 type=="text" 的 blocks
//
// 有变更时返回 (newRaw, true)，否则返回 (original, false)。
func obfuscateFieldRaw(raw json.RawMessage, m Matcher) (json.RawMessage, bool) {
	// 先尝试当作字符串。
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
	// 再尝试当作 content blocks 数组。
	var blocks []json.RawMessage
	if json.Unmarshal(raw, &blocks) == nil {
		blockChanged := false
		for i, blkRaw := range blocks {
			var blk map[string]json.RawMessage
			if json.Unmarshal(blkRaw, &blk) != nil {
				continue
			}
			// 仅处理 type=="text" 的 block。
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

// obfuscateString 在 s 中每个匹配到的词条首个 rune 之后插入 U+200B。
// 当至少做了一次替换时返回 (modified, true)。
func obfuscateString(s string, m Matcher) (string, bool) {
	if m.re == nil {
		return s, false
	}
	changed := false
	result := m.re.ReplaceAllStringFunc(s, func(match string) string {
		// 在首个 rune 之后插入 ZWSP。
		_, size := utf8.DecodeRuneInString(match)
		changed = true
		return match[:size] + zwsp + match[size:]
	})
	return result, changed
}
