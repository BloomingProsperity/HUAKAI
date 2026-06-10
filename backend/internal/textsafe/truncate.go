// Package textsafe 提供 UTF-8 安全的文本边界处理。
package textsafe

import "unicode/utf8"

// TruncateBytes 返回 s 的前缀,长度不超过 maxBytes 字节,并保证不会把一个
// 多字节 UTF-8 码点切成两半。
//
// 动机:对合法 UTF-8 文本做裸字节切片(s[:n])可能把一个码点劈开,产生非法
// UTF-8 字节序列;Postgres 的 text/varchar 列会以 SQLSTATE 22021 直接拒收,
// 连带让吊销 / 注册 / 审计等整条写库事务失败。
//
// 实现:UTF-8 一个码点最多 4 字节,首字节之外都是 0b10xxxxxx 续字节。我们把
// 切点从 maxBytes 处向前移到最近的码点首字节(utf8.RuneStart 判定),最多回退
// 3 个续字节即停——O(1),无需对整段前缀反复做合法性校验。
func TruncateBytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	if cut < 0 {
		cut = 0
	}
	// 若 cut 落在某个码点的续字节上,向前退到该码点的首字节之前。
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
