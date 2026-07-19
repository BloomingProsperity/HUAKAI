package accounthealthview

import (
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionprofile"
)

// SubscriptionAxis 是账号套餐当前投影的只读运维视图。它只呈现数据库投影，
// 不重新解析凭据，也不参与授权、计费或配额判断。
type SubscriptionAxis struct {
	Vendor          string  `json:"vendor"`
	Plan            string  `json:"plan"`
	Label           string  `json:"label"`
	RawPlan         *string `json:"raw_plan,omitempty"`
	Scope           string  `json:"scope"`
	Source          string  `json:"source"`
	Trust           string  `json:"trust"`
	Verification    string  `json:"verification"`
	Status          string  `json:"status"`
	MappingVersion  int32   `json:"mapping_version"`
	ErrorClass      *string `json:"error_class,omitempty"`
	FirstObservedAt *string `json:"first_observed_at"`
	ObservedAt      *string `json:"observed_at"`
	ChangedAt       *string `json:"changed_at"`
}

// BuildSubscription 从管理健康查询的同一行构建套餐视图和只读系统标签。
func BuildSubscription(row admindb.GetAdminProviderAccountHealthRow) (*SubscriptionAxis, []string) {
	if row.SubscriptionVendor == nil || row.SubscriptionPlan == nil || row.SubscriptionScope == nil ||
		row.SubscriptionSource == nil || row.SubscriptionTrust == nil || row.SubscriptionVerification == nil ||
		row.SubscriptionStatus == nil || row.SubscriptionMappingVersion == nil {
		return nil, []string{}
	}
	observation := subscriptionprofile.Observation{Vendor: *row.SubscriptionVendor, Plan: *row.SubscriptionPlan}
	label := observation.Label()
	labels := []string{}
	if label != "" {
		labels = append(labels, label)
	}
	return &SubscriptionAxis{
		Vendor: *row.SubscriptionVendor, Plan: *row.SubscriptionPlan, Label: label,
		RawPlan: row.SubscriptionRawPlan, Scope: *row.SubscriptionScope,
		Source: *row.SubscriptionSource, Trust: *row.SubscriptionTrust,
		Verification: *row.SubscriptionVerification, Status: *row.SubscriptionStatus,
		MappingVersion: *row.SubscriptionMappingVersion, ErrorClass: row.SubscriptionErrorClass,
		FirstObservedAt: formatPGTime(row.SubscriptionFirstObservedAt),
		ObservedAt:      formatPGTime(row.SubscriptionObservedAt),
		ChangedAt:       formatPGTime(row.SubscriptionChangedAt),
	}, labels
}
