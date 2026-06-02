// HUAKAI · iKun

package routeadmin

import "strings"

// ValidateModelPattern 校验 routes.model_pattern_match 的合法形态, 与
// subscriptionenforce.ModelPatternMatches 的匹配语义单一来源对齐:
//
//	''        → 全匹配(等价 '*')
//	'*'       → 全匹配
//	'prefix*' → 前缀匹配(唯一一个 '*' 在末尾)
//	'exact'   → 精确相等(无 '*')
//
// 中段或多个 '*'(如 'a*b'、'*x'、'a**')会被 ModelPatternMatches 误解 —— 'a*b'/'*x' 当精确串(永远失配),
// 'a**' 走 HasSuffix('*') 分支当前缀 'a*'(把字面 '*' 当前缀字符)—— 均与管理员"通配"意图不符 → 静默错配。
// 故写入时一律拒, 把困惑挡在创建期(retro S3)。
func ValidateModelPattern(p string) error {
	if p == "" || p == "*" {
		return nil
	}
	idx := strings.IndexByte(p, '*')
	if idx == -1 {
		// 纯精确串, 合法。
		return nil
	}
	// 含 '*': 仅当恰好一个且位于末尾才合法(prefix*)。
	if strings.Count(p, "*") == 1 && idx == len(p)-1 {
		return nil
	}
	return ErrInvalidModelPattern
}
