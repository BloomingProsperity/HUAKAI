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
	Plans        []credentialacq.ModePlan
	Registry     *credentialstore.HandlerRegistry
	Entries      []Entry
	FeatureFlags map[string]bool
}

type Provider interface {
	Catalog(context.Context) (Catalog, error)
}

type StaticProvider struct {
	Input CatalogInput
}

func DefaultProvider() StaticProvider {
	return StaticProvider{Input: CatalogInput{
		Plans:    credentialacq.DefaultModePlans(),
		Registry: credentialstore.DefaultHandlerRegistry(),
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
		Plans:    credentialacq.DefaultModePlans(),
		Registry: credentialstore.DefaultHandlerRegistry(),
	})
}

func BuildCatalog(in CatalogInput) Catalog {
	registry := in.Registry
	if registry == nil {
		registry = credentialstore.DefaultHandlerRegistry()
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
		out.Modes = append(out.Modes, modeFromPlan(plan, helpers))
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
		hiddenFlagChannel(credentialstore.VendorDeepSeek, credentialstore.AuthModeAPIKey),
		hiddenFlagChannel(credentialstore.VendorGrok, credentialstore.AuthModeAPIKey),
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

func modeFromPlan(plan credentialacq.ModePlan, helpers []credentialacq.FlowKind) Mode {
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
	}
}
