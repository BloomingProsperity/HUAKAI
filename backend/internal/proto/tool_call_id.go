package proto

import (
	"errors"
	"fmt"
	"strings"
)

var ErrToolCallIDTranslationFail = errors.New("proto: tool call ID translation failed")
const maxToolCallIDSuffixLength = 256

func ToCanonicalCallID(upstreamID string, upstream UpstreamProtocol) (string, error) {
	hex, err := stripCallPrefix(upstreamID, upstream)
	if err != nil {
		return "", err
	}
	return "call_" + hex, nil
}

func FromCanonicalCallID(canonicalID string, upstream UpstreamProtocol) (string, error) {
	if !strings.HasPrefix(canonicalID, "call_") {
		return "", fmt.Errorf("%w: canonical id missing call_ prefix", ErrToolCallIDTranslationFail)
	}
	hex := strings.TrimPrefix(canonicalID, "call_")
	if !isValidCallIDSuffix(hex) {
		return "", fmt.Errorf("%w: canonical id has invalid suffix; expected 1-%d chars from [A-Za-z0-9_-]", ErrToolCallIDTranslationFail, maxToolCallIDSuffixLength)
	}
	switch upstream {
	case UpstreamProtocolAnthropic:
		return "toolu_" + hex, nil
	case UpstreamProtocolOpenAI:
		return canonicalID, nil
	case UpstreamProtocolGemini:
		return "func_" + hex, nil
	case UpstreamProtocolBedrock:
		return "tool_" + hex, nil
	case UpstreamProtocolAntigravity:
		return "call_" + hex, nil
	default:
		return "", fmt.Errorf("%w: unsupported upstream protocol %q", ErrToolCallIDTranslationFail, upstream)
	}
}

func stripCallPrefix(id string, upstream UpstreamProtocol) (string, error) {
	prefix := ""
	switch upstream {
	case UpstreamProtocolAnthropic:
		prefix = "toolu_"
	case UpstreamProtocolOpenAI, UpstreamProtocolAntigravity:
		prefix = "call_"
	case UpstreamProtocolGemini:
		prefix = "func_"
	case UpstreamProtocolBedrock:
		prefix = "tool_"
	default:
		return "", fmt.Errorf("%w: unsupported upstream protocol %q", ErrToolCallIDTranslationFail, upstream)
	}
	if !strings.HasPrefix(id, prefix) {
		return "", fmt.Errorf("%w: id missing %s prefix", ErrToolCallIDTranslationFail, prefix)
	}
	hex := strings.TrimPrefix(id, prefix)
	if !isValidCallIDSuffix(hex) {
		return "", fmt.Errorf("%w: id has invalid suffix; expected 1-%d chars from [A-Za-z0-9_-]", ErrToolCallIDTranslationFail, maxToolCallIDSuffixLength)
	}
	return hex, nil
}

func isValidCallIDSuffix(s string) bool {
	if s == "" || len(s) > 256 {
		return false
	}
	for _, c := range []byte(s) {
		if (c >= '0' && c <= '9') ||
			(c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			c == '_' || c == '-' {
			continue
		}
		return false
	}
	return true
}
