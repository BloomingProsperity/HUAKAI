package billing

import (
	"errors"
	"fmt"
	"strings"
)

// StreamInputOnlyInterruptedPolicyKey 保存流式仅输入后中断场景的结算策略。
const StreamInputOnlyInterruptedPolicyKey = "stream_input_only_interrupted_policy"

// BalanceEnforcementModeKey 保存余额强制模式;缺省为 mandatory。
const BalanceEnforcementModeKey = "balance_enforcement_mode"

// StreamInputOnlyInterruptedPolicy 表示流式仅输入后中断场景的结算策略。
type StreamInputOnlyInterruptedPolicy string

const (
	// StreamInputOnlyInterruptedPolicyNoBill 保持当前行为: 不结算、不记录用量。
	StreamInputOnlyInterruptedPolicyNoBill StreamInputOnlyInterruptedPolicy = "no_bill"
	// StreamInputOnlyInterruptedPolicyNoBillRecord 后续热路径批次会用于零成本记录审计。
	StreamInputOnlyInterruptedPolicyNoBillRecord StreamInputOnlyInterruptedPolicy = "no_bill_record"
)

const streamInputOnlyInterruptedPolicyBillInputRoadmap = "bill_input"

type BalanceEnforcementMode string

const (
	BalanceEnforcementModeMandatory BalanceEnforcementMode = "mandatory"
	BalanceEnforcementModeOptIn     BalanceEnforcementMode = "opt_in"
)

var (
	// ErrBillingSettingInvalid 表示设置键、租户或值不合法。
	ErrBillingSettingInvalid = errors.New("billing: setting invalid")
	// ErrBillingPolicyRoadmap 表示值已知但尚未允许持久化。
	ErrBillingPolicyRoadmap = errors.New("billing: policy value roadmap")
)

// DefaultStreamInputOnlyInterruptedPolicy 是配置缺失或读取失败时的安全默认值。
const DefaultStreamInputOnlyInterruptedPolicy = StreamInputOnlyInterruptedPolicyNoBill

const DefaultBalanceEnforcementMode = BalanceEnforcementModeMandatory

func (p StreamInputOnlyInterruptedPolicy) String() string {
	return string(p)
}

func (m BalanceEnforcementMode) String() string {
	return string(m)
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

func ParseBalanceEnforcementMode(value string) (BalanceEnforcementMode, error) {
	trimmed := strings.TrimSpace(value)
	switch BalanceEnforcementMode(trimmed) {
	case BalanceEnforcementModeMandatory:
		return BalanceEnforcementModeMandatory, nil
	case BalanceEnforcementModeOptIn:
		return BalanceEnforcementModeOptIn, nil
	default:
		return "", fmt.Errorf("%w: %s", ErrBillingSettingInvalid, BalanceEnforcementModeKey)
	}
}
