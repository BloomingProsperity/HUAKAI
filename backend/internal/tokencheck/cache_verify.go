package tokencheck

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

const (
	defaultRatioTolerance = 0.02
	cacheVerifyCode       = "cache_verify_mismatch"
)

// CacheVerify 用 HopAttestation.Detail 里的 cache 证据校验 Usage cache 字段。
type CacheVerify struct {
	RatioTolerance float64
	TokenTolerance int
}

// Verify 校验 hop chain 中的 cache_hit_ratio / cache_read_tokens 是否与 Usage 一致。
func (v CacheVerify) Verify(usage proto.CanonicalUsage, hops []proto.HopAttestation) CacheVerifyResult {
	v = v.withDefaults()
	evidence := extractCacheEvidence(hops)
	result := CacheVerifyResult{
		EvidenceFound:      evidence.found,
		ExpectedReadTokens: usage.CacheReadInputTokens,
		ExpectedHitRatio:   expectedHitRatio(usage),
	}
	if !evidence.found {
		return result
	}

	if evidence.hasReadTokens {
		result.HasReportedReadToken = true
		result.ReportedReadTokens = evidence.readTokens
		if absInt(evidence.readTokens-usage.CacheReadInputTokens) > v.TokenTolerance {
			result.ProtocolLoss = append(result.ProtocolLoss, cacheWarning(
				"accounting.usage.cache_read_input_tokens",
				"hop detail cache_read_tokens 与 Usage.CacheReadInputTokens 不一致",
				map[string]string{
					"hop_cache_read_tokens":   strconv.Itoa(evidence.readTokens),
					"usage_cache_read_tokens": strconv.Itoa(usage.CacheReadInputTokens),
				},
			))
		}
	}

	if evidence.hasHitRatio {
		result.HasReportedHitRatio = true
		result.ReportedHitRatio = evidence.hitRatio
		if math.Abs(evidence.hitRatio-result.ExpectedHitRatio) > v.RatioTolerance {
			result.ProtocolLoss = append(result.ProtocolLoss, cacheWarning(
				"accounting.usage.cache_hit_ratio",
				"hop detail cache_hit_ratio 与 Usage 推导命中率不一致",
				map[string]string{
					"hop_cache_hit_ratio":   fmt.Sprintf("%.6f", evidence.hitRatio),
					"usage_cache_hit_ratio": fmt.Sprintf("%.6f", result.ExpectedHitRatio),
				},
			))
		}
	}

	return result
}

func (v CacheVerify) withDefaults() CacheVerify {
	if v.RatioTolerance <= 0 {
		v.RatioTolerance = defaultRatioTolerance
	}
	if v.TokenTolerance < 0 {
		v.TokenTolerance = 0
	}
	return v
}

type cacheEvidence struct {
	found         bool
	hasReadTokens bool
	readTokens    int
	hasHitRatio   bool
	hitRatio      float64
}

func extractCacheEvidence(hops []proto.HopAttestation) cacheEvidence {
	var out cacheEvidence
	for _, hop := range hops {
		next, ok := parseCacheDetail(hop.Detail)
		if !ok {
			continue
		}
		out.found = true
		if next.hasReadTokens {
			out.hasReadTokens = true
			out.readTokens = next.readTokens
		}
		if next.hasHitRatio {
			out.hasHitRatio = true
			out.hitRatio = next.hitRatio
		}
	}
	return out
}

func parseCacheDetail(raw json.RawMessage) (cacheEvidence, bool) {
	if len(raw) == 0 {
		return cacheEvidence{}, false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var detail map[string]any
	if err := dec.Decode(&detail); err != nil {
		return cacheEvidence{}, false
	}
	var out cacheEvidence
	if value, ok := firstValue(detail, "cache_read_tokens", "cache_read_input_tokens", "cacheReadTokens"); ok {
		if tokens, ok := numberAsInt(value); ok {
			out.hasReadTokens = true
			out.readTokens = tokens
		}
	}
	if value, ok := firstValue(detail, "cache_hit_ratio", "cacheHitRatio"); ok {
		if ratio, ok := numberAsFloat(value); ok {
			out.hasHitRatio = true
			out.hitRatio = normalizeRatio(ratio)
		}
	}
	return out, out.hasReadTokens || out.hasHitRatio
}

func expectedHitRatio(usage proto.CanonicalUsage) float64 {
	if usage.InputTokens > 0 {
		return float64(usage.CacheReadInputTokens) / float64(usage.InputTokens)
	}
	denom := usage.CacheReadInputTokens + usage.CacheCreationInputTokens
	if denom <= 0 {
		return 0
	}
	return float64(usage.CacheReadInputTokens) / float64(denom)
}

func firstValue(values map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value, true
		}
	}
	return nil, false
}

func numberAsInt(value any) (int, bool) {
	switch v := value.(type) {
	case json.Number:
		i, err := v.Int64()
		return int(i), err == nil
	case float64:
		return int(v), math.Trunc(v) == v
	default:
		return 0, false
	}
}

func numberAsFloat(value any) (float64, bool) {
	switch v := value.(type) {
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case float64:
		return v, true
	default:
		return 0, false
	}
}

func normalizeRatio(ratio float64) float64 {
	if ratio > 1 && ratio <= 100 {
		return ratio / 100
	}
	return ratio
}

func cacheWarning(field, reason string, details map[string]string) proto.ProtocolLossEntry {
	return proto.ProtocolLossEntry{
		Field:    field,
		Severity: proto.ProtocolLossWarning,
		Reason:   reason,
		Code:     cacheVerifyCode,
		Details:  details,
	}
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
