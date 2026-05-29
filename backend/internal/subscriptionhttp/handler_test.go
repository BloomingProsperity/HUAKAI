// HUAKAI · iKun

package subscriptionhttp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/subscription"
)

func sampleSubscription() subscription.UserSubscription {
	ts := time.Date(2026, 5, 29, 1, 2, 3, 0, time.UTC)
	monthly := decimal.RequireFromString("10")
	return subscription.UserSubscription{
		ID: 1, TenantID: 5, UserID: 7, PlanID: 3, GrantedGroup: "premium",
		MonthlyCapUSD: &monthly, Status: subscription.StatusActive, Source: subscription.SourceAdmin,
		AssignedByAdminID: 99, PrevUserGroup: "vip-secret-prev",
		StartsAt: ts, ExpiresAt: ts.AddDate(0, 0, 30), CreatedAt: ts, UpdatedAt: ts,
	}
}

// 守数据泄露: 用户视图绝不暴露内部/管理字段 (user_id / prev_user_group / source / assigned_by_admin_id), 且全 snake_case。
// mutation: handler 改回直返 subscription.UserSubscription → PascalCase + 内部字段 + prev 组值 → 红。
func TestUserSubscriptionViewHidesInternalFields(t *testing.T) {
	raw, err := json.Marshal(toSubscriptionView(sampleSubscription()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(raw)
	for _, leaked := range []string{
		"user_id", "prev_user_group", "source", "assigned_by_admin_id", "tenant_id",
		"UserID", "PrevUserGroup", "AssignedByAdminID", "GrantedGroup",
	} {
		if strings.Contains(js, leaked) {
			t.Fatalf("user subscription view leaked field %q: %s", leaked, js)
		}
	}
	if strings.Contains(js, "vip-secret-prev") {
		t.Fatalf("user subscription view leaked prev_user_group value: %s", js)
	}
	// 公开字段在且 snake_case。
	if !strings.Contains(js, `"plan_id"`) || !strings.Contains(js, `"monthly_cap_usd"`) || !strings.Contains(js, `"status"`) {
		t.Fatalf("user subscription view missing public snake_case fields: %s", js)
	}
}

func TestAdminSubscriptionViewIncludesAdminFieldsSnakeCase(t *testing.T) {
	raw, err := json.Marshal(toAdminSubscriptionView(sampleSubscription()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(raw)
	for _, want := range []string{`"user_id"`, `"source"`, `"prev_user_group"`, `"assigned_by_admin_id"`} {
		if !strings.Contains(js, want) {
			t.Fatalf("admin subscription view missing field %s: %s", want, js)
		}
	}
	if strings.Contains(js, "UserID") || strings.Contains(js, "PrevUserGroup") {
		t.Fatalf("admin subscription view leaked PascalCase field: %s", js)
	}
}

// 守 cap 渲染: nil cap 不应出现 (omitempty), 设了的 cap 是 decimal 字符串。
func TestPlanViewCapRendering(t *testing.T) {
	daily := decimal.RequireFromString("5")
	plan := subscription.Plan{
		ID: 1, TenantID: 5, Name: "p", CurrencyCode: "USD", ValidityDays: 30,
		GrantedGroup: "premium", DailyCapUSD: &daily, // weekly/monthly nil
		ForSale: true, Enabled: true,
	}
	raw, _ := json.Marshal(toPlanView(plan))
	js := string(raw)
	if !strings.Contains(js, `"daily_cap_usd":"5"`) {
		t.Fatalf("daily cap should render as decimal string: %s", js)
	}
	// nil 的 weekly/monthly 不应出现 (omitempty 防止 null 混淆 0)。
	if strings.Contains(js, "weekly_cap_usd") || strings.Contains(js, "monthly_cap_usd") {
		t.Fatalf("nil caps must be omitted, not null: %s", js)
	}
}
