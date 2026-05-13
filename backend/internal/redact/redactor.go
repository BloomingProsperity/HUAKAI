package redact

import "sort"

// Redact 接受任意 map[string]any 形态的 log entry，过滤出仅含 allowlist 字段
// 的浅拷贝。**禁字段（prompt / completion / messages / content / tool_input /
// tool_output / system 等）一律剔除**，不做模糊匹配。
//
// 设计选择：strict allowlist 而不是 denylist。原因：新增 vendor 字段时
// denylist 容易漏，allowlist 强制 reviewer 显式加入。
func Redact(entry map[string]any) map[string]any {
	if entry == nil {
		return nil
	}
	out := make(map[string]any, len(entry))
	for k, v := range entry {
		if IsSafeField(k) {
			out[k] = v
		}
	}
	return out
}

// DroppedFields 返回输入 entry 中被 Redact 丢弃的字段名列表，按字典序排序。
// 主要用于审计 / 测试断言"redaction 真的发生了"。
func DroppedFields(entry map[string]any) []string {
	if entry == nil {
		return nil
	}
	var dropped []string
	for k := range entry {
		if !IsSafeField(k) {
			dropped = append(dropped, k)
		}
	}
	sort.Strings(dropped)
	return dropped
}
