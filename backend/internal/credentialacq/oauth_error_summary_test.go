package credentialacq

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestOAuthErrorSummary_StructuredPassThrough 锁定:规范 OAuth 错误字段(error / error_description)仍透出
// (它们是面向操作者的标准诊断字段),正常短文本不被多余改写。
func TestOAuthErrorSummary_StructuredPassThrough(t *testing.T) {
	got := oauthErrorSummary([]byte(`{"error":"invalid_grant","error_description":"token expired"}`))
	if got != "invalid_grant: token expired" {
		t.Fatalf("结构化 OAuth 错误应原样返回, got %q", got)
	}
}

// TestOAuthErrorSummary_NonStandardBodyBounded 抓对抗 bug-hunt S3:非标准响应体绝不逐字回显。
// 构造超长(>cap)且内嵌敏感标记的非 OAuth-JSON 体,断言摘要有界、不含标记。
// §14 变异:把 fallback 改回逐字回显(return raw)→ 输出含 SENSITIVE-MARKER 且超长 → 本测试红。
func TestOAuthErrorSummary_NonStandardBodyBounded(t *testing.T) {
	secret := "SENSITIVE-MARKER-9f3a"
	body := "<html><body>internal proxy error " + strings.Repeat("x", 600) + secret + "</body></html>"
	got := oauthErrorSummary([]byte(body))
	if strings.Contains(got, secret) {
		t.Fatalf("敏感标记不应出现在摘要里(逐字回显泄漏): %q", got)
	}
	if len([]rune(got)) > 260 {
		t.Fatalf("摘要应有界, 实长 %d rune: %q", len([]rune(got)), got)
	}
	if !strings.Contains(got, "bytes") {
		t.Fatalf("应标注原始字节数以保留排障线索, got %q", got)
	}
}

// TestOAuthErrorSummary_CollapsesNewlines 防日志注入:多行/控制字符的非标准响应体(非 JSON → 走 fallback)
// 必须折叠为单行、去控制字符。§14 变异:中和 strings.Map(IsControl) 的删除(命中改 return r)→ 非空白控制
// 字符 \x00\x07 残留 → 本测试红。
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

// TestOAuthErrorSummary_StructuredFieldSanitized 抓对抗审查 S3-1:规范字段路径(比 fallback 更常见)也必须
// 折叠换行、去控制字符、设界——防被攻陷/异常上游借 error_description 注入换行或塞超长内容。
// 用 json.Marshal 构造合法 JSON(控制字符被正确转义),保证解码端真正走结构化路径(裸控制字节会令 JSON
// 非法、退到 fallback 就测不到结构化路径)。
// §14 变异:把规范分支改回 `return field`(不消毒)→ got 含真实 \n\r\x07 + 超长 → 本测试红。
func TestOAuthErrorSummary_StructuredFieldSanitized(t *testing.T) {
	payload := map[string]string{
		"error":             "invalid_grant",
		"error_description": "first line\nsecond line\r\n\x07ctl " + strings.Repeat("Z", 5000),
	}
	body, mErr := json.Marshal(payload)
	if mErr != nil {
		t.Fatalf("marshal: %v", mErr)
	}
	got := oauthErrorSummary(body)
	if strings.ContainsAny(got, "\n\r\x07") {
		t.Fatalf("规范字段也应去换行/控制字符(防日志注入): %q", got)
	}
	if len([]rune(got)) > maxOAuthErrorField+24 {
		t.Fatalf("规范字段应设界, 实长 %d rune", len([]rune(got)))
	}
	if !strings.Contains(got, "invalid_grant") {
		t.Fatalf("应保留 error code 排障线索, got %q", got)
	}
}

// TestOAuthErrorSummary_MultibyteTruncationValid 抓:多字节 UTF-8 体在截断处不被切碎(按 []rune 截断)。
// §14 变异:把 truncateRunes 改成按字节切(s[:max])→ 末尾出现非法 UTF-8 / U+FFFD → 本测试红。
func TestOAuthErrorSummary_MultibyteTruncationValid(t *testing.T) {
	got := oauthErrorSummary([]byte(strings.Repeat("世", 600)))
	if !utf8.ValidString(got) {
		t.Fatalf("截断后应仍是合法 UTF-8, got %q", got)
	}
	if strings.ContainsRune(got, '�') {
		t.Fatalf("截断后不应出现替换符 U+FFFD(说明按字节切坏了多字节序列)")
	}
}

// TestOAuthErrorSummary_AllControlFallsBackToByteCount 抓:全控制字符体去除后变空,应回退到带字节数的固定
// 文案而非返回空串。§14 变异:删 preview=="" 回退分支 → 返回 "...: " 空尾 → 本测试红。
func TestOAuthErrorSummary_AllControlFallsBackToByteCount(t *testing.T) {
	got := oauthErrorSummary([]byte("\x01\x02\x03\x04"))
	if got != "non-standard error response (4 bytes)" {
		t.Fatalf("全控制字符体应回退到带字节数的固定文案, got %q", got)
	}
}
