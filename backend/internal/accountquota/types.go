// Package accountquota 定义厂商额度事实的统一合同。
package accountquota

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

type State string

const (
	StateAvailable State = "available"
	StateExhausted State = "exhausted"
	StateUnknown   State = "unknown"
	StateError     State = "error"
)

type Source string

const (
	SourceUpstreamUsage        Source = "upstream_usage"
	SourceUpstreamBilling      Source = "upstream_billing"
	SourceUpstreamModelCatalog Source = "upstream_model_catalog"
	SourceResponseHeaders      Source = "response_headers"
	SourceCapabilityContract   Source = "capability_contract"
)

// Fact 是一个账号、一个额度维度、可选一个模型的规范化观测。
type Fact struct {
	MetricKey          string
	ModelKey           string
	State              State
	UsedValue          *float64
	LimitValue         *float64
	RemainingValue     *float64
	Unit               string
	UtilizationPercent *float64
	RemainingPercent   *float64
	ResetsAt           *time.Time
	ValidUntil         *time.Time
	ErrorClass         string
}

// Snapshot 是一次厂商只读接口返回的完整事实集合。
type Snapshot struct {
	TenantID          int64
	ProviderAccountID int64
	Vendor            string
	Source            Source
	ObservedAt        time.Time
	// Complete 表示本轮完整覆盖该来源的所有维度；部分成功时只合并已返回事实。
	Complete bool
	Facts    []Fact
}

func (s Snapshot) Validate() error {
	if s.TenantID <= 0 || s.ProviderAccountID <= 0 {
		return errors.New("额度快照账号身份无效")
	}
	if strings.TrimSpace(s.Vendor) == "" {
		return errors.New("额度快照 vendor 为空")
	}
	switch s.Source {
	case SourceUpstreamUsage, SourceUpstreamBilling, SourceUpstreamModelCatalog, SourceResponseHeaders, SourceCapabilityContract:
	default:
		return fmt.Errorf("额度快照来源无效：%q", s.Source)
	}
	if s.ObservedAt.IsZero() {
		return errors.New("额度快照观测时间为空")
	}
	if len(s.Facts) == 0 {
		return errors.New("额度快照没有事实；未知结果必须显式写为 unknown")
	}
	seen := make(map[string]struct{}, len(s.Facts))
	for i := range s.Facts {
		if err := s.Facts[i].validate(); err != nil {
			return fmt.Errorf("额度事实[%d]：%w", i, err)
		}
		key := strings.TrimSpace(s.Facts[i].MetricKey) + "\x00" + strings.TrimSpace(s.Facts[i].ModelKey)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("额度事实[%d]重复维度", i)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (f Fact) validate() error {
	if strings.TrimSpace(f.MetricKey) == "" {
		return errors.New("metric_key 为空")
	}
	switch f.State {
	case StateAvailable, StateExhausted, StateUnknown:
		if strings.TrimSpace(f.ErrorClass) != "" {
			return errors.New("非错误事实携带 error_class")
		}
	case StateError:
		if strings.TrimSpace(f.ErrorClass) == "" {
			return errors.New("错误事实缺少 error_class")
		}
	default:
		return fmt.Errorf("state 无效：%q", f.State)
	}
	if f.State == StateUnknown && (f.UsedValue != nil || f.LimitValue != nil || f.RemainingValue != nil || f.UtilizationPercent != nil || f.RemainingPercent != nil) {
		return errors.New("unknown 事实不得携带数值")
	}
	for name, value := range map[string]*float64{
		"used_value": f.UsedValue, "limit_value": f.LimitValue, "remaining_value": f.RemainingValue,
	} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0) {
			return fmt.Errorf("%s 无效", name)
		}
	}
	for name, value := range map[string]*float64{
		"utilization_percent": f.UtilizationPercent, "remaining_percent": f.RemainingPercent,
	} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 || *value > 100) {
			return fmt.Errorf("%s 无效", name)
		}
	}
	return nil
}
