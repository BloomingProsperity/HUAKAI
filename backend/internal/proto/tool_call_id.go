package proto

import (
	"errors"
	"fmt"
	"strings"
)

var ErrToolCallIDTranslationFail = errors.New("proto: tool call ID translation failed")

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
	if !isHexID(hex) {
		return "", fmt.Errorf("%w: canonical id has malformed suffix", ErrToolCallIDTranslationFail)
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
	if !isHexID(hex) {
		return "", fmt.Errorf("%w: id has malformed suffix", ErrToolCallIDTranslationFail)
	}
	return hex, nil
}

func isHexID(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}
