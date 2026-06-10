// 入站 anthropic-beta 请求头解析(DM-03)。客户端 beta token 在进入
// dispatch 链前统一规范化,后续 anthropic 族 adapter 直接消费。
package provider

import "strings"

// 入站 beta token 硬上限:防 header 轰炸/出站头膨胀。
const (
	maxInboundBetaTokens   = 16
	maxInboundBetaTokenLen = 64
)

// ParseInboundBetaTokens 把客户端 anthropic-beta 请求头值(可多个 header,
// 每个可逗号分隔)解析成规范 token 列表:trim+小写+语法校验+去重+上限。
// 语法仅放行 [a-z0-9._-] 且首字符必须字母数字——天然排除 CR/LF/空格等
// header 注入载荷。无合法 token 返回 nil。
func ParseInboundBetaTokens(headerValues []string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, hv := range headerValues {
		for _, raw := range strings.Split(hv, ",") {
			tok := strings.ToLower(strings.TrimSpace(raw))
			if tok == "" || len(tok) > maxInboundBetaTokenLen || !validBetaToken(tok) {
				continue
			}
			if _, dup := seen[tok]; dup {
				continue
			}
			seen[tok] = struct{}{}
			out = append(out, tok)
			if len(out) >= maxInboundBetaTokens {
				return out
			}
		}
	}
	return out
}

func validBetaToken(tok string) bool {
	for i := 0; i < len(tok); i++ {
		c := tok[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case i > 0 && (c == '-' || c == '_' || c == '.'):
		default:
			return false
		}
	}
	return true
}
