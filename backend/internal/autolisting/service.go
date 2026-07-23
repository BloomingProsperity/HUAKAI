// Package autolisting 实现自动上架管道(part 2)的"自动挡"驱动:扫描模型发现箱里待处理的
// 模型,对走自动挡的 vendor 且能查到官方基准价的,自动 promote(promote 内已自动绑池),
// 让"发现了模型"无需人工一路变成"用户能用的模型"。查不到基准价或 vendor 走人工挡的,留在
// 发现箱等运营审批。总闸(auto_listing_enabled)默认关 → 纯人工挡,零生产行为变。
package autolisting

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

const (
	defaultActor    = "auto_listing_worker"
	defaultPageSize = 200
	// 单轮 promote 上限:promote 是带 Serializable 事务的昂贵动作,按它计预算防一次箱内爆量
	// 把整轮拖太久,剩余留待下一轮。刻意【不】按"已扫描"计:扫描/跳过是内存廉价判断,截断扫描
	// 会让"最新 N 条恰好都是人工挡/无价"时更旧的可上架项永远够不到(跨轮饥饿)。已 promote 的
	// 项会转 promoted 离开 pending 集,故下一轮从头扫自然推进到剩余项,无饥饿。
	maxPromotePerRun = 500
)

// DiscoveryStore 是发现箱的读 + 上架写(由 registry.PostgresRegistry 实现)。
type DiscoveryStore interface {
	ListModelDiscoveries(context.Context, registry.ModelDiscoveryListParams) (registry.ModelDiscoveryPage, error)
	PromoteModelDiscovery(context.Context, registry.ModelDiscoveryDecision) (registry.ModelDiscovery, error)
}

// PriceTableSource 提供全局公开基准价表,用于判定某模型是否已有官方定价。
type PriceTableSource interface {
	PublicModelPrices(context.Context, int64) (billing.PublicPriceTable, error)
}

// SettingsSource 提供自动挡运行期配置(总闸 + auto-vendor 白名单)。
type SettingsSource interface {
	AutoListing(context.Context) platformsettings.AutoListingConfig
}

type Service struct {
	store    DiscoveryStore
	prices   PriceTableSource
	settings SettingsSource
	actor    string
}

func NewService(store DiscoveryStore, prices PriceTableSource, settings SettingsSource) *Service {
	return &Service{store: store, prices: prices, settings: settings, actor: defaultActor}
}

// settingsGate 把 SettingsSource 适配成 worker 的每-tick 总闸判定。
type settingsGate struct{ src SettingsSource }

// NewSettingsGate 返回一个只读总闸,worker 每 tick 现读 auto_listing_enabled。
func NewSettingsGate(src SettingsSource) SettingsGate { return settingsGate{src: src} }

func (g settingsGate) Enabled(ctx context.Context) bool {
	if g.src == nil {
		return false
	}
	return g.src.AutoListing(ctx).Enabled
}

// Result 汇总一轮自动上架的分类结果,供日志与观测辨识(§21b)。
type Result struct {
	Enabled             bool
	Scanned             int
	Promoted            int
	SkippedManualVendor int
	SkippedNoPrice      int
	Failed              int
}

// ProcessPending 执行一轮自动上架。总闸关时直接返回(Enabled=false),不触任何写。
func (s *Service) ProcessPending(ctx context.Context) (Result, error) {
	if s == nil || s.store == nil || s.prices == nil || s.settings == nil {
		return Result{}, errors.New("autolisting: service not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cfg := s.settings.AutoListing(ctx)
	if !cfg.Enabled {
		return Result{Enabled: false}, nil
	}
	// 官方基准价表拉一次,整轮复用(池倍率在计费时叠加,这里只判"有没有基准价")。
	priceTable, err := s.prices.PublicModelPrices(ctx, 0)
	if err != nil {
		return Result{Enabled: true}, fmt.Errorf("autolisting: load public prices: %w", err)
	}

	result := Result{Enabled: true}
	var beforeID int64
	for result.Promoted+result.Failed < maxPromotePerRun {
		page, err := s.store.ListModelDiscoveries(ctx, registry.ModelDiscoveryListParams{
			Access:   registry.ModelDiscoveryAccess{Role: admin.RolePlatformAdmin, Actor: s.actor},
			Status:   registry.ModelDiscoveryPending,
			BeforeID: beforeID,
			Limit:    defaultPageSize,
		})
		if err != nil {
			return result, fmt.Errorf("autolisting: list pending discoveries: %w", err)
		}
		if len(page.Items) == 0 {
			break
		}
		for _, item := range page.Items {
			if result.Promoted+result.Failed >= maxPromotePerRun {
				break
			}
			result.Scanned++
			if !cfg.VendorIsAuto(string(item.Vendor)) {
				result.SkippedManualVendor++
				continue
			}
			// 完整可计费判定:文本模型必须 input 与 output 基准价都在。展示用 Lookup 宽松
			// (任一边即算有价),自动上架若沿用它,残缺单边价会上架一个必 503 的坏模型,
			// 违背"有官方基准价才自动上架"的合同。故这里额外要求双向价齐全。
			price, ok := priceTable.Lookup(item.ProviderModelID)
			if !ok || !price.HasInput || !price.HasOutput {
				result.SkippedNoPrice++
				continue
			}
			if _, err := s.store.PromoteModelDiscovery(ctx, registry.ModelDiscoveryDecision{
				Access: registry.ModelDiscoveryAccess{Role: admin.RolePlatformAdmin, Actor: s.actor},
				ID:     item.ID,
				Reason: "auto-listing: official pricing present, vendor auto-mode",
			}); err != nil {
				// 单条上架失败(如并发 promote 冲突)不拖垮整轮;记高辨识度日志,继续下一条。
				result.Failed++
				slog.WarnContext(ctx, "auto-listing promote failed",
					"component", "auto_listing_worker",
					"event_class", "auto_promote_failed",
					"vendor", string(item.Vendor),
					"discovery_id", item.ID,
					"provider_model_id", item.ProviderModelID,
					"error", err.Error())
				continue
			}
			result.Promoted++
		}
		if page.NextBeforeID == nil {
			break
		}
		beforeID = *page.NextBeforeID
	}
	return result, nil
}
