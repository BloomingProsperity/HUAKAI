package credentialacq

import (
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/accountident"
)

// 承载自动提取出的上游账户身份的 RedactedContext key。它们只保存非机密的
// account id / email / 来源 —— 绝不保存原始 id_token 或任何 bearer 凭据材料，
// 因此能通过 ValidateRedactedContext 的 secret scrubber。
const (
	RedactedKeyUpstreamAccountID    = "upstream_account_id"
	RedactedKeyUpstreamAccountEmail = "upstream_account_email"
	RedactedKeyAccountIDSource      = "account_id_source"
)

// AttachIdentity 把自动提取出的上游账户身份挂接到一个 credential candidate 上：
// 它设置持久化的 candidate 字段（落库为可查询的列），并把非机密的值镜像进
// RedactedContext（用于即时的审计/UI 展示，零 schema 改动）。对于空 identity 它
// 是 no-op，从而让 manual/operator 绑定为准。原始 id_token 绝不会放在这里 ——
// 只放提取出的、非机密的值。
func AttachIdentity(candidate *CredentialCandidate, identity accountident.Identity) {
	if candidate == nil {
		return
	}
	accountID := strings.TrimSpace(identity.AccountID)
	subjectID := strings.TrimSpace(identity.SubjectID)
	candidate.AccountIDSource = strings.TrimSpace(identity.Source)
	if identity.Empty() {
		// 即使没有提取到任何内容也记录来源，让 UI 能展示该绑定已回退为 manual，
		// 但不添加任何 id/email key。
		if candidate.AccountIDSource != "" {
			candidate.RedactedContext = setRedactedKey(candidate.RedactedContext, RedactedKeyAccountIDSource, candidate.AccountIDSource)
		}
		return
	}
	candidate.ExternalAccountID = accountID
	candidate.ExternalSubjectID = subjectID
	candidate.ExternalAccountEmail = strings.TrimSpace(identity.Email)

	ctx := candidate.RedactedContext
	if accountID != "" {
		ctx = setRedactedKey(ctx, RedactedKeyUpstreamAccountID, accountID)
	}
	if candidate.ExternalAccountEmail != "" {
		ctx = setRedactedKey(ctx, RedactedKeyUpstreamAccountEmail, candidate.ExternalAccountEmail)
	}
	if candidate.AccountIDSource != "" {
		ctx = setRedactedKey(ctx, RedactedKeyAccountIDSource, candidate.AccountIDSource)
	}
	candidate.RedactedContext = ctx
}

func setRedactedKey(ctx map[string]any, key, value string) map[string]any {
	if ctx == nil {
		ctx = map[string]any{}
	}
	ctx[key] = value
	return ctx
}
