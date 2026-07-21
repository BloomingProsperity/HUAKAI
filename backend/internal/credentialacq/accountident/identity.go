// Package accountident 从凭据获取时捕获的 OAuth token-exchange 响应或 id_token 中，
// 提取上游 provider 的账户身份（account id + email）。它作为一个职责独立的包存在，
// 以免让更大的 credentialacq 包吸收掉这部分逻辑。
//
// 这里捕获的身份是账户管理元数据，不是 HUAKAI 登录授权依据。未验签提取器只用于
// 端点和客户端身份均由服务端固定、且令牌刚由上游 TLS 换码返回的流程；普通文件导入
// 会被重新标记为不可信来源，不能借这里的来源名自动选择已有账号。要求强身份保证的
// 厂商使用独立验签入口。解析失败返回 manual 身份，不阻断凭据获取。
package accountident

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// Identity.Source 的来源标识值。它们让 admin 界面能区分自动探测出的上游 id
// 与 operator 手动录入的回退值，并供审计使用。
const (
	SourceManual             = "manual"
	SourceAnthropicAccountID = "anthropic_account_uuid"
	SourceChatGPTJWTClaim    = "chatgpt_jwt_claim"
	SourceOpenAITokenBody    = "openai_token_response"
	SourceGoogleIDTokenSub   = "google_id_token_sub"
	SourceXAIOIDCSubject     = "xai_oidc_verified_sub"
	SourceImportPayload      = "import_payload"
)

// FromVerifiedOIDCClaims 只接收已经完成签名、发行方、受众、时效和 nonce 校验的
// OIDC claims。主体是账号消歧的稳定依据；邮箱只作为运营展示元数据。
func FromVerifiedOIDCClaims(accountID, subject, email, source string) Identity {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return manualIdentity()
	}
	accountID = firstNonEmpty(accountID, subject)
	return Identity{
		AccountID: accountID,
		SubjectID: subject,
		Email:     strings.TrimSpace(email),
		Source:    strings.TrimSpace(source),
	}
}

// openAIAuthClaimKey 是 ChatGPT/Codex id_token 中带命名空间的自定义 claim，
// 携带 chatgpt 账户标识。它是一个 provider 定义的 claim 名（属于公开协议事实，
// 类似 header 名），并非借用的实现。
const openAIAuthClaimKey = "https://api.openai.com/auth"

// Identity 是获取时捕获的、非机密的上游账户元数据。
// 一个空的 Identity（或 Source == SourceManual 的 Identity）表示提取没有得到
// 任何结果，应以 manual/operator 录入的值为准。
type Identity struct {
	AccountID string
	SubjectID string
	Email     string
	Source    string
}

// Empty 报告该 identity 是否不携带任何上游账号、个人主体或邮箱标识。
func (i Identity) Empty() bool {
	return strings.TrimSpace(i.AccountID) == "" &&
		strings.TrimSpace(i.SubjectID) == "" &&
		strings.TrimSpace(i.Email) == ""
}

// manualIdentity 是 fail-open 的结果：无 id、无 email、来源为 manual。
func manualIdentity() Identity {
	return Identity{Source: SourceManual}
}

// ParseJWTClaimsUnverified 按 "." 切分 compact JWT，对 payload 段做 base64url 解码
// （补回 compact 编码省略的 padding），再反序列化成一个通用的 claims map。签名有意
// 不做验证：这是对 provider auth server 已经颁发的 token 做身份元数据自省，
// 不是一个认证步骤。调用方必须把结果当作不可信的展示用元数据来对待。
func ParseJWTClaimsUnverified(idToken string) (map[string]any, error) {
	trimmed := strings.TrimSpace(idToken)
	if trimmed == "" {
		return nil, fmt.Errorf("accountident: empty id_token")
	}
	parts := strings.Split(trimmed, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("accountident: id_token must have 3 segments, got %d", len(parts))
	}
	decoded, err := decodeBase64URLSegment(parts[1])
	if err != nil {
		return nil, fmt.Errorf("accountident: decode claims segment: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return nil, fmt.Errorf("accountident: unmarshal claims: %w", err)
	}
	return claims, nil
}

// decodeBase64URLSegment 解码一个可能缺少尾部 padding 的 base64url 段
// （compact JWT 序列化会丢弃 padding）。它补回 padding，使固定字母表的解码器
// 能够读取它；没有这一步，长度不是 4 的倍数的段会解码失败。
func decodeBase64URLSegment(segment string) ([]byte, error) {
	segment = strings.TrimSpace(segment)
	if pad := len(segment) % 4; pad != 0 {
		segment += strings.Repeat("=", 4-pad)
	}
	return base64.URLEncoding.DecodeString(segment)
}

// ExtractAnthropic 从 Anthropic token-exchange 响应字段构建一个 Identity。
// accountUUID 是响应中的 account.uuid，accountEmail 是 account.email_address，
// topEmail 是顶层 email。account uuid 是稳定的上游标识符；email 优先取
// account 作用域内的值，再取顶层值。
func ExtractAnthropic(accountUUID, accountEmail, topEmail string) Identity {
	id := strings.TrimSpace(accountUUID)
	if id == "" {
		return manualIdentity()
	}
	return Identity{
		AccountID: id,
		Email:     firstNonEmpty(accountEmail, topEmail),
		Source:    SourceAnthropicAccountID,
	}
}

// ExtractChatGPT 从 ChatGPT/Codex id_token 加上 token-body 回退值构建一个 Identity。
// account id 的优先级：带命名空间的 auth claim 中的 chatgpt account id，
// 然后是 token-body 值，再然后是标准的 subject claim。Email 取自 email claim。
// 格式错误/为空的 id_token 不会中止流程：若存在 body 回退值仍会使用它，
// 否则返回一个 manual 的 Identity。
func ExtractChatGPT(idToken, bodyAccountID, bodySubjectID string) Identity {
	bodyAccountID = strings.TrimSpace(bodyAccountID)
	bodySubjectID = strings.TrimSpace(bodySubjectID)
	claims, err := ParseJWTClaimsUnverified(idToken)
	if err != nil {
		accountID := firstNonEmpty(bodyAccountID, bodySubjectID)
		if accountID != "" {
			return Identity{
				AccountID: accountID,
				SubjectID: bodySubjectID,
				Source:    SourceOpenAITokenBody,
			}
		}
		return manualIdentity()
	}
	claimAccountID := chatgptAccountIDFromClaims(claims)
	claimSubjectID := stringClaim(claims, "sub")
	subject := firstNonEmpty(claimSubjectID, bodySubjectID)
	accountID := firstNonEmpty(claimAccountID, bodyAccountID, subject)
	if accountID == "" {
		return manualIdentity()
	}
	source := SourceChatGPTJWTClaim
	if claimAccountID == "" && (bodyAccountID != "" || claimSubjectID == "") {
		source = SourceOpenAITokenBody
	}
	return Identity{
		AccountID: accountID,
		SubjectID: subject,
		Email:     stringClaim(claims, "email"),
		Source:    source,
	}
}

// ExtractGemini 从 Google/Gemini id_token 构建一个 Identity。account id 取自
// subject claim；email 取自 email claim，并以传入的 userinfoEmail 作为回退。
// 格式错误/为空的 id_token 返回一个 manual 的 Identity（实时的 userinfo HTTP
// 查询被推迟为后续 roadmap 项，以避免在受 SSRF 防护的路径中引入新的 egress）。
func ExtractGemini(idToken, userinfoEmail string) Identity {
	claims, err := ParseJWTClaimsUnverified(idToken)
	if err != nil {
		return manualIdentity()
	}
	subject := stringClaim(claims, "sub")
	if subject == "" {
		return manualIdentity()
	}
	return Identity{
		AccountID: subject,
		SubjectID: subject,
		Email:     firstNonEmpty(stringClaim(claims, "email"), userinfoEmail),
		Source:    SourceGoogleIDTokenSub,
	}
}

// chatgptAccountIDFromClaims 从带命名空间的 auth claim 对象中读取 chatgpt account id。
// 该 claim 是一个任意的 JSON 对象；读取时做了防御性处理。
func chatgptAccountIDFromClaims(claims map[string]any) string {
	raw, ok := claims[openAIAuthClaimKey]
	if !ok {
		return ""
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	return stringClaim(obj, "chatgpt_account_id")
}

// stringClaim 从通用的 claims map 中读取 key 对应的字符串值并 trim 后返回。
func stringClaim(claims map[string]any, key string) string {
	if claims == nil {
		return ""
	}
	if v, ok := claims[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// firstNonEmpty 返回第一个 trim 后非空的值。
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
