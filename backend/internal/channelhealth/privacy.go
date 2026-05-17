package channelhealth

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

// sanitizePayloadMap 对 payload 做字段级 redaction：
//   - 敏感字段（api_key / cookie 等）被删除，safe 字段（tenant_id 等）保留
//   - 仅当 SanitizePayload 返回非 ErrUnsafePayload 错误（如 ErrFreeformString）
//     才返回整体 sentinel，避免把 safe 字段一并丢掉
func sanitizePayloadMap(ctx context.Context, payload map[string]any) map[string]any {
	if payload == nil {
		payload = map[string]any{}
	}
	raw, err := privacy.SanitizePayload(ctx, payload)
	// ErrUnsafePayload 表示字段级 redaction 已完成（部分字段被 strip），raw 仍有效
	if err != nil && !errors.Is(err, privacy.ErrUnsafePayload) {
		return map[string]any{"redaction_result": privacy.RedactionResultBlocked, "error_class": privacy.ErrorClassPrivacyGuardHit}
	}
	if len(raw) == 0 {
		return map[string]any{"redaction_result": privacy.RedactionResultBlocked, "error_class": privacy.ErrorClassPrivacyGuardHit}
	}
	var out map[string]any
	if jsonErr := json.Unmarshal(raw, &out); jsonErr != nil || out == nil {
		return map[string]any{"redaction_result": privacy.RedactionResultBlocked, "error_class": privacy.ErrorClassPrivacyGuardHit}
	}
	return out
}
