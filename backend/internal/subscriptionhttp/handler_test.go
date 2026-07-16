// HUAKAI · iKun

package subscriptionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/subscription"
	"github.com/BloomingProsperity/HUAKAI/internal/voucher"
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

// ---- 5d: 订阅券创建端点 ----

type fakeSubscriptionService struct {
	plan              subscription.Plan
	getPlanErr        error
	listByGroupCalled bool
	listByGroupTenant int64
	listByGroup       string
	listByGroupLimit  int
	listByGroupResult []subscription.UserSubscription
}

func (f *fakeSubscriptionService) CreatePlan(context.Context, subscription.CreatePlanInput) (subscription.Plan, error) {
	return subscription.Plan{}, nil
}
func (f *fakeSubscriptionService) GetPlan(context.Context, int64, int64) (subscription.Plan, error) {
	return f.plan, f.getPlanErr
}
func (f *fakeSubscriptionService) ListPlans(context.Context, int64, bool) ([]subscription.Plan, error) {
	return nil, nil
}
func (f *fakeSubscriptionService) DisablePlan(context.Context, int64, int64) error { return nil }
func (f *fakeSubscriptionService) UpdatePlan(context.Context, subscription.UpdatePlanInput) (subscription.Plan, error) {
	return subscription.Plan{}, nil
}
func (f *fakeSubscriptionService) AssignSubscription(context.Context, subscription.AssignSubscriptionInput) (subscription.AssignResult, error) {
	return subscription.AssignResult{}, nil
}
func (f *fakeSubscriptionService) BulkAssign(context.Context, subscription.BulkAssignInput) (subscription.BulkAssignResult, error) {
	return subscription.BulkAssignResult{}, nil
}
func (f *fakeSubscriptionService) CancelSubscription(context.Context, int64, int64, int64, string, string) (subscription.UserSubscription, error) {
	return subscription.UserSubscription{}, nil
}
func (f *fakeSubscriptionService) ExtendSubscription(context.Context, subscription.ExtendSubscriptionInput) (subscription.UserSubscription, error) {
	return subscription.UserSubscription{}, nil
}
func (f *fakeSubscriptionService) ResetQuota(context.Context, subscription.ResetQuotaInput) (subscription.UserSubscription, error) {
	return subscription.UserSubscription{}, nil
}
func (f *fakeSubscriptionService) ChangePlan(context.Context, subscription.ChangePlanInput) (subscription.UserSubscription, error) {
	return subscription.UserSubscription{}, nil
}
func (f *fakeSubscriptionService) RevokeSubscription(context.Context, subscription.RevokeSubscriptionInput) (subscription.UserSubscription, error) {
	return subscription.UserSubscription{}, nil
}
func (f *fakeSubscriptionService) SetAutoRenew(context.Context, int64, int64, bool) (subscription.UserSubscription, error) {
	return subscription.UserSubscription{}, nil
}
func (f *fakeSubscriptionService) GetSubscription(context.Context, int64, int64) (subscription.UserSubscription, error) {
	return subscription.UserSubscription{}, nil
}
func (f *fakeSubscriptionService) ListUserSubscriptions(context.Context, int64, int64) ([]subscription.UserSubscription, error) {
	return nil, nil
}
func (f *fakeSubscriptionService) ListUserSubscriptionsByGroup(_ context.Context, tenantID int64, group string, limit int) ([]subscription.UserSubscription, error) {
	f.listByGroupCalled = true
	f.listByGroupTenant = tenantID
	f.listByGroup = group
	f.listByGroupLimit = limit
	return f.listByGroupResult, nil
}
func (f *fakeSubscriptionService) ListAuditEvents(context.Context, int64, int64) ([]subscription.AuditEvent, error) {
	return nil, nil
}

type fakeVoucherService struct {
	called    bool
	gotCreate voucher.CreateInput
	result    voucher.CreateResult
	err       error
}

func (f *fakeVoucherService) Create(_ context.Context, in voucher.CreateInput) (voucher.CreateResult, error) {
	f.called = true
	f.gotCreate = in
	return f.result, f.err
}

type fakeAdminAuth struct {
	ident admin.AdminIdentity
	err   error
}

func (a fakeAdminAuth) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	if a.err != nil {
		return admin.AdminIdentity{}, a.err
	}
	return a.ident, nil
}

func newSubAdminTestRouter(d AdminDeps) http.Handler {
	r := chi.NewRouter()
	r.Route("/subs", func(r chi.Router) { MountSubscriptionAdminRoutes(r, d) })
	return r
}

func subVoucherBody(planID int64) []byte {
	vf := time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC)
	b, _ := json.Marshal(createSubscriptionVoucherRequest{
		TenantID: 5, PlanID: planID, AmountCents: 1990,
		ValidFrom: vf, ValidUntil: vf.AddDate(0, 0, 30), MaxRedemptions: 1,
	})
	return b
}

// 守 grant_kind 强制: 端点必须以 grant_kind=subscription + 套餐指针 + admin 身份 调 voucher.Create。
// mutation: handler 漏设 GrantKind=subscription → 捕获到的 GrantKind 空 → 红 (会建出余额券, 兑换反向偷钱);
// 漏设 SubscriptionPlanID → plan 指针 nil → 红。
func TestCreateSubscriptionVoucherForcesSubscriptionGrantKind(t *testing.T) {
	vsvc := &fakeVoucherService{result: voucher.CreateResult{
		Voucher: voucher.Voucher{ID: 1, GrantKind: voucher.GrantKindSubscription}, Code: "ABC",
	}}
	d := AdminDeps{
		Auth:           fakeAdminAuth{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin, TokenID: 77}},
		Service:        &fakeSubscriptionService{plan: subscription.Plan{ID: 42}},
		VoucherService: vsvc,
	}
	router := newSubAdminTestRouter(d)

	req := httptest.NewRequest(http.MethodPost, "/subs/vouchers", bytes.NewReader(subVoucherBody(42)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if !vsvc.called {
		t.Fatal("voucher Create not called")
	}
	if vsvc.gotCreate.GrantKind != voucher.GrantKindSubscription {
		t.Fatalf("voucher grant_kind = %q, want subscription (endpoint must force it)", vsvc.gotCreate.GrantKind)
	}
	if vsvc.gotCreate.SubscriptionPlanID == nil || *vsvc.gotCreate.SubscriptionPlanID != 42 {
		t.Fatalf("voucher subscription_plan_id = %v, want 42", vsvc.gotCreate.SubscriptionPlanID)
	}
	if vsvc.gotCreate.AdminID != 77 {
		t.Fatalf("voucher admin_id = %d, want 77 (from admin identity, not client)", vsvc.gotCreate.AdminID)
	}
	// 券码须回给建券 admin (一次性领取)。
	if !strings.Contains(rec.Body.String(), `"code":"ABC"`) {
		t.Fatalf("response missing voucher code: %s", rec.Body.String())
	}
}

// 守不建孤券: 套餐不存在时回 404 且绝不调 voucher.Create (否则建出指向不存在套餐的孤券 / 撞 FK)。
// mutation: handler 跳过 GetPlan 预检 → vsvc.called 变 true / 状态非 404 → 红。
func TestCreateSubscriptionVoucherPlanNotFound(t *testing.T) {
	vsvc := &fakeVoucherService{}
	d := AdminDeps{
		Auth:           fakeAdminAuth{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin, TokenID: 77}},
		Service:        &fakeSubscriptionService{getPlanErr: subscription.ErrPlanNotFound},
		VoucherService: vsvc,
	}
	router := newSubAdminTestRouter(d)

	req := httptest.NewRequest(http.MethodPost, "/subs/vouchers", bytes.NewReader(subVoucherBody(999)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if vsvc.called {
		t.Fatal("voucher Create must NOT be called when plan not found (no orphan voucher)")
	}
}

// 守非法 plan_id 早拒: plan_id<=0 → 400, 不调 voucher.Create。
func TestCreateSubscriptionVoucherRejectsNonPositivePlan(t *testing.T) {
	vsvc := &fakeVoucherService{}
	d := AdminDeps{
		Auth:           fakeAdminAuth{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin, TokenID: 77}},
		Service:        &fakeSubscriptionService{},
		VoucherService: vsvc,
	}
	router := newSubAdminTestRouter(d)

	req := httptest.NewRequest(http.MethodPost, "/subs/vouchers", bytes.NewReader(subVoucherBody(0)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if vsvc.called {
		t.Fatal("voucher Create must not be called for invalid plan_id")
	}
}

// 守 nil 依赖安全: VoucherService 未注入时该端点回 503 (而非 panic), 不影响其余订阅 admin 端点。
func TestCreateSubscriptionVoucherNilVoucherServiceReturns503(t *testing.T) {
	d := AdminDeps{
		Auth:    fakeAdminAuth{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin, TokenID: 77}},
		Service: &fakeSubscriptionService{plan: subscription.Plan{ID: 42}},
		// VoucherService 故意 nil
	}
	router := newSubAdminTestRouter(d)

	req := httptest.NewRequest(http.MethodPost, "/subs/vouchers", bytes.NewReader(subVoucherBody(42)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
}

// 守 SUB-045: 后台 assignments 支持按 granted_group 只读筛选, 不再强制 user_id。
// MUTATION: handler 仍只走 user_id 路径 → group 查询 400 或未调用 ListUserSubscriptionsByGroup → 红。
func TestAdminListAssignmentsByGroupUsesGroupFilter(t *testing.T) {
	svc := &fakeSubscriptionService{listByGroupResult: []subscription.UserSubscription{
		{ID: 1, TenantID: 5, UserID: 7, GrantedGroup: "vip"},
		{ID: 2, TenantID: 5, UserID: 8, GrantedGroup: "vip"},
	}}
	d := AdminDeps{
		Auth:    fakeAdminAuth{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin, TokenID: 77}},
		Service: svc,
	}
	router := newSubAdminTestRouter(d)

	req := httptest.NewRequest(http.MethodGet, "/subs/assignments?tenant_id=5&group=vip&limit=10", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !svc.listByGroupCalled {
		t.Fatal("group query must call ListUserSubscriptionsByGroup")
	}
	if svc.listByGroupTenant != 5 || svc.listByGroup != "vip" || svc.listByGroupLimit != 10 {
		t.Fatalf("group call = tenant %d group %q limit %d, want 5/vip/10",
			svc.listByGroupTenant, svc.listByGroup, svc.listByGroupLimit)
	}
	var resp struct {
		Subscriptions []adminSubscriptionView `json:"subscriptions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Subscriptions) != 2 {
		t.Fatalf("subscriptions len=%d want 2; body=%s", len(resp.Subscriptions), rec.Body.String())
	}
}
