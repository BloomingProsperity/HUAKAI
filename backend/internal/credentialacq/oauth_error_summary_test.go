package credentialacq

import (
	"strings"
	"testing"
)

// TestOAuthErrorSummary_StructuredPassThrough 锁定:规范 OAuth 错误字段(error / error_description)
// 仍按原样返回(它们是面向操作者的标准诊断字段,不应被打码)。
func TestOAuthErrorSummary_StructuredPassThrough(t *testing.T) {
	got := oauthErrorSummary([]byte(`{"error":"invalid_grant","error_description":"token expired"}`))
	if got != "invalid_grant: token expired" {
		t.Fatalf("结构化 OAuth 错误应原样返回, got %q", got)
	}
}

// TestOAuthErrorSummary_NonStandardBodyBounded 抓对抗 bug-hunt S3:非标准响应体绝不逐字回显。
// 构造一个超长(>cap)且内嵌敏感标记的非 OAuth-JSON 体,断言摘要有界、不含标记。
// §14 变异:把 fallback 改回 `return strings.TrimSpace(string(raw))`(逐字回显)→ 输出含 SENSITIVE-MARKER
// 且超长 → 本测试红。
func TestOAuthErrorSummary_NonStandardBodyBounded(t *testing.T) {
	secret := "SENSITIVE-MARKER-9f3a"
	body := "<html><body>internal proxy error " + strings.Repeat("x", 600) + secret + "</body></html>"
	got := oauthErrorSummary([]byte(body))
	if strings.Contains(got, secret) {
		t.Fatalf("敏感标记不应出现在摘要里(逐字回显泄漏): %q", got)
	}
	if len([]rune(got)) > 260 { // 有界:cap 200 rune + 前缀/字节数 < 260
		t.Fatalf("摘要应有界, 实长 %d rune: %q", len([]rune(got)), got)
	}
	if !strings.Contains(got, "bytes") {
		t.Fatalf("应标注原始字节数以保留排障线索, got %q", got)
	}
}

// TestOAuthErrorSummary_CollapsesNewlines 防日志注入:多行/控制字符响应体必须被折叠为单行、去控制字符。
// §14 变异:中和 boundedOAuthErrorPreview 里 strings.Map(IsControl) 的删除(命中改 return r)→ 非空白
// 控制字符 \x00\x07 残留 → 本测试红。(\n\r\t 同时被 strings.Fields 折叠与 strings.Map 覆盖,故用专属
// strings.Map 兜的非空白控制字符 \x00\x07 作判别;另:把整个 fallback 改回逐字回显也会令本测试红。)
func TestOAuthErrorSummary_CollapsesNewlines(t *testing.T) {
	got := oauthErrorSummary([]byte("error line1\nerror line2\r\n\tindented\x00\x07"))
	if strings.ContainsAny(got, "\n\r\t\x00\x07") {
		t.Fatalf("摘要不应含换行/制表/控制字符(防日志注入): %q", got)
	}
	if !strings.Contains(got, "error line1") || !strings.Contains(got, "error line2") {
		t.Fatalf("折叠后应仍保留可读文本, got %q", got)
	}
}

// TestOAuthErrorSummary_Empty 空体回退到固定文案(不返回空串)。
func TestOAuthErrorSummary_Empty(t *testing.T) {
	if got := oauthErrorSummary([]byte("   ")); got != "empty response body" {
		t.Fatalf("空体应返回固定文案, got %q", got)
	}
}
