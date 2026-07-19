// Package accountsubscription 负责账号列表的套餐筛选与只读投影。
package accountsubscription

import (
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionprofile"
)

// View 是管理端账号返回的套餐当前投影。
type View struct {
	Vendor          string     `json:"vendor"`
	Plan            string     `json:"plan"`
	Label           string     `json:"label"`
	RawPlan         *string    `json:"raw_plan,omitempty"`
	Scope           string     `json:"scope"`
	SubjectRef      *string    `json:"subject_ref,omitempty"`
	WorkspaceRef    *string    `json:"workspace_ref,omitempty"`
	Source          string     `json:"source"`
	Trust           string     `json:"trust"`
	Verification    string     `json:"verification"`
	Status          string     `json:"status"`
	MappingVersion  int32      `json:"mapping_version"`
	ErrorClass      *string    `json:"error_class,omitempty"`
	FirstObservedAt *time.Time `json:"first_observed_at"`
	ObservedAt      *time.Time `json:"observed_at"`
	ChangedAt       *time.Time `json:"changed_at"`
}

// Filters 是已规范化且通过白名单校验的套餐查询条件。
type Filters struct {
	Vendor string
	Plan   string
	Scope  string
	Status string
	Source string
}

// ParseError 保留稳定的 HTTP 错误码与运维消息。
type ParseError struct {
	Code    string
	Message string
}

// Parse 解析套餐维度和 vendor:plan 系统标签筛选。
func Parse(query url.Values) (Filters, *ParseError) {
	filters := Filters{
		Vendor: normalizeToken(query.Get("subscription_vendor")),
		Plan:   normalizeToken(query.Get("subscription_plan")),
		Scope:  normalizeToken(query.Get("subscription_scope")),
		Status: normalizeToken(query.Get("subscription_status")),
		Source: normalizeToken(query.Get("subscription_source")),
	}
	if label := strings.TrimSpace(query.Get("system_label")); label != "" {
		parts := strings.Split(label, ":")
		if len(parts) != 2 {
			return Filters{}, &ParseError{Code: "invalid_system_label", Message: "system_label must use vendor:plan"}
		}
		vendor, plan := normalizeToken(parts[0]), normalizeToken(parts[1])
		if vendor == "" || plan == "" ||
			(filters.Vendor != "" && filters.Vendor != vendor) ||
			(filters.Plan != "" && filters.Plan != plan) {
			return Filters{}, &ParseError{Code: "invalid_system_label", Message: "system_label conflicts with subscription filters"}
		}
		filters.Vendor, filters.Plan = vendor, plan
	}
	if !validToken(filters.Vendor) || !validToken(filters.Plan) {
		return Filters{}, &ParseError{Code: "invalid_subscription_filter", Message: "subscription vendor or plan filter is invalid"}
	}
	if !oneOf(filters.Scope, "", subscriptionprofile.ScopeUnknown, subscriptionprofile.ScopePersonal, subscriptionprofile.ScopeWorkspace) {
		return Filters{}, &ParseError{Code: "invalid_subscription_scope", Message: "subscription_scope is invalid"}
	}
	if !oneOf(filters.Status, "", subscriptionprofile.StatusObserved, subscriptionprofile.StatusUnknownValue,
		subscriptionprofile.StatusMissing, subscriptionprofile.StatusStale, subscriptionprofile.StatusParseFailed,
		subscriptionprofile.StatusConflict) {
		return Filters{}, &ParseError{Code: "invalid_subscription_status", Message: "subscription_status is invalid"}
	}
	if !oneOf(filters.Source, "", subscriptionprofile.SourceProviderAPI, subscriptionprofile.SourceOAuthResponse,
		subscriptionprofile.SourceIDTokenClaim, subscriptionprofile.SourceAccessTokenClaim,
		subscriptionprofile.SourceImportPayload, subscriptionprofile.SourceOperator,
		subscriptionprofile.SourceCredentialRefresh) {
		return Filters{}, &ParseError{Code: "invalid_subscription_source", Message: "subscription_source is invalid"}
	}
	return filters, nil
}

// Build 把同一条账号 SQL 行转成套餐视图和派生系统标签。
func Build(row admindb.AdminProviderAccountRow) (*View, []string) {
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
	return &View{
		Vendor: *row.SubscriptionVendor, Plan: *row.SubscriptionPlan, Label: label,
		RawPlan: row.SubscriptionRawPlan, Scope: *row.SubscriptionScope,
		SubjectRef: row.SubscriptionSubjectRef, WorkspaceRef: row.SubscriptionWorkspaceRef,
		Source: *row.SubscriptionSource, Trust: *row.SubscriptionTrust,
		Verification: *row.SubscriptionVerification, Status: *row.SubscriptionStatus,
		MappingVersion: *row.SubscriptionMappingVersion, ErrorClass: row.SubscriptionErrorClass,
		FirstObservedAt: pgTimePtr(row.SubscriptionFirstObservedAt),
		ObservedAt:      pgTimePtr(row.SubscriptionObservedAt),
		ChangedAt:       pgTimePtr(row.SubscriptionChangedAt),
	}, labels
}

func normalizeToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer("-", "_", " ", "_", "/", "_").Replace(value)
	for strings.Contains(value, "__") {
		value = strings.ReplaceAll(value, "__", "_")
	}
	return strings.Trim(value, "_")
}

func validToken(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 64 {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func pgTimePtr(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	value := ts.Time.UTC()
	return &value
}
