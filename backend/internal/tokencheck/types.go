package tokencheck

import "github.com/BloomingProsperity/HUAKAI/internal/proto"

// Verdict 是 token 交叉校验结论；unknown 表示缺少可比对的正数。
type Verdict string

const (
	VerdictOK      Verdict = "ok"
	VerdictWarn5   Verdict = "warn_5pct"
	VerdictFail20  Verdict = "fail_20pct"
	VerdictUnknown Verdict = "unknown"
)

// Thresholds 定义 reported 与 estimated 的相对偏差阈值。
type Thresholds struct {
	WarnRatio float64
	FailRatio float64
}

// DefaultThresholds 默认 5% 预警、20% 失败。
var DefaultThresholds = Thresholds{
	WarnRatio: 0.05,
	FailRatio: 0.20,
}

// Discrepancy 记录上游报告值与 HUAKAI 本地估算值的差异。
type Discrepancy struct {
	Reported  int     `json:"reported"`
	Estimated int     `json:"estimated"`
	Ratio     float64 `json:"ratio"`
	Verdict   Verdict `json:"verdict"`
}

// CacheVerifyResult 记录 cache 命中证据与 Usage 字段的交叉验证结果。
type CacheVerifyResult struct {
	EvidenceFound        bool                      `json:"evidence_found"`
	ReportedReadTokens   int                       `json:"reported_read_tokens"`
	ExpectedReadTokens   int                       `json:"expected_read_tokens"`
	ReportedHitRatio     float64                   `json:"reported_hit_ratio"`
	ExpectedHitRatio     float64                   `json:"expected_hit_ratio"`
	ProtocolLoss         []proto.ProtocolLossEntry `json:"protocol_loss,omitempty"`
	HasReportedReadToken bool                      `json:"has_reported_read_token"`
	HasReportedHitRatio  bool                      `json:"has_reported_hit_ratio"`
}

// OK 表示 cache 校验没有发现不一致。
func (r CacheVerifyResult) OK() bool {
	return len(r.ProtocolLoss) == 0
}
