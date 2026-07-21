// Package subscriptionprofile 负责把上游明确给出的订阅套餐归一成账号管理事实。
package subscriptionprofile

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

const (
	MappingVersion = 2

	VendorAnthropic   = "anthropic"
	VendorOpenAI      = "openai"
	VendorGemini      = "gemini"
	VendorAntigravity = "antigravity"
	VendorGrok        = "grok"
	VendorKimi        = "kimi"
	VendorCopilot     = "copilot"
	VendorWindsurf    = "windsurf"

	PlanUnknown = "unknown"

	ScopeUnknown   = "unknown"
	ScopePersonal  = "personal"
	ScopeWorkspace = "workspace"

	StatusObserved     = "observed"
	StatusUnknownValue = "unknown_value"
	StatusMissing      = "missing"
	StatusStale        = "stale"
	StatusParseFailed  = "parse_failed"
	StatusConflict     = "conflict"

	SourceProviderAPI       = "provider_api"
	SourceOAuthResponse     = "oauth_token_response"
	SourceIDTokenClaim      = "id_token_claim"
	SourceAccessTokenClaim  = "access_token_claim"
	SourceImportPayload     = "import_payload"
	SourceOperator          = "operator"
	SourceCredentialRefresh = "credential_refresh"

	TrustVerifiedAPI    = "verified_api"
	TrustIssuerResponse = "issuer_response"
	TrustVerifiedToken  = "verified_token"
	TrustUnverifiedJWT  = "unverified_token"
	TrustImported       = "imported"
	TrustManual         = "manual"

	VerificationVerified       = "verified"
	VerificationIssuerResponse = "issuer_response"
	VerificationUnverified     = "unverified"
	VerificationOperator       = "operator"
)

const openAIAuthClaimKey = "https://api.openai.com/auth"

// Observation 是一次不含秘密的套餐观测。RawPlan 必须保留上游原值，Plan 只保存规范值。
type Observation struct {
	Vendor         string `json:"vendor"`
	Plan           string `json:"plan"`
	RawPlan        string `json:"raw_plan,omitempty"`
	Scope          string `json:"scope"`
	SubjectRef     string `json:"subject_ref,omitempty"`
	WorkspaceRef   string `json:"workspace_ref,omitempty"`
	Source         string `json:"source"`
	Trust          string `json:"trust"`
	Verification   string `json:"verification"`
	Status         string `json:"status"`
	MappingVersion int    `json:"mapping_version"`
	ErrorClass     string `json:"error_class,omitempty"`
}

// Empty 表示调用方没有提供任何套餐观测，而不是上游明确返回了 unknown。
func (o Observation) Empty() bool {
	return strings.TrimSpace(o.Vendor) == "" || strings.TrimSpace(o.Status) == ""
}

// Label 返回只读系统标签。人工 tags 不得写入或覆盖该值。
func (o Observation) Label() string {
	vendor := normalizeToken(o.Vendor)
	plan := normalizeToken(o.Plan)
	if vendor == "" {
		return ""
	}
	if plan == "" {
		plan = PlanUnknown
	}
	return vendor + ":" + plan
}

// FromRaw 把一个明确出现的上游值转为规范观测；未知值保留原文且绝不降级成免费套餐。
func FromRaw(vendor, rawPlan, source, trust, verification, subjectRef, workspaceRef string) Observation {
	vendor = canonicalVendor(vendor)
	rawPlan = strings.TrimSpace(rawPlan)
	observation := Observation{
		Vendor: vendor, RawPlan: rawPlan,
		Source: strings.TrimSpace(source), Trust: strings.TrimSpace(trust),
		Verification: strings.TrimSpace(verification),
		SubjectRef:   strings.TrimSpace(subjectRef), WorkspaceRef: strings.TrimSpace(workspaceRef),
		MappingVersion: MappingVersion,
	}
	if observation.Source == "" {
		observation.Source = SourceImportPayload
	}
	if observation.Trust == "" {
		observation.Trust = TrustImported
	}
	if observation.Verification == "" {
		observation.Verification = VerificationUnverified
	}
	if rawPlan == "" {
		observation.Plan = PlanUnknown
		observation.Scope = ScopeUnknown
		observation.Status = StatusMissing
		return observation
	}
	observation.Plan, observation.Scope = canonicalPlan(vendor, rawPlan)
	// 个人套餐令牌也可能携带账号标识，但它不是工作区套餐归属。
	// 账号身份由凭据元数据单独保存，套餐投影只保留真正的工作区引用。
	if observation.Scope == ScopePersonal {
		observation.WorkspaceRef = ""
	}
	if observation.Plan == PlanUnknown {
		observation.Status = StatusUnknownValue
	} else {
		observation.Status = StatusObserved
	}
	return observation
}

// Missing 记录一次成功处理但没有套餐字段的结果。它不会声称账号是免费套餐。
func Missing(vendor, source string) Observation {
	return FromRaw(vendor, "", source, TrustImported, VerificationUnverified, "", "")
}

// ParseFailed 记录结构化套餐证据解析失败。凭据本身仍可继续导入。
func ParseFailed(vendor, source, errorClass string) Observation {
	observation := Missing(vendor, source)
	observation.Status = StatusParseFailed
	observation.ErrorClass = strings.TrimSpace(errorClass)
	return observation
}

// DetectPayload 从 HUAKAI 已经接收的凭据对象中提取非阻断套餐观测。
// 令牌只解码 payload，不把未验签声明用于授权、计费或配额。
func DetectPayload(vendor, authMode string, raw []byte) Observation {
	if !Supported(vendor, authMode) {
		return Observation{}
	}
	vendor = canonicalVendorForMode(vendor, authMode)
	var fields map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &fields) != nil || fields == nil {
		return ParseFailed(vendor, SourceImportPayload, "credential_payload_invalid")
	}
	subjectRef := firstString(fields, "external_subject_id", "chatgpt_user_id", "user_id")
	workspaceRef := firstString(fields, "external_account_id", "chatgpt_account_id", "account_id", "workspace_id", "organization_id")
	if vendor == VendorOpenAI {
		if token := firstString(fields, "id_token", "idToken"); token != "" {
			if observation, found, parsed := openAIJWTObservation(token, SourceIDTokenClaim); found {
				return fillRefs(observation, subjectRef, workspaceRef)
			} else if !parsed {
				if explicit := explicitPlan(fields); explicit != "" {
					return FromRaw(vendor, explicit, SourceImportPayload, TrustImported, VerificationUnverified, subjectRef, workspaceRef)
				}
				return ParseFailed(vendor, SourceIDTokenClaim, "id_token_claim_invalid")
			}
		}
		if token := firstString(fields, "access_token", "accessToken", "session_token"); token != "" {
			if observation, found, _ := openAIJWTObservation(token, SourceAccessTokenClaim); found {
				return fillRefs(observation, subjectRef, workspaceRef)
			}
		}
	}
	if explicit := explicitPlan(fields); explicit != "" {
		return FromRaw(vendor, explicit, SourceImportPayload, TrustImported, VerificationUnverified, subjectRef, workspaceRef)
	}
	return Missing(vendor, SourceImportPayload)
}

// Supported 表示该凭据模式存在可验证或可明确缺失的套餐证据入口。
func Supported(vendor, authMode string) bool {
	vendor = canonicalVendor(vendor)
	mode := normalizeToken(authMode)
	switch vendor {
	case VendorOpenAI:
		return mode == "chatgpt_oauth" || mode == "codex_cli_oauth" ||
			mode == "codex_web_oauth" || mode == "codex_agent_identity" || mode == "refresh_token"
	case VendorAnthropic:
		return mode == "claude_ai_oauth" || mode == "claude_code" || mode == "claude_setup_token"
	case VendorAntigravity:
		return mode == "oauth"
	case VendorGemini:
		return mode == "code_assist" || mode == "google_one" || mode == "oauth" || mode == "antigravity"
	case VendorGrok:
		return mode == "xai_oauth"
	case VendorKimi:
		return mode == "kimi_oauth"
	case VendorCopilot:
		return mode == "copilot_oauth"
	case VendorWindsurf:
		return mode == "oauth"
	default:
		return false
	}
}

// TrustRank 只用于同一账号作用域内的事实仲裁；值越高，证据越强。
func TrustRank(trust string) int {
	switch strings.TrimSpace(trust) {
	case TrustVerifiedAPI:
		return 60
	case TrustIssuerResponse:
		return 50
	case TrustVerifiedToken:
		return 45
	case TrustUnverifiedJWT:
		return 30
	case TrustImported:
		return 20
	case TrustManual:
		return 10
	default:
		return 0
	}
}

func canonicalVendorForMode(vendor, authMode string) string {
	vendor = canonicalVendor(vendor)
	mode := normalizeToken(authMode)
	if (vendor == "gemini" && mode == "antigravity") || vendor == VendorAntigravity {
		return VendorAntigravity
	}
	return vendor
}

func canonicalVendor(vendor string) string {
	return normalizeToken(vendor)
}

func canonicalPlan(vendor, raw string) (string, string) {
	value := normalizeToken(raw)
	switch vendor {
	case VendorOpenAI:
		switch value {
		case "free", "go", "plus", "pro", "team", "business":
			return value, openAIScope(value)
		case "prolite", "pro_lite":
			return "pro_lite", ScopePersonal
		case "self_serve_business_usage_based", "business_usage_based":
			return "business_usage_based", ScopeWorkspace
		case "enterprise", "hc":
			return "enterprise", ScopeWorkspace
		case "enterprise_cbp_usage_based", "enterprise_usage_based":
			return "enterprise_usage_based", ScopeWorkspace
		case "education", "edu":
			return "education", ScopeWorkspace
		}
	case VendorAnthropic:
		switch value {
		case "free", "pro", "max":
			return value, ScopePersonal
		case "max_5x", "max5x", "max_5":
			return "max_5x", ScopePersonal
		case "max_20x", "max20x", "max_20":
			return "max_20x", ScopePersonal
		case "team", "business", "enterprise":
			return value, ScopeWorkspace
		}
	case VendorAntigravity:
		switch value {
		case "free", "free_tier":
			return "free", ScopePersonal
		case "pro", "g1_pro", "g1_pro_tier":
			return "pro", ScopePersonal
		case "ultra", "g1_ultra", "g1_ultra_tier":
			return "ultra", ScopePersonal
		}
	case VendorGemini:
		switch value {
		case "google_one_free", "free", "free_tier":
			return "free", ScopePersonal
		case "google_ai_plus", "ai_plus", "plus":
			return "plus", ScopePersonal
		case "google_ai_pro", "pro":
			return "pro", ScopePersonal
		case "google_ai_ultra", "ultra":
			return "ultra", ScopePersonal
		case "gcp_standard", "standard":
			return "standard", ScopeWorkspace
		case "gcp_enterprise", "enterprise":
			return "enterprise", ScopeWorkspace
		case "aistudio_free":
			return "aistudio_free", ScopePersonal
		case "aistudio_paid":
			return "aistudio_paid", ScopeUnknown
		}
	case VendorGrok:
		switch value {
		case "free":
			return "free", ScopePersonal
		case "supergrok_lite", "super_grok_lite":
			return "supergrok_lite", ScopePersonal
		case "supergrok":
			return "supergrok", ScopePersonal
		case "supergrok_heavy", "super_grok_heavy":
			return "supergrok_heavy", ScopePersonal
		case "business", "enterprise":
			return value, ScopeWorkspace
		}
	case VendorCopilot:
		switch value {
		case "free", "student", "pro", "max":
			return value, ScopePersonal
		case "pro_plus", "pro+":
			return "pro_plus", ScopePersonal
		case "business", "enterprise":
			return value, ScopeWorkspace
		}
	case VendorKimi:
		switch value {
		case "free", "adagio":
			return "adagio", ScopePersonal
		case "moderato", "allegretto", "allegro", "vivace":
			return value, ScopePersonal
		}
	case VendorWindsurf:
		switch value {
		case "free", "pro", "max":
			return value, ScopePersonal
		case "team", "teams":
			return "teams", ScopeWorkspace
		case "enterprise":
			return "enterprise", ScopeWorkspace
		}
	}
	return PlanUnknown, ScopeUnknown
}

func openAIScope(plan string) string {
	switch plan {
	case "team", "business", "business_usage_based", "enterprise", "enterprise_usage_based", "education":
		return ScopeWorkspace
	default:
		return ScopePersonal
	}
}

func openAIJWTObservation(token, source string) (Observation, bool, bool) {
	claims, err := parseJWTClaims(token)
	if err != nil {
		return Observation{}, false, false
	}
	authClaims, _ := claims[openAIAuthClaimKey].(map[string]any)
	plan := firstString(authClaims, "chatgpt_plan_type", "plan_type")
	if plan == "" {
		return Observation{}, false, true
	}
	subjectRef := firstString(authClaims, "chatgpt_user_id")
	if subjectRef == "" {
		subjectRef = firstString(claims, "sub")
	}
	workspaceRef := firstString(authClaims, "chatgpt_account_id", "workspace_id", "organization_id")
	return FromRaw(VendorOpenAI, plan, source, TrustUnverifiedJWT, VerificationUnverified, subjectRef, workspaceRef), true, true
}

func parseJWTClaims(token string) (map[string]any, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return nil, errors.New("JWT 必须包含三个分段")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func explicitPlan(fields map[string]any) string {
	return firstString(fields, "chatgpt_plan_type", "plan_type", "subscription_plan", "subscription_tier", "subscription_tier_raw", "tier_id")
}

func fillRefs(observation Observation, subjectRef, workspaceRef string) Observation {
	if observation.SubjectRef == "" {
		observation.SubjectRef = strings.TrimSpace(subjectRef)
	}
	if observation.Scope != ScopePersonal && observation.WorkspaceRef == "" {
		observation.WorkspaceRef = strings.TrimSpace(workspaceRef)
	}
	return observation
}

func firstString(fields map[string]any, names ...string) string {
	for _, name := range names {
		if value, ok := fields[name].(string); ok {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func normalizeToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer("-", "_", " ", "_", "/", "_").Replace(value)
	for strings.Contains(value, "__") {
		value = strings.ReplaceAll(value, "__", "_")
	}
	return strings.Trim(value, "_")
}
