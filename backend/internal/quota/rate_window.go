package quota

import "time"

var (
	manualWindowStart = time.Unix(0, 0).UTC()
	manualWindowEnd   = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
)

// ComputeWindow 只计算与 policy 无关的 UTC 窗口边界。
// WindowNone 返回 ok=false; service 会用 policy.ValidFrom 构造累计窗口。
// WindowManual 在 B2a 暂作单一开放窗口, admin 手动 reset 留到 B2b/admin。
func ComputeWindow(kind WindowKind, windowSeconds int64, at time.Time) (start time.Time, end time.Time, ok bool) {
	at = at.UTC()
	switch kind {
	case WindowNone:
		return time.Time{}, time.Time{}, false
	case WindowFixed:
		if windowSeconds <= 0 {
			return time.Time{}, time.Time{}, false
		}
		startUnix := floorUnix(at.Unix(), windowSeconds)
		start = time.Unix(startUnix, 0).UTC()
		return start, start.Add(time.Duration(windowSeconds) * time.Second), true
	case WindowCalendarDay:
		y, m, d := at.Date()
		start = time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
		return start, start.AddDate(0, 0, 1), true
	case WindowCalendarWeek:
		y, m, d := at.Date()
		dayStart := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
		daysSinceMonday := (int(at.Weekday()) + 6) % 7
		start = dayStart.AddDate(0, 0, -daysSinceMonday)
		return start, start.AddDate(0, 0, 7), true
	case WindowCalendarMonth:
		// 当月 1 号 00:00 UTC 到下月 1 号 00:00 UTC。用 AddDate 月进位,
		// 自动处理 28/29/30/31 天月长差异与跨年 (12 月 -> 次年 1 月)。
		y, m, _ := at.Date()
		start = time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
		return start, start.AddDate(0, 1, 0), true
	case WindowManual:
		return manualWindowStart, manualWindowEnd, true
	default:
		return time.Time{}, time.Time{}, false
	}
}

func floorUnix(sec, windowSeconds int64) int64 {
	remainder := sec % windowSeconds
	if remainder < 0 {
		remainder += windowSeconds
	}
	return sec - remainder
}
