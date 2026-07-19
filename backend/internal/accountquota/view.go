package accountquota

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

type ViewFact struct {
	MetricKey          string     `json:"metric_key"`
	ModelKey           string     `json:"model_key,omitempty"`
	State              State      `json:"state"`
	UsedValue          *float64   `json:"used_value,omitempty"`
	LimitValue         *float64   `json:"limit_value,omitempty"`
	RemainingValue     *float64   `json:"remaining_value,omitempty"`
	Unit               *string    `json:"unit,omitempty"`
	UtilizationPercent *float64   `json:"utilization_percent,omitempty"`
	RemainingPercent   *float64   `json:"remaining_percent,omitempty"`
	ResetsAt           *time.Time `json:"resets_at,omitempty"`
	ObservedAt         time.Time  `json:"observed_at"`
	ValidUntil         *time.Time `json:"valid_until,omitempty"`
	Source             Source     `json:"source"`
	ErrorClass         *string    `json:"error_class,omitempty"`
	Fresh              bool       `json:"fresh"`
}

// ParseView 把数据库 JSON 投影成稳定合同；过期只改变 fresh，不篡改原始额度状态。
func ParseView(raw []byte, now time.Time) ([]ViewFact, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return []ViewFact{}, nil
	}
	var facts []ViewFact
	if err := json.Unmarshal(raw, &facts); err != nil {
		return nil, fmt.Errorf("解析账号额度投影：%w", err)
	}
	for i := range facts {
		facts[i].ObservedAt = facts[i].ObservedAt.UTC()
		facts[i].Fresh = !facts[i].ObservedAt.After(now.UTC())
		if facts[i].ValidUntil != nil {
			until := facts[i].ValidUntil.UTC()
			facts[i].ValidUntil = &until
			facts[i].Fresh = facts[i].Fresh && until.After(now.UTC())
		}
		if facts[i].ResetsAt != nil {
			reset := facts[i].ResetsAt.UTC()
			facts[i].ResetsAt = &reset
		}
	}
	if facts == nil {
		facts = []ViewFact{}
	}
	return facts, nil
}
