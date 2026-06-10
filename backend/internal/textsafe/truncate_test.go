package textsafe

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// 判别测试:截断点落在 3 字节汉字中间时必须回退到 rune 边界——裸 s[:n] 会产生
// 非法 UTF-8(PG 22021 拒收)。Mutation guard: 去掉回退循环 → ValidString 断言红。
func TestTruncateBytes_NeverSplitsRune(t *testing.T) {
	// "中" = 3 字节;256 % 3 == 1 → s[:256] 必切半(模拟 revoke reason[:256])
	s := strings.Repeat("中", 100) // 300 字节
	got := TruncateBytes(s, 256)
	if !utf8.ValidString(got) {
		t.Fatalf("截断产生非法 UTF-8(PG 22021 会拒收): %q", got[len(got)-6:])
	}
	if len(got) > 256 {
		t.Fatalf("超出字节上限: %d", len(got))
	}
	if len(got) != 255 { // 85 个汉字 = 255 字节,回退 1 字节
		t.Fatalf("len=%d want 255", len(got))
	}
	// 纯 ASCII 不动
	if got := TruncateBytes("hello", 256); got != "hello" {
		t.Fatalf("ASCII 误截: %q", got)
	}
	// 上限内原样
	if got := TruncateBytes("中文", 64); got != "中文" {
		t.Fatalf("上限内误截: %q", got)
	}
	// 4 字节 emoji 边界
	e := strings.Repeat("\U0001F600", 10) // 40 字节
	got = TruncateBytes(e, 6)             // 6 落在第二个 emoji 中间
	if !utf8.ValidString(got) || len(got) != 4 {
		t.Fatalf("emoji 边界处理错: len=%d valid=%v", len(got), utf8.ValidString(got))
	}
}
