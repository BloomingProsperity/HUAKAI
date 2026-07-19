package quotawindowview

import (
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestFromPostgresProjectsActiveWindowsAndRemainingPercent(t *testing.T) {
	now := time.Date(2026, 7, 19, 3, 0, 0, 0, time.UTC)
	start5h := timestampValue(now.Add(-time.Hour))
	end5h := timestampValue(now.Add(4 * time.Hour))
	start7d := timestampValue(now.Add(-24 * time.Hour))
	end7d := timestampValue(now.Add(6 * 24 * time.Hour))
	source := "usage_endpoint"
	outcome := "success"

	got := FromPostgres(PostgresSnapshot{
		ObservedAt: timestampValue(now.Add(-time.Minute)), Source: &source, Outcome: &outcome,
		FiveHourStart: start5h, FiveHourEnd: end5h, FiveHourUtilization: numericValue(t, 37.5),
		SevenDayStart: start7d, SevenDayEnd: end7d, SevenDayUtilization: numericValue(t, 62.25),
	}, now)
	if got.ObservedAt == nil || !got.ObservedAt.Equal(now.Add(-time.Minute)) || got.Source == nil || *got.Source != source ||
		got.Outcome == nil || *got.Outcome != outcome || got.ErrorClass != nil {
		t.Fatalf("观测元数据=%+v，期望可区分来源、时间与结果", got)
	}
	if got.FiveHour.State != StateActive || got.FiveHour.UtilizationPercent == nil || *got.FiveHour.UtilizationPercent != 37.5 ||
		got.FiveHour.RemainingPercent == nil || *got.FiveHour.RemainingPercent != 62.5 {
		t.Fatalf("5h 窗口=%+v，期望活动且剩余 62.5%%", got.FiveHour)
	}
	if got.SevenDay.State != StateActive || got.SevenDay.UtilizationPercent == nil || *got.SevenDay.UtilizationPercent != 62.25 ||
		got.SevenDay.RemainingPercent == nil || *got.SevenDay.RemainingPercent != 37.75 {
		t.Fatalf("7d 窗口=%+v，期望活动且剩余 37.75%%", got.SevenDay)
	}
}

func TestFromPostgresDoesNotInventRemainingForExpiredOrMissingSnapshots(t *testing.T) {
	now := time.Date(2026, 7, 19, 3, 0, 0, 0, time.UTC)
	got := FromPostgres(PostgresSnapshot{
		FiveHourStart: timestampValue(now.Add(-6 * time.Hour)),
		FiveHourEnd:   timestampValue(now.Add(-time.Hour)), FiveHourUtilization: numericValue(t, 100),
		SevenDayEnd: timestampValue(now.Add(24 * time.Hour)),
	}, now)
	if got.FiveHour.State != StateExpired || got.FiveHour.UtilizationPercent != nil || got.FiveHour.RemainingPercent != nil {
		t.Fatalf("过期窗口不得继续显示百分比：%+v", got.FiveHour)
	}
	if got.SevenDay.State != StateUnavailable || got.SevenDay.UtilizationPercent != nil || got.SevenDay.RemainingPercent != nil {
		t.Fatalf("缺失利用率不得伪造为 0%% 或 100%%：%+v", got.SevenDay)
	}
}

func timestampValue(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func numericValue(t *testing.T, value float64) pgtype.Numeric {
	t.Helper()
	var result pgtype.Numeric
	if err := result.Scan(strconv.FormatFloat(value, 'f', -1, 64)); err != nil {
		t.Fatalf("构造 numeric: %v", err)
	}
	return result
}
