package servingcapability

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
)

const (
	streamFramingSSE                = "sse"
	streamFramingNDJSON             = "ndjson"
	streamFramingBedrockEventStream = "bedrock_eventstream"
	streamFramingNone               = "none"
	requestShapeNativeRaw           = "native_raw"
)

// ContractRegistry 是只读的 product-intent 登记表。
type ContractRegistry struct {
	byFamily map[string]ServingCapabilityContract
	families []string
}

// NewContractRegistry 校验并复制 contract，避免调用方后续修改切片影响判定。
func NewContractRegistry(contracts []ServingCapabilityContract) (*ContractRegistry, error) {
	r := &ContractRegistry{byFamily: make(map[string]ServingCapabilityContract, len(contracts))}
	for _, raw := range contracts {
		contract := cloneContract(raw)
		contract.Family = normalize(contract.Family)
		contract.Vendor = normalize(contract.Vendor)
		contract.Lane = ServingLane(normalize(string(contract.Lane)))
		if contract.Lane == "" {
			contract.Lane = ServingLaneChatHCSF
		}
		if contract.Family == "" || contract.Vendor == "" {
			return nil, fmt.Errorf("servingcapability: contract family/vendor 不能为空")
		}
		if !validServingLane(contract.Lane) {
			return nil, fmt.Errorf("servingcapability: family %s serving lane %q 非法", contract.Family, contract.Lane)
		}
		if !validReleaseState(contract.ReleaseState) {
			return nil, fmt.Errorf("servingcapability: family %s release state %q 非法", contract.Family, contract.ReleaseState)
		}
		if contract.ModelDiscoveryScope != ModelDiscoveryGlobal && contract.ModelDiscoveryScope != ModelDiscoveryAccountScoped {
			return nil, fmt.Errorf("servingcapability: family %s discovery scope %q 非法", contract.Family, contract.ModelDiscoveryScope)
		}
		if _, exists := r.byFamily[contract.Family]; exists {
			return nil, fmt.Errorf("servingcapability: family %s contract 重复", contract.Family)
		}
		contract.AuthModes = normalizedUnique(contract.AuthModes)
		contract.RuntimeCredentialKinds = normalizedUnique(contract.RuntimeCredentialKinds)
		r.byFamily[contract.Family] = contract
		r.families = append(r.families, contract.Family)
	}
	sort.Strings(r.families)
	return r, nil
}

// MustNewContractRegistry 在静态产品声明非法时 fail-loud。
func MustNewContractRegistry(contracts []ServingCapabilityContract) *ContractRegistry {
	r, err := NewContractRegistry(contracts)
	if err != nil {
		panic(err)
	}
	return r
}

// Lookup 返回指定 family 的 contract 副本。
func (r *ContractRegistry) Lookup(family string) (ServingCapabilityContract, bool) {
	if r == nil {
		return ServingCapabilityContract{}, false
	}
	contract, ok := r.byFamily[normalize(family)]
	return cloneContract(contract), ok
}

// ForMode 返回声明允许该 vendor/auth mode 的全部 contract。
func (r *ContractRegistry) ForMode(vendor, authMode string) []ServingCapabilityContract {
	if r == nil {
		return nil
	}
	vendor, authMode = normalize(vendor), normalize(authMode)
	out := make([]ServingCapabilityContract, 0, 2)
	for _, family := range r.families {
		contract := r.byFamily[family]
		if contract.Vendor == vendor && contains(contract.AuthModes, authMode) {
			out = append(out, cloneContract(contract))
		}
	}
	return out
}

// All 返回按 family 排序的 contract 副本。
func (r *ContractRegistry) All() []ServingCapabilityContract {
	if r == nil {
		return nil
	}
	out := make([]ServingCapabilityContract, 0, len(r.families))
	for _, family := range r.families {
		out = append(out, cloneContract(r.byFamily[family]))
	}
	return out
}

// HasContract 报告某 family 是否有 serving 契约。供 G1 全族兼容校验判定:无契约的
// family 保守跳过(不误拒 R0 未覆盖族),有契约的才强制 vendor/auth/runtime 一致。
func HasContract(family string) bool {
	_, ok := DefaultContractRegistry().Lookup(family)
	return ok
}

// ValidateAccountCompatibility 校验 family、vendor、auth mode 与物化后的
// runtime kind 是否同时落在同一 capability contract 内。配置面可在写入前
// 调用，热路径则在发网前复核，避免错误账号混入同一 provider 后被选号。
func ValidateAccountCompatibility(family, vendor, authMode, runtimeKind string) error {
	contract, ok := DefaultContractRegistry().Lookup(family)
	if !ok {
		return fmt.Errorf("servingcapability: family %q contract missing", family)
	}
	vendor, authMode, runtimeKind = normalize(vendor), normalize(authMode), normalize(runtimeKind)
	if vendor != contract.Vendor {
		return fmt.Errorf("servingcapability: family %q requires vendor %q", contract.Family, contract.Vendor)
	}
	if !contains(contract.AuthModes, authMode) {
		return fmt.Errorf("servingcapability: family %q rejects auth mode %q", contract.Family, authMode)
	}
	if runtimeKind == "" || !contains(contract.RuntimeCredentialKinds, runtimeKind) {
		return fmt.Errorf("servingcapability: family %q rejects runtime credential %q", contract.Family, runtimeKind)
	}
	return nil
}

var (
	defaultContractsOnce sync.Once
	defaultContracts     *ContractRegistry
)

// DefaultContractRegistry 返回 HUAKAI 当前最小产品意图表。
func DefaultContractRegistry() *ContractRegistry {
	defaultContractsOnce.Do(func() {
		defaultContracts = MustNewContractRegistry(DefaultContracts())
	})
	return defaultContracts
}

// DefaultContracts 覆盖当前默认与 env-gated family，并额外保留尚未注册的
// Claude OAuth family。adapter/parser/scanner/vendor/transport 等运行事实不在此复制。
func DefaultContracts() []ServingCapabilityContract {
	contracts := []ServingCapabilityContract{
		releasedContract(registrydefault.ProtocolOpenAIChat, credentialstore.VendorOpenAI,
			[]string{credentialstore.AuthModeAPIKey, credentialstore.AuthModeAzure, credentialstore.AuthModeRefreshToken},
			[]string{credentialstore.RuntimeAPIKey, credentialstore.RuntimeUpstreamPassthrough},
			registrydefault.ProtocolOpenAIChat, registrydefault.ProtocolOpenAIChat, streamFramingSSE, ModelDiscoveryGlobal),
		releasedContract(registrydefault.ProtocolOpenAIResponses, credentialstore.VendorOpenAI,
			[]string{credentialstore.AuthModeAPIKey, credentialstore.AuthModeAzure, credentialstore.AuthModeRefreshToken},
			[]string{credentialstore.RuntimeAPIKey, credentialstore.RuntimeUpstreamPassthrough},
			registrydefault.ProtocolOpenAIResponses, registrydefault.ProtocolOpenAIResponses, streamFramingSSE, ModelDiscoveryGlobal),
		releasedContract(registrydefault.ProtocolOpenAICodex, credentialstore.VendorOpenAI,
			[]string{credentialstore.AuthModeChatGPTOAuth, credentialstore.AuthModeCodexCLIOAuth, credentialstore.AuthModeCodexWebOAuth},
			[]string{credentialstore.RuntimeSessionToken, credentialstore.RuntimeUpstreamPassthrough},
			registrydefault.ProtocolOpenAIResponses, registrydefault.ProtocolOpenAIResponses, streamFramingSSE, ModelDiscoveryAccountScoped),
		releasedContract(registrydefault.ProtocolAnthropicMessages, credentialstore.VendorAnthropic,
			[]string{credentialstore.AuthModeAPIKey},
			[]string{credentialstore.RuntimeAPIKey, credentialstore.RuntimeUpstreamPassthrough},
			registrydefault.ProtocolAnthropicMessages, registrydefault.ProtocolAnthropicMessages, streamFramingSSE, ModelDiscoveryGlobal),
		releasedContract(registrydefault.ProtocolAnthropicClaudeSession, credentialstore.VendorAnthropic,
			[]string{credentialstore.AuthModeClaudeAIOAuth, credentialstore.AuthModeClaudeCode, credentialstore.AuthModeClaudeSetupToken},
			[]string{credentialstore.RuntimeOAuthAccessToken, credentialstore.RuntimeSessionToken, credentialstore.RuntimeUpstreamPassthrough},
			registrydefault.ProtocolAnthropicMessages, registrydefault.ProtocolAnthropicMessages, streamFramingSSE, ModelDiscoveryAccountScoped),
		releasedContract(registrydefault.ProtocolGeminiMessages, credentialstore.VendorGemini,
			[]string{credentialstore.AuthModeAIStudioAPIKey},
			[]string{credentialstore.RuntimeAPIKey, credentialstore.RuntimeUpstreamPassthrough},
			registrydefault.ProtocolGeminiMessages, registrydefault.ProtocolGeminiMessages, streamFramingSSE, ModelDiscoveryGlobal),
		releasedContract(registrydefault.ProtocolBedrockInvoke, credentialstore.VendorAnthropic,
			[]string{credentialstore.AuthModeBedrock},
			[]string{credentialstore.RuntimeAWSSigV4, credentialstore.RuntimeUpstreamPassthrough},
			requestShapeNativeRaw, registrydefault.ProtocolAnthropicMessages, streamFramingBedrockEventStream, ModelDiscoveryGlobal),
		releasedContract(registrydefault.ProtocolVertexGemini, credentialstore.VendorGemini,
			[]string{credentialstore.AuthModeVertexSA}, []string{credentialstore.RuntimeUpstreamPassthrough},
			registrydefault.ProtocolGeminiMessages, registrydefault.ProtocolGeminiMessages, streamFramingSSE, ModelDiscoveryGlobal),
		releasedContract(registrydefault.ProtocolVertexAnthropic, credentialstore.VendorAnthropic,
			[]string{credentialstore.AuthModeVertexAnthropic}, []string{credentialstore.RuntimeUpstreamPassthrough},
			registrydefault.ProtocolAnthropicMessages, registrydefault.ProtocolAnthropicMessages, streamFramingSSE, ModelDiscoveryGlobal),
		contract(registrydefault.ProtocolGeminiCodeAssist, credentialstore.VendorGemini,
			[]string{credentialstore.AuthModeCodeAssist},
			[]string{credentialstore.RuntimeSessionToken, credentialstore.RuntimeOAuthAccessToken, credentialstore.RuntimeUpstreamPassthrough},
			registrydefault.ProtocolGeminiMessages, registrydefault.ProtocolGeminiCodeAssist, streamFramingSSE,
			ReleaseStateExperimental, ModelDiscoveryAccountScoped, true, ""),
		unverifiedSessionContract(registrydefault.ProtocolGeminiAdvancedSession, credentialstore.VendorGemini,
			[]string{credentialstore.AuthModeGoogleOne}, []string{credentialstore.RuntimeSessionToken, credentialstore.RuntimeUpstreamPassthrough}),
		contract(registrydefault.ProtocolAntigravitySession, credentialstore.VendorAntigravity,
			[]string{credentialstore.AuthModeOAuth}, []string{credentialstore.RuntimeSessionToken, credentialstore.RuntimeUpstreamPassthrough},
			registrydefault.ProtocolAntigravitySession, registrydefault.ProtocolAntigravitySession, streamFramingSSE,
			ReleaseStateExperimental, ModelDiscoveryAccountScoped, false, ReasonExperimentalWireUnverified),
		unverifiedSessionContract(registrydefault.ProtocolCursorSession, credentialstore.VendorCursor,
			[]string{credentialstore.AuthModeOAuth}, []string{credentialstore.RuntimeSessionToken, credentialstore.RuntimeUpstreamPassthrough}),
		unverifiedSessionContract(registrydefault.ProtocolCopilotSession, credentialstore.VendorCopilot,
			[]string{credentialstore.AuthModeCopilotOAuth}, []string{credentialstore.RuntimeSessionToken, credentialstore.RuntimeUpstreamPassthrough}),
		unverifiedSessionContract(registrydefault.ProtocolKiroSession, "kiro",
			[]string{"aws_sso"}, []string{credentialstore.RuntimeSessionToken, credentialstore.RuntimeUpstreamPassthrough}),
		unverifiedSessionContract(registrydefault.ProtocolWindsurfSession, credentialstore.VendorWindsurf,
			[]string{credentialstore.AuthModeOAuth}, []string{credentialstore.RuntimeSessionToken, credentialstore.RuntimeUpstreamPassthrough}),
	}

	contracts = append(contracts,
		contract(registrydefault.ProtocolOpenRouterChat, credentialstore.VendorOpenRouter,
			[]string{credentialstore.AuthModeAPIKey}, []string{credentialstore.RuntimeAPIKey, credentialstore.RuntimeUpstreamPassthrough},
			registrydefault.ProtocolOpenAIChat, registrydefault.ProtocolOpenAIChat, streamFramingSSE,
			ReleaseStateScaffold, ModelDiscoveryGlobal, true, ReasonNoCredentialHandler),
		releasedOpenAICompatible(registrydefault.ProtocolGrokChat, credentialstore.VendorGrok,
			[]string{credentialstore.AuthModeAPIKey, credentialstore.AuthModeXAIOAuth},
			[]string{credentialstore.RuntimeAPIKey, credentialstore.RuntimeOAuthAccessToken, credentialstore.RuntimeUpstreamPassthrough}),
		releasedOpenAICompatible(registrydefault.ProtocolKimiChat, credentialstore.VendorKimi,
			[]string{credentialstore.AuthModeAPIKey, credentialstore.AuthModeKimiOAuth},
			[]string{credentialstore.RuntimeAPIKey, credentialstore.RuntimeUpstreamPassthrough}),
	)

	for _, row := range []struct {
		family string
		vendor string
	}{
		{registrydefault.ProtocolDeepSeekChat, credentialstore.VendorDeepSeek},
		{registrydefault.ProtocolQwenChat, credentialstore.VendorQwen},
		{registrydefault.ProtocolGLMChat, credentialstore.VendorGLM},
		{registrydefault.ProtocolYiChat, credentialstore.VendorYi},
		{registrydefault.ProtocolBaichuanChat, credentialstore.VendorBaichuan},
		{registrydefault.ProtocolDoubaoChat, credentialstore.VendorDoubao},
		{registrydefault.ProtocolErnieChat, credentialstore.VendorErnie},
		{registrydefault.ProtocolStepChat, credentialstore.VendorStep},
		{registrydefault.ProtocolHunyuanChat, credentialstore.VendorHunyuan},
		{registrydefault.ProtocolMinimaxChat, credentialstore.VendorMiniMax},
	} {
		contracts = append(contracts, releasedOpenAICompatible(row.family, row.vendor,
			[]string{credentialstore.AuthModeAPIKey}, []string{credentialstore.RuntimeAPIKey, credentialstore.RuntimeUpstreamPassthrough}))
	}

	for _, row := range []struct {
		family string
		vendor string
	}{
		{registrydefault.ProtocolMistralChat, credentialstore.VendorMistral},
		{registrydefault.ProtocolGroqCloudChat, credentialstore.VendorGroqCloud},
		{registrydefault.ProtocolTogetherChat, credentialstore.VendorTogether},
		{registrydefault.ProtocolPerplexityChat, credentialstore.VendorPerplexity},
		{registrydefault.ProtocolFireworksChat, credentialstore.VendorFireworks},
	} {
		contracts = append(contracts, contract(row.family, row.vendor,
			[]string{credentialstore.AuthModeAPIKey}, []string{credentialstore.RuntimeAPIKey, credentialstore.RuntimeUpstreamPassthrough},
			registrydefault.ProtocolOpenAIChat, registrydefault.ProtocolOpenAIChat, streamFramingSSE,
			ReleaseStateScaffold, ModelDiscoveryGlobal, true, "product_not_released"))
	}

	replicateImage := contract(registrydefault.ProtocolReplicateImage, "replicate", []string{credentialstore.AuthModeAPIKey},
		[]string{credentialstore.RuntimeAPIKey, credentialstore.RuntimeUpstreamPassthrough},
		registrydefault.ProtocolReplicateImage, registrydefault.ProtocolReplicateImage, streamFramingNone,
		ReleaseStateScaffold, ModelDiscoveryGlobal, true, ReasonNoCredentialHandler)
	replicateImage.Lane = ServingLaneImage

	contracts = append(contracts,
		contract(registrydefault.ProtocolCohereChat, "cohere", []string{credentialstore.AuthModeAPIKey},
			[]string{credentialstore.RuntimeAPIKey, credentialstore.RuntimeUpstreamPassthrough},
			registrydefault.ProtocolOpenAIChat, registrydefault.ProtocolOpenAIChat, streamFramingSSE,
			ReleaseStateScaffold, ModelDiscoveryGlobal, true, ReasonNoCredentialHandler),
		contract(registrydefault.ProtocolOllamaChat, "ollama", []string{credentialstore.AuthModeAPIKey},
			[]string{credentialstore.RuntimeAPIKey, credentialstore.RuntimeUpstreamPassthrough},
			registrydefault.ProtocolOpenAIChat, registrydefault.ProtocolOpenAIChat, streamFramingSSE,
			ReleaseStateScaffold, ModelDiscoveryGlobal, true, ReasonNoCredentialHandler),
		contract(registrydefault.ProtocolOllamaNative, "ollama", []string{credentialstore.AuthModeAPIKey},
			[]string{credentialstore.RuntimeAPIKey, credentialstore.RuntimeUpstreamPassthrough},
			registrydefault.ProtocolOllamaNative, registrydefault.ProtocolOllamaNative, streamFramingNDJSON,
			ReleaseStateScaffold, ModelDiscoveryGlobal, true, ReasonNoCredentialHandler),
		contract(registrydefault.ProtocolDifyChat, "dify", []string{credentialstore.AuthModeAPIKey},
			[]string{credentialstore.RuntimeAPIKey, credentialstore.RuntimeUpstreamPassthrough},
			registrydefault.ProtocolDifyChat, registrydefault.ProtocolDifyChat, streamFramingSSE,
			ReleaseStateScaffold, ModelDiscoveryGlobal, true, ReasonNoCredentialHandler),
		replicateImage,
	)
	return contracts
}

func releasedOpenAICompatible(family, vendor string, authModes, runtimeKinds []string) ServingCapabilityContract {
	return releasedContract(family, vendor, authModes, runtimeKinds,
		registrydefault.ProtocolOpenAIChat, registrydefault.ProtocolOpenAIChat, streamFramingSSE, ModelDiscoveryGlobal)
}

func releasedContract(family, vendor string, authModes, runtimeKinds []string, requestShape, responseShape, stream string, scope ModelDiscoveryScope) ServingCapabilityContract {
	return contract(family, vendor, authModes, runtimeKinds, requestShape, responseShape, stream,
		ReleaseStateReleased, scope, true, "")
}

func unverifiedSessionContract(family, vendor string, authModes, runtimeKinds []string) ServingCapabilityContract {
	return contract(family, vendor, authModes, runtimeKinds, family, family, streamFramingSSE,
		ReleaseStateExperimental, ModelDiscoveryAccountScoped, false, ReasonExperimentalWireUnverified)
}

func contract(family, vendor string, authModes, runtimeKinds []string, requestShape, responseShape, stream string, state ReleaseState, scope ModelDiscoveryScope, wireVerified bool, reason string) ServingCapabilityContract {
	return ServingCapabilityContract{
		Family: family, Vendor: vendor, Lane: ServingLaneChatHCSF, AuthModes: authModes, RuntimeCredentialKinds: runtimeKinds,
		RequestMarshalShape: requestShape, ResponseParseShape: responseShape, StreamFraming: stream,
		ReleaseState: state, MustPriceToSell: true, ModelDiscoveryScope: scope,
		WireVerified: wireVerified, ReadinessReason: reason,
	}
}

func cloneContract(in ServingCapabilityContract) ServingCapabilityContract {
	in.AuthModes = append([]string(nil), in.AuthModes...)
	in.RuntimeCredentialKinds = append([]string(nil), in.RuntimeCredentialKinds...)
	return in
}

func validReleaseState(state ReleaseState) bool {
	switch state {
	case ReleaseStateScaffold, ReleaseStateExperimental, ReleaseStateReleased, ReleaseStateRetired:
		return true
	default:
		return false
	}
}

func validServingLane(lane ServingLane) bool {
	switch lane {
	case ServingLaneChatHCSF, ServingLaneImage:
		return true
	default:
		return false
	}
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizedUnique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = normalize(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func contains(values []string, want string) bool {
	want = normalize(want)
	for _, value := range values {
		if normalize(value) == want {
			return true
		}
	}
	return false
}
