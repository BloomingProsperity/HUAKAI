package moderation

import (
	"regexp"
	"strings"
	"unicode"
)

const moderationExcerptRunes = 240

var (
	pemPrivateKeyPattern  = regexp.MustCompile(`(?is)-----BEGIN [^-]*PRIVATE KEY-----.*?-----END [^-]*PRIVATE KEY-----`)
	jwtPattern            = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`)
	prefixedSecretPattern = regexp.MustCompile(`(?i)\b(?:hk_(?:live|test|admin)_|sk-ant-|sk-|toolu_|aiv_|gho_|ghp_|github_pat_|AIza)[A-Za-z0-9._-]{4,}\b`)
	bearerPattern         = regexp.MustCompile(`(?i)\bBearer\s+[^\s"',;]+`)
	labeledSecretPattern  = regexp.MustCompile(`(?i)(?:\b(authorization|access_token|refresh_token|id_token|token|password|secret|cookie|session|api_key|apikey)\b|(密码|令牌|密钥|凭据|会话))(\s*[:=：]\s*)("[^"]*"|'[^']*'|[^\s,;}，。]+)`)
	emailPattern          = regexp.MustCompile(`(?i)\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b`)
	phonePattern          = regexp.MustCompile(`(?:\+?\d[\d ()-]{8,}\d)`)
	opaqueTokenPattern    = regexp.MustCompile(`[A-Za-z0-9_-]{32,}`)
)

func redactModerationExcerpt(value string) string {
	value = pemPrivateKeyPattern.ReplaceAllString(value, "[已隐藏私钥]")
	value = jwtPattern.ReplaceAllString(value, "[已隐藏令牌]")
	value = prefixedSecretPattern.ReplaceAllString(value, "[已隐藏凭据]")
	value = bearerPattern.ReplaceAllString(value, "Bearer [已隐藏凭据]")
	value = labeledSecretPattern.ReplaceAllString(value, "$1$2$3[已隐藏凭据]")
	value = emailPattern.ReplaceAllString(value, "[已隐藏邮箱]")
	value = phonePattern.ReplaceAllStringFunc(value, func(candidate string) string {
		if decimalRuneCount(candidate) < 10 {
			return candidate
		}
		return "[已隐藏电话]"
	})
	value = opaqueTokenPattern.ReplaceAllStringFunc(value, func(candidate string) string {
		if looksOpaqueToken(candidate) {
			return "[已隐藏长令牌]"
		}
		return candidate
	})
	// 所有替换都在截断前完成，避免截断恰好切开一个秘密形态后漏检。
	return truncateRunes(strings.TrimSpace(value), moderationExcerptRunes)
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func decimalRuneCount(value string) int {
	count := 0
	for _, r := range value {
		if unicode.IsDigit(r) {
			count++
		}
	}
	return count
}

func looksOpaqueToken(value string) bool {
	hasLower := false
	hasUpper := false
	hasDigit := false
	hasSymbol := false
	for _, r := range value {
		switch {
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsDigit(r):
			hasDigit = true
		default:
			hasSymbol = true
		}
	}
	classes := 0
	for _, present := range []bool{hasLower, hasUpper, hasDigit, hasSymbol} {
		if present {
			classes++
		}
	}
	return classes >= 2 || len([]rune(value)) >= 48
}
