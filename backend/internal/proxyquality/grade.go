// 包 proxyquality 把一次线路探测折成可过期的质量档。档位只解释连通与延迟，
// 不含出口地区；过期后不得把存档档位当成仍优质。
package proxyquality

import "time"

const (
	Freshness = 15 * time.Minute

	GradeExcellent = "excellent"
	GradeGood      = "good"
	GradeDegraded  = "degraded"
	GradePoor      = "poor"
	GradeUnknown   = "unknown"

	SourceManual   = "manual"
	SourcePeriodic = "periodic"
)

// Grade 按本次是否打通与往返毫秒写入存档档。失败一律差档。
func Grade(ok bool, latencyMS int64) string {
	if !ok {
		return GradePoor
	}
	switch {
	case latencyMS < 400:
		return GradeExcellent
	case latencyMS < 1200:
		return GradeGood
	case latencyMS < 3000:
		return GradeDegraded
	default:
		return GradePoor
	}
}

// Fresh 判断存档探测是否仍在新鲜窗内。
func Fresh(probedAt, now time.Time) bool {
	if probedAt.IsZero() {
		return false
	}
	return !now.Before(probedAt) && now.Sub(probedAt) <= Freshness
}

// Effective 是运营展示用档：过期或从未探测都是未知，不得沿用过期优质。
func Effective(stored string, probedAt, now time.Time) string {
	if !Fresh(probedAt, now) {
		return GradeUnknown
	}
	if stored == "" {
		return GradeUnknown
	}
	return stored
}
