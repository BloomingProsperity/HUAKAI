// Package adminhttp 构建供 admin UI 使用的、经过审定的 account-mode 目录(catalog)。
// 它依据 credentialacq 的 ModePlan 以及 credentialstore 的 finalizer 支持情况来推导可见性,
// 对不完整的 mode 采取 fail closed。
package adminhttp

import (
	"context"
	"errors"
	"sort"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
	"github.com/BloomingProsperity/HUAKAI/internal/servingcapability"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

const (
	DispositionEnabled          = "enabled"
	DispositionHiddenFlag       = "hidden-flag"
	DispositionExperimental     = "experimental"
	DispositionMandatoryRoadmap = "mandatory-roadmap"
)

type Catalog struct {
	Modes []Mode `json:"modes"`
}

type Mode struct {
	Vendor               string                    `json:"vendor"`
	AuthMode             string                    `json:"auth_mode"`
	FlowKind             credentialacq.FlowKind    `json:"flow_kind"`
	ClientIdentitySource string                    `json:"client_identity_source"`
	ManualFirst          bool                      `json:"manual_first"`
	LongLivedToggle      bool                      `json:"long_lived_toggle"`
	AllowedHelpers       []credentialacq.FlowKind  `json:"allowed_helpers"`
	RequiredFields       []credentialacq.FieldSpec `json:"required_fields"`
	IsEnabled            bool                      `json:"is_enabled"`
	IsExperimental       bool                      `json:"is_experimental"`
	FeatureFlag          string                    `json:"feature_flag"`
	RiskLevel            credentialacq.RiskLevel   `json:"risk_level"`
	RiskReasons          []string                  `json:"risk_reasons"`
	ServingReadiness     ServingReadiness          `json:"serving_readiness"`
}

// ServingReadiness 是账号模式目录面向前端暴露的最小闭合结论。站点级证据仍由
// servingcapability 保留，这里只投影控制面需要稳定消费的状态。
type ServingReadiness struct {
	Family         string                            `json:"family,omitempty"`
	ReleaseState   servingcapability.ReleaseState    `json:"release_state,omitempty"`
	Ready          bool                              `json:"ready"`
	Enableable     bool                              `json:"enableable"`
	TrafficAllowed bool                              `json:"traffic_allowed"`
	Status         servingcapability.ReadinessStatus `json:"status"`
	Action         servingcapability.Action          `json:"action"`
	Reason         string                            `json:"reason,omitempty"`
}

type Entry struct {
	Vendor               string
	AuthMode             string
	FlowKind             credentialacq.FlowKind
	ClientIdentitySource string
	ManualFirst          bool
	LongLivedToggle      bool
	AllowedHelpers       []credentialacq.FlowKind
	RequiredFields       []credentialacq.FieldSpec
	IsEnabled            bool
	IsExperimental       bool
	FeatureFlag          string
	RiskLevel            credentialacq.RiskLevel
	RiskReasons          []string
}

type CatalogInput struct {
	Plans            []credentialacq.ModePlan
	Registry         *credentialstore.HandlerRegistry
	RefreshAdapters  *credentialworker.ModeAdapterRegistry
	ServingEvaluator *servingcapability.Evaluator
	Entries          []Entry
	FeatureFlags     map[string]bool
}

type Provider interface {
	Catalog(context.Context) (Catalog, error)
}

type StaticProvider struct {
	Input CatalogInput
}

func DefaultProvider() StaticProvider {
	return StaticProvider{Input: CatalogInput{
		Plans:            credentialacq.DefaultModePlans(),
		Registry:         credentialstore.DefaultHandlerRegistry(),
		RefreshAdapters:  credentialworker.DefaultModeAdapterRegistry(),
		ServingEvaluator: defaultServingCapabilityEvaluator(),
	}}
}

func (p StaticProvider) Catalog(context.Context) (Catalog, error) {
	registry := p.Input.Registry
	if registry == nil {
		return Catalog{}, errors.New("accountmode: credential finalizer registry is nil")
	}
	return BuildCatalog(p.Input), nil
}

func DefaultCatalog() Catalog {
	return BuildCatalog(CatalogInput{
		Plans:            credentialacq.DefaultModePlans(),
		Registry:         credentialstore.DefaultHandlerRegistry(),
		RefreshAdapters:  credentialworker.DefaultModeAdapterRegistry(),
		ServingEvaluator: defaultServingCapabilityEvaluator(),
	})
}

func BuildCatalog(in CatalogInput) Catalog {
	registry := in.Registry
	if registry == nil {
		registry = credentialstore.DefaultHandlerRegistry()
	}
	refreshAdapters := in.RefreshAdapters
	if refreshAdapters == nil {
		refreshAdapters = credentialworker.DefaultModeAdapterRegistry()
	}
	evaluator := in.ServingEvaluator
	if evaluator == nil {
		evaluator = defaultServingCapabilityEvaluator()
	}
	entries := in.Entries
	if len(entries) == 0 {
		entries = entriesFromPlans(in.Plans)
	}
	plans := plansByKey(in.Plans)
	out := Catalog{Modes: make([]Mode, 0, len(entries))}
	for _, entry := range entries {
		key := credentialstore.ModeKey(entry.Vendor, entry.AuthMode)
		plan, ok := plans[key]
		if !ok {
			plan = planFromEntry(entry)
		}
		if !ok {
			continue
		}
		if _, ok := registry.Lookup(plan.Vendor, plan.AuthMode); !ok {
			continue
		}
		helpers := entry.AllowedHelpers
		if len(helpers) == 0 {
			helpers = plan.AllowedHelpers
		}
		if !helpersSubset(helpers, plan.AllowedHelpers) {
			continue
		}
		if !planVisible(plan, in.FeatureFlags) {
			continue
		}
		if !typedFieldContract(plan.RequiredFields) {
			continue
		}
		_, refreshPresent := refreshAdapters.Lookup(plan.Vendor, plan.AuthMode)
		readiness := evaluator.EvaluateCatalogMode(servingcapability.CatalogModeInput{
			Vendor:                        plan.Vendor,
			AuthMode:                      plan.AuthMode,
			FinalizerPresent:              true,
			AcquisitionDispositionPresent: len(plan.AllowedHelpers) > 0,
			RefreshDispositionPresent:     refreshPresent,
		})
		out.Modes = append(out.Modes, modeFromPlan(plan, helpers, readiness))
	}
	sort.Slice(out.Modes, func(i, j int) bool {
		left := credentialstore.ModeKey(out.Modes[i].Vendor, out.Modes[i].AuthMode)
		right := credentialstore.ModeKey(out.Modes[j].Vendor, out.Modes[j].AuthMode)
		return left < right
	})
	return out
}

type ChannelDisposition struct {
	Vendor         string `json:"vendor"`
	AuthMode       string `json:"auth_mode"`
	Disposition    string `json:"disposition"`
	FeatureFlag    string `json:"feature_flag,omitempty"`
	IsExperimental bool   `json:"is_experimental"`
	RiskReasons    []string
}

func RequestedChannelDispositions() []ChannelDisposition {
	return []ChannelDisposition{
		hiddenFlagChannel(credentialstore.VendorOpenRouter, credentialstore.AuthModeAPIKey),
		// 官 key 厂商(2026-07-02 Owner 指派):迁移 0169 已放行存储约束,审定为 enabled。
		enabledChannel(credentialstore.VendorDeepSeek, credentialstore.AuthModeAPIKey),
		enabledChannel(credentialstore.VendorGrok, credentialstore.AuthModeAPIKey),
		enabledChannel(credentialstore.VendorKimi, credentialstore.AuthModeAPIKey),
		enabledChannel(credentialstore.VendorQwen, credentialstore.AuthModeAPIKey),
		enabledChannel(credentialstore.VendorGLM, credentialstore.AuthModeAPIKey),
		enabledChannel(credentialstore.VendorYi, credentialstore.AuthModeAPIKey),
		enabledChannel(credentialstore.VendorBaichuan, credentialstore.AuthModeAPIKey),
		enabledChannel(credentialstore.VendorDoubao, credentialstore.AuthModeAPIKey),
		enabledChannel(credentialstore.VendorMiniMax, credentialstore.AuthModeAPIKey),
		enabledChannel(credentialstore.VendorErnie, credentialstore.AuthModeAPIKey),
		enabledChannel(credentialstore.VendorHunyuan, credentialstore.AuthModeAPIKey),
		enabledChannel(credentialstore.VendorStep, credentialstore.AuthModeAPIKey),
		// 全球推理托管云:Owner 明确不接,维持 hidden-flag(存储层 0169 亦未放行,双层拒绝)。
		hiddenFlagChannel(credentialstore.VendorMistral, credentialstore.AuthModeAPIKey),
		hiddenFlagChannel(credentialstore.VendorGroqCloud, credentialstore.AuthModeAPIKey),
		hiddenFlagChannel(credentialstore.VendorTogether, credentialstore.AuthModeAPIKey),
		hiddenFlagChannel(credentialstore.VendorPerplexity, credentialstore.AuthModeAPIKey),
		hiddenFlagChannel(credentialstore.VendorFireworks, credentialstore.AuthModeAPIKey),
		{
			Vendor: credentialstore.VendorCursor, AuthMode: credentialstore.AuthModeOAuth,
			Disposition: DispositionMandatoryRoadmap, IsExperimental: true,
			RiskReasons: []string{"acquisition and credential finalizer are not released for default catalog exposure"},
		},
		{
			Vendor: "kiro", AuthMode: "aws_sso",
			Disposition: DispositionMandatoryRoadmap, IsExperimental: true,
			RiskReasons: []string{"SSO acquisition, finalizer, and licensing posture require Owner-confirmed release gate"},
		},
	}
}

func enabledChannel(vendor, mode string) ChannelDisposition {
	return ChannelDisposition{Vendor: vendor, AuthMode: mode, Disposition: DispositionEnabled}
}

func hiddenFlagChannel(vendor, mode string) ChannelDisposition {
	return ChannelDisposition{
		Vendor: vendor, AuthMode: mode,
		Disposition: DispositionHiddenFlag, FeatureFlag: "account_modes.openai_compatible", IsExperimental: true,
		RiskReasons: []string{"account credential storage constraints are not released for this provider"},
	}
}

func entriesFromPlans(plans []credentialacq.ModePlan) []Entry {
	out := make([]Entry, 0, len(plans))
	for _, plan := range plans {
		out = append(out, Entry{
			Vendor:               plan.Vendor,
			AuthMode:             plan.AuthMode,
			FlowKind:             plan.Kind,
			ClientIdentitySource: plan.ClientIdentitySource,
			ManualFirst:          plan.ManualFirst,
			LongLivedToggle:      plan.LongLivedToggle,
			AllowedHelpers:       plan.AllowedHelpers,
			RequiredFields:       plan.RequiredFields,
			IsEnabled:            plan.IsEnabled,
			IsExperimental:       plan.IsExperimental,
			FeatureFlag:          plan.FeatureFlag,
			RiskLevel:            plan.RiskLevel,
			RiskReasons:          plan.RiskReasons,
		})
	}
	return out
}

func planFromEntry(entry Entry) credentialacq.ModePlan {
	return credentialacq.ModePlan{
		Vendor:               entry.Vendor,
		AuthMode:             entry.AuthMode,
		Kind:                 entry.FlowKind,
		ClientIdentitySource: entry.ClientIdentitySource,
		ManualFirst:          entry.ManualFirst,
		LongLivedToggle:      entry.LongLivedToggle,
		AllowedHelpers:       entry.AllowedHelpers,
		RequiredFields:       entry.RequiredFields,
		IsEnabled:            entry.IsEnabled,
		IsExperimental:       entry.IsExperimental,
		FeatureFlag:          entry.FeatureFlag,
		RiskLevel:            entry.RiskLevel,
		RiskReasons:          entry.RiskReasons,
	}
}

func plansByKey(plans []credentialacq.ModePlan) map[string]credentialacq.ModePlan {
	out := make(map[string]credentialacq.ModePlan, len(plans))
	for _, plan := range plans {
		if key := credentialstore.ModeKey(plan.Vendor, plan.AuthMode); key != "" {
			out[key] = plan
		}
	}
	return out
}

func helpersSubset(advertised, allowed []credentialacq.FlowKind) bool {
	if len(advertised) == 0 {
		return false
	}
	allowedSet := map[credentialacq.FlowKind]struct{}{}
	for _, helper := range allowed {
		allowedSet[credentialacq.NormalizeFlowKind(helper)] = struct{}{}
	}
	for _, helper := range advertised {
		if _, ok := allowedSet[credentialacq.NormalizeFlowKind(helper)]; !ok {
			return false
		}
	}
	return true
}

func planVisible(plan credentialacq.ModePlan, flags map[string]bool) bool {
	if !plan.IsEnabled || plan.IsExperimental || plan.RiskLevel == credentialacq.RiskLevelBlocked {
		return false
	}
	if plan.FeatureFlag != "" && !flags[plan.FeatureFlag] {
		return false
	}
	return true
}

func typedFieldContract(fields []credentialacq.FieldSpec) bool {
	if len(fields) == 0 {
		return false
	}
	hasTypedField := false
	for _, field := range fields {
		if field.Name == "" || field.Kind == "" || field.Redaction == "" || field.Group == "" {
			return false
		}
		if field.Kind != credentialacq.FieldKindJSONObject || field.Name != "credentials" {
			hasTypedField = true
		}
	}
	return hasTypedField
}

func modeFromPlan(plan credentialacq.ModePlan, helpers []credentialacq.FlowKind, readiness servingcapability.CheckResult) Mode {
	riskReasons := append([]string(nil), plan.RiskReasons...)
	if riskReasons == nil {
		riskReasons = []string{}
	}
	return Mode{
		Vendor: plan.Vendor, AuthMode: plan.AuthMode, FlowKind: plan.Kind,
		ClientIdentitySource: plan.ClientIdentitySource,
		ManualFirst:          plan.ManualFirst,
		LongLivedToggle:      plan.LongLivedToggle,
		AllowedHelpers:       append([]credentialacq.FlowKind(nil), helpers...),
		RequiredFields:       append([]credentialacq.FieldSpec(nil), plan.RequiredFields...),
		IsEnabled:            plan.IsEnabled,
		IsExperimental:       plan.IsExperimental,
		FeatureFlag:          plan.FeatureFlag,
		RiskLevel:            plan.RiskLevel,
		RiskReasons:          riskReasons,
		ServingReadiness: ServingReadiness{
			Family:         readiness.Family,
			ReleaseState:   readiness.ReleaseState,
			Ready:          readiness.Ready,
			Enableable:     readiness.Allowed,
			TrafficAllowed: readiness.TrafficAllowed,
			Status:         readiness.Status,
			Action:         readiness.Action,
			Reason:         readiness.Reason,
		},
	}
}

func defaultServingCapabilityEvaluator() *servingcapability.Evaluator {
	return servingcapability.NewEvaluator(servingcapability.DefaultContractRegistry(), servingcapability.RuntimeSources{
		ProviderAdapters:   registrydefault.Build(),
		ResponseParsers:    gateway.BuildDefaultProtocolAdapterRegistry(),
		RequestMarshal:     gateway.HCSFRequestMarshalShape,
		StreamScanners:     gateway.BuildDefaultStreamScannerRegistry(),
		PoolVendor:         pool.VendorFromProtocolFamily,
		TransportModes:     servingTransportModes,
		CredentialHandlers: credentialstore.DefaultHandlerRegistry(),
	})
}

func servingTransportModes(platform string) []string {
	modes := transport.AllowedModesForProvider(transport.ProviderCode(platform))
	out := make([]string, 0, len(modes))
	for _, mode := range modes {
		out = append(out, string(mode))
	}
	return out
}
