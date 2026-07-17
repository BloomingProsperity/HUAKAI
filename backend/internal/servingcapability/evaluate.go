package servingcapability

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// ProviderAdapterRegistry 是 provider.StaticRegistry 的最小查询面。
type ProviderAdapterRegistry interface {
	For(string) (provider.Adapter, error)
}

// RuntimeSources 只持有现有运行时真相源的查询函数或窄接口。
type RuntimeSources struct {
	ProviderAdapters   ProviderAdapterRegistry
	ResponseParsers    gateway.ProtocolAdapterRegistry
	RequestMarshal     func(string) (string, bool)
	StreamScanners     gateway.StreamScannerRegistry
	PoolVendor         func(string) string
	TransportModes     func(string) []string
	CredentialHandlers *credentialstore.HandlerRegistry
}

// Evaluator 对 contract 与当前进程实际注册面做交叉判定。
type Evaluator struct {
	contracts *ContractRegistry
	sources   RuntimeSources
}

// NewEvaluator 构造一个可注入真实或故障变异来源的检查器。
func NewEvaluator(contracts *ContractRegistry, sources RuntimeSources) *Evaluator {
	if contracts == nil {
		contracts = DefaultContractRegistry()
	}
	return &Evaluator{contracts: contracts, sources: sources}
}

func (e *Evaluator) contractRegistry() *ContractRegistry {
	if e == nil || e.contracts == nil {
		return DefaultContractRegistry()
	}
	return e.contracts
}

// Evaluate 一次执行输入中涉及的五类不变量。
func (e *Evaluator) Evaluate(ctx context.Context, input EvaluationInput) Report {
	report := Report{Checks: make([]CheckResult, 0,
		len(input.CatalogModes)+len(input.ProviderConfigs)+len(input.Accounts)+len(input.Models)+len(input.Settings))}
	for _, item := range input.CatalogModes {
		report.Checks = append(report.Checks, e.EvaluateCatalogMode(item))
	}
	for _, item := range input.ProviderConfigs {
		report.Checks = append(report.Checks, e.EvaluateProviderConfig(item))
	}
	for _, item := range input.Accounts {
		report.Checks = append(report.Checks, e.EvaluateAccountEligibility(item))
	}
	for _, item := range input.Models {
		report.Checks = append(report.Checks, e.EvaluateModelSellability(ctx, item))
	}
	for _, item := range input.Settings {
		report.Checks = append(report.Checks, EvaluateSettingAdvertisement(item))
	}
	return report
}

// EvaluateProviderConfig 按 contract 声明的车道检查 serving 站点与产品状态。
func (e *Evaluator) EvaluateProviderConfig(input ProviderConfigInput) CheckResult {
	family := normalize(input.Family)
	result := CheckResult{Invariant: InvariantProviderConfig, Subject: family, Family: family}
	contract, contractFound := e.contractRegistry().Lookup(family)
	if contractFound {
		result.ReleaseState = contract.ReleaseState
	}
	chatStationsBlocking := contract.Lane != ServingLaneImage
	result.Stations = append(result.Stations,
		station(StationContract, contractFound, "serving_contract_missing", family),
		station(StationReleaseState, contractFound && releaseCanServe(contract.ReleaseState), "release_state_not_servable", string(contract.ReleaseState)),
		station(StationWireVerified, contractFound && contract.WireVerified, firstNonEmpty(contract.ReadinessReason, "wire_unverified"), contract.RequestMarshalShape),
	)

	var adapter provider.Adapter
	if e != nil && e.sources.ProviderAdapters != nil {
		adapter, _ = e.sources.ProviderAdapters.For(family)
	}
	adapterPresent := adapter != nil
	result.Stations = append(result.Stations, station(StationProviderAdapter, adapterPresent, ReasonAdapterNotRegistered, typeName(adapter)))

	var response any
	if e != nil && e.sources.ResponseParsers != nil {
		response, _ = e.sources.ResponseParsers.For(family)
	}
	result.Stations = append(result.Stations,
		stationWithBlocking(StationResponseParser, response != nil, chatStationsBlocking, ReasonResponseParserMissing,
			shapeEvidence(contract.ResponseParseShape, typeName(response))))

	requestShape, requestPresent := "", false
	if e != nil && e.sources.RequestMarshal != nil {
		requestShape, requestPresent = e.sources.RequestMarshal(family)
	}
	if requestPresent && contractFound && strings.TrimSpace(contract.RequestMarshalShape) != "" {
		requestPresent = normalize(requestShape) == normalize(contract.RequestMarshalShape)
	}
	requestReason := ReasonRequestMarshalMissing
	if requestShape != "" && contractFound && normalize(requestShape) != normalize(contract.RequestMarshalShape) {
		requestReason = "request_marshal_shape_mismatch"
	}
	result.Stations = append(result.Stations,
		stationWithBlocking(StationRequestMarshal, requestPresent, chatStationsBlocking, requestReason,
			shapeEvidence(contract.RequestMarshalShape, requestShape)))

	var scanner any
	if e != nil && e.sources.StreamScanners != nil {
		scanner, _ = e.sources.StreamScanners.For(family)
	}
	result.Stations = append(result.Stations,
		stationWithBlocking(StationStreamScanner, scanner != nil, chatStationsBlocking, ReasonStreamScannerMissing,
			shapeEvidence(contract.StreamFraming, typeName(scanner))))

	vendor := ""
	if e != nil && e.sources.PoolVendor != nil {
		vendor = normalize(e.sources.PoolVendor(family))
	}
	result.Stations = append(result.Stations, station(StationPoolVendor, vendor != "", ReasonPoolVendorMissing, vendor))

	platform := ""
	if adapter != nil {
		platform = normalize(adapter.Platform())
	}
	modes := []string(nil)
	if e != nil && e.sources.TransportModes != nil && platform != "" {
		modes = e.sources.TransportModes(platform)
	}
	result.Stations = append(result.Stations,
		station(StationTransportPolicy, len(modes) > 0, ReasonTransportPolicyMissing,
			fmt.Sprintf("provider=%s modes=%s", platform, strings.Join(modes, ","))))

	// Ready/TrafficAllowed 的语义边界:只表示**dispatch 能力**闭合(adapter/parser/
	// marshal/scanner/pool-vendor/transport 六站 + 契约/发布态/wire 校验)。它不保证某次
	// 真实请求端到端必成——那还需运行时数据:选号阶段有 eligible 账号(pool 选号强制)、
	// 该模型有可解析定价(model-sellability 不变量,发布守卫属 R1B)。反转族的官方客户端
	// 门是编译期硬接线,不作运行时可缺 station。切勿把 TrafficAllowed 读作"可售卖/必计费成功"。
	result.Ready = stationsReady(result.Stations)
	result.Allowed = !input.Enabled || result.Ready
	result.TrafficAllowed = input.Enabled && result.Ready
	result.Reason = resultReason(contract, result.Stations)
	result.Status, result.Action = providerStatusAction(contract, contractFound, result.Ready)
	result.StartupBlocking = input.Enabled && !result.Ready && contract.ReleaseState == ReleaseStateReleased
	return result
}

// RequireProviderConfigEnabled 把结构化判定转换为管理端写入拒绝信号。
func (e *Evaluator) RequireProviderConfigEnabled(family string) error {
	result := e.EvaluateProviderConfig(ProviderConfigInput{Family: family, Enabled: true})
	if result.Allowed {
		return nil
	}
	return &ReadinessError{Result: result}
}

// EvaluateCatalogMode 检查采集目录能否宣称 serving ready。
func (e *Evaluator) EvaluateCatalogMode(input CatalogModeInput) CheckResult {
	vendor, authMode := normalize(input.Vendor), normalize(input.AuthMode)
	result := CheckResult{
		Invariant: InvariantCatalogVisible,
		Subject:   modeKey(vendor, authMode),
		Stations: []StationResult{
			station(StationFinalizer, input.FinalizerPresent, "finalizer_missing", modeKey(vendor, authMode)),
			station(StationAcquisition, input.AcquisitionDispositionPresent, "acquisition_disposition_missing", modeKey(vendor, authMode)),
			station(StationRefresh, input.RefreshDispositionPresent, "refresh_disposition_missing", modeKey(vendor, authMode)),
		},
	}

	contracts := e.contractRegistry().ForMode(vendor, authMode)
	eligibleContracts := make([]ServingCapabilityContract, 0, len(contracts))
	var best CheckResult
	for _, contract := range contracts {
		if !releaseCanServe(contract.ReleaseState) {
			if best.Family == "" {
				best = e.EvaluateProviderConfig(ProviderConfigInput{Family: contract.Family, Enabled: true})
			}
			continue
		}
		eligibleContracts = append(eligibleContracts, contract)
		candidate := e.EvaluateProviderConfig(ProviderConfigInput{Family: contract.Family, Enabled: true})
		if best.Family == "" || candidate.Ready || releaseRank(candidate.ReleaseState) > releaseRank(best.ReleaseState) {
			best = candidate
		}
		if candidate.Ready && candidate.ReleaseState == ReleaseStateReleased {
			break
		}
	}

	servingContractPresent := len(eligibleContracts) > 0
	servingReady := best.Ready && servingContractPresent
	result.Stations = append(result.Stations,
		station(StationServingContract, servingContractPresent, "released_or_experimental_contract_missing", familiesOf(contracts)),
		station(StationServingReady, servingReady, firstNonEmpty(best.Reason, "serving_not_ready"), best.Family),
	)
	result.Family = best.Family
	result.ReleaseState = best.ReleaseState
	result.Ready = stationsReady(result.Stations)
	result.Allowed = result.Ready
	result.TrafficAllowed = result.Ready && result.ReleaseState == ReleaseStateReleased
	result.Reason = primaryReason(result.Stations)
	result.Status, result.Action = catalogStatusAction(contracts, best, result.Ready)
	if best.Reason != "" && !result.Ready {
		result.Reason = best.Reason
	}
	return result
}

// EvaluateAccountEligibility 把 runtime credential、过期与健康状态同 serving adapter 对齐。
func (e *Evaluator) EvaluateAccountEligibility(input AccountEligibilityInput) CheckResult {
	family, authMode := normalize(input.Family), normalize(input.AuthMode)
	runtimeKind := normalize(input.RuntimeCredentialKind)
	result := CheckResult{Invariant: InvariantAccountEligible, Subject: modeKey(family, authMode), Family: family}
	contract, found := e.contractRegistry().Lookup(family)
	if found {
		result.ReleaseState = contract.ReleaseState
	}
	result.Stations = append(result.Stations,
		station(StationContract, found, "serving_contract_missing", family),
		station(StationAuthMode, found && contains(contract.AuthModes, authMode), "auth_mode_not_allowed", authMode),
		station(StationRuntimeCredential, found && contains(contract.RuntimeCredentialKinds, runtimeKind), "runtime_kind_not_declared", runtimeKind),
	)

	handlerKind, handlerPresent := "", false
	if e != nil && e.sources.CredentialHandlers != nil {
		if handler, ok := e.sources.CredentialHandlers.Lookup(contract.Vendor, authMode); ok {
			handlerPresent = true
			handlerKind = normalize(handler.RuntimeKind())
		}
	}
	result.Stations = append(result.Stations,
		station(StationFinalizer, handlerPresent && handlerKind == runtimeKind, "runtime_kind_finalizer_mismatch",
			fmt.Sprintf("handler=%s runtime=%s", handlerKind, runtimeKind)))

	var adapter provider.Adapter
	if e != nil && e.sources.ProviderAdapters != nil {
		adapter, _ = e.sources.ProviderAdapters.For(family)
	}
	accepted := adapterAcceptsRuntime(adapter, runtimeKind)
	result.Stations = append(result.Stations,
		station(StationProviderAdapter, adapter != nil, ReasonAdapterNotRegistered, typeName(adapter)),
		station(StationRuntimeCredential, accepted, "adapter_rejects_runtime_kind", runtimeKind),
	)

	now := input.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	notExpired := input.ExpiresAt.IsZero() || now.Before(input.ExpiresAt)
	result.Stations = append(result.Stations,
		station(StationCredentialNotExpired, notExpired, "credential_expired", formatTime(input.ExpiresAt)),
		station(StationAuthoritativeHealth, input.HealthAuthoritative && input.HealthAllowed, "authoritative_health_denied", fmt.Sprintf("authoritative=%t allowed=%t", input.HealthAuthoritative, input.HealthAllowed)),
	)
	serving := e.EvaluateProviderConfig(ProviderConfigInput{Family: family, Enabled: true})
	result.Stations = append(result.Stations, station(StationServingReady, serving.Ready, firstNonEmpty(serving.Reason, "serving_not_ready"), serving.StatusString()))
	result.Ready = stationsReady(result.Stations)
	result.Allowed = result.Ready
	result.TrafficAllowed = result.Ready
	result.Status = StatusNotReady
	result.Action = ActionMarkRed
	if result.Ready {
		result.Status, result.Action = StatusReady, ActionAllow
	}
	result.Reason = primaryReason(result.Stations)
	return result
}

// EvaluateModelSellability 在发布/绑定期判定模型，绝不修改价格或执行 reserve。
func (e *Evaluator) EvaluateModelSellability(ctx context.Context, input ModelSellabilityInput) CheckResult {
	family, alias := normalize(input.Family), strings.TrimSpace(input.Alias)
	result := CheckResult{Invariant: InvariantModelSellable, Subject: alias, Family: family, Status: StatusTestOnly, Action: ActionRejectPublish}
	contract, found := e.contractRegistry().Lookup(family)
	if found {
		result.ReleaseState = contract.ReleaseState
	}
	result.Stations = append(result.Stations,
		station(StationContract, found, "serving_contract_missing", family),
		station(StationReleaseState, found && contract.ReleaseState == ReleaseStateReleased, "model_family_not_released", string(contract.ReleaseState)),
		station(StationModelActive, input.ModelActive, "model_inactive", alias),
		station(StationBindingActive, input.BindingActive, "binding_inactive", alias),
	)

	eligible := false
	for _, candidate := range input.Accounts {
		if !candidate.SupportsWireModel {
			continue
		}
		account := e.EvaluateAccountEligibility(candidate.Account)
		if account.Ready {
			eligible = true
			break
		}
	}
	result.Stations = append(result.Stations, station(StationEligibleAccount, eligible, "eligible_account_wire_model_missing", alias))

	pricingOK, pricingReason := !contract.MustPriceToSell, "pricing_not_required"
	if contract.MustPriceToSell {
		if input.Pricing == nil {
			pricingOK, pricingReason = false, ReasonPricingMissing
		} else {
			aliases := append([]string{alias}, input.PricingAliases...)
			pricingOK, pricingReason = input.Pricing.ProbePricing(ctx, family, contract.Vendor, aliases)
			if strings.TrimSpace(pricingReason) == "" && !pricingOK {
				pricingReason = ReasonPricingMissing
			}
		}
	}
	result.Stations = append(result.Stations,
		station(StationPricing, pricingOK, pricingReason, contract.Vendor),
		station(StationUsageSettlement, input.UsageSettlementSupported, "usage_settlement_unsupported", family),
	)
	result.Ready = stationsReady(result.Stations)
	result.Allowed = result.Ready
	result.TrafficAllowed = result.Ready
	if result.Ready {
		result.Status, result.Action = StatusReady, ActionAllow
	}
	result.Reason = primaryReason(result.Stations)
	return result
}

// RequireModelSellable 把 sellability 结果转换成发布/绑定期阻断信号。
func (e *Evaluator) RequireModelSellable(ctx context.Context, input ModelSellabilityInput) error {
	result := e.EvaluateModelSellability(ctx, input)
	if result.Allowed {
		return nil
	}
	return &ReadinessError{Result: result}
}

// EvaluateSettingAdvertisement 检查设置是否既有消费者又能观察最终生效值。
func EvaluateSettingAdvertisement(input SettingAdvertisementInput) CheckResult {
	consumers := compactNonEmpty(input.ProductionConsumers)
	result := CheckResult{
		Invariant: InvariantSettingAdvertised,
		Subject:   normalize(input.Key),
		Status:    StatusNotReady,
		Action:    ActionReportOnly,
		Stations: []StationResult{
			station(StationProductionConsumer, len(consumers) > 0, "production_consumer_missing", strings.Join(consumers, ",")),
			station(StationEffectiveValue, input.EffectiveValueObservable, "effective_value_not_observable", input.Key),
		},
	}
	result.Ready = stationsReady(result.Stations)
	result.Allowed = result.Ready
	if result.Ready {
		result.Status, result.Action = StatusReady, ActionAllow
	}
	result.Reason = primaryReason(result.Stations)
	return result
}

func releaseCanServe(state ReleaseState) bool {
	return state == ReleaseStateReleased || state == ReleaseStateExperimental
}

func providerStatusAction(contract ServingCapabilityContract, found, ready bool) (ReadinessStatus, Action) {
	if ready {
		if contract.ReleaseState == ReleaseStateExperimental {
			return StatusExperimental, ActionAllow
		}
		return StatusReady, ActionAllow
	}
	if !found {
		return StatusScaffold, ActionHideReadOnly
	}
	if contract.ReadinessReason == ReasonCollectableNotServing {
		return StatusCollectableNotServing, ActionHideReadOnly
	}
	if contract.ReadinessReason == ReasonExperimentalWireUnverified {
		return StatusExperimentalWireUnverified, ActionMarkRed
	}
	switch contract.ReleaseState {
	case ReleaseStateExperimental:
		return StatusExperimental, ActionMarkRed
	case ReleaseStateRetired:
		return StatusRetired, ActionHideReadOnly
	case ReleaseStateScaffold:
		return StatusScaffold, ActionHideReadOnly
	default:
		return StatusNotReady, ActionRejectWrite
	}
}

func catalogStatusAction(contracts []ServingCapabilityContract, best CheckResult, ready bool) (ReadinessStatus, Action) {
	if ready {
		if best.ReleaseState == ReleaseStateExperimental {
			return StatusExperimental, ActionMarkRed
		}
		return StatusReady, ActionAllow
	}
	if best.Status == StatusCollectableNotServing || best.Status == StatusExperimentalWireUnverified {
		return best.Status, best.Action
	}
	if len(contracts) == 0 {
		return StatusCollectOnly, ActionHideReadOnly
	}
	// serving family 自身 ready 并不代表采集目录闭合。finalizer、acquisition
	// 或 refresh 任一缺失时必须显式降级，不能沿用下层的 ready/allow。
	if best.Ready {
		return StatusNotReady, ActionHideReadOnly
	}
	if best.Status != "" {
		return best.Status, best.Action
	}
	return StatusCollectOnly, ActionHideReadOnly
}

func resultReason(contract ServingCapabilityContract, stations []StationResult) string {
	if !contract.WireVerified && strings.TrimSpace(contract.ReadinessReason) != "" {
		return contract.ReadinessReason
	}
	if contract.ReleaseState == ReleaseStateScaffold && strings.TrimSpace(contract.ReadinessReason) != "" {
		return contract.ReadinessReason
	}
	return primaryReason(stations)
}

func station(id StationID, present bool, reason, evidence string) StationResult {
	return StationResult{Station: id, Present: present, Blocking: true, Reason: reason, Evidence: strings.TrimSpace(evidence)}
}

func stationWithBlocking(id StationID, present, blocking bool, reason, evidence string) StationResult {
	result := station(id, present, reason, evidence)
	result.Blocking = blocking
	return result
}

func stationsReady(stations []StationResult) bool {
	for _, item := range stations {
		if item.Blocking && !item.Present {
			return false
		}
	}
	return true
}

func primaryReason(stations []StationResult) string {
	for _, item := range stations {
		if item.Blocking && !item.Present {
			if strings.TrimSpace(item.Reason) != "" {
				return strings.TrimSpace(item.Reason)
			}
			return string(item.Station)
		}
	}
	return ""
}

func adapterAcceptsRuntime(adapter provider.Adapter, runtimeKind string) bool {
	if adapter == nil {
		return false
	}
	want, ok := providerCredentialType(runtimeKind)
	if !ok {
		return false
	}
	for _, allowed := range adapter.AcceptableCredentialTypes() {
		if allowed == want {
			return true
		}
	}
	return false
}

func providerCredentialType(runtimeKind string) (provider.CredentialType, bool) {
	switch normalize(runtimeKind) {
	case credentialstore.RuntimeAPIKey:
		return provider.CredentialTypeAPIKey, true
	case credentialstore.RuntimeOAuthAccessToken:
		return provider.CredentialTypeOAuthAccessToken, true
	case credentialstore.RuntimeSessionToken:
		return provider.CredentialTypeSessionToken, true
	case credentialstore.RuntimeAWSSigV4:
		return provider.CredentialTypeAWSSigV4, true
	case credentialstore.RuntimeUpstreamPassthrough:
		return provider.CredentialTypeUpstreamPassthrough, true
	default:
		return "", false
	}
}

// RuntimeKindForProviderCredential 把 provider adapter 的凭据形态投影回
// credentialstore contract 字面量，供 dispatch 前兼容性二次校验复用。
func RuntimeKindForProviderCredential(credentialType provider.CredentialType) (string, bool) {
	switch credentialType {
	case provider.CredentialTypeAPIKey:
		return credentialstore.RuntimeAPIKey, true
	case provider.CredentialTypeOAuthAccessToken:
		return credentialstore.RuntimeOAuthAccessToken, true
	case provider.CredentialTypeSessionToken:
		return credentialstore.RuntimeSessionToken, true
	case provider.CredentialTypeAWSSigV4:
		return credentialstore.RuntimeAWSSigV4, true
	case provider.CredentialTypeUpstreamPassthrough:
		return credentialstore.RuntimeUpstreamPassthrough, true
	default:
		return "", false
	}
}

// ValidateRuntimeAccountCompatibility 在发网前复核已解析账号与凭据是否属于目标协议族。
// 尚未建立能力合同的历史协议保持兼容；已有合同的协议必须完整匹配。
func ValidateRuntimeAccountCompatibility(family string, credential provider.Credential, account provider.AccountInfo) error {
	if !HasContract(family) {
		return nil
	}
	runtimeKind, _ := RuntimeKindForProviderCredential(credential.Type)
	return ValidateAccountCompatibility(family, account.Platform, account.AccountType, runtimeKind)
}

func releaseRank(state ReleaseState) int {
	switch state {
	case ReleaseStateReleased:
		return 3
	case ReleaseStateExperimental:
		return 2
	case ReleaseStateScaffold:
		return 1
	default:
		return 0
	}
}

func familiesOf(contracts []ServingCapabilityContract) string {
	families := make([]string, 0, len(contracts))
	for _, contract := range contracts {
		families = append(families, contract.Family)
	}
	return strings.Join(families, ",")
}

func modeKey(left, right string) string {
	left, right = normalize(left), normalize(right)
	if left == "" {
		return right
	}
	if right == "" {
		return left
	}
	return left + "/" + right
}

func typeName(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprintf("%T", value)
}

func shapeEvidence(expected, actual string) string {
	return fmt.Sprintf("expected=%s actual=%s", strings.TrimSpace(expected), strings.TrimSpace(actual))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func compactNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return "none"
	}
	return value.UTC().Format(time.RFC3339)
}

func (r CheckResult) StatusString() string {
	return fmt.Sprintf("status=%s reason=%s", r.Status, r.Reason)
}
