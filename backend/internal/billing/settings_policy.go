package billing

import (
	"errors"
	"fmt"
	"strings"
)

// StreamInputOnlyInterruptedPolicyKey 是 Case C 当前唯一可配置的设置键。
const StreamInputOnlyInterruptedPolicyKey = "stream_input_only_interrupted_policy"

// StreamInputOnlyInterruptedPolicy 表示流式仅输入后中断场景的结算策略。
type StreamInputOnlyInterruptedPolicy string

const (
	// StreamInputOnlyInterruptedPolicyNoBill 保持当前行为: 不结算、不记录用量。
	StreamInputOnlyInterruptedPolicyNoBill StreamInputOnlyInterruptedPolicy = "no_bill"
	// StreamInputOnlyInterruptedPolicyNoBillRecord 后续热路径批次会用于零成本记录审计。
	StreamInputOnlyInterruptedPolicyNoBillRecord StreamInputOnlyInterruptedPolicy = "no_bill_record"
)

const streamInputOnlyInterruptedPolicyBillInputRoadmap = "bill_input"

var (
	// ErrBillingSettingInvalid 表示设置键、租户或值不合法。
	ErrBillingSettingInvalid = errors.New("billing: setting invalid")
	// ErrBillingPolicyRoadmap 表示值已知但尚未允许持久化。
	ErrBillingPolicyRoadmap = errors.New("billing: policy value roadmap")
)

// DefaultStreamInputOnlyInterruptedPolicy 是配置缺失或读取失败时的安全默认值。
const DefaultStreamInputOnlyInterruptedPolicy = StreamInputOnlyInterruptedPolicyNoBill

func (p StreamInputOnlyInterruptedPolicy) String() string {
	return string(p)
}

// Valid 只接受阶段 1A+1B 已批准的持久值。
func (p StreamInputOnlyInterruptedPolicy) Valid() bool {
	switch p {
	case StreamInputOnlyInterruptedPolicyNoBill, StreamInputOnlyInterruptedPolicyNoBillRecord:
		return true
	default:
		return false
	}
}

// ParseStreamInputOnlyInterruptedPolicy 解析并校验持久化值。
func ParseStreamInputOnlyInterruptedPolicy(value string) (StreamInputOnlyInterruptedPolicy, error) {
	trimmed := strings.TrimSpace(value)
	switch StreamInputOnlyInterruptedPolicy(trimmed) {
	case StreamInputOnlyInterruptedPolicyNoBill:
		return StreamInputOnlyInterruptedPolicyNoBill, nil
	case StreamInputOnlyInterruptedPolicyNoBillRecord:
		return StreamInputOnlyInterruptedPolicyNoBillRecord, nil
	case StreamInputOnlyInterruptedPolicy(streamInputOnlyInterruptedPolicyBillInputRoadmap):
		return "", fmt.Errorf("%w: %s", ErrBillingPolicyRoadmap, trimmed)
	default:
		return "", fmt.Errorf("%w: %s", ErrBillingSettingInvalid, StreamInputOnlyInterruptedPolicyKey)
	}
}
