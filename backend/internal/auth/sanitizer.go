package auth

import (
	"errors"
	"regexp"
	"strings"
)

const redactedToken = "[REDACTED]"

var (
	bearerTokenPattern = regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{8,}|toolu_[A-Za-z0-9_-]{8,}|aiv_[A-Za-z0-9_-]{8,}|gho_[A-Za-z0-9_-]{8,})\b`)
	jwtTokenPattern    = regexp.MustCompile(`\b[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)
	openAIOrgPattern   = regexp.MustCompile(`\borg_[A-Za-z0-9_-]{4,}\b`)
	anthropicPattern   = regexp.MustCompile(`\bant-[A-Za-z0-9_-]{8,}\b`)
	jsonSecretPattern  = regexp.MustCompile(`(?i)("(?:bearer|access_token|refresh_token|id_token|token|client_secret|client_assertion|password|secret|authorization)"\s*:\s*)(?:"(?:[^"\\]|\\.)*"|[^,\s}]+)`)
	authLabelPattern   = regexp.MustCompile(`(?i)\b(authorization\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^,;\r\n]+)`)
	labeledSecret      = regexp.MustCompile(`(?i)\b((?:bearer|access_token|refresh_token|id_token|token|client_secret|client_assertion|password|secret)\s*[:=]\s*)("[^"]*"|'[^']*'|[^&"'\s,;}]+)`)
)

type OAuthErrorSanitizer struct{}

func (OAuthErrorSanitizer) Sanitize(message string) string {
	msg := strings.TrimSpace(message)
	if msg == "" {
		return ""
	}
	msg = bearerTokenPattern.ReplaceAllString(msg, redactedToken)
	msg = jwtTokenPattern.ReplaceAllString(msg, redactedToken)
	msg = openAIOrgPattern.ReplaceAllString(msg, redactedToken)
	msg = anthropicPattern.ReplaceAllString(msg, redactedToken)
	msg = jsonSecretPattern.ReplaceAllString(msg, `${1}"`+redactedToken+`"`)
	msg = authLabelPattern.ReplaceAllString(msg, `${1}`+redactedToken)
	msg = labeledSecret.ReplaceAllString(msg, `${1}`+redactedToken)
	return msg
}

func (s OAuthErrorSanitizer) SanitizeError(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(s.Sanitize(err.Error()))
}

func SanitizeOAuthMessage(message string) string {
	return (OAuthErrorSanitizer{}).Sanitize(message)
}
