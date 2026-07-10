// Package servingcapability 把采集、serving、模型售卖和设置消费之间的闭合关系
// 汇总为一个只读判定层。它不拥有这些事实，只查询各层现有真相源。
package servingcapability

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ReleaseState 表示产品对一条 serving contract 的发布意图。
type ReleaseState string

const (
	ReleaseStateScaffold     ReleaseState = "scaffold"
	ReleaseStateExperimental ReleaseState = "experimental"
	ReleaseStateReleased     ReleaseState = "released"
	ReleaseStateRetired      ReleaseState = "retired"
)

// ServingLane 表示 family 实际消费的请求、响应与流处理车道。
type ServingLane string

const (
	// ServingLaneChatHCSF 是零值 contract 的安全默认车道。
	ServingLaneChatHCSF ServingLane = "chat_hcsf"
	ServingLaneImage    ServingLane = "image"
)

// ModelDiscoveryScope 表示模型目录是全局事实还是账号级观察。
type ModelDiscoveryScope string

const (
	ModelDiscoveryGlobal        ModelDiscoveryScope = "global"
	ModelDiscoveryAccountScoped ModelDiscoveryScope = "account_scoped"
)

// ServingCapabilityContract 只保存现有运行对象无法独立推出的产品意图。
type ServingCapabilityContract struct {
	Family                 string
	Vendor                 string
	Lane                   ServingLane
	AuthModes              []string
	RuntimeCredentialKinds []string
	RequestMarshalShape    string
	ResponseParseShape     string
	StreamFraming          string
	ReleaseState           ReleaseState
	MustPriceToSell        bool
	ModelDiscoveryScope    ModelDiscoveryScope
	WireVerified           bool
	ReadinessReason        string
}

// InvariantID 标识五条跨层不变量。
type InvariantID string

const (
	InvariantCatalogVisible    InvariantID = "catalog_visible"
	InvariantProviderConfig    InvariantID = "provider_config_enabled"
	InvariantAccountEligible   InvariantID = "account_eligible"
	InvariantModelSellable     InvariantID = "model_sellable"
	InvariantSettingAdvertised InvariantID = "setting_advertised"
)

// StationID 标识闭合链上的单个站点。
type StationID string

const (
	StationContract             StationID = "contract"
	StationReleaseState         StationID = "release_state"
	StationWireVerified         StationID = "wire_verified"
	StationFinalizer            StationID = "finalizer"
	StationAcquisition          StationID = "acquisition_disposition"
	StationRefresh              StationID = "refresh_disposition"
	StationServingContract      StationID = "serving_contract"
	StationServingReady         StationID = "serving_ready"
	StationProviderAdapter      StationID = "provider_adapter"
	StationResponseParser       StationID = "response_parser"
	StationRequestMarshal       StationID = "request_marshal"
	StationStreamScanner        StationID = "stream_scanner"
	StationPoolVendor           StationID = "pool_vendor"
	StationTransportPolicy      StationID = "transport_policy"
	StationAuthMode             StationID = "auth_mode"
	StationRuntimeCredential    StationID = "runtime_credential_kind"
	StationCredentialNotExpired StationID = "credential_not_expired"
	StationAuthoritativeHealth  StationID = "authoritative_health"
	StationModelActive          StationID = "model_active"
	StationBindingActive        StationID = "binding_active"
	StationEligibleAccount      StationID = "eligible_account_wire_model"
	StationPricing              StationID = "pricing_resolvable"
	StationUsageSettlement      StationID = "usage_settlement"
	StationProductionConsumer   StationID = "production_consumer"
	StationEffectiveValue       StationID = "effective_value_observable"
)

// ReadinessStatus 是给控制面和运维面展示的稳定状态。
type ReadinessStatus string

const (
	StatusReady                      ReadinessStatus = "ready"
	StatusExperimental               ReadinessStatus = "experimental"
	StatusCollectOnly                ReadinessStatus = "collect_only"
	StatusCollectableNotServing      ReadinessStatus = "collectable_not_serving"
	StatusExperimentalWireUnverified ReadinessStatus = "experimental_wire_unverified"
	StatusScaffold                   ReadinessStatus = "scaffold"
	StatusRetired                    ReadinessStatus = "retired"
	StatusNotReady                   ReadinessStatus = "not_ready"
	StatusTestOnly                   ReadinessStatus = "test_only"
)

// Action 表示违反不变量时调用方应采取的姿态。
type Action string

const (
	ActionAllow         Action = "allow"
	ActionRejectWrite   Action = "reject_write"
	ActionMarkRed       Action = "mark_red"
	ActionHideReadOnly  Action = "hide_read_only"
	ActionRejectPublish Action = "reject_publish"
	ActionReportOnly    Action = "report_only"
)

const (
	ReasonCollectableNotServing      = "collectable_not_serving"
	ReasonExperimentalWireUnverified = "experimental_wire_unverified"
	ReasonAdapterNotRegistered       = "adapter_not_registered"
	ReasonResponseParserMissing      = "response_parser_missing"
	ReasonRequestMarshalMissing      = "request_marshal_missing"
	ReasonStreamScannerMissing       = "stream_scanner_missing"
	ReasonPoolVendorMissing          = "pool_vendor_missing"
	ReasonTransportPolicyMissing     = "transport_policy_missing"
	ReasonPricingMissing             = "pricing_unresolvable"
)

// StationResult 保留一个站点的存在性、阻断性和证据摘要。
type StationResult struct {
	Station  StationID `json:"station"`
	Present  bool      `json:"present"`
	Blocking bool      `json:"blocking"`
	Reason   string    `json:"reason,omitempty"`
	Evidence string    `json:"evidence,omitempty"`
}

// CheckResult 是一条不变量的结构化结果，不在第一个缺口处短路。
type CheckResult struct {
	Invariant       InvariantID     `json:"invariant"`
	Subject         string          `json:"subject"`
	Family          string          `json:"family,omitempty"`
	ReleaseState    ReleaseState    `json:"release_state,omitempty"`
	Ready           bool            `json:"ready"`
	Allowed         bool            `json:"allowed"`
	TrafficAllowed  bool            `json:"traffic_allowed"`
	StartupBlocking bool            `json:"startup_blocking"`
	Status          ReadinessStatus `json:"status"`
	Action          Action          `json:"action"`
	Reason          string          `json:"reason,omitempty"`
	Stations        []StationResult `json:"stations"`
}

// MissingStations 返回所有缺失且具有阻断性的站点。
func (r CheckResult) MissingStations() []StationResult {
	out := make([]StationResult, 0)
	for _, station := range r.Stations {
		if station.Blocking && !station.Present {
			out = append(out, station)
		}
	}
	return out
}

// ReadinessError 让管理端写入和发布端复用同一结构化判定。
type ReadinessError struct {
	Result CheckResult
}

func (e *ReadinessError) Error() string {
	if e == nil {
		return "serving capability not ready"
	}
	reason := strings.TrimSpace(e.Result.Reason)
	if reason == "" {
		reason = "closure_incomplete"
	}
	return fmt.Sprintf("%s %q not ready: %s", e.Result.Invariant, e.Result.Subject, reason)
}

// CatalogModeInput 是目录层从 ModePlan、finalizer、exchanger 和 refresher 真相源
// 读取后的最小事实。
type CatalogModeInput struct {
	Vendor                        string
	AuthMode                      string
	FinalizerPresent              bool
	AcquisitionDispositionPresent bool
	RefreshDispositionPresent     bool
}

// ProviderConfigInput 描述一次 provider 配置启用判定。
type ProviderConfigInput struct {
	Family  string
	Enabled bool
}

// AccountEligibilityInput 描述一次账号资格判定所需的实时状态。
type AccountEligibilityInput struct {
	Family                string
	AuthMode              string
	RuntimeCredentialKind string
	ExpiresAt             time.Time
	Now                   time.Time
	HealthAuthoritative   bool
	HealthAllowed         bool
}

// ModelAccountInput 把账号资格与账号是否支持 wire model 绑定在同一候选上。
type ModelAccountInput struct {
	Account           AccountEligibilityInput
	SupportsWireModel bool
}

// PricingProbe 查询当前 pricing snapshot 是否能按真实解析链解析候选模型。
type PricingProbe interface {
	ProbePricing(context.Context, string, string, []string) (bool, string)
}

// ModelSellabilityInput 描述发布/绑定期的只读预检事实。
type ModelSellabilityInput struct {
	Alias                    string
	Family                   string
	ModelActive              bool
	BindingActive            bool
	Accounts                 []ModelAccountInput
	Pricing                  PricingProbe
	PricingAliases           []string
	UsageSettlementSupported bool
}

// SettingAdvertisementInput 描述设置是否具备生产消费者与有效值观测面。
type SettingAdvertisementInput struct {
	Key                      string
	ProductionConsumers      []string
	EffectiveValueObservable bool
}

// EvaluationInput 可在启动期一次评估多个控制面对象。
type EvaluationInput struct {
	CatalogModes    []CatalogModeInput
	ProviderConfigs []ProviderConfigInput
	Accounts        []AccountEligibilityInput
	Models          []ModelSellabilityInput
	Settings        []SettingAdvertisementInput
}

// Report 聚合全部检查，保留每个对象的所有缺口。
type Report struct {
	Checks []CheckResult `json:"checks"`
}
