package adminhttp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestCatalogHidesEntryWithoutModePlan(t *testing.T) {
	plans := []credentialacq.ModePlan{visibleAPIKeyPlan("openai", "api_key")}
	registry := credentialstore.NewHandlerRegistry()
	registerAPIKeyHandler(t, registry, "openai", "api_key")
	registerAPIKeyHandler(t, registry, "ghost", "api_key")

	catalog := BuildCatalog(CatalogInput{
		Plans:    plans,
		Registry: registry,
		Entries: []Entry{
			{Vendor: "openai", AuthMode: "api_key"},
			{
				Vendor: "ghost", AuthMode: "api_key", FlowKind: credentialacq.FlowKindPaste,
				ClientIdentitySource: credentialacq.ClientSourceNone,
				AllowedHelpers:       []credentialacq.FlowKind{credentialacq.FlowKindPaste},
				RequiredFields:       visibleAPIKeyPlan("ghost", "api_key").RequiredFields,
				IsEnabled:            true,
				RiskLevel:            credentialacq.RiskLevelLow,
			},
		},
	})

	if hasMode(catalog, "ghost", "api_key") {
		t.Fatalf("entry without ModePlan became visible: %+v", catalog.Modes)
	}
	if !hasMode(catalog, "openai", "api_key") {
		t.Fatalf("valid ModePlan was hidden: %+v", catalog.Modes)
	}
}

func TestCatalogRejectsAdvertisedHelperOutsideModePlanAllowlist(t *testing.T) {
	plan := visibleOAuthPlan("openai", "chatgpt_oauth")
	registry := credentialstore.NewHandlerRegistry()
	registerSessionHandler(t, registry, "openai", "chatgpt_oauth")

	catalog := BuildCatalog(CatalogInput{
		Plans:    []credentialacq.ModePlan{plan},
		Registry: registry,
		Entries: []Entry{{
			Vendor:         "openai",
			AuthMode:       "chatgpt_oauth",
			AllowedHelpers: []credentialacq.FlowKind{credentialacq.FlowKindOAuth, credentialacq.FlowKindPaste},
		}},
	})

	if hasMode(catalog, "openai", "chatgpt_oauth") {
		t.Fatalf("OAuth-only mode with advertised paste helper must be hidden: %+v", catalog.Modes)
	}
}

func TestCatalogDefaultFailClosedForExperimentalAndFlaggedModes(t *testing.T) {
	registry := credentialstore.NewHandlerRegistry()
	registerAPIKeyHandler(t, registry, "safe", "api_key")
	registerAPIKeyHandler(t, registry, "preview", "api_key")
	registerAPIKeyHandler(t, registry, "flagged", "api_key")

	experimental := visibleAPIKeyPlan("preview", "api_key")
	experimental.IsExperimental = true
	flagged := visibleAPIKeyPlan("flagged", "api_key")
	flagged.FeatureFlag = "account_modes.preview"

	defaultCatalog := BuildCatalog(CatalogInput{
		Plans: []credentialacq.ModePlan{
			visibleAPIKeyPlan("safe", "api_key"),
			experimental,
			flagged,
		},
		Registry: registry,
	})
	if !hasMode(defaultCatalog, "safe", "api_key") {
		t.Fatalf("safe mode hidden: %+v", defaultCatalog.Modes)
	}
	if hasMode(defaultCatalog, "preview", "api_key") || hasMode(defaultCatalog, "flagged", "api_key") {
		t.Fatalf("experimental or closed-flag mode leaked by default: %+v", defaultCatalog.Modes)
	}

	flaggedOnly := BuildCatalog(CatalogInput{
		Plans: []credentialacq.ModePlan{
			visibleAPIKeyPlan("safe", "api_key"),
			experimental,
			flagged,
		},
		Registry:     registry,
		FeatureFlags: map[string]bool{"account_modes.preview": true},
	})
	if !hasMode(flaggedOnly, "flagged", "api_key") {
		t.Fatalf("enabled feature flag should expose non-experimental mode: %+v", flaggedOnly.Modes)
	}
	if hasMode(flaggedOnly, "preview", "api_key") {
		t.Fatalf("experimental mode must stay hidden even when other flags are on: %+v", flaggedOnly.Modes)
	}
}

func TestDefaultCatalogHidesPostLaunchIDEAccountModes(t *testing.T) {
	catalog := DefaultCatalog()
	for _, mode := range []struct {
		vendor   string
		authMode string
	}{
		{vendor: credentialstore.VendorCopilot, authMode: credentialstore.AuthModeCopilotOAuth},
		{vendor: credentialstore.VendorWindsurf, authMode: credentialstore.AuthModeOAuth},
		{vendor: "cursor", authMode: credentialstore.AuthModeOAuth},
	} {
		if hasMode(catalog, mode.vendor, mode.authMode) {
			t.Fatalf("上线后封存模式 %s/%s 不得出现在默认账号目录", mode.vendor, mode.authMode)
		}
	}
}

func TestVisibleModesExposeTypedRequiredFields(t *testing.T) {
	catalog := DefaultCatalog()
	if len(catalog.Modes) < 19 {
		t.Fatalf("default catalog shrank below existing 19 modes: got %d", len(catalog.Modes))
	}
	for _, mode := range catalog.Modes {
		if len(mode.RequiredFields) == 0 {
			t.Fatalf("%s/%s has no required_fields", mode.Vendor, mode.AuthMode)
		}
		var typed bool
		for _, field := range mode.RequiredFields {
			if field.Kind == "" {
				t.Fatalf("%s/%s field %q has empty kind", mode.Vendor, mode.AuthMode, field.Name)
			}
			if field.Kind != credentialacq.FieldKindJSONObject || field.Name != "credentials" {
				typed = true
			}
		}
		if !typed {
			t.Fatalf("%s/%s exposes only bare JSON credentials: %+v", mode.Vendor, mode.AuthMode, mode.RequiredFields)
		}
	}
}

func TestCatalogRejectsMalformedTrailingField(t *testing.T) {
	plan := visibleAPIKeyPlan("openai", "api_key")
	plan.RequiredFields = append(plan.RequiredFields, credentialacq.FieldSpec{Name: "broken"})
	registry := credentialstore.NewHandlerRegistry()
	registerAPIKeyHandler(t, registry, "openai", "api_key")

	catalog := BuildCatalog(CatalogInput{
		Plans:    []credentialacq.ModePlan{plan},
		Registry: registry,
	})
	if hasMode(catalog, "openai", "api_key") {
		t.Fatalf("mode with malformed trailing field must be hidden: %+v", catalog.Modes)
	}
}

func TestCatalogEncodesEmptyRiskReasonsAsArray(t *testing.T) {
	body, err := json.Marshal(DefaultCatalog())
	if err != nil {
		t.Fatalf("marshal catalog: %v", err)
	}
	if strings.Contains(string(body), `"risk_reasons":null`) {
		t.Fatalf("risk_reasons must encode as an array, got %s", string(body))
	}
	if !strings.Contains(string(body), `"risk_reasons":[]`) {
		t.Fatalf("expected at least one empty risk_reasons array, got %s", string(body))
	}
}

func TestCatalogDoesNotAdvertiseAzureMockTokenEndpoint(t *testing.T) {
	catalog := DefaultCatalog()
	var azure *Mode
	for i := range catalog.Modes {
		if catalog.Modes[i].Vendor == credentialstore.VendorOpenAI && catalog.Modes[i].AuthMode == credentialstore.AuthModeAzure {
			azure = &catalog.Modes[i]
			break
		}
	}
	if azure == nil {
		t.Fatalf("openai/azure mode missing from default catalog")
	}
	for _, field := range azure.RequiredFields {
		if field.Name == "mock_token_endpoint" {
			t.Fatalf("openai/azure catalog must not advertise mock_token_endpoint: %+v", azure.RequiredFields)
		}
	}
}

func TestRequestedChannelDispositionsCoverOpenAICompatibleAndIDESubscriptions(t *testing.T) {
	dispositions := RequestedChannelDispositions()
	// 三段审定:enabled=官 key 厂商(迁移 0169 已放行存储约束,Owner 2026-07-02 指派接入);
	// hidden=全球推理托管云(Owner 明确不接,存储约束仍未放行);roadmap=IDE 订阅类。
	wantEnabled := []string{
		"deepseek/api_key",
		"grok/api_key",
		"kimi/api_key",
		"qwen/api_key",
		"glm/api_key",
		"yi/api_key",
		"baichuan/api_key",
		"doubao/api_key",
		"minimax/api_key",
		"ernie/api_key",
		"hunyuan/api_key",
		"step/api_key",
	}
	wantHidden := []string{
		"openrouter/api_key",
		"mistral/api_key",
		"groqcloud/api_key",
		"together/api_key",
		"perplexity/api_key",
		"fireworks/api_key",
	}
	wantRoadmap := []string{
		"cursor/oauth",
		"kiro/aws_sso",
	}
	total := len(wantEnabled) + len(wantHidden) + len(wantRoadmap)
	if len(dispositions) != total {
		t.Fatalf("requested channel disposition count=%d want %d: %+v", len(dispositions), total, dispositions)
	}
	seen := map[string]ChannelDisposition{}
	for _, d := range dispositions {
		seen[credentialstore.ModeKey(d.Vendor, d.AuthMode)] = d
		if d.Disposition == "" {
			t.Fatalf("empty disposition for %+v", d)
		}
	}
	for _, key := range append(append(append([]string{}, wantEnabled...), wantHidden...), wantRoadmap...) {
		if _, ok := seen[key]; !ok {
			t.Fatalf("missing requested channel disposition %s; got keys=%v", key, keys(seen))
		}
	}
	catalog := DefaultCatalog()
	for _, key := range wantEnabled {
		if seen[key].Disposition != DispositionEnabled {
			t.Fatalf("%s disposition=%q want enabled(存储约束已由 0169 放行)", key, seen[key].Disposition)
		}
		// 判别性断言:enabled 渠道必须真的出现在默认目录里(计划提升 + handlerSpec 注册双条件,
		// 二者缺一 BuildCatalog 都会将其剔除——变异「把计划改回 hidden」或「删 handlerSpec」此断言即红)。
		if !hasMode(catalog, strings.TrimSuffix(key, "/api_key"), credentialstore.AuthModeAPIKey) {
			t.Fatalf("%s 应在默认目录可见(0169 已放行 + handlerSpec 已注册),实际缺席", key)
		}
	}
	for _, key := range wantHidden {
		if seen[key].Disposition != DispositionHiddenFlag || seen[key].FeatureFlag != "account_modes.openai_compatible" || !seen[key].IsExperimental {
			t.Fatalf("%s disposition=%q flag=%q experimental=%v want hidden flag", key, seen[key].Disposition, seen[key].FeatureFlag, seen[key].IsExperimental)
		}
		if hasMode(catalog, strings.TrimSuffix(key, "/api_key"), credentialstore.AuthModeAPIKey) {
			t.Fatalf("%s must not be visible in the default catalog while storage constraints are unreleased", key)
		}
	}
	for _, key := range wantRoadmap {
		if seen[key].Disposition != DispositionMandatoryRoadmap || !seen[key].IsExperimental {
			t.Fatalf("%s disposition=%q experimental=%v want roadmap+experimental", key, seen[key].Disposition, seen[key].IsExperimental)
		}
	}
}

func visibleAPIKeyPlan(vendor, mode string) credentialacq.ModePlan {
	return credentialacq.ModePlan{
		Vendor:               vendor,
		AuthMode:             mode,
		Kind:                 credentialacq.FlowKindPaste,
		ClientIdentitySource: credentialacq.ClientSourceNone,
		AllowedHelpers:       []credentialacq.FlowKind{credentialacq.FlowKindPaste},
		RequiredFields: []credentialacq.FieldSpec{{
			Name:      "api_key",
			Kind:      credentialacq.FieldKindSecret,
			Required:  true,
			Redaction: credentialacq.RedactionSecret,
			Group:     credentialacq.FieldGroupCredential,
		}},
		IsEnabled: true,
		RiskLevel: credentialacq.RiskLevelLow,
	}
}

func visibleOAuthPlan(vendor, mode string) credentialacq.ModePlan {
	return credentialacq.ModePlan{
		Vendor:               vendor,
		AuthMode:             mode,
		Kind:                 credentialacq.FlowKindOAuth,
		ClientIdentitySource: credentialacq.ClientSourcePublicCLI,
		AllowedHelpers:       []credentialacq.FlowKind{credentialacq.FlowKindOAuth},
		RequiredFields: []credentialacq.FieldSpec{
			{Name: "session_token", Kind: credentialacq.FieldKindSecret, OneOfGroup: "runtime_token", Redaction: credentialacq.RedactionSecret, Group: credentialacq.FieldGroupCredential},
			{Name: "access_token", Kind: credentialacq.FieldKindSecret, OneOfGroup: "runtime_token", Redaction: credentialacq.RedactionSecret, Group: credentialacq.FieldGroupCredential},
		},
		IsEnabled: true,
		RiskLevel: credentialacq.RiskLevelMedium,
	}
}

func registerAPIKeyHandler(t *testing.T, registry *credentialstore.HandlerRegistry, vendor, mode string) {
	t.Helper()
	if err := registry.Register(testModeHandler{vendor: vendor, authMode: mode, runtimeKind: credentialstore.RuntimeAPIKey}); err != nil {
		t.Fatalf("register %s/%s: %v", vendor, mode, err)
	}
}

func registerSessionHandler(t *testing.T, registry *credentialstore.HandlerRegistry, vendor, mode string) {
	t.Helper()
	if err := registry.Register(testModeHandler{vendor: vendor, authMode: mode, runtimeKind: credentialstore.RuntimeSessionToken}); err != nil {
		t.Fatalf("register %s/%s: %v", vendor, mode, err)
	}
}

type testModeHandler struct {
	vendor      string
	authMode    string
	runtimeKind string
}

func (h testModeHandler) Vendor() string               { return h.vendor }
func (h testModeHandler) AuthMode() string             { return h.authMode }
func (h testModeHandler) RuntimeKind() string          { return h.runtimeKind }
func (h testModeHandler) Refreshable() bool            { return false }
func (h testModeHandler) AllowGrace() bool             { return false }
func (h testModeHandler) ValidatePayload([]byte) error { return nil }
func (h testModeHandler) RuntimeMaterial([]byte) (credentialstore.RuntimeMaterial, error) {
	return credentialstore.RuntimeMaterial{Kind: h.runtimeKind, Value: "secret"}, nil
}

func hasMode(catalog Catalog, vendor, authMode string) bool {
	key := credentialstore.ModeKey(vendor, authMode)
	for _, mode := range catalog.Modes {
		if credentialstore.ModeKey(mode.Vendor, mode.AuthMode) == key {
			return true
		}
	}
	return false
}

func keys(dispositions map[string]ChannelDisposition) []string {
	out := make([]string, 0, len(dispositions))
	for key := range dispositions {
		out = append(out, key)
	}
	return out
}
