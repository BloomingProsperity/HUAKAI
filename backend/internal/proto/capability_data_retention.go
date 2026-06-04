package proto

// DataRetentionLabel 锁定 5 词汇枚举。
//
//   - unknown：未声明
//   - request_store_false：客户端请求 store=false（OpenAI Responses 等）
//   - provider_contract_required：合同声明的 provider 不存储
//   - regional_asserted：区域承诺（regional 数据驻留）
//   - zdr_verified：ZDR vendor/account proof 已验证
type DataRetentionLabel string

const (
	DataRetentionUnknown                  DataRetentionLabel = "unknown"
	DataRetentionRequestStoreFalse        DataRetentionLabel = "request_store_false"
	DataRetentionProviderContractRequired DataRetentionLabel = "provider_contract_required"
	DataRetentionRegionalAsserted         DataRetentionLabel = "regional_asserted"
	DataRetentionZDRVerified              DataRetentionLabel = "zdr_verified"
)

// AllDataRetentionLabels 列出所有合法 label，envelope_validate 用。
var AllDataRetentionLabels = []DataRetentionLabel{
	DataRetentionUnknown,
	DataRetentionRequestStoreFalse,
	DataRetentionProviderContractRequired,
	DataRetentionRegionalAsserted,
	DataRetentionZDRVerified,
}

// DataRetentionValue 是 5 词汇枚举别名。
type DataRetentionValue = DataRetentionLabel

// DataRetentionNode 是 data_retention capability 的 payload。
type DataRetentionNode struct {
	// Value 必填；只能取 5 词汇之一。
	Value DataRetentionLabel `json:"value"`

	// Enforcement 必填；unknown/asserted/contract_required/verified。
	Enforcement string `json:"enforcement"`

	// Region 可选；regional_asserted 时填写区域。
	Region string `json:"region,omitempty"`

	// RequestStore 可选；request_store_false 时必须为 false。
	RequestStore *bool `json:"request_store,omitempty"`

	// NoTrain 可选；表达 no-train intent，不等同 ZDR proof。
	NoTrain bool `json:"no_train,omitempty"`

	// EvidenceRef 可选；zdr_verified 必须填 account/vendor proof ref。
	EvidenceRef string `json:"evidence_ref,omitempty"`

	// AuditLabel 必填；用于 native/policy audit 查询。
	AuditLabel string `json:"audit_label"`
}
