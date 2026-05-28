package quota

import (
	"encoding/json"
	"time"
)

type policySnapshotRecord struct {
	ID            int64  `json:"id"`
	ScopeKind     string `json:"scope_kind"`
	ScopeID       string `json:"scope_id"`
	Metric        string `json:"metric"`
	WindowKind    string `json:"window_kind"`
	WindowStart   string `json:"window_start,omitempty"`
	WindowEnd     string `json:"window_end,omitempty"`
	LimitValue    string `json:"limit_value"`
	Mode          string `json:"mode"`
	ManualComment string `json:"manual_comment,omitempty"`
}

func marshalPolicySnapshot(policies []Policy) []byte {
	records := make([]policySnapshotRecord, 0, len(policies))
	for _, policy := range policies {
		record := policySnapshotRecord{
			ID:         policy.ID,
			ScopeKind:  string(policy.Scope.Kind),
			ScopeID:    normalizeScopeID(policy.Scope.Kind, policy.Scope.ID),
			Metric:     string(policy.Metric),
			WindowKind: string(policy.Window.Kind),
			LimitValue: policy.LimitValue.String(),
			Mode:       string(policy.Mode),
		}
		if !policy.Window.Start.IsZero() {
			record.WindowStart = policy.Window.Start.UTC().Format(time.RFC3339Nano)
		}
		if !policy.Window.End.IsZero() {
			record.WindowEnd = policy.Window.End.UTC().Format(time.RFC3339Nano)
		}
		if policy.Mode == ModeManualFirst {
			record.ManualComment = "manual_first=限额已配但需运营手动激活才 enforce,暂不阻断"
		}
		records = append(records, record)
	}
	if records == nil {
		records = []policySnapshotRecord{}
	}
	data, err := json.Marshal(records)
	if err != nil {
		return []byte("[]")
	}
	return data
}
