// Package quotawindowview 把上游配额窗口快照投影为稳定的管理端展示合同。
package quotawindowview

import (
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

const (
	StateActive      = "active"
	StateExpired     = "expired"
	StateUnavailable = "unavailable"
)

type Window struct {
	State              string     `json:"state"`
	StartsAt           *time.Time `json:"starts_at"`
	ResetsAt           *time.Time `json:"resets_at"`
	UtilizationPercent *float64   `json:"utilization_percent"`
	RemainingPercent   *float64   `json:"remaining_percent"`
}

type Matrix struct {
	ObservedAt *time.Time `json:"observed_at"`
	Source     *string    `json:"source"`
	Outcome    *string    `json:"outcome"`
	ErrorClass *string    `json:"error_class"`
	FiveHour   Window     `json:"five_hour"`
	SevenDay   Window     `json:"seven_day"`
}

type PostgresSnapshot struct {
	ObservedAt          pgtype.Timestamptz
	Source              *string
	Outcome             *string
	ErrorClass          *string
	FiveHourStart       pgtype.Timestamptz
	FiveHourEnd         pgtype.Timestamptz
	FiveHourUtilization pgtype.Numeric
	SevenDayStart       pgtype.Timestamptz
	SevenDayEnd         pgtype.Timestamptz
	SevenDayUtilization pgtype.Numeric
}

func FromPostgres(snapshot PostgresSnapshot, now time.Time) Matrix {
	return Matrix{
		ObservedAt: timestamp(snapshot.ObservedAt),
		Source:     stringPointer(snapshot.Source),
		Outcome:    stringPointer(snapshot.Outcome),
		ErrorClass: stringPointer(snapshot.ErrorClass),
		FiveHour:   project(snapshot.FiveHourStart, snapshot.FiveHourEnd, snapshot.FiveHourUtilization, now),
		SevenDay:   project(snapshot.SevenDayStart, snapshot.SevenDayEnd, snapshot.SevenDayUtilization, now),
	}
}

func project(start, end pgtype.Timestamptz, utilization pgtype.Numeric, now time.Time) Window {
	window := Window{
		State:    StateUnavailable,
		StartsAt: timestamp(start),
		ResetsAt: timestamp(end),
	}
	if !end.Valid {
		return window
	}
	if !end.Time.After(now.UTC()) {
		window.State = StateExpired
		return window
	}
	value, err := utilization.Float64Value()
	if err != nil || !value.Valid || math.IsNaN(value.Float64) || math.IsInf(value.Float64, 0) || value.Float64 < 0 || value.Float64 > 100 {
		return window
	}
	used := value.Float64
	remaining := 100 - used
	window.State = StateActive
	window.UtilizationPercent = &used
	window.RemainingPercent = &remaining
	return window
}

func timestamp(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	at := value.Time.UTC()
	return &at
}

func stringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
