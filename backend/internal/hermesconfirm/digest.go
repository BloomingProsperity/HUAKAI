package hermesconfirm

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

// BindingDigest 是确认值绑定的固定长度摘要。摘要只用于判断确认请求是否仍对应原预览，
// 不替代请求参数校验，也不会作为凭据材料返回或写入日志。
type BindingDigest [sha256.Size]byte

// DigestArguments 对已经通过工具 JSON Schema 校验的参数生成稳定摘要。
func DigestArguments(args map[string]any) (BindingDigest, error) {
	if args == nil {
		args = map[string]any{}
	}
	return digestValue(args)
}

// DigestPlan 对解析后的目标与脱敏预览生成稳定摘要。确认前重新解析出的计划必须得到同一
// 摘要，才能执行改动。
func DigestPlan(targetType string, targetID int64, lockKey string, preview map[string]any) (BindingDigest, error) {
	return digestValue(struct {
		TargetType string         `json:"target_type"`
		TargetID   int64          `json:"target_id"`
		LockKey    string         `json:"lock_key"`
		Preview    map[string]any `json:"preview"`
	}{
		TargetType: targetType,
		TargetID:   targetID,
		LockKey:    lockKey,
		Preview:    preview,
	})
}

func digestValue(value any) (BindingDigest, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return BindingDigest{}, fmt.Errorf("hermesconfirm: encode confirmation binding: %w", err)
	}
	return sha256.Sum256(encoded), nil
}

func (d BindingDigest) valid() bool {
	return d != BindingDigest{}
}
