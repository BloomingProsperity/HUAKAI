package rate

import (
	"bytes"
	"encoding/json"
	"time"
)

// TempUnschedulableRule 是按账号存储为 JSONB 的错误封禁(error-ban)规则。
// schema 来自 sql/migrations/0004_rate_limiting.up.sql:
//
//	{ error_code, keywords[], duration_minutes, description }
//
// 当 error_code 等于上游状态码,且(若有关键词)至少有一个 keyword 作为
// 不区分大小写的子串出现在响应体中时,规则即匹配。空的 keywords 列表表示
// 「任意 body」。
type TempUnschedulableRule struct {
	ErrorCode       int      `json:"error_code"`
	Keywords        []string `json:"keywords"`
	DurationMinutes int      `json:"duration_minutes"`
	Description     string   `json:"description"`
}

// AccountErrorRulesProvider 向 rate service 提供按账号的错误封禁配置。实现
// 可使用进程内缓存。nil 的 provider 被视为「无规则」(零配置的空操作)。
//
// provider 负责应用两个 enable 标志(temp_unschedulable_enabled 和
// custom_error_codes_enabled):对任何被禁用的特性,它都返回空切片,因此
// 调用方永远不需要一个单独的 enabled bool。
type AccountErrorRulesProvider interface {
	// GetAccountErrorRules 返回给定账号生效的 temp-unschedulable 规则和
	// custom error codes,且两个 enable 标志均已应用。空切片表示
	//「特性关闭 / 无配置」(空操作)。
	GetAccountErrorRules(accountID int64) (rules []TempUnschedulableRule, customErrorCodes []int32)
}

const maxBodyBytesForMatch = 8 * 1024 // 8 KB 上限 —— 绝不要记录这段切片

// ParseTempUnschedulableRules 反序列化来自 DB 的原始 JSONB 字节。
// 对空或非法输入返回 nil(视为无规则)。
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
			return makeTempUnschedDecision(dur, now, disableCooling, ReasonTempUnschedRule)
		}
	}
	return Decision{}
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
