package registry

import (
	"fmt"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/modelsync"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
)

type vendorAliasState struct {
	AliasNormalized string
	Source          string
	Status          string
}

type vendorDiscoveryState struct {
	ModelIDNormalized string
	Status            string
}

type vendorCatalogPlan struct {
	Upserts           []modelsync.Model
	Discoveries       []modelsync.Model
	DisableAliases    []string
	ReactivateAliases []string
	AbsentDiscoveries []string
}

// planVendorCatalogApply 只自动刷新已由同步管理或已被明确上架的模型。未知模型
// 进入发现箱；人工别名不在自动覆盖或禁用范围内。
func planVendorCatalogApply(catalog modelsync.Catalog, current []vendorAliasState, discoveries []vendorDiscoveryState) (vendorCatalogPlan, error) {
	incoming := make(map[string]modelsync.Model, len(catalog.Models))
	currentByAlias := make(map[string]vendorAliasState, len(current))
	for _, state := range current {
		alias := AliasNormalize(state.AliasNormalized)
		if alias != "" {
			currentByAlias[alias] = state
		}
	}
	discoveryByAlias := make(map[string]vendorDiscoveryState, len(discoveries))
	for _, state := range discoveries {
		alias := AliasNormalize(state.ModelIDNormalized)
		if alias != "" {
			discoveryByAlias[alias] = state
		}
	}
	out := vendorCatalogPlan{
		Upserts:     make([]modelsync.Model, 0, len(catalog.Models)),
		Discoveries: make([]modelsync.Model, 0, len(catalog.Models)),
	}
	for _, model := range catalog.Models {
		model.ID = strings.TrimSpace(model.ID)
		if model.ID == "" {
			return vendorCatalogPlan{}, fmt.Errorf("registry model sync: empty model id")
		}
		alias := AliasNormalize(model.ID)
		if alias == "" {
			return vendorCatalogPlan{}, fmt.Errorf("registry model sync: invalid model id %q", model.ID)
		}
		if _, exists := incoming[alias]; exists {
			return vendorCatalogPlan{}, fmt.Errorf("registry model sync: duplicate model id %q", model.ID)
		}
		incoming[alias] = model
		aliasState, hasAlias := currentByAlias[alias]
		discoveryState, hasDiscovery := discoveryByAlias[alias]
		if (hasAlias && aliasState.Source == modelSyncSource) ||
			(hasDiscovery && discoveryState.Status == ModelDiscoveryPromoted) {
			out.Upserts = append(out.Upserts, model)
			continue
		}
		out.Discoveries = append(out.Discoveries, model)
	}

	activeAutoSynced := 0
	for _, state := range current {
		if state.Source != modelSyncSource {
			continue
		}
		alias := AliasNormalize(state.AliasNormalized)
		if alias == "" {
			continue
		}
		if state.Status == "active" {
			activeAutoSynced++
		}
		_, stillPresent := incoming[alias]
		if stillPresent {
			if state.Status != "active" {
				out.ReactivateAliases = append(out.ReactivateAliases, alias)
			}
			continue
		}
		if state.Status == "active" {
			out.DisableAliases = append(out.DisableAliases, alias)
		}
	}

	pendingDiscoveries := 0
	for _, state := range discoveries {
		if state.Status != ModelDiscoveryPending {
			continue
		}
		pendingDiscoveries++
		alias := AliasNormalize(state.ModelIDNormalized)
		if _, present := incoming[alias]; !present {
			out.AbsentDiscoveries = append(out.AbsentDiscoveries, alias)
		}
	}

	// 上游空响应或截断不得一次性禁用全部已上线目录。拒绝可疑快照会让事务
	// 回滚并保留现状，等待后续重试或管理员核对。
	if len(out.DisableAliases) > 0 {
		if len(catalog.Models) == 0 && activeAutoSynced > 0 {
			return vendorCatalogPlan{}, fmt.Errorf(
				"registry model sync: refusing empty %s catalog that would disable %d active alias(es) (likely upstream blip)",
				catalog.Vendor, len(out.DisableAliases))
		}
		if activeAutoSynced >= 4 && len(out.DisableAliases)*2 > activeAutoSynced {
			return vendorCatalogPlan{}, fmt.Errorf(
				"registry model sync: refusing %s catalog that would disable %d of %d active alias(es) (>50%%, catastrophic shrink; manual confirm required)",
				catalog.Vendor, len(out.DisableAliases), activeAutoSynced)
		}
	}
	if len(out.AbsentDiscoveries) > 0 {
		if len(catalog.Models) == 0 {
			return vendorCatalogPlan{}, fmt.Errorf(
				"registry model sync: refusing empty %s catalog that would mark %d pending discovery item(s) absent",
				catalog.Vendor, len(out.AbsentDiscoveries))
		}
		if pendingDiscoveries >= 4 && len(out.AbsentDiscoveries)*2 > pendingDiscoveries {
			return vendorCatalogPlan{}, fmt.Errorf(
				"registry model sync: refusing %s catalog that would mark %d of %d pending discovery item(s) absent (>50%%, catastrophic shrink)",
				catalog.Vendor, len(out.AbsentDiscoveries), pendingDiscoveries)
		}
	}
	return out, nil
}

func vendorCanonicalID(vendor modelsync.Vendor, modelID string) string {
	return string(vendor) + "/" + strings.TrimSpace(modelID)
}

func vendorCanonicalLike(vendor modelsync.Vendor) string {
	return string(vendor) + "/%"
}

func defaultProtocolForVendor(vendor modelsync.Vendor) string {
	switch vendor {
	case modelsync.VendorAnthropic:
		return registrydefault.ProtocolAnthropicMessages
	case modelsync.VendorGemini:
		return registrydefault.ProtocolGeminiMessages
	default:
		return registrydefault.ProtocolOpenAIChat
	}
}

func normalizeSyncedProtocolFamily(protocol string) string {
	switch strings.TrimSpace(protocol) {
	case "gemini":
		return registrydefault.ProtocolGeminiMessages
	default:
		return strings.TrimSpace(protocol)
	}
}

func defaultOwnerForVendor(vendor modelsync.Vendor) string {
	switch vendor {
	case modelsync.VendorAnthropic:
		return "anthropic"
	case modelsync.VendorGemini:
		return "google"
	default:
		return "openai"
	}
}

func snapshotReason(vendor modelsync.Vendor, opts modelsync.ApplyOptions) string {
	base := "model sync " + string(vendor)
	reason := strings.TrimSpace(opts.Reason)
	if reason == "" {
		return base
	}
	return base + ": " + reason
}

func snapshotActor(opts modelsync.ApplyOptions) string {
	actor := strings.TrimSpace(opts.Actor)
	if actor == "" {
		return modelSyncSource
	}
	return actor
}
