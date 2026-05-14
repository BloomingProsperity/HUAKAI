package tokencheck

import (
	"math"
	"unicode"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// TokenEstimator 根据 HCSF CanonicalContentBlock 估算 token 数。
type TokenEstimator interface {
	Estimate(blocks []proto.CanonicalContentBlock) int
}

// HeuristicEstimator 用字符数启发式估算 token：英文约 4 字符 1 token，
// 中文约 1.5 字符 1 token。它只用于反虚报交叉检查，不用于精确计费。
type HeuristicEstimator struct{}

// Estimate 返回一组 content block 的本地估算 token 数。
func (HeuristicEstimator) Estimate(blocks []proto.CanonicalContentBlock) int {
	total := 0
	for _, block := range blocks {
		total += estimateBlock(block)
	}
	return total
}

// NoopEstimator 用于显式禁用 token 估算。
type NoopEstimator struct{}

// Estimate 对任何输入都返回 0，调用方可据此得到 unknown verdict。
func (NoopEstimator) Estimate(_ []proto.CanonicalContentBlock) int {
	return 0
}

func estimateBlock(block proto.CanonicalContentBlock) int {
	total := estimateText(block.Text)
	total += estimateText(block.ReasoningSummary)
	total += estimateText(block.Name)
	total += estimateBytes(block.Input)
	total += estimateBytes(block.ToolResult)
	total += estimateBytes(block.Image)
	if total > 0 && block.Type != "" {
		// 每个非空 block 计入极小结构开销，避免 tool/json block 被低估为纯正文。
		total++
	}
	return total
}

func estimateBytes(raw []byte) int {
	if len(raw) == 0 {
		return 0
	}
	return int(math.Ceil(float64(len(raw)) / 4.0))
}

func estimateText(text string) int {
	if text == "" {
		return 0
	}
	var ascii, cjk, other int
	for _, r := range text {
		if unicode.IsSpace(r) {
			continue
		}
		switch {
		case unicode.Is(unicode.Han, r):
			cjk++
		case r < unicode.MaxASCII:
			ascii++
		default:
			other++
		}
	}
	estimate := float64(ascii)/4.0 + float64(cjk)/1.5 + float64(other)/3.0
	if estimate <= 0 {
		return 0
	}
	return int(math.Ceil(estimate))
}
