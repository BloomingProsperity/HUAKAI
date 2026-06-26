package megroupshttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/pricingcatalog"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionenforce"
)

// --- 桩 --------------------------------------------------------------------

type authStub struct {
	identity auth.Identity
	err      error
}

func (s authStub) Resolve(context.Context, *http.Request) (auth.Identity, error) {
	if s.err != nil {
		return auth.Identity{}, s.err
	}
	return s.identity, nil
}

type userGroupStub struct {
	group string
	err   error
	calls []groupCall
}

type groupCall struct {
	tenantID int64
	userID   int64
}

func (s *userGroupStub) UserGroup(_ context.Context, tenantID, userID int64) (string, error) {
	s.calls = append(s.calls, groupCall{tenantID: tenantID, userID: userID})
	if s.err != nil {
		return "", s.err
	}
	return s.group, nil
}

// routesStub 只为它被预置的那个精确 (tenant, userGroup) 返回 allowed 的
// pool-group 集合;其它任何情况都产出空集合。
type routesStub struct {
	tenantID  int64
	userGroup string
	allowed   []int64
	err       error
	calls     []routesCall
}

type routesCall struct {
	tenantID  int64
	userGroup string
	model     string
}

func (s *routesStub) GroupRoutes(_ context.Context, tenantID int64, userGroup, model string) (subscriptionenforce.GroupRoutes, error) {
	s.calls = append(s.calls, routesCall{tenantID: tenantID, userGroup: userGroup, model: model})
	if s.err != nil {
		return subscriptionenforce.GroupRoutes{}, s.err
	}
	out := subscriptionenforce.GroupRoutes{Allowed: make(map[int64]struct{})}
	if tenantID == s.tenantID && userGroup == s.userGroup {
		out.Configured = true
		for _, id := range s.allowed {
			out.Allowed[id] = struct{}{}
		}
	}
	return out, nil
}

type ratioStub struct {
	rows map[int64][]pricingcatalog.GroupPricingRatio // 按 tenant 索引
	err  error
}

func (s *ratioStub) ListRatios(_ context.Context, tenantID int64) ([]pricingcatalog.GroupPricingRatio, error) {
	if s.err != nil {
		return nil, s.err
	}
	return append([]pricingcatalog.GroupPricingRatio(nil), s.rows[tenantID]...), nil
}

type poolNameStub struct {
	names map[int64]map[int64]string // tenant -> id -> 名称
	err   error
}

func (s *poolNameStub) PoolNames(_ context.Context, tenantID int64) (map[int64]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.names[tenantID], nil
}

func ratio(tenantID, poolGroupID int64, text string, public bool) pricingcatalog.GroupPricingRatio {
	return pricingcatalog.GroupPricingRatio{
		TenantID:    tenantID,
		PoolGroupID: poolGroupID,
		Ratio:       decimal.RequireFromString(text),
		RatioText:   text,
		PublicRatio: public,
	}
}

func invoke(h http.Handler) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/me/groups", nil))
	return rec
}

func decodeItems(t *testing.T, rec *httptest.ResponseRecorder) (string, []map[string]any) {
	t.Helper()
	var body struct {
		Object    string           `json:"object"`
		UserGroup string           `json:"user_group"`
		Items     []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v raw=%s", err, rec.Body.String())
	}
	return body.UserGroup, body.Items
}

func itemByID(items []map[string]any, id int64) map[string]any {
	for _, it := range items {
		if v, ok := it["pool_group_id"].(float64); ok && int64(v) == id {
			return it
		}
	}
	return nil
}

// --- 测试 ------------------------------------------------------------------

// TestPublicRatioGate 守护金钱/竞争情报泄漏:ratio 行为 public_ratio=false 的
// group 绝不能序列化其倍率。
//
// fixture 有区分度 —— group B 的隐藏 ratio "9.90000000" 既不同于被省略的字段,
// 也不同于默认的 1.0,因此泄漏会一目了然。
//
// 变异:去掉 handler.go 中的 `row.PublicRatio` 守卫(总是输出 ratio)。那样
// group B 会序列化出 "9.90000000",泄漏断言转红。
func TestPublicRatioGate(t *testing.T) {
	ident := auth.Identity{TenantID: 1, UserID: 42}
	d := Deps{
		Auth:       authStub{identity: ident},
		UserGroups: &userGroupStub{group: "default"},
		RoutesRepo: &routesStub{tenantID: 1, userGroup: "default", allowed: []int64{7, 12}},
		Ratios: &ratioStub{rows: map[int64][]pricingcatalog.GroupPricingRatio{
			1: {
				ratio(1, 12, "1.50000000", true), // A:公开
				ratio(1, 7, "9.90000000", false), // B:隐藏的内部倍率
			},
		}},
		Pools: &poolNameStub{names: map[int64]map[int64]string{
			1: {12: "premium-pool", 7: "standard-pool"},
		}},
	}

	rec := invoke(NewHandler(d))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	_, items := decodeItems(t, rec)

	a := itemByID(items, 12)
	if a == nil {
		t.Fatalf("public group 12 missing: %v", items)
	}
	if a["ratio"] != "1.50000000" {
		t.Fatalf("public group ratio=%v want 1.50000000", a["ratio"])
	}
	if a["has_public_ratio"] != true {
		t.Fatalf("public group has_public_ratio=%v want true", a["has_public_ratio"])
	}

	b := itemByID(items, 7)
	if b == nil {
		t.Fatalf("group 7 should still be listed: %v", items)
	}
	if _, present := b["ratio"]; present {
		t.Fatalf("non-public group 7 leaked ratio=%v (must be omitted)", b["ratio"])
	}
	if b["has_public_ratio"] != false {
		t.Fatalf("non-public group has_public_ratio=%v want false", b["has_public_ratio"])
	}
	// 纵深防御:隐藏的倍率绝不能出现在传输 body 的任何地方。
	if got := rec.Body.String(); contains(got, "9.90000000") {
		t.Fatalf("hidden internal multiplier leaked into response body: %s", got)
	}
}

// TestTenantUserScoping 守护 CMB-5 跨 tenant 读取。会话是 (tenant=1,user=42);
// routes 桩只为这个精确的对返回 group。tenant 2 有 group {99},但 handler 必须
// 从会话推导 tenant,因此 tenant 2 的 group 永远不会出现。
//
// 变异:让 handler 把一个来自请求或为零的 tenant id 传入 GroupRoutes/UserGroup。
// 那样 (tenant,userGroup) 匹配会失败(或匹配到 tenant 2),要么 items 在本应
// 有值处变空,要么 tenant 2 的 group 99 浮现 —— 记录的 store 调用 tenant 断言
// + 「没有 group 99」断言都会转红。
func TestTenantUserScoping(t *testing.T) {
	ident := auth.Identity{TenantID: 1, UserID: 42}
	ug := &userGroupStub{group: "default"}
	routes := &routesStub{tenantID: 1, userGroup: "default", allowed: []int64{7}}
	d := Deps{
		Auth:       authStub{identity: ident},
		UserGroups: ug,
		RoutesRepo: routes,
		Ratios: &ratioStub{rows: map[int64][]pricingcatalog.GroupPricingRatio{
			1: {ratio(1, 7, "1.00000000", true)},
			2: {ratio(2, 99, "5.00000000", true)},
		}},
		Pools: &poolNameStub{names: map[int64]map[int64]string{
			1: {7: "tenant1-pool"},
			2: {99: "tenant2-secret-pool"},
		}},
	}

	rec := invoke(NewHandler(d))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	_, items := decodeItems(t, rec)

	if itemByID(items, 99) != nil {
		t.Fatalf("cross-tenant group 99 leaked into response: %v", items)
	}
	if itemByID(items, 7) == nil {
		t.Fatalf("own-tenant group 7 missing: %v", items)
	}
	// 下游存储必须只用会话的 tenant/user 被查询。
	if len(ug.calls) != 1 || ug.calls[0].tenantID != 1 || ug.calls[0].userID != 42 {
		t.Fatalf("user_group lookup scope=%v want one call tenant:1 user:42", ug.calls)
	}
	if len(routes.calls) != 1 || routes.calls[0].tenantID != 1 {
		t.Fatalf("routes lookup tenant=%v want session tenant 1", routes.calls)
	}
	if got := rec.Body.String(); contains(got, "tenant2-secret-pool") {
		t.Fatalf("cross-tenant pool name leaked: %s", got)
	}
}

// TestTierFiltering 守护功能正确性:items 只反映该 tier 允许的 pool group,
// 而非每个已定价的 group。该 tenant 有一条 group 99(另一个 tier 的 group)的
// ratio 行,但调用方所在 tier 只允许 {7,12}。
//
// 变异:跳过 GroupRoutes 而改为列出每条 ratio/pool。那样 group 99 会出现,
// 「99 必须缺席」断言转红。99 是一个异 tier 的 group(而非空集副产物),
// 因此该 fixture 有区分度。
func TestTierFiltering(t *testing.T) {
	ident := auth.Identity{TenantID: 1, UserID: 42}
	d := Deps{
		Auth:       authStub{identity: ident},
		UserGroups: &userGroupStub{group: "default"},
		RoutesRepo: &routesStub{tenantID: 1, userGroup: "default", allowed: []int64{7, 12}},
		Ratios: &ratioStub{rows: map[int64][]pricingcatalog.GroupPricingRatio{
			1: {
				ratio(1, 7, "1.10000000", true),
				ratio(1, 12, "1.20000000", true),
				ratio(1, 99, "8.80000000", true), // 已定价但属于另一个 tier
			},
		}},
		Pools: &poolNameStub{names: map[int64]map[int64]string{
			1: {7: "p7", 12: "p12", 99: "p99-other-tier"},
		}},
	}

	rec := invoke(NewHandler(d))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	_, items := decodeItems(t, rec)
	if len(items) != 2 {
		t.Fatalf("items len=%d want 2 (groups 7,12 only); body=%s", len(items), rec.Body.String())
	}
	if itemByID(items, 99) != nil {
		t.Fatalf("foreign-tier group 99 leaked: %v", items)
	}
	// 排序:pool_group_id 升序。
	if got := int64(items[0]["pool_group_id"].(float64)); got != 7 {
		t.Fatalf("items[0]=%d want 7 (ascending order)", got)
	}
}

// TestUnauthenticated 守护鉴权门:无效/缺失会话产出 401,且永不触达下游存储。
//
// 变异:去掉 `ident.UserID <= 0` / 错误检查。那样 handler 会带着零身份继续
// 并返回 200,使此处转红。桩的调用计数断言进一步证明没有触达任何存储。
func TestUnauthenticated(t *testing.T) {
	ug := &userGroupStub{group: "default"}
	d := Deps{
		Auth:       authStub{err: auth.ErrUnauthorized},
		UserGroups: ug,
		RoutesRepo: &routesStub{},
		Ratios:     &ratioStub{},
		Pools:      &poolNameStub{},
	}

	rec := invoke(NewHandler(d))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401 body=%s", rec.Code, rec.Body.String())
	}
	if len(ug.calls) != 0 {
		t.Fatalf("unauthenticated request reached user-group store: %v", ug.calls)
	}
}

// TestAllowedGroupWithoutRatioRow 守护「未配置 = 不给误导性默认值」规则:
// 一个没有 ratio 行的 allowed group 必须报告 has_public_ratio=false 并完全省略
// ratio —— 既不崩溃,也不凭空造出 1.0。
//
// 变异:让 handler 把未定价 group 默认成 ratio "1.00000000" /
// has_public_ratio=true。那样该 group 会带上一个(误导性的)ratio,
// 「ratio 缺席 + has_public_ratio 为 false」断言转红。对缺失 map 条目的
// nil 解引用会返回 500,同样会失败。
func TestAllowedGroupWithoutRatioRow(t *testing.T) {
	ident := auth.Identity{TenantID: 1, UserID: 42}
	d := Deps{
		Auth:       authStub{identity: ident},
		UserGroups: &userGroupStub{group: "default"},
		RoutesRepo: &routesStub{tenantID: 1, userGroup: "default", allowed: []int64{7}},
		Ratios:     &ratioStub{rows: map[int64][]pricingcatalog.GroupPricingRatio{1: {}}}, // 无行
		Pools: &poolNameStub{names: map[int64]map[int64]string{
			1: {7: "unpriced-pool"},
		}},
	}

	rec := invoke(NewHandler(d))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	_, items := decodeItems(t, rec)
	it := itemByID(items, 7)
	if it == nil {
		t.Fatalf("allowed group 7 should be listed even with no ratio: %v", items)
	}
	if _, present := it["ratio"]; present {
		t.Fatalf("unpriced group must omit ratio, got %v", it["ratio"])
	}
	if it["has_public_ratio"] != false {
		t.Fatalf("unpriced group has_public_ratio=%v want false", it["has_public_ratio"])
	}
	if it["name"] != "unpriced-pool" {
		t.Fatalf("name=%v want unpriced-pool", it["name"])
	}
}

// TestDependencyUnset 守护 503 接线守卫。
func TestDependencyUnset(t *testing.T) {
	rec := invoke(NewHandler(Deps{}))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503 body=%s", rec.Code, rec.Body.String())
	}
}

// TestBackendErrorIsServiceUnavailable 守护:瞬态存储失败应呈现为 503,
// 而非 panic/200。
func TestBackendErrorIsServiceUnavailable(t *testing.T) {
	ident := auth.Identity{TenantID: 1, UserID: 42}
	d := Deps{
		Auth:       authStub{identity: ident},
		UserGroups: &userGroupStub{err: errors.New("db down")},
		RoutesRepo: &routesStub{tenantID: 1, userGroup: "default", allowed: []int64{7}},
		Ratios:     &ratioStub{},
		Pools:      &poolNameStub{},
	}
	rec := invoke(NewHandler(d))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503 body=%s", rec.Code, rec.Body.String())
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
