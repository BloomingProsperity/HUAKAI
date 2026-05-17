package dlq

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

const redacted = "[REDACTED]"

var secretKeyPattern = regexp.MustCompile(`(?i)(access[_-]?token|refresh[_-]?token|id[_-]?token|cookie|credential|secret|password|prompt|completion|api[_-]?key|authorization|bearer\s+[a-z0-9._-]+)`)

func RedactString(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if secretKeyPattern.MatchString(value) || privacy.ContainsForbiddenRawData([]byte(value)) {
		return redacted
	}
	return value
}

func SanitizePayload(raw json.RawMessage) json.RawMessage {
	out, err := privacy.DefaultRedactor().SanitizePayload(context.Background(), raw)
	if err != nil && privacy.ContainsForbiddenRawData(out) {
		return json.RawMessage(privacy.BlockedPayload(privacy.ErrorClassPrivacyGuardHit))
	}
	if !json.Valid(out) {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(out)
}

func ContainsForbiddenRawData(raw []byte) bool {
	return privacy.ContainsForbiddenRawData(raw)
}
