package privacy

import "strings"

// CredentialPlaceholder 是自由文本里凭证 token 被替换后的占位符。
const CredentialPlaceholder = "[已脱敏]"

// RedactCredentialTokens 把自由文本中形似凭证的 token 换成占位符，其余文字原样保留。
//
// 与结构化脱敏(SanitizePayload)的分工：结构化侧按字段名/字段值判定并整体替换，
// 适用于已知 shape 的 payload；本函数面向没有 shape 的人类文本(例如需要留给运营
// 判读的内容片段)，只能逐 token 判定，命中才替换，不整体丢弃——否则片段会失去
// 全部可读性，达不到「让人看懂这段在说什么」的目的。
//
// 判定复用 keyLooksLikeCredential 的同一套前缀真相源(含 apikeyns 自签发前缀与
// 大小写敏感的 JWT/Google key 形态)，避免凭证形态表在两处漂移。
//
// 已知边界：不带任何已知前缀的不透明 token(例如某些自定义 bearer 值)无法凭形态
// 识别，本函数不会命中。调用方不能把本函数当成「文本一定不含凭证」的证明，只能
// 当作降低明文残留面的一道处理。
func RedactCredentialTokens(text string) string {
	if text == "" {
		return ""
	}
	var out strings.Builder
	out.Grow(len(text))
	start := -1
	for i, r := range text {
		if isCredentialTokenRune(r) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			out.WriteString(redactOneToken(text[start:i]))
			start = -1
		}
		out.WriteRune(r)
	}
	if start >= 0 {
		out.WriteString(redactOneToken(text[start:]))
	}
	return out.String()
}

// redactOneToken 对单个 token 做判定；非凭证原样返回。
func redactOneToken(token string) string {
	if keyLooksLikeCredential(token) {
		return CredentialPlaceholder
	}
	return token
}

// isCredentialTokenRune 界定 token 边界。凭证串由字母数字与 - _ . / + = 组成
// (覆盖 base64url、JWT 的点分段与 padding)，其余字符视为分隔符。空白、中文、
// 标点因此都会切断 token，避免把整句话吞成一个 token 后被误判。
func isCredentialTokenRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r == '-', r == '_', r == '.', r == '/', r == '+', r == '=':
		return true
	default:
		return false
	}
}
