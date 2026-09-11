package servicetier

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/pricingeval"
)

const (
	snapRequested = "service_tier_requested"
	snapActual    = "service_tier_actual"
	snapBilled    = "service_tier_billed"
	snapMult      = "service_tier_mult"
	snapReason    = "service_tier_reason"
	snapPending   = "pending_reconciliation=service_tier"
)

// Apply 把处理档倍率施加到已算出的 token/缓存成本上，并写入永久快照标记。
// 工具按次附加费应在本函数之后叠加，避免把固定工具费误乘档位倍率。
func Apply(res pricingeval.Result, d Decision) pricingeval.Result {
	if d.Pending {
		res.PendingReconciliation = true
	}
	res.CostSnapshot = AppendSnapshot(res.CostSnapshot, d)
	mult := effectiveMultiplier(d.Multiplier)
	if mult.Equal(one) {
		return res
	}
	res.Total = res.Total.Mul(mult)
	res.CacheCreationCost = res.CacheCreationCost.Mul(mult)
	res.CacheReadCost = res.CacheReadCost.Mul(mult)
	return res
}

// AppendSnapshot 把请求档/实档/结算档/倍率写入资金快照。值只允许档名与数字。
func AppendSnapshot(snapshot string, d Decision) string {
	parts := []string{
		fmt.Sprintf("%s=%s", snapRequested, sanitizeMarker(d.Requested)),
		fmt.Sprintf("%s=%s", snapActual, sanitizeMarker(d.Actual)),
		fmt.Sprintf("%s=%s", snapBilled, sanitizeMarker(d.Billed)),
		fmt.Sprintf("%s=%s", snapMult, effectiveMultiplier(d.Multiplier).String()),
		fmt.Sprintf("%s=%s", snapReason, sanitizeMarker(d.Reason)),
	}
	if d.Pending {
		parts = append(parts, snapPending)
	}
	marker := strings.Join(parts, ";")
	snapshot = strings.TrimSpace(snapshot)
	if snapshot == "" {
		return marker
	}
	if strings.Contains(snapshot, snapBilled+"=") {
		return snapshot
	}
	return snapshot + ";" + marker
}

// ParseBilled 从永久快照恢复结算档。缺标记视为历史 1× 行，不发明档位。
func ParseBilled(snapshot string) (Decision, bool) {
	fields := parseMarkers(snapshot)
	if _, ok := fields[snapBilled]; !ok && fields[snapMult] == "" {
		return Decision{Billed: standard, Multiplier: one, Reason: "legacy_identity"}, false
	}
	mult := one
	if raw := fields[snapMult]; raw != "" {
		parsed, err := decimal.NewFromString(raw)
		if err == nil && parsed.IsPositive() {
			mult = parsed
		}
	}
	return Decision{
		Requested:  fields[snapRequested],
		Actual:     fields[snapActual],
		Billed:     fields[snapBilled],
		Multiplier: mult,
		Pending:    strings.Contains(snapshot, snapPending) || fields[snapReason] == "unpublished_actual",
		Reason:     fields[snapReason],
	}, true
}

func effectiveMultiplier(mult decimal.Decimal) decimal.Decimal {
	if mult.IsPositive() {
		return mult
	}
	return one
}

func sanitizeMarker(v string) string {
	v = normalizeLiteral(v)
	if v == "" {
		return ""
	}
	if i := strings.IndexAny(v, ";=& \t"); i >= 0 {
		v = v[:i]
	}
	var b strings.Builder
	for _, r := range v {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func parseMarkers(snapshot string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(snapshot, ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(key)] = sanitizeMarker(value)
	}
	return out
}
