package channelhealth

import (
	"context"
	"encoding/json"

	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

func sanitizePayloadMap(ctx context.Context, payload map[string]any) map[string]any {
	if payload == nil {
		payload = map[string]any{}
	}
	raw := privacy.SafePayloadOrBlocked(ctx, payload)
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		return map[string]any{"redaction_result": privacy.RedactionResultBlocked, "error_class": privacy.ErrorClassPrivacyGuardHit}
	}
	return out
}
