package credentialacq

import (
	"encoding/json"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/accountident"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionprofile"
)

// 承载自动提取出的上游账户身份的 RedactedContext key。它们只保存非机密的
// account id / email / 来源 —— 绝不保存原始 id_token 或任何 bearer 凭据材料，
// 因此能通过 ValidateRedactedContext 的 secret scrubber。
const (
	RedactedKeyUpstreamAccountID    = "upstream_account_id"
	RedactedKeyUpstreamAccountEmail = "upstream_account_email"
	RedactedKeyAccountIDSource      = "account_id_source"
	RedactedKeySubscriptionLabel    = "subscription_label"
	RedactedKeySubscriptionStatus   = "subscription_status"
	RedactedKeySubscriptionSource   = "subscription_source"
	RedactedKeyClientIdentitySource = "client_identity_source"
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

// AttachSubscription 把凭据载荷中可观察到的套餐事实挂到候选项，并只把非机密的
// 系统标签、状态和来源镜像进预览上下文。套餐信息只服务账号管理，不能参与授权、
// 计费或配额判断；缺字段与解析失败也不会阻断凭据导入。
func AttachSubscription(candidate *CredentialCandidate) {
	if candidate == nil {
		return
	}
	if candidate.Subscription.Empty() {
		candidate.Subscription = subscriptionprofile.DetectPayload(candidate.Vendor, candidate.AuthMode, candidate.Payload)
	}
	if candidate.Subscription.Empty() {
		return
	}
	candidate.RedactedContext = setRedactedKey(candidate.RedactedContext, RedactedKeySubscriptionLabel, candidate.Subscription.Label())
	candidate.RedactedContext = setRedactedKey(candidate.RedactedContext, RedactedKeySubscriptionStatus, candidate.Subscription.Status)
	candidate.RedactedContext = setRedactedKey(candidate.RedactedContext, RedactedKeySubscriptionSource, candidate.Subscription.Source)
}

// attachOAuthResponseSubscription 先保留令牌自带的套餐声明；令牌没有可用
// 声明时，才把同次 OAuth 换码响应的明确套餐字段作为发行方证据。
// 这样既不会让响应体中的旧字段覆盖新令牌，也不会把真实换码响应
// 降级成普通导入证据。
func attachOAuthResponseSubscription(candidate *CredentialCandidate, rawPlan string) {
	if candidate == nil {
		return
	}
	AttachSubscription(candidate)
	rawPlan = strings.TrimSpace(rawPlan)
	if rawPlan == "" || candidate.Subscription.Source == subscriptionprofile.SourceIDTokenClaim ||
		candidate.Subscription.Source == subscriptionprofile.SourceAccessTokenClaim {
		return
	}
	candidate.Subscription = subscriptionprofile.FromRaw(
		strings.ToLower(strings.TrimSpace(candidate.Vendor)), rawPlan,
		subscriptionprofile.SourceOAuthResponse,
		subscriptionprofile.TrustIssuerResponse,
		subscriptionprofile.VerificationIssuerResponse,
		candidate.ExternalSubjectID, candidate.ExternalAccountID,
	)
	AttachSubscription(candidate)
}

func candidateFromDeviceTokenPayload(session Session, raw []byte) CredentialCandidate {
	candidate := CredentialCandidate{
		TenantID: session.TenantID, ProviderAccountID: session.ProviderAccountID,
		Vendor: session.Vendor, AuthMode: session.AuthMode, Payload: raw, ActorID: session.ActorID,
	}
	var fields map[string]any
	if json.Unmarshal(raw, &fields) != nil {
		return candidate
	}
	attachClientIdentitySource(&candidate, fields, session.ClientIdentitySource)
	if credentialstore.Normalize(candidate.Vendor) == credentialstore.VendorCopilot &&
		credentialstore.Normalize(candidate.AuthMode) == credentialstore.AuthModeCopilotOAuth {
		// 设备码端点签发的是 GitHub 授权材料，不是可直接调用 Copilot 的短期
		// 会话令牌。换名后 RuntimeMaterial 会保持拒绝，凭据存储层会立即排入
		// 刷新队列，由唯一刷新器换取 session_token 与动态 endpoint。
		if stringField(fields, "github_access_token") == "" {
			fields["github_access_token"] = stringField(fields, "access_token")
		}
		delete(fields, "access_token")
		delete(fields, "session_token")
		if normalized, err := json.Marshal(fields); err == nil {
			candidate.Payload = normalized
		}
	}
	if credentialstore.Normalize(candidate.Vendor) == credentialstore.VendorOpenAI {
		identity := accountident.ExtractChatGPT(
			stringField(fields, "id_token"),
			stringField(fields, "chatgpt_account_id"),
			stringField(fields, "chatgpt_user_id"),
		)
		if identity.Email == "" {
			identity.Email = stringField(fields, "email")
		}
		AttachIdentity(&candidate, identity)
	}
	attachOAuthResponseSubscription(&candidate, firstNonEmpty(
		stringField(fields, "chatgpt_plan_type"),
		stringField(fields, "subscription_tier"),
	))
	return candidate
}

// attachClientIdentitySource 将授权时选定的客户端身份写入最终凭据材料，确保后续
// 刷新继续使用同一套客户端合同；预览上下文只保存来源枚举，不保存客户端密钥。
func attachClientIdentitySource(candidate *CredentialCandidate, fields map[string]any, source string) {
	if candidate == nil || fields == nil {
		return
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return
	}
	fields["client_id_source"] = source
	if normalized, err := json.Marshal(fields); err == nil {
		candidate.Payload = normalized
	}
	candidate.RedactedContext = setRedactedKey(candidate.RedactedContext, RedactedKeyClientIdentitySource, source)
}

func setRedactedKey(ctx map[string]any, key, value string) map[string]any {
	if ctx == nil {
		ctx = map[string]any{}
	}
	ctx[key] = value
	return ctx
}
