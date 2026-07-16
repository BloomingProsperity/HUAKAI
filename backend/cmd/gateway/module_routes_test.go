package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauth"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	legacydlq "github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/mediatask"
	"github.com/BloomingProsperity/HUAKAI/internal/modelsync"
	"github.com/BloomingProsperity/HUAKAI/internal/modulehttp"
	"github.com/BloomingProsperity/HUAKAI/internal/moduleregistry"
	"github.com/BloomingProsperity/HUAKAI/internal/passkey"
	"github.com/BloomingProsperity/HUAKAI/internal/payment"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/subscription"
	"github.com/BloomingProsperity/HUAKAI/internal/twofa"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
	"github.com/BloomingProsperity/HUAKAI/internal/voucher"
)

// TestModulesEndpointIsAdminGated —— 对 /admin/v1/modules 的无凭据请求,
// 由与其它每条 /admin/v1 路由相同的 admin 中间件处理。
//
// 回归(变异:从 routes_modules.go 去掉 adminGate 并挂载裸 handler):
// 在有 adminGate 且 admin resolver 的 queries 为 nil 时,请求会 fail closed
// 返回 503 admin_backend_error——这是只有 admin gate 才发出的 code。若 gate
// 被移除,裸 handler 反而会返回 200 加一个 modules 响应体,届时状态码断言
// 和缺失 code 断言都会变红。这是有区分力的 fixture:有 gate => 503/admin_backend_error;
// 无 gate => 200。
func TestModulesEndpointIsAdminGated(t *testing.T) {
	d := &deps{
		// queries 为 nil -> Resolve 返回 ErrAdminBackend(fail-closed 503),
		// 与 Hermes admin-gate 测试所依赖的行为完全一致。
		adminAuth:      adminsessionauth.New(admin.NewAdminResolver(nil), nil, nil, nil, 0),
		moduleRegistry: moduleregistry.New(),
	}
	r := chi.NewRouter()
	// 只挂载 module-registry 路由(这里真正走到了 adminGate 包装器);
	// 整体的 mountAdminRoutes 在最小化 deps 上会发生 nil 解引用,因为它会接线
	// 数十个与本测试无关的、依赖 DB 的子系统。
	mountModuleRegistryRoutes(r, d)

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/modules", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503 (admin gate, not bare handler)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "admin_backend_error") {
		t.Fatalf("body=%s want admin_backend_error (proves admin gate mounted, not the 200 module list)", rec.Body.String())
	}
}

// TestModulesEndpointReturnsSeededModulesForAdmin —— 在 gate 被满足时
// (注入了一个 platform-admin 身份),合并后的 handler 返回 live 播种项。
// 这用一个由 buildModuleRegistry 播种的真实 registry(而非 fake)来检验
// handler+source 的接线,因此播种注册中的回归(例如漏了一次 Register 调用)
// 会在这里变红。
//
// 回归:若 buildModuleRegistry 停止注册这三个播种项,计数断言(>=3)变红;
// 若 handler 丢掉响应体,decode 会失败。
func TestModulesEndpointReturnsSeededModulesForAdmin(t *testing.T) {
	// 用最小化 deps 构建一个真实的、已播种的 registry(probe 引用到的字段
	// 留 nil -> probe 报告 degraded,这没问题:这里断言的是身份而非健康)。
	d := &deps{}
	reg := buildModuleRegistry(d)
	src := newModuleSource(reg)

	// 直接调用 handler(gate 已在上面单独断言);这样正向测试就不必
	// 依赖 DB 支撑的 admin 解析。
	h := modulehttp.NewModulesHandler(src)
	req := httptest.NewRequest(http.MethodGet, "/admin/v1/modules", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp modulehttp.ModulesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Modules) < 3 {
		t.Fatalf("modules=%d want >=3 seeded (billing/routing/credentials)", len(resp.Modules))
	}
	// billing 播种项必须带上它的静态 catalog 叠加层(join 已接线)。
	var sawBillingOverlay bool
	for _, m := range resp.Modules {
		if m.ID == "billing.service" && m.Catalog != nil && m.Catalog.FeatureID != "" {
			sawBillingOverlay = true
		}
	}
	if !sawBillingOverlay {
		t.Fatalf("billing.service missing static catalog overlay; seedCatalogJoin/merge regression")
	}
}

// TestModulesEndpointCategoryFilterForAdmin —— ?category= 把 live 播种项收窄。
// 回归:若 handler 忽略 ?category=,money-path 会返回全部 3 个播种项而非
// 仅 billing.service -> 红。之所以有区分力,是因为这些播种项横跨三个不同的 category。
func TestModulesEndpointCategoryFilterForAdmin(t *testing.T) {
	src := newModuleSource(buildModuleRegistry(&deps{}))
	h := modulehttp.NewModulesHandler(src)

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/modules?category=money-path", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	var resp modulehttp.ModulesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Modules) != 1 || resp.Modules[0].ID != "billing.service" {
		ids := make([]string, len(resp.Modules))
		for i, m := range resp.Modules {
			ids[i] = m.ID
		}
		t.Fatalf("money-path filter -> %v want [billing.service]", ids)
	}
}

// TestModulesRegistryWiresChannelHealthAndDLQ — Hermes 模块感知扩展:channelhealth + dlq
// 必须注册进 module spine(Hermes 经 GET /v1/hermes/context 看到),探针随服务接线 OK/degraded,
// 且 seedCatalogJoin 把静态 catalog overlay 接上。
// 判别:① 若 buildModuleRegistry 停止注册任一 → "未注册" RED;② 若探针 nil-guard 反了
// (服务 nil 仍报 OK)→ degraded 断言 RED;③ 若 seedCatalogJoin 漏接 → overlay nil RED。
func TestModulesRegistryWiresChannelHealthAndDLQ(t *testing.T) {
	fetch := func(d *deps) map[string]modulehttp.ModuleView {
		src := newModuleSource(buildModuleRegistry(d))
		h := modulehttp.NewModulesHandler(src)
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodGet, "/admin/v1/modules", nil))
		var resp modulehttp.ModulesResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		out := map[string]modulehttp.ModuleView{}
		for _, m := range resp.Modules {
			out[m.ID] = m
		}
		return out
	}

	// wired:服务非 nil(零值指针即可,探针只判 nil)→ 探针 StatusOK + catalog overlay 接上。
	wired := fetch(&deps{channelHealth: &channelhealth.Service{}, dlqService: &legacydlq.Service{}})
	for _, id := range []string{"channelhealth.service", "dlq.service"} {
		m, ok := wired[id]
		if !ok {
			t.Fatalf("%s 未注册进 module registry(Hermes 看不到该模块)", id)
		}
		if m.LiveProbe.Status != moduleregistry.StatusOK {
			t.Fatalf("%s 服务接线时探针应 StatusOK,got %v", id, m.LiveProbe.Status)
		}
		if m.Catalog == nil {
			t.Fatalf("%s 缺 catalog overlay(seedCatalogJoin 未接)", id)
		}
	}

	// degraded:服务 nil → 探针 StatusDegraded(身份仍注册;nil-guard 判别)。
	degraded := fetch(&deps{})
	for _, id := range []string{"channelhealth.service", "dlq.service"} {
		m, ok := degraded[id]
		if !ok {
			t.Fatalf("%s 应仍注册(身份与健康无关)", id)
		}
		if m.LiveProbe.Status != moduleregistry.StatusDegraded {
			t.Fatalf("%s 服务未接线时探针应 StatusDegraded,got %v", id, m.LiveProbe.Status)
		}
	}
}

// TestModulesRegistryWiresRoutingStack — Hermes 感知扩展第 2 批:补全路由栈感知
// (registry.model 解析 → router.planner 规划 → routing.selector 选号,selector 已有)。
// 判别同 channelhealth/dlq:注册 + 探针 wired/degraded + catalog overlay。
func TestModulesRegistryWiresRoutingStack(t *testing.T) {
	fetch := func(d *deps) map[string]modulehttp.ModuleView {
		src := newModuleSource(buildModuleRegistry(d))
		h := modulehttp.NewModulesHandler(src)
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodGet, "/admin/v1/modules", nil))
		var resp modulehttp.ModulesResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		out := map[string]modulehttp.ModuleView{}
		for _, m := range resp.Modules {
			out[m.ID] = m
		}
		return out
	}

	// wired:模型注册表 + 路由规划器非 nil → 探针 StatusOK + catalog overlay 接上。
	wired := fetch(&deps{modelRegistry: &registry.PostgresRegistry{}, routePlanner: router.NewDefaultRouter()})
	for _, id := range []string{"registry.model", "router.planner"} {
		m, ok := wired[id]
		if !ok {
			t.Fatalf("%s 未注册进 module registry(Hermes 看不到该模块)", id)
		}
		if m.LiveProbe.Status != moduleregistry.StatusOK {
			t.Fatalf("%s 接线时探针应 StatusOK,got %v", id, m.LiveProbe.Status)
		}
		if m.Catalog == nil {
			t.Fatalf("%s 缺 catalog overlay(seedCatalogJoin 未接)", id)
		}
	}

	// degraded:nil → StatusDegraded(身份仍注册)。
	degraded := fetch(&deps{})
	for _, id := range []string{"registry.model", "router.planner"} {
		m, ok := degraded[id]
		if !ok {
			t.Fatalf("%s 应仍注册(身份与健康无关)", id)
		}
		if m.LiveProbe.Status != moduleregistry.StatusDegraded {
			t.Fatalf("%s 未接线时探针应 StatusDegraded,got %v", id, m.LiveProbe.Status)
		}
	}
}

// TestModulesRegistryWiresCommerce — Hermes 感知扩展第 3 批:money/变现域(中转站 SaaS 卖额度的核心)。
// payment/subscription/voucher 三服务注册 + 探针 wired/degraded。voucher 有 catalog overlay;
// payment/subscription 是 live-only(catalog 无对应 pkg → Catalog 应为 nil,不夸大成有静态条目)。
func TestModulesRegistryWiresCommerce(t *testing.T) {
	fetch := func(d *deps) map[string]modulehttp.ModuleView {
		src := newModuleSource(buildModuleRegistry(d))
		h := modulehttp.NewModulesHandler(src)
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodGet, "/admin/v1/modules", nil))
		var resp modulehttp.ModulesResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		out := map[string]modulehttp.ModuleView{}
		for _, m := range resp.Modules {
			out[m.ID] = m
		}
		return out
	}

	ids := []string{"payment.service", "subscription.service", "voucher.service"}

	// wired:三服务非 nil → 探针 StatusOK。
	wired := fetch(&deps{
		paymentService:      &payment.Service{},
		subscriptionService: &subscription.Service{},
		voucherService:      &voucher.Service{},
	})
	for _, id := range ids {
		m, ok := wired[id]
		if !ok {
			t.Fatalf("%s 未注册进 module registry(Hermes 看不到该模块)", id)
		}
		if m.LiveProbe.Status != moduleregistry.StatusOK {
			t.Fatalf("%s 接线时探针应 StatusOK,got %v", id, m.LiveProbe.Status)
		}
	}
	// voucher 有 catalog overlay(catalog 有 voucher pkg);payment/subscription live-only(Catalog 应 nil)。
	if wired["voucher.service"].Catalog == nil {
		t.Fatal("voucher.service 缺 catalog overlay(seedCatalogJoin 未接)")
	}
	if wired["payment.service"].Catalog != nil || wired["subscription.service"].Catalog != nil {
		t.Fatalf("payment/subscription 应 live-only(catalog 无 pkg),不应有 overlay")
	}

	// degraded:nil → StatusDegraded(身份仍注册)。
	degraded := fetch(&deps{})
	for _, id := range ids {
		m, ok := degraded[id]
		if !ok {
			t.Fatalf("%s 应仍注册(身份与健康无关)", id)
		}
		if m.LiveProbe.Status != moduleregistry.StatusDegraded {
			t.Fatalf("%s 未接线时探针应 StatusDegraded,got %v", id, m.LiveProbe.Status)
		}
	}
}

// TestModulesRegistryWiresAuth — Hermes 感知扩展第 4 批:auth/identity 域(运营最关键——
// auth 降级则全系统不可用)。userauth/passkey/twofa 三服务注册 + 探针 wired/degraded。
// userauth 有 catalog overlay;passkey/twofa live-only(catalog 无 pkg → Catalog 应 nil)。
func TestModulesRegistryWiresAuth(t *testing.T) {
	fetch := func(d *deps) map[string]modulehttp.ModuleView {
		src := newModuleSource(buildModuleRegistry(d))
		h := modulehttp.NewModulesHandler(src)
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodGet, "/admin/v1/modules", nil))
		var resp modulehttp.ModulesResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		out := map[string]modulehttp.ModuleView{}
		for _, m := range resp.Modules {
			out[m.ID] = m
		}
		return out
	}

	ids := []string{"auth.userauth", "auth.passkey", "auth.twofa"}

	wired := fetch(&deps{
		userAuth:  &userauth.Service{},
		passkeys:  &passkey.Service{},
		twoFactor: &twofa.Service{},
	})
	for _, id := range ids {
		m, ok := wired[id]
		if !ok {
			t.Fatalf("%s 未注册进 module registry(Hermes 看不到该模块)", id)
		}
		if m.LiveProbe.Status != moduleregistry.StatusOK {
			t.Fatalf("%s 接线时探针应 StatusOK,got %v", id, m.LiveProbe.Status)
		}
	}
	// userauth 有 catalog overlay;passkey/twofa live-only(Catalog 应 nil)。
	if wired["auth.userauth"].Catalog == nil {
		t.Fatal("auth.userauth 缺 catalog overlay(seedCatalogJoin 未接)")
	}
	if wired["auth.passkey"].Catalog != nil || wired["auth.twofa"].Catalog != nil {
		t.Fatalf("passkey/twofa 应 live-only(catalog 无 pkg),不应有 overlay")
	}

	degraded := fetch(&deps{})
	for _, id := range ids {
		m, ok := degraded[id]
		if !ok {
			t.Fatalf("%s 应仍注册(身份与健康无关)", id)
		}
		if m.LiveProbe.Status != moduleregistry.StatusDegraded {
			t.Fatalf("%s 未接线时探针应 StatusDegraded,got %v", id, m.LiveProbe.Status)
		}
	}
}

// TestModulesRegistryWiresSyncAndMedia — Hermes 感知扩展第 5 批(收尾):registry.modelsync(上游
// 模型目录同步,陈旧则路由错)+ media.task(异步媒体)。均 live-only(catalog 无 pkg → Catalog 应 nil)。
func TestModulesRegistryWiresSyncAndMedia(t *testing.T) {
	fetch := func(d *deps) map[string]modulehttp.ModuleView {
		src := newModuleSource(buildModuleRegistry(d))
		h := modulehttp.NewModulesHandler(src)
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodGet, "/admin/v1/modules", nil))
		var resp modulehttp.ModulesResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		out := map[string]modulehttp.ModuleView{}
		for _, m := range resp.Modules {
			out[m.ID] = m
		}
		return out
	}

	ids := []string{"registry.modelsync", "media.task"}

	wired := fetch(&deps{modelSync: &modelsync.Service{}, mediaTaskService: &mediatask.Service{}})
	for _, id := range ids {
		m, ok := wired[id]
		if !ok {
			t.Fatalf("%s 未注册进 module registry", id)
		}
		if m.LiveProbe.Status != moduleregistry.StatusOK {
			t.Fatalf("%s 接线时探针应 StatusOK,got %v", id, m.LiveProbe.Status)
		}
		if m.Catalog != nil {
			t.Fatalf("%s 应 live-only(catalog 无 pkg),不应有 overlay", id)
		}
	}

	degraded := fetch(&deps{})
	for _, id := range ids {
		m, ok := degraded[id]
		if !ok {
			t.Fatalf("%s 应仍注册(身份与健康无关)", id)
		}
		if m.LiveProbe.Status != moduleregistry.StatusDegraded {
			t.Fatalf("%s 未接线时探针应 StatusDegraded,got %v", id, m.LiveProbe.Status)
		}
	}
}
