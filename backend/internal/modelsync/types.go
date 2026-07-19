package modelsync

import "time"

// Vendor 是 HUAKAI 支持自动拉取模型目录的上游枚举。
type Vendor string

const (
	VendorAnthropic Vendor = "anthropic"
	VendorOpenAI    Vendor = "openai"
	VendorGemini    Vendor = "gemini"
)

// Model 是 vendor model-list API 归一化后的最小目录项。
// 只保留 registry 需要的公开元数据，不包含凭据、价格和绑定。
type Model struct {
	ID             string
	DisplayName    string
	OwnedBy        string
	ProtocolFamily string
	ContextWindow  int
	CreatedAt      time.Time
	Capabilities   []string
}

// Catalog 是一次 vendor 拉取的完整快照。
type Catalog struct {
	Vendor Vendor
	Models []Model
}

// ApplyOptions 描述一次写入的操作者和原因，用于 snapshot/audit 追踪。
type ApplyOptions struct {
	Reason string
	Actor  string
}

// ApplyResult 是 registry 应用一个 vendor 快照后的结果。
type ApplyResult struct {
	Vendor           Vendor   `json:"vendor"`
	Added            int      `json:"added"`
	Updated          int      `json:"updated"`
	Reactivated      int      `json:"reactivated"`
	Disabled         int      `json:"disabled"`
	Discovered       int      `json:"discovered"`
	DiscoveryUpdated int      `json:"discovery_updated"`
	DiscoveryAbsent  int      `json:"discovery_absent"`
	Unchanged        int      `json:"unchanged"`
	SnapshotBumps    int      `json:"snapshot_bumps"`
	Detected         []string `json:"detected,omitempty"`
	Ignored          []string `json:"ignored,omitempty"`
	Removed          []string `json:"removed,omitempty"`
}

// SyncResult 汇总一次手动或定时同步。
type SyncResult struct {
	StartedAt             time.Time     `json:"started_at"`
	CompletedAt           time.Time     `json:"completed_at"`
	Results               []ApplyResult `json:"results"`
	TotalAdded            int           `json:"total_added"`
	TotalUpdated          int           `json:"total_updated"`
	TotalDisabled         int           `json:"total_disabled"`
	TotalDiscovered       int           `json:"total_discovered"`
	TotalDiscoveryUpdated int           `json:"total_discovery_updated"`
	TotalDiscoveryAbsent  int           `json:"total_discovery_absent"`
}
