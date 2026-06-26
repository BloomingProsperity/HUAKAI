// Package gateway 上游 provider 错误归一化(A13 ERROR_RULES 规则表)。
// 规格: docs/specs/rate-limiting.md A13 / DR-009 1 Q1。
//
// 硬底线(DR-009 6.6): FSM 绝不能仅凭一个 ambiguous 信号就自动到达
// disabled。结构上强制保证: ambiguous 规则只能产出
// RetryActionCountedDisable / Cooldown / WarnOnly,绝不会产出
// RetryActionPermanentDisable。
//
// D8 新增(2026-05-06 vendor-drift-audit.md):
// Anthropic 现在记录了 3 个新的 typed error class:
//
//	402 → billing_error   (R-021, 在 keyword 专用的 R-007 之后兜底)
//	504 → timeout_error   (R-022, upstream gateway 超时)
//	413 → request_too_large (R-023, 客户端错误, 不重试)
//
// 来源: platform.claude.com/docs/en/api/errors (2026-05-06 抓取)。
package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ErrorClass 枚举来自 A13 的 14 个归一化错误类别(D8 已更新)。
type ErrorClass string

const (
	ErrorClassOAuthInvalidGrant    ErrorClass = "oauth_invalid_grant"
	ErrorClassTokenRevoked         ErrorClass = "token_revoked"
	ErrorClassKYCRequired          ErrorClass = "kyc_required"
	ErrorClassOrgDisabled          ErrorClass = "org_disabled"
	ErrorClassWorkspaceDeactivated ErrorClass = "workspace_deactivated"
	ErrorClassCreditExhausted      ErrorClass = "credit_exhausted"
	ErrorClassPlatformPolicy       ErrorClass = "platform_policy"
	ErrorClassRateLimited          ErrorClass = "upstream_rate_limited"
	ErrorClassOverloaded           ErrorClass = "upstream_overloaded"
	ErrorClassServerError          ErrorClass = "upstream_5xx"
	ErrorClassNetworkTimeout       ErrorClass = "network_timeout"
	ErrorClassUnknown              ErrorClass = "unknown_upstream"

	// D8 新增 —— Anthropic 新的 typed error class(2026-05-06)。
	// ErrorClassUpstreamTimeout 区分 upstream 自身的 gateway 超时(504)
	// 与本地 network 超时(R-019 ErrorClassNetworkTimeout, status=0)。
	ErrorClassUpstreamTimeout ErrorClass = "upstream_timeout"
	ErrorClassRequestTooLarge ErrorClass = "request_too_large"
)

// Confidence 是 Classification 中携带的粗粒度信号质量指示。
type Confidence string

const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

// DisableTier 编码 DR-009 Q1: iron_clad = 永久失效的无歧义铁证
// (5 个 keyword); ambiguous = 暂时性/未知。
type DisableTier string

const (
	TierIronClad  DisableTier = "iron_clad"
	TierAmbiguous DisableTier = "ambiguous"
	TierNone      DisableTier = ""
)

// RetryAction 是给调用方规定的动作。
type RetryAction string

const (
	RetryActionPermanentDisable RetryAction = "permanent_disable"
	RetryActionCountedDisable   RetryAction = "counted_disable"
	RetryActionCooldown         RetryAction = "cooldown"
	RetryActionWarnOnly         RetryAction = "warn_only"
	RetryActionPassThrough      RetryAction = "pass_through"
)

// FsmTransition 是建议的 A22 FSM 目标状态。分类器不会变更 FSM 状态;
// 此字段只是给 FSM 调用方的提示。
type FsmTransition string

const (
	FsmTransitionDisabled   FsmTransition = "disabled"
	FsmTransitionDegraded   FsmTransition = "degraded"
	FsmTransitionCooling    FsmTransition = "cooling_down"
	FsmTransitionNoChange   FsmTransition = "no_transition"
	FsmTransitionManualOnly FsmTransition = "operator_review"
)

// HeaderMatch 可选地按响应 header 对规则加以约束。
type HeaderMatch struct {
	Name     string
	Equals   string
	Contains string
}

// ErrorRule 是 ERROR_RULES 表中的一行。
type ErrorRule struct {
	RuleID      string
	Version     int
	Priority    int    // 升序 = 优先级更高
	Provider    string // "*" = 通配
	HTTPStatus  string // "*" = 通配, "5xx" = 区间, 否则为精确整数
	BodyKeyword string // 大小写不敏感的子串匹配; "" = 无约束
	HeaderMatch HeaderMatch
	Class       ErrorClass
	Action      RetryAction
	Tier        DisableTier
}

// Classification 是 Classify() 的输出。它携带了 A22(FSM)与 A11(审计)
// 所需的全部信息, 无需再次解析 upstream 响应。
type Classification struct {
	Class         ErrorClass
	Confidence    Confidence
	RuleID        string
	RuleVersion   int
	Tier          DisableTier
	RetryAction   RetryAction
	FsmTransition FsmTransition
	RetryAfterMs  int64
}

// IronCladKeywords 是 DR-009 1 Q1 规定的恰好 5 个 keyword 集合。
// 外部调用方(自定义规则加载器、审计重分类)应查询此集合,
// 而不要在本地硬编码这个列表。
var IronCladKeywords = map[string]struct{}{
	"invalid_grant":         {},
	"identity verification": {},
	"org_disabled":          {},
	"token_revoked":         {},
	"deactivated_workspace": {},
}

// IsIronCladKeyword 报告某个 keyword 是否属于 DR-009 1 Q1 / 综合 6.6
// 规定的恰好 5 个 iron_clad 集合。
func IsIronCladKeyword(keyword string) bool {
	_, ok := IronCladKeywords[strings.ToLower(strings.TrimSpace(keyword))]
	return ok
}

const (
	keywordInvalidGrant                = "invalid_grant"
	keywordIdentityVerification        = "identity verification"
	keywordOrgDisabled                 = "org_disabled"
	keywordTokenRevoked                = "token_revoked"
	keywordDeactivatedWorkspace        = "deactivated_workspace"
	keywordTokenInvalidated            = "token_invalidated"
	keywordCredit                      = "credit"
	keywordCreditBalance               = "credit balance"
	keywordValidation                  = "validation"
	keywordPermissionDenied            = "permission denied"
	keywordThrottling                  = "throttling"
	keywordThrottlingException         = "ThrottlingException"
	keywordServiceUnavailableException = "ServiceUnavailableException"
	keywordTimeout                     = "timeout"
)

// errorRules 是 A13 规则表, 按「优先级再具体度」的顺序求值。
var errorRules = []ErrorRule{
	// 优先级 10 - iron_clad 永久信号(5 个强制 + 1 个别名)
	{RuleID: "R-001", Version: 1, Priority: 10, Provider: "*", HTTPStatus: "401",
		BodyKeyword: keywordInvalidGrant, Class: ErrorClassOAuthInvalidGrant,
		Action: RetryActionPermanentDisable, Tier: TierIronClad},
	{RuleID: "R-002", Version: 1, Priority: 10, Provider: "*", HTTPStatus: "400",
		BodyKeyword: keywordIdentityVerification, Class: ErrorClassKYCRequired,
		Action: RetryActionPermanentDisable, Tier: TierIronClad},
	{RuleID: "R-003", Version: 1, Priority: 10, Provider: "*", HTTPStatus: "400",
		BodyKeyword: keywordOrgDisabled, Class: ErrorClassOrgDisabled,
		Action: RetryActionPermanentDisable, Tier: TierIronClad},
	{RuleID: "R-004", Version: 1, Priority: 10, Provider: "*", HTTPStatus: "401",
		BodyKeyword: keywordTokenRevoked, Class: ErrorClassTokenRevoked,
		Action: RetryActionPermanentDisable, Tier: TierIronClad},
	// 漂移 D3 (docs/reference_delta/2026-05-06/vendor-drift-audit.md):
	// OpenAI 文档不再为 billing/deactivation 给出 402。把
	// deactivated_workspace keyword 保留为 OpenAI 范围内、跨状态码的防御性匹配。
	// 抓取 URL: https://developers.openai.com/api/docs/guides/error-codes
	// 与 https://platform.claude.com/docs/en/api/errors (2026-05-06 抓取)。
	{RuleID: "R-005", Version: 2, Priority: 10, Provider: "openai", HTTPStatus: "*",
		BodyKeyword: keywordDeactivatedWorkspace, Class: ErrorClassWorkspaceDeactivated,
		Action: RetryActionPermanentDisable, Tier: TierIronClad},
	// R-006 token_invalidated 等同 token_revoked 处理(厂商同义词)。
	{RuleID: "R-006", Version: 1, Priority: 10, Provider: "*", HTTPStatus: "401",
		BodyKeyword: keywordTokenInvalidated, Class: ErrorClassTokenRevoked,
		Action: RetryActionPermanentDisable, Tier: TierIronClad},

	// 优先级 20 - credit / billing iron_clad。
	// 漂移 D3 把遗留的 402/400 credit keyword 范围收窄到仅 Anthropic;OpenAI
	// 现行文档对 rate_limit_error 用 429, 不再记录 402。
	{RuleID: "R-007", Version: 2, Priority: 20, Provider: "anthropic", HTTPStatus: "402",
		BodyKeyword: keywordCredit, Class: ErrorClassCreditExhausted,
		Action: RetryActionPermanentDisable, Tier: TierIronClad},
	{RuleID: "R-008", Version: 2, Priority: 20, Provider: "anthropic", HTTPStatus: "400",
		BodyKeyword: keywordCreditBalance, Class: ErrorClassCreditExhausted,
		Action: RetryActionPermanentDisable, Tier: TierIronClad},

	// 优先级 25 - D8: Anthropic 402 兜底 billing_error。
	// 在 R-007(优先级 20, keyword 专用)之后触发。任何不带 credit keyword 的
	// Anthropic 402, 按 Anthropic 新的 typed error class 文档仍代表 billing_error
	// (platform.claude.com/docs/en/api/errors, 2026-05-06 抓取)。
	{RuleID: "R-021", Version: 1, Priority: 25, Provider: "anthropic", HTTPStatus: "402",
		BodyKeyword: "", Class: ErrorClassCreditExhausted,
		Action: RetryActionPermanentDisable, Tier: TierIronClad},

	// 优先级 30 - 通用 401(无 keyword): 按规格永久禁用
	{RuleID: "R-009", Version: 1, Priority: 30, Provider: "*", HTTPStatus: "401",
		Class:  ErrorClassOAuthInvalidGrant,
		Action: RetryActionPermanentDisable, Tier: TierIronClad},

	// 优先级 35 - Gemini 专用的带 permission_denied 的 403(计数禁用, 非 iron_clad)
	{RuleID: "R-017", Version: 1, Priority: 35, Provider: "gemini", HTTPStatus: "403",
		BodyKeyword: keywordPermissionDenied, Class: ErrorClassPlatformPolicy,
		Action: RetryActionCountedDisable, Tier: TierAmbiguous},

	// 优先级 40 - 403 平台专用
	{RuleID: "R-010", Version: 1, Priority: 40, Provider: "openai", HTTPStatus: "403",
		Class:  ErrorClassPlatformPolicy,
		Action: RetryActionCountedDisable, Tier: TierAmbiguous},
	{RuleID: "R-011", Version: 1, Priority: 40, Provider: "anthropic", HTTPStatus: "403",
		BodyKeyword: keywordValidation, Class: ErrorClassPlatformPolicy,
		Action: RetryActionPermanentDisable, Tier: TierIronClad},
	{RuleID: "R-012", Version: 1, Priority: 40, Provider: "*", HTTPStatus: "403",
		Class:  ErrorClassPlatformPolicy,
		Action: RetryActionPermanentDisable, Tier: TierIronClad},

	// 优先级 45 - Bedrock 漂移 D2(2026-05-06 厂商审计):
	// 429 ThrottlingException 是 quota/限流;503
	// ServiceUnavailableException 是容量/过载, 不是限流。
	// 抓取 URL: https://docs.aws.amazon.com/bedrock/latest/userguide/troubleshooting-api-error-codes.html
	{RuleID: "R-018", Version: 2, Priority: 45, Provider: "bedrock", HTTPStatus: "429",
		BodyKeyword: keywordThrottlingException, Class: ErrorClassRateLimited,
		Action: RetryActionCooldown, Tier: TierAmbiguous},
	{RuleID: "R-020", Version: 1, Priority: 45, Provider: "bedrock", HTTPStatus: "503",
		BodyKeyword: keywordServiceUnavailableException, Class: ErrorClassOverloaded,
		Action: RetryActionCooldown, Tier: TierAmbiguous},

	// 优先级 50 - 限流与过载(始终 ambiguous, 仅 cooldown)
	{RuleID: "R-013", Version: 1, Priority: 50, Provider: "*", HTTPStatus: "429",
		Class:  ErrorClassRateLimited,
		Action: RetryActionCooldown, Tier: TierAmbiguous},
	{RuleID: "R-014", Version: 1, Priority: 50, Provider: "*", HTTPStatus: "529",
		Class:  ErrorClassOverloaded,
		Action: RetryActionCooldown, Tier: TierAmbiguous},

	// D8: Anthropic 504 timeout_error —— upstream gateway 超时(ambiguous, cooldown)。
	// 与 R-019 network 超时(status=0, 本地合成)不同。
	// 来源: platform.claude.com/docs/en/api/errors (2026-05-06 抓取)。
	{RuleID: "R-022", Version: 1, Priority: 50, Provider: "anthropic", HTTPStatus: "504",
		Class:  ErrorClassUpstreamTimeout,
		Action: RetryActionCooldown, Tier: TierAmbiguous},

	// D8: Anthropic 413 request_too_large —— 客户端载荷错误, 不重试。
	// PassThrough: 调用方必须减小请求大小; 不改变 FSM 状态。
	// 来源: platform.claude.com/docs/en/api/errors (2026-05-06 抓取)。
	{RuleID: "R-023", Version: 1, Priority: 50, Provider: "anthropic", HTTPStatus: "413",
		Class:  ErrorClassRequestTooLarge,
		Action: RetryActionPassThrough, Tier: TierNone},

	// 优先级 55 - 合成的 network 超时(status 0 + body 提示)
	{RuleID: "R-019", Version: 1, Priority: 55, Provider: "*", HTTPStatus: "0",
		BodyKeyword: keywordTimeout, Class: ErrorClassNetworkTimeout,
		Action: RetryActionCooldown, Tier: TierAmbiguous},

	// 优先级 60 - 通用 5xx(仅告警)
	{RuleID: "R-015", Version: 1, Priority: 60, Provider: "*", HTTPStatus: "5xx",
		Class:  ErrorClassServerError,
		Action: RetryActionWarnOnly, Tier: TierAmbiguous},

	// 优先级 70 - 通配兜底
	{RuleID: "R-016", Version: 1, Priority: 70, Provider: "*", HTTPStatus: "*",
		Class:  ErrorClassUnknown,
		Action: RetryActionPassThrough, Tier: TierNone},
}

// ErrNoMatchingRule 在没有任何规则(含通配兜底)匹配时由 Classify 返回。
// 实践中不可达, 因为 R-016 匹配一切。
var ErrNoMatchingRule = errors.New("no matching error normalization rule")

// Classify 用 ERROR_RULES 表对一个 upstream 响应求值并返回 Classification。
// 分类器从不变更状态; FsmTransition 只是提示, 实际状态转移由 FSM 调用方(A22)负责。
//
// httpStatus 0 代表一个合成响应(没有 upstream 回复, 例如 network 超时)——
// 与 BodyKeyword "timeout" 组合即匹配 R-019。
func Classify(httpStatus int, headers http.Header, body []byte, provider string) (Classification, error) {
	if httpStatus < 0 {
		return Classification{}, errors.New("http status must be non-negative")
	}

	rule, ok := matchRule(httpStatus, headers, body, provider)
	if !ok {
		return Classification{}, ErrNoMatchingRule
	}

	return Classification{
		Class:         rule.Class,
		Confidence:    confidenceForTier(rule.Tier),
		RuleID:        rule.RuleID,
		RuleVersion:   rule.Version,
		Tier:          rule.Tier,
		RetryAction:   rule.Action,
		FsmTransition: transitionFor(rule.Action, rule.Tier),
		RetryAfterMs:  max(retryAfterMillis(headers), retryAfterFromBody(body, time.Now())),
	}, nil
}

// RemapClientStatus 仅对客户端响应应用一个可选的 channel 级状态码映射。
// 空配置或未命中映射时, 原样返回从 upstream 推导出的状态码; 调用方必须让
// classification、body 与计费输入仍基于原始的 upstream 状态码。
func RemapClientStatus(status int, mapping map[int]int) int {
	if len(mapping) == 0 {
		return status
	}
	if mapped, ok := mapping[status]; ok && mapped > 0 {
		return mapped
	}
	return status
}

func matchRule(httpStatus int, headers http.Header, body []byte, provider string) (ErrorRule, bool) {
	normalizedProvider := normalizeProvider(provider)
	normalizedBody := strings.ToLower(string(body))

	var best ErrorRule
	found := false
	for _, rule := range errorRules {
		if !providerMatches(rule.Provider, normalizedProvider) {
			continue
		}
		if !statusMatches(rule.HTTPStatus, httpStatus) {
			continue
		}
		if rule.BodyKeyword != "" && !strings.Contains(normalizedBody, strings.ToLower(rule.BodyKeyword)) {
			continue
		}
		if !headerMatches(headers, rule.HeaderMatch) {
			continue
		}
		if !found || betterRule(rule, best, normalizedProvider) {
			best = rule
			found = true
		}
	}
	return best, found
}

func normalizeProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", "*":
		return "*"
	case "anthropic_messages":
		return "anthropic"
	default:
		return strings.ToLower(strings.TrimSpace(provider))
	}
}

func providerMatches(ruleProvider, provider string) bool {
	ruleProvider = strings.ToLower(strings.TrimSpace(ruleProvider))
	return ruleProvider == "*" || ruleProvider == provider
}

func statusMatches(ruleStatus string, httpStatus int) bool {
	switch strings.ToLower(strings.TrimSpace(ruleStatus)) {
	case "*":
		return true
	case "5xx":
		return httpStatus >= 500 && httpStatus <= 599
	case "0":
		return httpStatus == 0
	default:
		want, err := strconv.Atoi(ruleStatus)
		return err == nil && want == httpStatus
	}
}

func headerMatches(headers http.Header, match HeaderMatch) bool {
	if match.Name == "" {
		return true
	}
	values, ok := headers[http.CanonicalHeaderKey(match.Name)]
	if !ok {
		values = headers[match.Name]
	}
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		normalized := strings.ToLower(value)
		if match.Equals != "" && normalized == strings.ToLower(match.Equals) {
			return true
		}
		if match.Contains != "" && strings.Contains(normalized, strings.ToLower(match.Contains)) {
			return true
		}
	}
	return match.Equals == "" && match.Contains == ""
}

func betterRule(candidate, current ErrorRule, provider string) bool {
	if candidate.Priority != current.Priority {
		return candidate.Priority < current.Priority
	}
	if candidate.Version != current.Version {
		return candidate.Version > current.Version
	}
	return providerSpecificity(candidate.Provider, provider) > providerSpecificity(current.Provider, provider)
}

func providerSpecificity(ruleProvider, provider string) int {
	if strings.EqualFold(ruleProvider, provider) && provider != "*" {
		return 1
	}
	return 0
}

func confidenceForTier(tier DisableTier) Confidence {
	switch tier {
	case TierIronClad:
		return ConfidenceHigh
	case TierAmbiguous:
		return ConfidenceMedium
	default:
		return ConfidenceLow
	}
}

// transitionFor 结构上强制 DR-009 6.6 的硬底线:
// ambiguous 档位的规则无论是什么 action 都到不了 FsmTransitionDisabled。
func transitionFor(action RetryAction, tier DisableTier) FsmTransition {
	switch action {
	case RetryActionPermanentDisable:
		if tier == TierIronClad {
			return FsmTransitionDisabled
		}
		return FsmTransitionManualOnly
	case RetryActionCountedDisable, RetryActionWarnOnly:
		return FsmTransitionDegraded
	case RetryActionCooldown:
		return FsmTransitionCooling
	default:
		return FsmTransitionNoChange
	}
}

// retryAfterMillis 解析 RFC 7231 Retry-After(delta-seconds 或 HTTP-date)。
func retryAfterMillis(headers http.Header) int64 {
	if headers == nil {
		return 0
	}
	raw := strings.TrimSpace(headers.Get("Retry-After"))
	if raw == "" {
		return 0
	}
	if seconds, err := strconv.ParseFloat(raw, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		return int64(seconds * 1000)
	}
	if when, err := http.ParseTime(raw); err == nil {
		delta := time.Until(when)
		if delta <= 0 {
			return 0
		}
		return delta.Milliseconds()
	}
	return 0
}

// retryAfterFromBody 在没有提供 Retry-After header 时(RR-02), 从 provider 的
// 错误 body 中提取冷却时长。Codex 的 usage_limit_reached 只在 body 里携带
// error.resets_at(unix 秒)/ error.resets_in_seconds, 因此只解析 header 的
// 解析器会用错误的(默认)时长把账号停用。
func retryAfterFromBody(body []byte, now time.Time) int64 {
	if len(body) == 0 {
		return 0
	}
	var parsed struct {
		Error struct {
			ResetsAt        *int64   `json:"resets_at"`
			ResetsInSeconds *float64 `json:"resets_in_seconds"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0
	}
	if parsed.Error.ResetsInSeconds != nil && *parsed.Error.ResetsInSeconds > 0 {
		return int64(*parsed.Error.ResetsInSeconds * 1000)
	}
	if parsed.Error.ResetsAt != nil && *parsed.Error.ResetsAt > 0 {
		if delta := time.Unix(*parsed.Error.ResetsAt, 0).Sub(now); delta > 0 {
			return delta.Milliseconds()
		}
	}
	return 0
}
