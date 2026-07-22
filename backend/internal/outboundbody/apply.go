package outboundbody

import (
	"github.com/BloomingProsperity/HUAKAI/internal/claudecodecloak"
	"github.com/BloomingProsperity/HUAKAI/internal/mimicryidentity"
)

// Apply 按 Plan 顺序施加变换并返回新 body（或 fail-open 的当前拷贝）。
// 顺序：system 三块 → metadata.user_id（user_id 依赖可能已改过的 body，无交叉依赖）。
func Apply(body []byte, plan Plan) []byte {
	if plan.SkipAll {
		return clone(body)
	}
	out := body
	if plan.SystemCloak {
		res := claudecodecloak.Apply(out, claudecodecloak.Options{CLIVersion: plan.CLIVersion})
		if len(res.Body) > 0 {
			out = res.Body
		}
	}
	if plan.IdentityUserID {
		out = mimicryidentity.RewriteForDispatch(
			out,
			plan.AccountID,
			plan.ExternalAccountID,
			plan.AccountType,
			plan.ClientSessionID,
			plan.CLIVersion,
		)
	}
	return out
}

func clone(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
