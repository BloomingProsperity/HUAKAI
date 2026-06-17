package proto

import (
	"crypto/sha1"
	"errors"
	"fmt"
	"strings"
)

var ErrToolCallIDTranslationFail = errors.New("proto: tool call ID translation failed")
const maxToolCallIDSuffixLength = 256

// SynthesizeCanonicalCallID 把一个无法被 ToCanonicalCallID 正常翻译的上游 tool-call id
// 转换成一个保证非空、下游可用的 canonical id（call_<suffix>）。许多 OpenAI 兼容供应商
// （Mistral 的 9 字符 id、Qwen、GLM、Kimi 等）以及部分携带 id 的 Gemini functionCall 发出的
// id 不带本协议要求的 call_/func_ 前缀；此时绝不能把真实 id 丢成空串——空 CallID 会让下游
// 客户端硬报错或发出无法关联 tool_result 的 tool_use。优先保留：清洗到 canonical 后缀字符集
// [A-Za-z0-9_-] 后包成 call_<sanitized>；若清洗后为空（原 id 全是非法字符），回退到对原始字节的
// 确定性 SHA-1 哈希。镜像本仓 anthropic 适配器对缺失/畸形 id 的 fallback 行为，保持各
// provider 上游→canonical 路径一致。返回值始终满足 isValidCallIDSuffix，可被 FromCanonicalCallID 反向翻译。
func SynthesizeCanonicalCallID(rawID string) string {
	suffix := sanitizeCallIDSuffix(rawID)
	if suffix == "" {
		sum := sha1.Sum([]byte(rawID))
		suffix = fmt.Sprintf("%x", sum[:8])
	}
	if len(suffix) > maxToolCallIDSuffixLength {
		suffix = suffix[:maxToolCallIDSuffixLength]
	}
	return "call_" + suffix
}

// sanitizeCallIDSuffix 仅保留 canonical 后缀允许的字符 [A-Za-z0-9_-]，丢弃其余字符。
func sanitizeCallIDSuffix(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if (c >= '0' && c <= '9') ||
			(c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			c == '_' || c == '-' {
			b.WriteByte(c)
		}
	}
	return b.String()
}

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
