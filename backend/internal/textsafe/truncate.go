// Package textsafe 提供 UTF-8 安全的文本边界处理。
package textsafe

import "unicode/utf8"

// TruncateBytes 把 s 截到至多 maxBytes 字节,且绝不切断多字节 UTF-8 序列
// (截断点回退到最近的 rune 边界)。裸字节切片截断(s[:n])会把多字节字符
// 切半产生非法 UTF-8——Postgres text 列直接拒收(SQLSTATE 22021),让吊销/
// 注册/审计等整个写库操作失败(参照 sub2api c10598df 同类修复)。
func TruncateBytes(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	s = s[:maxBytes]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
