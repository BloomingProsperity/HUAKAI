// HUAKAI · iKun

package payment

import (
	"context"
	"encoding/json"

	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

// sanitizeAuditPayload 对审计 payload 过隐私红线脱敏 — 严禁落真实商户密钥 / 完整 webhook body /
// 个人敏感支付材料。命中红线则整段替换为脱敏标记。
func sanitizeAuditPayload(ctx context.Context, payload map[string]any) map[string]any {
	if len(payload) == 0 {
		return nil
	}
	raw := privacy.SafePayloadOrBlocked(ctx, payload)
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		return map[string]any{
			"redaction_result": privacy.RedactionResultBlocked,
			"error_class":      privacy.ErrorClassPrivacyGuardHit,
		}
	}
	return out
}
