package thinkingnorm

import (
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// ReplayFamily 是出站思考回放族。只由账号真实 vendor（RequestMeta.Provider）判定，
// 不读客户端自报模型名，也不按请求体自称官方/兼容。
type ReplayFamily string

const (
	// FamilyOfficial：官方 Claude 宿主。重组路径可剥无签名历史思考；有签名/密文块必须留。
	FamilyOfficial ReplayFamily = "official"
	// FamilyCompat：国产或第三方兼容通道。历史思考必须回传，不得为迎合官方签名预剥。
	FamilyCompat ReplayFamily = "compat"
	// FamilyUnknown：vendor 空或无法判定。不猜成官方可剥。
	FamilyUnknown ReplayFamily = "unknown"
)

// ClassifyReplay 按出站账号 vendor 分族。bedrock/vertex 承载官方 Claude 消息面，算官方族。
// 空 vendor 为未知。其余非空 vendor（含国产兼容）为必须回传族。
func ClassifyReplay(provider string) ReplayFamily {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "":
		return FamilyUnknown
	case "anthropic", "claude", "bedrock", "vertex":
		return FamilyOfficial
	default:
		return FamilyCompat
	}
}

// DropUnsignedHistory 报告官方族重组是否应丢掉这条无签名历史思考。
// 顶层思考控制节点、密文/已签名块、未知/兼容族一律不丢。
func DropUnsignedHistory(provider string, n proto.CapabilityNode) bool {
	if ClassifyReplay(provider) != FamilyOfficial {
		return false
	}
	if n.Kind != proto.CapabilityThinking || n.Thinking == nil {
		return false
	}
	if isThinkingControl(n) {
		return false
	}
	return unsignedHistory(n.Thinking)
}

func isThinkingControl(n proto.CapabilityNode) bool {
	if n.Source == nil {
		return false
	}
	switch n.Source.RequestField {
	case "thinking", "reasoning_effort":
		return true
	default:
		return false
	}
}

func unsignedHistory(t *proto.ThinkingNode) bool {
	if t.Redaction == proto.RedactionRedacted {
		return false
	}
	if strings.TrimSpace(t.Signature) != "" {
		return false
	}
	for _, b := range t.Blocks {
		if b.Type == "redacted_thinking" || len(bytesTrim(b.Data)) > 0 {
			return false
		}
		if strings.TrimSpace(b.Signature) != "" {
			return false
		}
	}
	return true
}

func bytesTrim(raw []byte) []byte {
	return []byte(strings.TrimSpace(string(raw)))
}
