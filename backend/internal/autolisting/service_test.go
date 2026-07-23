package autolisting

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/modelsync"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

type stubStore struct {
	pending  []registry.ModelDiscovery
	listed   bool
	promoted []int64
}

func (s *stubStore) ListModelDiscoveries(_ context.Context, params registry.ModelDiscoveryListParams) (registry.ModelDiscoveryPage, error) {
	// 只在首页返回全部 pending;第二页返回空,终止分页循环。
	if s.listed {
		return registry.ModelDiscoveryPage{}, nil
	}
	s.listed = true
	return registry.ModelDiscoveryPage{Items: s.pending}, nil
}

func (s *stubStore) PromoteModelDiscovery(_ context.Context, in registry.ModelDiscoveryDecision) (registry.ModelDiscovery, error) {
	s.promoted = append(s.promoted, in.ID)
	return registry.ModelDiscovery{ID: in.ID}, nil
}

type stubPrices struct{ priced map[string]struct{} }

func (s stubPrices) PublicModelPrices(_ context.Context, _ int64) (billing.PublicPriceTable, error) {
	prices := map[string]billing.PublicPrice{}
	for id := range s.priced {
		// 完整双向价:自动挡要求 input 与 output 基准价都在才算"有官方价"。
		prices[id] = billing.PublicPrice{
			InputPerToken: decimal.NewFromInt(1), OutputPerToken: decimal.NewFromInt(2),
			HasInput: true, HasOutput: true,
		}
	}
	return billing.NewPublicPriceTable("1.0", prices), nil
}

type stubSettings struct{ cfg platformsettings.AutoListingConfig }

func (s stubSettings) AutoListing(context.Context) platformsettings.AutoListingConfig { return s.cfg }

func discovery(id int64, vendor modelsync.Vendor, providerModelID string) registry.ModelDiscovery {
	return registry.ModelDiscovery{ID: id, Vendor: vendor, ProviderModelID: providerModelID, Status: registry.ModelDiscoveryPending}
}

func autoCfg(enabled bool, vendors ...string) platformsettings.AutoListingConfig {
	set := make(map[string]struct{}, len(vendors))
	for _, v := range vendors {
		set[v] = struct{}{}
	}
	return platformsettings.AutoListingConfig{Enabled: enabled, AutoVendors: set}
}

// 自动挡关时绝不 promote(纯人工挡)。守卫:把 cfg.Enabled 翻成 true 本测转红。
func TestProcessPendingDisabledPromotesNothing(t *testing.T) {
	store := &stubStore{pending: []registry.ModelDiscovery{discovery(1, modelsync.VendorOpenAI, "gpt-x")}}
	svc := NewService(store, stubPrices{priced: map[string]struct{}{"gpt-x": {}}}, stubSettings{cfg: autoCfg(false, "openai")})
	res, err := svc.ProcessPending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Enabled {
		t.Fatalf("总闸关时 Enabled 应为 false")
	}
	if len(store.promoted) != 0 {
		t.Fatalf("总闸关时不得 promote,却 promote 了 %v", store.promoted)
	}
}

// 开启后只对"vendor 走自动挡 且 有官方基准价"的模型 promote;其余分类跳过。
func TestProcessPendingPromotesOnlyPricedAutoVendor(t *testing.T) {
	store := &stubStore{pending: []registry.ModelDiscovery{
		discovery(1, modelsync.VendorOpenAI, "gpt-priced"),      // auto vendor + 有价 → promote
		discovery(2, modelsync.VendorOpenAI, "gpt-unpriced"),    // auto vendor + 无价 → 跳过
		discovery(3, modelsync.VendorGrok, "grok-priced"),       // 人工挡 vendor → 跳过(即便有价)
	}}
	prices := stubPrices{priced: map[string]struct{}{"gpt-priced": {}, "grok-priced": {}}}
	svc := NewService(store, prices, stubSettings{cfg: autoCfg(true, "openai")})
	res, err := svc.ProcessPending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(store.promoted) != 1 || store.promoted[0] != 1 {
		t.Fatalf("应只 promote id=1(auto+有价),实际 promoted=%v", store.promoted)
	}
	if res.Promoted != 1 || res.SkippedNoPrice != 1 || res.SkippedManualVendor != 1 || res.Scanned != 3 {
		t.Fatalf("分类计数不符:%+v", res)
	}
}

// 残缺单边价(只有 input 或只有 output)不算"有官方价",不自动上架——否则上成必 503 的坏模型。
// 守卫:把 service 的判定放宽回 Lookup 的 HasInput||HasOutput,本测转红。
func TestProcessPendingSkipsSingleSidedPrice(t *testing.T) {
	store := &stubStore{pending: []registry.ModelDiscovery{discovery(1, modelsync.VendorOpenAI, "gpt-half")}}
	svc := NewService(store, halfPricedStub{}, stubSettings{cfg: autoCfg(true, "openai")})
	res, err := svc.ProcessPending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(store.promoted) != 0 {
		t.Fatalf("单边价不应 promote,却 promote 了 %v", store.promoted)
	}
	if res.SkippedNoPrice != 1 {
		t.Fatalf("单边价应计入 SkippedNoPrice,got %+v", res)
	}
}

type halfPricedStub struct{}

func (halfPricedStub) PublicModelPrices(_ context.Context, _ int64) (billing.PublicPriceTable, error) {
	return billing.NewPublicPriceTable("1.0", map[string]billing.PublicPrice{
		// 只有 input 价,无 output 价。
		"gpt-half": {InputPerToken: decimal.NewFromInt(1), HasInput: true},
	}), nil
}
