package rate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
)

// TempUnschedulableRule 是按账号存储为 JSONB 的错误策略。
// 冷却、客户端投影和健康记账开关共用同一个首匹配规则，但客户端投影绝不改变
// 静态错误分类、重试、鉴权刷新、扣费或永久禁用结论。
type TempUnschedulableRule struct {
	RuleID          string   `json:"rule_id,omitempty"`
	ErrorCode       int      `json:"error_code"`
	Keywords        []string `json:"keywords"`
	DurationMinutes int      `json:"duration_minutes"`
	Description     string   `json:"description,omitempty"`
	ClientStatus    *int     `json:"client_status,omitempty"`
	ClientCode      string   `json:"client_code,omitempty"`
	MessageMode     string   `json:"message_mode,omitempty"`
	ClientMessage   string   `json:"client_message,omitempty"`
	AffectHealth    *bool    `json:"affect_health,omitempty"`
}

const (
	maxTempUnschedulableRules    = 64
	maxTempRuleKeywords          = 16
	maxTempRuleKeywordRunes      = 128
	maxTempRuleDescriptionRunes  = 256
	maxTempRuleDurationMinutes   = 365 * 24 * 60
	maxTempUnschedulableJSONSize = 64 * 1024
)

var (
	tempRuleIDPattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)
	clientCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

// AccountErrorPolicy 是单个账号在错误决策树中的完整本地状态策略。
type AccountErrorPolicy struct {
	Rules            []TempUnschedulableRule
	CustomErrorCodes []int32
	PoolMode         bool
}

// AccountErrorRulesProvider 向 rate service 提供按账号的错误封禁配置。实现
// 可使用进程内缓存。nil 的 provider 被视为「无规则」(零配置的空操作)。
//
// provider 负责应用两个 enable 标志(temp_unschedulable_enabled 和
// custom_error_codes_enabled):对任何被禁用的特性,它都返回空切片,因此
// 调用方永远不需要一个单独的 enabled bool。
type AccountErrorRulesProvider interface {
	// GetAccountErrorPolicy 已应用两个 enable 标志；查询失败时返回零值以维持既有行为。
	GetAccountErrorPolicy(accountID int64) AccountErrorPolicy
}

const maxBodyBytesForMatch = 8 * 1024 // 8 KB 上限 —— 绝不要记录这段切片

// ParseTempUnschedulableRules 反序列化来自 DB 的原始 JSONB 字节。
// 对空或非法输入返回 nil。旧规则没有新增字段时仍保留原冷却行为；只有结构完整、
// 带稳定 rule_id 的新规则才能影响客户端投影或健康记账。
func ParseTempUnschedulableRules(raw []byte) []TempUnschedulableRule {
	if len(raw) == 0 {
		return nil
	}
	var rules []TempUnschedulableRule
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil
	}
	return rules
}

// NormalizeTempUnschedulableRulesForWrite 严格解码并规范化管理端提交的规则。
// 它拒绝未知字段、尾随 JSON、重复 rule_id 和可能意外变成通配的空关键词。
func NormalizeTempUnschedulableRulesForWrite(raw []byte) ([]byte, error) {
	if len(raw) == 0 || len(raw) > maxTempUnschedulableJSONSize {
		return nil, fmt.Errorf("规则 JSON 须为不超过 %d 字节的数组", maxTempUnschedulableJSONSize)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var rules []TempUnschedulableRule
	if err := decoder.Decode(&rules); err != nil || rules == nil {
		return nil, fmt.Errorf("须为规则数组且不得包含未知字段")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("规则数组后不得有额外 JSON")
	}
	if len(rules) > maxTempUnschedulableRules {
		return nil, fmt.Errorf("规则最多 %d 条", maxTempUnschedulableRules)
	}
	seenRuleIDs := make(map[string]struct{}, len(rules))
	for i := range rules {
		if err := normalizeWritableTempRule(&rules[i], seenRuleIDs); err != nil {
			return nil, fmt.Errorf("第 %d 条规则: %w", i+1, err)
		}
	}
	normalized, err := json.Marshal(rules)
	if err != nil {
		return nil, fmt.Errorf("规则无法编码")
	}
	return normalized, nil
}

func normalizeWritableTempRule(rule *TempUnschedulableRule, seenRuleIDs map[string]struct{}) error {
	rule.RuleID = strings.ToLower(strings.TrimSpace(rule.RuleID))
	if !tempRuleIDPattern.MatchString(rule.RuleID) {
		return fmt.Errorf("rule_id 须以小写字母开头，且只含小写字母、数字、点、下划线或连字符，最长 64 字符")
	}
	if _, exists := seenRuleIDs[rule.RuleID]; exists {
		return fmt.Errorf("rule_id %q 重复", rule.RuleID)
	}
	seenRuleIDs[rule.RuleID] = struct{}{}
	if rule.ErrorCode < 100 || rule.ErrorCode > 599 {
		return fmt.Errorf("error_code 须在 100 到 599 之间")
	}
	if rule.DurationMinutes <= 0 || rule.DurationMinutes > maxTempRuleDurationMinutes {
		return fmt.Errorf("duration_minutes 须在 1 到 %d 之间", maxTempRuleDurationMinutes)
	}
	if len(rule.Keywords) > maxTempRuleKeywords {
		return fmt.Errorf("keywords 最多 %d 个", maxTempRuleKeywords)
	}
	seenKeywords := make(map[string]struct{}, len(rule.Keywords))
	for i := range rule.Keywords {
		keyword := strings.TrimSpace(rule.Keywords[i])
		if keyword == "" {
			return fmt.Errorf("keywords[%d] 不得为空；空数组才表示匹配任意正文", i)
		}
		if !utf8.ValidString(keyword) || utf8.RuneCountInString(keyword) > maxTempRuleKeywordRunes {
			return fmt.Errorf("keywords[%d] 须为不超过 %d 字符的 UTF-8 文本", i, maxTempRuleKeywordRunes)
		}
		key := strings.ToLower(keyword)
		if _, exists := seenKeywords[key]; exists {
			return fmt.Errorf("keywords[%d] 与前项重复", i)
		}
		seenKeywords[key] = struct{}{}
		rule.Keywords[i] = keyword
	}
	rule.Description = strings.TrimSpace(rule.Description)
	if !utf8.ValidString(rule.Description) || utf8.RuneCountInString(rule.Description) > maxTempRuleDescriptionRunes {
		return fmt.Errorf("description 最长 %d 字符", maxTempRuleDescriptionRunes)
	}
	if rule.ClientStatus != nil && (*rule.ClientStatus < 400 || *rule.ClientStatus > 599) {
		return fmt.Errorf("client_status 须在 400 到 599 之间")
	}
	rule.ClientCode = strings.ToLower(strings.TrimSpace(rule.ClientCode))
	if rule.ClientCode != "" && !clientCodePattern.MatchString(rule.ClientCode) {
		return fmt.Errorf("client_code 须以小写字母开头，只含小写字母、数字或下划线，最长 64 字符")
	}
	rule.MessageMode = strings.ToLower(strings.TrimSpace(rule.MessageMode))
	if rule.MessageMode == "" {
		rule.MessageMode = clienterr.MessageModeFixed
	}
	rule.ClientMessage = strings.TrimSpace(rule.ClientMessage)
	switch rule.MessageMode {
	case clienterr.MessageModeFixed:
		if rule.ClientMessage != "" {
			return fmt.Errorf("message_mode=fixed 时不得设置 client_message")
		}
	case clienterr.MessageModeCustom:
		message, ok := clienterr.SafeConfiguredMessage(rule.ClientMessage)
		if !ok {
			return fmt.Errorf("message_mode=custom 需要不超过 %d 字符且不含秘密的 client_message", clienterr.MaxProjectedMessageRunes)
		}
		rule.ClientMessage = message
	case clienterr.MessageModeUpstreamSafe:
		if rule.ClientMessage != "" {
			return fmt.Errorf("message_mode=upstream_safe 时不得设置 client_message")
		}
	default:
		return fmt.Errorf("message_mode 须为 fixed/custom/upstream_safe")
	}
	if rule.AffectHealth == nil {
		value := true
		rule.AffectHealth = &value
	}
	return nil
}

// evalAccountErrorRules 是一个纯函数,检查上游响应是否匹配任何由运营者
// 配置的封禁信号(ban-signal)规则。
//
// 匹配语义(F-RATE-001 §1.6):
//  1. custom_error_codes:若 statusCode 是其中成员 → StateTempUnsched / ReasonCustomErrorCode。
//  2. temp_unschedulable_rules:首个满足 error_code == statusCode 且
//     (keywords 为空 或 任一 keyword 是 body 的不区分大小写子串)的规则 → 匹配。
//
// 无任何匹配时返回零值 Decision(StateNoChange)。
// 纯函数:无 I/O、无日志、无副作用。
func evalAccountErrorRules(
	statusCode int,
	respBody []byte,
	rules []TempUnschedulableRule,
	customErrorCodes []int32,
	durationFromRule func(minutes int) time.Duration,
	defaultCooldown time.Duration,
	now time.Time,
	disableCooling bool,
) Decision {
	// 1. 自定义错误码(成员检查)。
	// 使用 defaultCooldown,确保 CooldownUntil 总会被设置(永不为零)。
	for _, code := range customErrorCodes {
		if int(code) == statusCode {
			return makeTempUnschedDecision(defaultCooldown, now, disableCooling, ReasonCustomErrorCode)
		}
	}

	// 准备一个长度受限、已转小写的 body 拷贝用于子串匹配。
	// 这段切片仅用于匹配 —— 绝不记录日志。
	body := respBody
	if len(body) > maxBodyBytesForMatch {
		body = body[:maxBodyBytesForMatch]
	}
	lowerBody := bytes.ToLower(body)

	// 2. Temp-unschedulable 规则(首个匹配胜出)。
	for _, r := range rules {
		if r.ErrorCode != statusCode {
			continue
		}
		if matchesKeywords(lowerBody, r.Keywords) {
			dur := durationFromRule(r.DurationMinutes)
			dec := makeTempUnschedDecision(dur, now, disableCooling, ReasonTempUnschedRule)
			applyTempRuleProjection(&dec, r, respBody)
			return dec
		}
	}
	return Decision{}
}

func applyTempRuleProjection(dec *Decision, rule TempUnschedulableRule, body []byte) {
	if dec == nil || !tempRuleIDPattern.MatchString(rule.RuleID) {
		return
	}
	mode := strings.ToLower(strings.TrimSpace(rule.MessageMode))
	if mode == "" {
		mode = clienterr.MessageModeFixed
	}
	if rule.ClientStatus != nil {
		if *rule.ClientStatus < 400 || *rule.ClientStatus > 599 {
			return
		}
		dec.ClientStatus = *rule.ClientStatus
	}
	code := strings.ToLower(strings.TrimSpace(rule.ClientCode))
	if code != "" {
		if !clientCodePattern.MatchString(code) {
			return
		}
		dec.ClientCode = code
	}
	switch mode {
	case clienterr.MessageModeFixed:
		if strings.TrimSpace(rule.ClientMessage) != "" {
			return
		}
	case clienterr.MessageModeCustom:
		message, ok := clienterr.SafeConfiguredMessage(rule.ClientMessage)
		if !ok {
			return
		}
		dec.ClientMessage = message
	case clienterr.MessageModeUpstreamSafe:
		dec.ClientMessage = clienterr.SafeUpstreamMessage(body)
	default:
		return
	}
	dec.ClientRuleID = rule.RuleID
	dec.SuppressHealthSignal = rule.AffectHealth != nil && !*rule.AffectHealth
}

// matchesKeywords 在 keywords 为空(通配)或任一 keyword 作为不区分大小写的
// 子串出现在 lowerBody 中时返回 true。
// lowerBody 必须已由调用方转为小写。
func matchesKeywords(lowerBody []byte, keywords []string) bool {
	if len(keywords) == 0 {
		return true
	}
	for _, kw := range keywords {
		if kw == "" {
			continue
		}
		if bytes.Contains(lowerBody, bytes.ToLower([]byte(kw))) {
			return true
		}
	}
	return false
}

func makeTempUnschedDecision(dur time.Duration, now time.Time, disableCooling bool, reason Reason) Decision {
	dec := Decision{
		StateChange:    StateTempUnsched,
		Reason:         reason,
		ShouldFailover: true,
	}
	if dur > 0 {
		dec.RetryAfterSeconds = durationSeconds(dur)
		if !disableCooling {
			dec.CooldownUntil = now.Add(dur).UTC()
		}
	}
	return dec
}
