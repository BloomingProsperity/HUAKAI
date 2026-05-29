package quota

import (
	"testing"
	"time"
)

func TestComputeWindow_Boundaries(t *testing.T) {
	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	cases := []struct {
		name          string
		kind          WindowKind
		windowSeconds int64
		at            time.Time
		wantStart     time.Time
		wantEnd       time.Time
		wantOK        bool
	}{
		{
			name:          "none has no rate window",
			kind:          WindowNone,
			windowSeconds: 0,
			at:            time.Date(2026, 5, 28, 10, 23, 45, 0, time.UTC),
			wantOK:        false,
		},
		{
			name:          "fixed floors to aligned duration",
			kind:          WindowFixed,
			windowSeconds: int64(time.Hour / time.Second),
			at:            time.Date(2026, 5, 28, 10, 23, 45, 0, time.UTC),
			wantStart:     time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC),
			wantEnd:       time.Date(2026, 5, 28, 11, 0, 0, 0, time.UTC),
			wantOK:        true,
		},
		{
			name:          "fixed exact boundary starts a new window",
			kind:          WindowFixed,
			windowSeconds: int64(15 * time.Minute / time.Second),
			at:            time.Date(2026, 5, 28, 10, 30, 0, 0, time.UTC),
			wantStart:     time.Date(2026, 5, 28, 10, 30, 0, 0, time.UTC),
			wantEnd:       time.Date(2026, 5, 28, 10, 45, 0, 0, time.UTC),
			wantOK:        true,
		},
		{
			name:          "calendar day uses UTC boundary",
			kind:          WindowCalendarDay,
			windowSeconds: 0,
			at:            time.Date(2026, 5, 29, 7, 59, 59, 0, shanghai),
			wantStart:     time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC),
			wantEnd:       time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC),
			wantOK:        true,
		},
		{
			name:          "calendar week uses UTC Monday boundary",
			kind:          WindowCalendarWeek,
			windowSeconds: 0,
			at:            time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC),
			wantStart:     time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC),
			wantEnd:       time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
			wantOK:        true,
		},
		{
			name:          "calendar week exact Monday boundary starts a new week",
			kind:          WindowCalendarWeek,
			windowSeconds: 0,
			at:            time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
			wantStart:     time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
			wantEnd:       time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC),
			wantOK:        true,
		},
		{
			// 2 月只有 28 天: 天真的 AddDate(0,0,30) 会给出 3/3, 此例钉死月长。
			name:          "calendar month February uses month-length boundary",
			kind:          WindowCalendarMonth,
			windowSeconds: 0,
			at:            time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC),
			wantStart:     time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
			wantEnd:       time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			wantOK:        true,
		},
		{
			name:          "calendar month exact first-of-month starts a new month",
			kind:          WindowCalendarMonth,
			windowSeconds: 0,
			at:            time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			wantStart:     time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			wantEnd:       time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
			wantOK:        true,
		},
		{
			// 12 月跨年: end 必须是次年 1/1, 钉死月进位的跨年处理。
			name:          "calendar month December crosses into next year",
			kind:          WindowCalendarMonth,
			windowSeconds: 0,
			at:            time.Date(2026, 12, 20, 8, 30, 0, 0, time.UTC),
			wantStart:     time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
			wantEnd:       time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
			wantOK:        true,
		},
		{
			// 本地 3/1 07:00 (+8) = UTC 2/28 23:00, 必须落在 2 月窗口而非 3 月, 钉死 UTC 锚定。
			name:          "calendar month anchors to UTC not local month",
			kind:          WindowCalendarMonth,
			windowSeconds: 0,
			at:            time.Date(2026, 3, 1, 7, 0, 0, 0, shanghai),
			wantStart:     time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
			wantEnd:       time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			wantOK:        true,
		},
		{
			name:          "manual uses one open administrative window",
			kind:          WindowManual,
			windowSeconds: 0,
			at:            time.Date(2026, 5, 28, 10, 23, 45, 0, time.UTC),
			wantStart:     manualWindowStart,
			wantEnd:       manualWindowEnd,
			wantOK:        true,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			start, end, ok := ComputeWindow(tt.kind, tt.windowSeconds, tt.at)
			if ok != tt.wantOK {
				t.Fatalf("ok=%v; want %v", ok, tt.wantOK)
			}
			if !ok {
				if !start.IsZero() || !end.IsZero() {
					t.Fatalf("window=%s/%s; want zero times when ok=false", start, end)
				}
				return
			}
			if !start.Equal(tt.wantStart) || !end.Equal(tt.wantEnd) {
				t.Fatalf("window=%s/%s; want %s/%s", start, end, tt.wantStart, tt.wantEnd)
			}
		})
	}
}

func TestComputeWindow_InvalidFixedDuration(t *testing.T) {
	start, end, ok := ComputeWindow(WindowFixed, 0, time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC))
	if ok {
		t.Fatalf("ok=true with invalid fixed duration; window=%s/%s", start, end)
	}
}
