package modulehttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/modulecatalog"
	"github.com/BloomingProsperity/HUAKAI/internal/moduleregistry"
)

// fakeSource 是单元测试用的可控 Source(无 DB、无真实接线)。
type fakeSource struct {
	snaps   []moduleregistry.ModuleSnapshot
	catalog map[string]modulecatalog.CatalogModule
	idToPkg map[string]string
}

func (f *fakeSource) Snapshot(context.Context) []moduleregistry.ModuleSnapshot { return f.snaps }
func (f *fakeSource) CatalogLookup(pkg string) (modulecatalog.CatalogModule, bool) {
	m, ok := f.catalog[pkg]
	return m, ok
}
func (f *fakeSource) CatalogPkgFor(id string) (string, bool) {
	p, ok := f.idToPkg[id]
	return p, ok
}

func newFakeSource() *fakeSource {
	return &fakeSource{
		snaps: []moduleregistry.ModuleSnapshot{
			{
				Descriptor: moduleregistry.ModuleDescriptor{
					ID: "billing.service", Category: "money-path", Title: "Billing",
					Capabilities: []string{"settle", "reserve"},
					Activation: &moduleregistry.ActivationSnapshot{
						Declared: boolPointer(true), Active: boolPointer(true),
						Endpoints: []moduleregistry.ActivationEndpoint{{Name: "chat", Active: boolPointer(true)}},
					},
				},
				Probe: moduleregistry.ProbeResult{Status: moduleregistry.StatusOK, Detail: "wired"},
			},
			{
				Descriptor: moduleregistry.ModuleDescriptor{
					ID: "routing.selector", Category: "routing", Title: "Selector",
					Activation: &moduleregistry.ActivationSnapshot{Declared: boolPointer(true)},
				},
				Probe: moduleregistry.ProbeResult{Status: moduleregistry.StatusUnknown},
			},
		},
		catalog: map[string]modulecatalog.CatalogModule{
			"billing": {Pkg: "billing", Section: "§5 计费", FeatureID: "F-BILL-001", Status: "tested", Parity: "strong"},
		},
		idToPkg: map[string]string{
			"billing.service": "billing",
			// routing.selector 刻意未映射 -> 纯实时,无覆盖层。
		},
	}
}

// TestMergeJoinsLiveAndCatalog —— 合并必须把静态覆盖层叠加到匹配的实时模块
// 上,并把实时探针传递下来。
// 回归:若 Merge 停止调用 CatalogLookup/CatalogPkgFor(覆盖层被丢弃),
// billing 的 Catalog 会为 nil;若它丢弃了探针,LiveProbe 状态会为空。
// 两者任一都会转红。
func TestMergeJoinsLiveAndCatalog(t *testing.T) {
	views := Merge(context.Background(), newFakeSource(), "")
	if len(views) != 2 {
		t.Fatalf("views=%d want 2", len(views))
	}
	var billing *ModuleView
	for i := range views {
		if views[i].ID == "billing.service" {
			billing = &views[i]
		}
	}
	if billing == nil {
		t.Fatalf("billing.service missing from merged view")
	}
	if billing.Catalog == nil || billing.Catalog.FeatureID != "F-BILL-001" {
		t.Fatalf("billing static overlay not merged: %+v", billing.Catalog)
	}
	if billing.LiveProbe.Status != moduleregistry.StatusOK {
		t.Fatalf("billing live probe=%q want ok (probe not carried through)", billing.LiveProbe.Status)
	}
	if billing.Activation == nil || billing.Activation.Verified == nil || !*billing.Activation.Verified {
		t.Fatalf("billing activation verification not projected: %+v", billing.Activation)
	}
	// routing.selector 未映射:它必须以纯实时形式出现,且无覆盖层。
	for _, v := range views {
		if v.ID == "routing.selector" && v.Catalog != nil {
			t.Fatalf("unmapped module got a spurious catalog overlay: %+v", v.Catalog)
		}
		if v.ID == "routing.selector" && v.Activation != nil && v.Activation.Verified != nil {
			t.Fatalf("未知探针不能冒充已验证状态: %+v", v.Activation)
		}
	}
}

func TestActivationWithFailedProbeMarksUnverified(t *testing.T) {
	activation := &moduleregistry.ActivationSnapshot{Declared: boolPointer(true), Active: boolPointer(true)}
	for _, status := range []moduleregistry.ProbeStatus{moduleregistry.StatusDegraded, moduleregistry.StatusError} {
		got := activationWithProbe(activation, status)
		if got == nil || got.Verified == nil || *got.Verified {
			t.Fatalf("status=%q activation=%+v，应明确标记未验证", status, got)
		}
		if activation.Verified != nil {
			t.Fatalf("投影不得修改注册表原始快照: %+v", activation)
		}
	}
}

func boolPointer(value bool) *bool { return &value }

// TestHandlerReturnsSeededModules —— 端点返回实时模块。
// 回归:若 handler 在有效 source 上返回空 body 或 500,计数断言或状态断言
// 会转红。
func TestHandlerReturnsSeededModules(t *testing.T) {
	h := NewModulesHandler(newFakeSource())
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/admin/v1/modules", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp ModulesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Modules) != 2 {
		t.Fatalf("modules=%d want 2", len(resp.Modules))
	}
}

// TestHandlerCategoryFilter —— ?category= 过滤到单个 category。
// 回归:若 handler 忽略查询参数(始终向 Merge 传 ""),?category=money-path
// 会返回两个模块,计数转红;这是一个有区分度的 fixture,因为两个 seed 有
// 不同的 category,所以坏掉的过滤会产出 2,正确的过滤会产出 1。
func TestHandlerCategoryFilter(t *testing.T) {
	h := NewModulesHandler(newFakeSource())
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/admin/v1/modules?category=money-path", nil))

	var resp ModulesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Modules) != 1 {
		t.Fatalf("filtered modules=%d want 1 (money-path only)", len(resp.Modules))
	}
	if resp.Modules[0].ID != "billing.service" {
		t.Fatalf("filtered to %q want billing.service", resp.Modules[0].ID)
	}
}

// TestHandlerNilSourceFailsClosed —— nil source 绝不能 panic;它返回 503
// 并带一个空列表,绝不返回暗示「存在零个模块」的 200。
func TestHandlerNilSourceFailsClosed(t *testing.T) {
	h := NewModulesHandler(nil)
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/admin/v1/modules", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil source status=%d want 503", rec.Code)
	}
}
