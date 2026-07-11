package servingcapability

import (
	"context"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

type providerAdapterRegistryFunc func(string) (provider.Adapter, error)

func (f providerAdapterRegistryFunc) For(family string) (provider.Adapter, error) {
	return f(family)
}

type protocolAdapterRegistryFunc func(string) (proto.UpstreamAdapter, error)

func (f protocolAdapterRegistryFunc) For(family string) (proto.UpstreamAdapter, error) {
	return f(family)
}

type streamScannerRegistryFunc func(string) (gateway.StreamScanner, error)

func (f streamScannerRegistryFunc) For(family string) (gateway.StreamScanner, error) {
	return f(family)
}

type pricingProbeFunc func(context.Context, string, string, []string) (bool, string)

func (f pricingProbeFunc) ProbePricing(ctx context.Context, family, vendor string, aliases []string) (bool, string) {
	return f(ctx, family, vendor, aliases)
}

func productionRuntimeSources(adapters ProviderAdapterRegistry) RuntimeSources {
	return RuntimeSources{
		ProviderAdapters:   adapters,
		ResponseParsers:    gateway.BuildDefaultProtocolAdapterRegistry(),
		RequestMarshal:     gateway.HCSFRequestMarshalShape,
		StreamScanners:     gateway.BuildDefaultStreamScannerRegistry(),
		PoolVendor:         pool.VendorFromProtocolFamily,
		TransportModes:     productionTransportModes,
		CredentialHandlers: credentialstore.DefaultHandlerRegistry(),
	}
}

func productionTransportModes(platform string) []string {
	modes := transport.AllowedModesForProvider(transport.ProviderCode(platform))
	out := make([]string, 0, len(modes))
	for _, mode := range modes {
		out = append(out, string(mode))
	}
	return out
}

func TestEvaluateCatalogModeDistinguishesAcquisitionGap(t *testing.T) {
	evaluator := NewEvaluator(nil, productionRuntimeSources(registrydefault.Build()))
	completeInput := CatalogModeInput{
		Vendor:                        credentialstore.VendorOpenAI,
		AuthMode:                      credentialstore.AuthModeAPIKey,
		FinalizerPresent:              true,
		AcquisitionDispositionPresent: true,
		RefreshDispositionPresent:     true,
	}
	complete := evaluator.EvaluateCatalogMode(completeInput)
	missingInput := completeInput
	missingInput.AcquisitionDispositionPresent = false
	missing := evaluator.EvaluateCatalogMode(missingInput)

	assertCapabilityTransition(t, complete, missing,
		StatusReady, ActionAllow, StatusNotReady, ActionHideReadOnly)
	assertMissingStation(t, missing, StationAcquisition, "acquisition_disposition_missing")
}

func TestEvaluateProviderConfigDistinguishesEveryServingStationGap(t *testing.T) {
	baseRegistry := registrydefault.Build()
	base := productionRuntimeSources(baseRegistry)
	complete := NewEvaluator(nil, base).EvaluateProviderConfig(ProviderConfigInput{
		Family: registrydefault.ProtocolOpenAIChat, Enabled: true,
	})
	if !complete.Ready || complete.Status != StatusReady || complete.Action != ActionAllow {
		t.Fatalf("完整 provider fixture 未 ready: %+v", complete)
	}

	tests := []struct {
		name    string
		station StationID
		reason  string
		mutate  func(RuntimeSources) RuntimeSources
	}{
		{
			name: "adapter 未注册", station: StationProviderAdapter, reason: ReasonAdapterNotRegistered,
			mutate: func(s RuntimeSources) RuntimeSources {
				original := s.ProviderAdapters
				s.ProviderAdapters = providerAdapterRegistryFunc(func(family string) (provider.Adapter, error) {
					if family == registrydefault.ProtocolOpenAIChat {
						return nil, provider.ErrAdapterNotRegistered
					}
					return original.For(family)
				})
				return s
			},
		},
		{
			name: "response parser 缺失", station: StationResponseParser, reason: ReasonResponseParserMissing,
			mutate: func(s RuntimeSources) RuntimeSources {
				original := s.ResponseParsers
				s.ResponseParsers = protocolAdapterRegistryFunc(func(family string) (proto.UpstreamAdapter, error) {
					if family == registrydefault.ProtocolOpenAIChat {
						return nil, gateway.ErrUnknownProtocolFamily
					}
					return original.For(family)
				})
				return s
			},
		},
		{
			name: "request marshal 缺失", station: StationRequestMarshal, reason: ReasonRequestMarshalMissing,
			mutate: func(s RuntimeSources) RuntimeSources {
				original := s.RequestMarshal
				s.RequestMarshal = func(family string) (string, bool) {
					if family == registrydefault.ProtocolOpenAIChat {
						return "", false
					}
					return original(family)
				}
				return s
			},
		},
		{
			name: "stream scanner 缺失", station: StationStreamScanner, reason: ReasonStreamScannerMissing,
			mutate: func(s RuntimeSources) RuntimeSources {
				original := s.StreamScanners
				s.StreamScanners = streamScannerRegistryFunc(func(family string) (gateway.StreamScanner, error) {
					if family == registrydefault.ProtocolOpenAIChat {
						return nil, gateway.ErrUnknownStreamScanner
					}
					return original.For(family)
				})
				return s
			},
		},
		{
			name: "pool vendor 缺失", station: StationPoolVendor, reason: ReasonPoolVendorMissing,
			mutate: func(s RuntimeSources) RuntimeSources {
				s.PoolVendor = func(string) string { return "" }
				return s
			},
		},
		{
			name: "transport policy 缺失", station: StationTransportPolicy, reason: ReasonTransportPolicyMissing,
			mutate: func(s RuntimeSources) RuntimeSources {
				s.TransportModes = func(string) []string { return nil }
				return s
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			missing := NewEvaluator(nil, tc.mutate(base)).EvaluateProviderConfig(ProviderConfigInput{
				Family: registrydefault.ProtocolOpenAIChat, Enabled: true,
			})
			assertCapabilityTransition(t, complete, missing,
				StatusReady, ActionAllow, StatusNotReady, ActionRejectWrite)
			assertMissingStation(t, missing, tc.station, tc.reason)
			if missing.Reason != tc.reason {
				t.Fatalf("reason=%q want %q; result=%+v", missing.Reason, tc.reason, missing)
			}
		})
	}
}

// 变异：若 TrafficAllowed 回退为仅跟随 Ready，禁用子项必须因被误报可承载流量而变红。
func TestEvaluateProviderConfigTrafficRequiresEnabledReadyConfig(t *testing.T) {
	evaluator := NewEvaluator(nil, productionRuntimeSources(registrydefault.Build()))
	disabled := evaluator.EvaluateProviderConfig(ProviderConfigInput{
		Family: registrydefault.ProtocolOpenAIChat, Enabled: false,
	})
	if !disabled.Ready || !disabled.Allowed || disabled.TrafficAllowed {
		t.Fatalf("完整但禁用的 provider 结论错误: %+v", disabled)
	}

	enabled := evaluator.EvaluateProviderConfig(ProviderConfigInput{
		Family: registrydefault.ProtocolOpenAIChat, Enabled: true,
	})
	if !enabled.Ready || !enabled.Allowed || !enabled.TrafficAllowed {
		t.Fatalf("完整且启用的 provider 结论错误: %+v", enabled)
	}
}

// 变异：把 replicate_image 的 Lane 改回 chat_hcsf，或把任一图片真实依赖
// 标成非 blocking；本测试必须分别因 ready 结论或站点阻断性漂移而变红。
func TestEvaluateProviderConfigImageLaneUsesOnlyImageRuntimeStations(t *testing.T) {
	const family = registrydefault.ProtocolReplicateImage
	if shape, ok := gateway.HCSFRequestMarshalShape(family); ok {
		t.Fatalf("测试前提失效: %s 意外接入 HCSF request marshal, shape=%q", family, shape)
	}

	sources := productionRuntimeSources(registrydefault.Build())
	// 该测试只隔离验证图片车道站点；生产契约是 scaffold，因此测试注册表临时把
	// 发布态提升为 Released，避免发布决策遮蔽 Lane 的判别性。
	contracts := DefaultContracts()
	for i := range contracts {
		if contracts[i].Family == family {
			contracts[i].ReleaseState = ReleaseStateReleased
			contracts[i].ReadinessReason = ""
		}
	}
	laneRegistry := MustNewContractRegistry(contracts)
	complete := NewEvaluator(laneRegistry, sources).EvaluateProviderConfig(ProviderConfigInput{Family: family, Enabled: true})
	if !complete.Ready || !complete.Allowed || !complete.TrafficAllowed || complete.Status != StatusReady || complete.Action != ActionAllow {
		t.Fatalf("图片车道完整 fixture 未 ready: %+v", complete)
	}
	for _, id := range []StationID{StationResponseParser, StationRequestMarshal, StationStreamScanner} {
		got := findStation(t, complete, id)
		if got.Present || got.Blocking {
			t.Fatalf("图片车道 chat 站点 %s=(present=%t blocking=%t), want false/false: %+v", id, got.Present, got.Blocking, got)
		}
	}
	for _, id := range []StationID{StationProviderAdapter, StationPoolVendor, StationTransportPolicy} {
		got := findStation(t, complete, id)
		if !got.Present || !got.Blocking {
			t.Fatalf("图片车道真实站点 %s=(present=%t blocking=%t), want true/true: %+v", id, got.Present, got.Blocking, got)
		}
	}

	tests := []struct {
		name    string
		station StationID
		reason  string
		mutate  func(RuntimeSources) RuntimeSources
	}{
		{
			name: "adapter 缺失", station: StationProviderAdapter, reason: ReasonAdapterNotRegistered,
			mutate: func(s RuntimeSources) RuntimeSources {
				s.ProviderAdapters = providerAdapterRegistryFunc(func(string) (provider.Adapter, error) {
					return nil, provider.ErrAdapterNotRegistered
				})
				return s
			},
		},
		{
			name: "pool vendor 缺失", station: StationPoolVendor, reason: ReasonPoolVendorMissing,
			mutate: func(s RuntimeSources) RuntimeSources {
				s.PoolVendor = func(string) string { return "" }
				return s
			},
		},
		{
			name: "transport policy 缺失", station: StationTransportPolicy, reason: ReasonTransportPolicyMissing,
			mutate: func(s RuntimeSources) RuntimeSources {
				s.TransportModes = func(string) []string { return nil }
				return s
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			missing := NewEvaluator(laneRegistry, tc.mutate(sources)).EvaluateProviderConfig(ProviderConfigInput{Family: family, Enabled: true})
			assertCapabilityTransition(t, complete, missing,
				StatusReady, ActionAllow, StatusNotReady, ActionRejectWrite)
			assertMissingStation(t, missing, tc.station, tc.reason)
			if missing.Reason != tc.reason {
				t.Fatalf("reason=%q want %q", missing.Reason, tc.reason)
			}
		})
	}
}

func TestReleasedContractsMatchDeclaredServingLane(t *testing.T) {
	evaluator := NewEvaluator(nil, productionRuntimeSources(registrydefault.Build()))
	for _, contract := range DefaultContractRegistry().All() {
		if contract.ReleaseState != ReleaseStateReleased {
			continue
		}
		t.Run(contract.Family, func(t *testing.T) {
			wantLane := ServingLaneChatHCSF
			if contract.Family == registrydefault.ProtocolReplicateImage {
				wantLane = ServingLaneImage
			}
			if contract.Lane != wantLane {
				t.Fatalf("lane=%q want %q", contract.Lane, wantLane)
			}
			result := evaluator.EvaluateProviderConfig(ProviderConfigInput{Family: contract.Family, Enabled: true})
			if !result.Ready || !result.Allowed || !result.TrafficAllowed {
				t.Fatalf("released family 被闭合门误伤: %+v", result)
			}
			wantChatBlocking := wantLane == ServingLaneChatHCSF
			for _, id := range []StationID{StationResponseParser, StationRequestMarshal, StationStreamScanner} {
				if got := findStation(t, result, id); got.Blocking != wantChatBlocking {
					t.Fatalf("站点 %s blocking=%t want %t: %+v", id, got.Blocking, wantChatBlocking, got)
				}
			}
		})
	}
}

func TestContractRegistryServingLaneDefaultsAndValidation(t *testing.T) {
	contracts := DefaultContracts()
	contracts[0].Lane = ""
	registry, err := NewContractRegistry(contracts)
	if err != nil {
		t.Fatalf("空 lane 应采用安全默认值: %v", err)
	}
	got, ok := registry.Lookup(contracts[0].Family)
	if !ok || got.Lane != ServingLaneChatHCSF {
		t.Fatalf("默认 lane=%q found=%t, want %q", got.Lane, ok, ServingLaneChatHCSF)
	}

	contracts[0].Lane = ServingLane("unknown")
	if _, err := NewContractRegistry(contracts); err == nil {
		t.Fatal("未知 serving lane 必须拒绝")
	}
}

func TestEvaluateAccountEligibilityDistinguishesExpiryAndHealthGaps(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	evaluator := NewEvaluator(nil, productionRuntimeSources(registrydefault.Build()))
	completeInput := AccountEligibilityInput{
		Family:                registrydefault.ProtocolOpenAIChat,
		AuthMode:              credentialstore.AuthModeAPIKey,
		RuntimeCredentialKind: credentialstore.RuntimeAPIKey,
		ExpiresAt:             now.Add(time.Hour),
		Now:                   now,
		HealthAuthoritative:   true,
		HealthAllowed:         true,
	}
	complete := evaluator.EvaluateAccountEligibility(completeInput)

	tests := []struct {
		name    string
		station StationID
		reason  string
		mutate  func(AccountEligibilityInput) AccountEligibilityInput
	}{
		{
			name: "账号过期", station: StationCredentialNotExpired, reason: "credential_expired",
			mutate: func(in AccountEligibilityInput) AccountEligibilityInput {
				in.ExpiresAt = now.Add(-time.Second)
				return in
			},
		},
		{
			name: "权威健康不允许", station: StationAuthoritativeHealth, reason: "authoritative_health_denied",
			mutate: func(in AccountEligibilityInput) AccountEligibilityInput {
				in.HealthAllowed = false
				return in
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			missing := evaluator.EvaluateAccountEligibility(tc.mutate(completeInput))
			assertCapabilityTransition(t, complete, missing,
				StatusReady, ActionAllow, StatusNotReady, ActionMarkRed)
			assertMissingStation(t, missing, tc.station, tc.reason)
			if missing.Reason != tc.reason {
				t.Fatalf("reason=%q want %q", missing.Reason, tc.reason)
			}
		})
	}
}

// TestClaudeSessionAccountCompatibility 咬住 session family 的 vendor/auth/runtime
// 三元组；任一维度退回 API key 或 transport vendor 字符串都必须被拒。
func TestClaudeSessionAccountCompatibility(t *testing.T) {
	valid := []struct {
		authMode string
		runtime  string
	}{
		{credentialstore.AuthModeClaudeAIOAuth, credentialstore.RuntimeOAuthAccessToken},
		{credentialstore.AuthModeClaudeCode, credentialstore.RuntimeSessionToken},
	}
	for _, tc := range valid {
		if err := ValidateAccountCompatibility(registrydefault.ProtocolAnthropicClaudeSession, credentialstore.VendorAnthropic, tc.authMode, tc.runtime); err != nil {
			t.Fatalf("valid %s/%s: %v", tc.authMode, tc.runtime, err)
		}
	}
	invalid := []struct {
		vendor   string
		authMode string
		runtime  string
	}{
		{"anthropic_claude_session", credentialstore.AuthModeClaudeAIOAuth, credentialstore.RuntimeOAuthAccessToken},
		{credentialstore.VendorAnthropic, credentialstore.AuthModeAPIKey, credentialstore.RuntimeAPIKey},
		{credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeCode, credentialstore.RuntimeAPIKey},
	}
	for _, tc := range invalid {
		if err := ValidateAccountCompatibility(registrydefault.ProtocolAnthropicClaudeSession, tc.vendor, tc.authMode, tc.runtime); err == nil {
			t.Fatalf("invalid tuple unexpectedly accepted: vendor=%s auth=%s runtime=%s", tc.vendor, tc.authMode, tc.runtime)
		}
	}
}

func TestEvaluateModelSellabilityDistinguishesPricingGap(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	evaluator := NewEvaluator(nil, productionRuntimeSources(registrydefault.Build()))
	completeInput := ModelSellabilityInput{
		Alias:         "gpt-ready",
		Family:        registrydefault.ProtocolOpenAIChat,
		ModelActive:   true,
		BindingActive: true,
		Accounts: []ModelAccountInput{{
			SupportsWireModel: true,
			Account: AccountEligibilityInput{
				Family: registrydefault.ProtocolOpenAIChat, AuthMode: credentialstore.AuthModeAPIKey,
				RuntimeCredentialKind: credentialstore.RuntimeAPIKey, ExpiresAt: now.Add(time.Hour), Now: now,
				HealthAuthoritative: true, HealthAllowed: true,
			},
		}},
		Pricing: pricingProbeFunc(func(context.Context, string, string, []string) (bool, string) {
			return true, "pricing_resolved"
		}),
		UsageSettlementSupported: true,
	}
	complete := evaluator.EvaluateModelSellability(context.Background(), completeInput)
	missingInput := completeInput
	missingInput.Pricing = nil
	missing := evaluator.EvaluateModelSellability(context.Background(), missingInput)

	assertCapabilityTransition(t, complete, missing,
		StatusReady, ActionAllow, StatusTestOnly, ActionRejectPublish)
	assertMissingStation(t, missing, StationPricing, ReasonPricingMissing)
	if missing.Reason != ReasonPricingMissing {
		t.Fatalf("reason=%q want %q", missing.Reason, ReasonPricingMissing)
	}
}

func TestEvaluateSettingAdvertisementDistinguishesConsumerGap(t *testing.T) {
	complete := EvaluateSettingAdvertisement(SettingAdvertisementInput{
		Key: "routing.timeout", ProductionConsumers: []string{"gateway_dispatch"}, EffectiveValueObservable: true,
	})
	missing := EvaluateSettingAdvertisement(SettingAdvertisementInput{
		Key: "routing.timeout", EffectiveValueObservable: true,
	})

	assertCapabilityTransition(t, complete, missing,
		StatusReady, ActionAllow, StatusNotReady, ActionReportOnly)
	assertMissingStation(t, missing, StationProductionConsumer, "production_consumer_missing")
}

func assertCapabilityTransition(t *testing.T, complete, missing CheckResult, completeStatus ReadinessStatus, completeAction Action, missingStatus ReadinessStatus, missingAction Action) {
	t.Helper()
	if !complete.Ready || complete.Status != completeStatus || complete.Action != completeAction {
		t.Fatalf("完整 fixture 结论=(ready=%t status=%s action=%s), want=(true %s %s): %+v",
			complete.Ready, complete.Status, complete.Action, completeStatus, completeAction, complete)
	}
	if missing.Ready || missing.Status != missingStatus || missing.Action != missingAction {
		t.Fatalf("缺站 fixture 结论=(ready=%t status=%s action=%s), want=(false %s %s): %+v",
			missing.Ready, missing.Status, missing.Action, missingStatus, missingAction, missing)
	}
}

func assertMissingStation(t *testing.T, result CheckResult, station StationID, reason string) {
	t.Helper()
	for _, got := range result.Stations {
		if got.Station == station && !got.Present && got.Blocking && got.Reason == reason {
			return
		}
	}
	t.Fatalf("未找到缺失站点 station=%s reason=%s: %+v", station, reason, result.Stations)
}

func findStation(t *testing.T, result CheckResult, station StationID) StationResult {
	t.Helper()
	for _, got := range result.Stations {
		if got.Station == station {
			return got
		}
	}
	t.Fatalf("未找到站点 %s: %+v", station, result.Stations)
	return StationResult{}
}
