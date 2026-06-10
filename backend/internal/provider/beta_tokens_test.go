package provider

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// MUTATION: ParseInboundBetaTokens 退化成裸 split(不校验/不去重/不限长)
// 时,注入/重复/上限子断言红(DM-03 入口卫生守卫)。
func TestParseInboundBetaTokens(t *testing.T) {
	got := ParseInboundBetaTokens([]string{
		" Context-Management-2025-06-27 , interleaved-thinking-2025-05-14",
		"context-management-2025-06-27", // 跨 header 值重复 → 去重
		"evil\r\nx-injected: 1",         // CR/LF 注入 → 丢
		"has space",                     // 空格 → 丢
		"-leading-dash",                 // 首字符非字母数字 → 丢
		"",
	})
	want := []string{"context-management-2025-06-27", "interleaved-thinking-2025-05-14"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	var many []string
	for i := 0; i < 40; i++ {
		many = append(many, fmt.Sprintf("tok-%d", i))
	}
	if got := ParseInboundBetaTokens([]string{strings.Join(many, ",")}); len(got) != maxInboundBetaTokens {
		t.Fatalf("cap: got %d want %d", len(got), maxInboundBetaTokens)
	}
	if got := ParseInboundBetaTokens([]string{strings.Repeat("x", maxInboundBetaTokenLen+1)}); got != nil {
		t.Fatalf("overlong token 应整体丢弃: %v", got)
	}
	if ParseInboundBetaTokens(nil) != nil {
		t.Fatal("nil in 应得 nil out")
	}
}
