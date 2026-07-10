package adminhttp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
	"github.com/BloomingProsperity/HUAKAI/internal/servingcapability"
)

func TestDefaultCatalogMakesNonServingStatesExplicit(t *testing.T) {
	setAdminSessionAdapterEnvironment(t, false)
	catalog := DefaultCatalog()

	tests := []struct {
		name     string
		vendor   string
		authMode string
		status   servingcapability.ReadinessStatus
		reason   string
	}{
		{
			name: "Antigravity wire 未验证", vendor: credentialstore.VendorAntigravity,
			authMode: credentialstore.AuthModeOAuth,
			status:   servingcapability.StatusExperimentalWireUnverified,
			reason:   servingcapability.ReasonExperimentalWireUnverified,
		},
		{
			name: "Code Assist 实验态", vendor: credentialstore.VendorGemini,
			authMode: credentialstore.AuthModeCodeAssist,
			status:   servingcapability.StatusExperimental,
			reason:   servingcapability.ReasonAdapterNotRegistered,
		},
		{
			name: "仅采集无 serving contract", vendor: credentialstore.VendorGemini,
			authMode: credentialstore.AuthModeOAuth,
			status:   servingcapability.StatusCollectOnly,
			reason:   "released_or_experimental_contract_missing",
		},
	}
	for _, authMode := range []string{credentialstore.AuthModeClaudeAIOAuth, credentialstore.AuthModeClaudeCode} {
		mode := findCatalogMode(t, catalog, credentialstore.VendorAnthropic, authMode)
		got := mode.ServingReadiness
		if !got.Ready || !got.Enableable || !got.TrafficAllowed || got.Status != servingcapability.StatusReady || got.Reason != "" {
			t.Fatalf("Anthropic/%s readiness=%+v, want released ready", authMode, got)
		}
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mode := findCatalogMode(t, catalog, tc.vendor, tc.authMode)
			got := mode.ServingReadiness
			if got.Ready || got.Enableable || got.TrafficAllowed || got.Status != tc.status || got.Reason != tc.reason {
				t.Fatalf("%s/%s readiness=%+v, want status=%s reason=%s 且三项 false",
					tc.vendor, tc.authMode, got, tc.status, tc.reason)
			}
		})
	}

	// scaffold 计划默认仍按 feature/risk 规则隐藏；显式可见时也必须带 scaffold，
	// 不能因 provider adapter 已注册而被抬成 ready。
	registry := credentialstore.NewHandlerRegistry()
	registerAPIKeyHandler(t, registry, credentialstore.VendorMistral, credentialstore.AuthModeAPIKey)
	scaffoldCatalog := BuildCatalog(CatalogInput{
		Plans:    []credentialacq.ModePlan{visibleAPIKeyPlan(credentialstore.VendorMistral, credentialstore.AuthModeAPIKey)},
		Registry: registry,
	})
	scaffold := findCatalogMode(t, scaffoldCatalog, credentialstore.VendorMistral, credentialstore.AuthModeAPIKey)
	if scaffold.ServingReadiness.Ready || scaffold.ServingReadiness.Status != servingcapability.StatusScaffold {
		t.Fatalf("scaffold mode 被误报 ready: %+v", scaffold.ServingReadiness)
	}
}

func TestProviderCatalogEnabledWriteRequiresCurrentProcessReadiness(t *testing.T) {
	setAdminSessionAdapterEnvironment(t, false)

	tests := []struct {
		name   string
		family string
		reason string
	}{
		{
			name: "env off family", family: registrydefault.ProtocolGeminiCodeAssist,
			reason: servingcapability.ReasonAdapterNotRegistered,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name+" enabled 拒绝", func(t *testing.T) {
			store := newProviderCatalogQueriesStub()
			rec := invokeProviderCatalogRequest(t, AdminProviderCatalogDeps{
				Auth: apiKeyAuthStub{ident: tenantOperator(7)}, Store: store,
			}, http.MethodPost, "/admin/v1/providers", map[string]any{
				"code": "readiness-probe", "display_name": "Readiness Probe",
				"upstream_protocol": tc.family, "enabled": true,
			})

			assertProviderCatalogStatus(t, rec, http.StatusUnprocessableEntity)
			var body struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			decodeProviderCatalogBody(t, rec, &body)
			if body.Error.Code != "provider_serving_not_ready" || body.Error.Message != tc.reason {
				t.Fatalf("readiness error=%+v want code=provider_serving_not_ready reason=%s", body.Error, tc.reason)
			}
			if store.createCalls != 0 {
				t.Fatalf("未闭合 enabled 写入触碰 store: calls=%d arg=%+v", store.createCalls, store.createArg)
			}
		})
	}

	t.Run("Claude session enabled 正向写入", func(t *testing.T) {
		store := newProviderCatalogQueriesStub()
		rec := invokeProviderCatalogRequest(t, AdminProviderCatalogDeps{
			Auth: apiKeyAuthStub{ident: tenantOperator(7)}, Store: store,
		}, http.MethodPost, "/admin/v1/providers", map[string]any{
			"code": "claude-session", "display_name": "Claude Session",
			"upstream_protocol": registrydefault.ProtocolAnthropicClaudeSession, "enabled": true,
		})
		assertProviderCatalogStatus(t, rec, http.StatusCreated)
		if store.createCalls != 1 || !store.createArg.Enabled || store.createArg.UpstreamProtocol != registrydefault.ProtocolAnthropicClaudeSession {
			t.Fatalf("Claude session enabled 写入未落到 store: calls=%d arg=%+v", store.createCalls, store.createArg)
		}
	})
}

// 变异：删掉 Claude session 默认注册后，canonical 与闭合门会产生相反结论，
// 本测试必须因 disabled 写入不再落库而变红。
func TestProviderCatalogDisabledWriteBypassesOnlyCanonicalFamilies(t *testing.T) {
	setAdminSessionAdapterEnvironment(t, false)
	tests := []struct {
		name      string
		family    string
		canonical bool
		reason    string
	}{
		{
			name: "canonical env-off family", family: registrydefault.ProtocolGeminiCodeAssist,
			canonical: true,
		},
		{
			name: "released Claude session family", family: registrydefault.ProtocolAnthropicClaudeSession,
			canonical: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isKnownProviderCatalogProtocol(tc.family); got != tc.canonical {
				t.Fatalf("canonical 判定=%t want %t", got, tc.canonical)
			}
			store := newProviderCatalogQueriesStub()
			rec := invokeProviderCatalogRequest(t, AdminProviderCatalogDeps{
				Auth: apiKeyAuthStub{ident: tenantOperator(7)}, Store: store,
			}, http.MethodPost, "/admin/v1/providers", map[string]any{
				"code": "readiness-probe", "display_name": "Readiness Probe",
				"upstream_protocol": tc.family, "enabled": false,
			})

			if tc.canonical {
				assertProviderCatalogStatus(t, rec, http.StatusCreated)
				if store.createCalls != 1 || store.createArg.Enabled || store.createArg.UpstreamProtocol != tc.family {
					t.Fatalf("canonical disabled 写入未保留: calls=%d arg=%+v", store.createCalls, store.createArg)
				}
				return
			}

			assertProviderCatalogStatus(t, rec, http.StatusUnprocessableEntity)
			var body struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			decodeProviderCatalogBody(t, rec, &body)
			if body.Error.Code != "provider_serving_not_ready" || body.Error.Message != tc.reason {
				t.Fatalf("contract-only disabled error=%+v want reason=%s", body.Error, tc.reason)
			}
			if store.createCalls != 0 {
				t.Fatalf("contract-only disabled 写入触碰 store: calls=%d arg=%+v", store.createCalls, store.createArg)
			}
		})
	}
}

// 变异：删除 mutation 层的 canonical 精确值守卫，ContractRegistry.Lookup
// 会把大小写变体归一后放行，本测试必须因出现 2xx/store 调用而变红。
func TestProviderCatalogMutationRejectsNonCanonicalProtocolValue(t *testing.T) {
	setAdminSessionAdapterEnvironment(t, false)
	tests := []struct {
		name   string
		method string
		target string
		body   map[string]any
	}{
		{
			name: "create uppercase", method: http.MethodPost, target: "/admin/v1/providers",
			body: map[string]any{
				"code": "bad-family", "display_name": "Bad Family",
				"upstream_protocol": "OPENAI_CHAT", "enabled": true,
			},
		},
		{
			name: "update padded", method: http.MethodPut, target: "/admin/v1/providers/bad-family",
			body: map[string]any{
				"display_name": "Bad Family", "upstream_protocol": " openai_chat ", "enabled": true,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newProviderCatalogQueriesStub()
			rec := invokeProviderCatalogRequest(t, AdminProviderCatalogDeps{
				Auth: apiKeyAuthStub{ident: tenantOperator(7)}, Store: store,
			}, tc.method, tc.target, tc.body)

			assertProviderCatalogStatus(t, rec, http.StatusBadRequest)
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			decodeProviderCatalogBody(t, rec, &body)
			if body.Error.Code != "invalid_upstream_protocol" {
				t.Fatalf("error code=%q want invalid_upstream_protocol", body.Error.Code)
			}
			if store.createCalls != 0 || store.updateCalls != 0 {
				t.Fatalf("非 canonical 写入触碰 store: create=%d update=%d", store.createCalls, store.updateCalls)
			}
		})
	}
}

func TestProviderCatalogReplicateImageEnabledWriteUsesImageLane(t *testing.T) {
	setAdminSessionAdapterEnvironment(t, false)
	store := newProviderCatalogQueriesStub()
	rec := invokeProviderCatalogRequest(t, AdminProviderCatalogDeps{
		Auth: apiKeyAuthStub{ident: tenantOperator(7)}, Store: store,
	}, http.MethodPost, "/admin/v1/providers", map[string]any{
		"code": "replicate", "display_name": "Replicate",
		"upstream_protocol": registrydefault.ProtocolReplicateImage, "enabled": true,
	})

	assertProviderCatalogStatus(t, rec, http.StatusCreated)
	if store.createCalls != 1 || !store.createArg.Enabled || store.createArg.UpstreamProtocol != registrydefault.ProtocolReplicateImage {
		t.Fatalf("image lane enabled 写入未落到 store: calls=%d arg=%+v", store.createCalls, store.createArg)
	}
}

// 变异：若 image 车道的非 blocking 规则被错误套到 chat_hcsf，缺少 response
// parser 的 openai_chat 会被放行，本测试必须因未返回 422 而变红。
func TestProviderCatalogChatResponseParserGapStaysHTTP422(t *testing.T) {
	evaluator := servingcapability.NewEvaluator(nil, servingcapability.RuntimeSources{
		ProviderAdapters: registrydefault.Build(),
		ResponseParsers:  gateway.NewStaticProtocolAdapterRegistry(),
		RequestMarshal:   gateway.HCSFRequestMarshalShape,
		StreamScanners:   gateway.BuildDefaultStreamScannerRegistry(),
		PoolVendor:       pool.VendorFromProtocolFamily,
		TransportModes:   func(string) []string { return []string{"standard"} },
	})
	rec := httptest.NewRecorder()
	if requireProviderCatalogServingReadinessUsing(
		rec, registrydefault.ProtocolOpenAIChat, true, evaluator.RequireProviderConfigEnabled,
	) {
		t.Fatal("缺 response parser 的 chat family 不得通过写入闭合门")
	}

	assertProviderCatalogStatus(t, rec, http.StatusUnprocessableEntity)
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeProviderCatalogBody(t, rec, &body)
	if body.Error.Code != "provider_serving_not_ready" || body.Error.Message != servingcapability.ReasonResponseParserMissing {
		t.Fatalf("response parser gap error=%+v", body.Error)
	}
}

func findCatalogMode(t *testing.T, catalog Catalog, vendor, authMode string) Mode {
	t.Helper()
	key := credentialstore.ModeKey(vendor, authMode)
	for _, mode := range catalog.Modes {
		if credentialstore.ModeKey(mode.Vendor, mode.AuthMode) == key {
			return mode
		}
	}
	t.Fatalf("catalog 缺少 mode %s: %+v", key, catalog.Modes)
	return Mode{}
}

func setAdminSessionAdapterEnvironment(t *testing.T, enabled bool) {
	t.Helper()
	value := "false"
	if enabled {
		value = "true"
	}
	for _, env := range []string{
		"HUAKAI_ENABLE_GEMINI_CODE_ASSIST_ADAPTER",
		"HUAKAI_ENABLE_CURSOR_SESSION_ADAPTER",
		"HUAKAI_ENABLE_COPILOT_SESSION_ADAPTER",
		"HUAKAI_ENABLE_GEMINI_ADVANCED_SESSION_ADAPTER",
		"HUAKAI_ENABLE_ANTIGRAVITY_SESSION_ADAPTER",
		"HUAKAI_ENABLE_KIRO_SESSION_ADAPTER",
		"HUAKAI_ENABLE_WINDSURF_SESSION_ADAPTER",
	} {
		t.Setenv(env, value)
	}
}
