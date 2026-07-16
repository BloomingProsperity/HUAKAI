package credentialacq

import (
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/accountident"
)

// 承载自动提取出的上游账户身份的 RedactedContext key。它们只保存账号作用域、
// email 和来源，不保存个人 subject、原始 id_token 或任何 bearer 凭据材料。
const (
	RedactedKeyUpstreamAccountID    = "upstream_account_id"
	RedactedKeyUpstreamAccountEmail = "upstream_account_email"
	RedactedKeyAccountIDSource      = "account_id_source"
)

// AttachIdentity 把自动提取出的上游账户身份挂接到一个 credential candidate 上：
// 它设置持久化的 candidate 字段，并把账号作用域、email 和来源镜像进
// RedactedContext。个人 subject 只进入专用元数据列，不进入审计上下文。
// 原始 id_token 和 bearer 凭据材料绝不会放在这里。
func AttachIdentity(candidate *CredentialCandidate, identity accountident.Identity) {
	if candidate == nil {
		return
	}
	accountID := strings.TrimSpace(identity.AccountID)
	subjectID := strings.TrimSpace(identity.SubjectID)
	email := strings.TrimSpace(identity.Email)
	candidate.AccountIDSource = strings.TrimSpace(identity.Source)
	if accountID == "" && subjectID == "" && email == "" {
		// 即使没有提取到任何内容也记录来源，让 UI 能展示该绑定已回退为 manual，
		// 但不添加任何 id/email key。
		if candidate.AccountIDSource != "" {
			candidate.RedactedContext = setRedactedKey(candidate.RedactedContext, RedactedKeyAccountIDSource, candidate.AccountIDSource)
		}
		return
	}
	candidate.ExternalAccountID = accountID
	candidate.ExternalSubjectID = subjectID
	candidate.ExternalAccountEmail = email

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
