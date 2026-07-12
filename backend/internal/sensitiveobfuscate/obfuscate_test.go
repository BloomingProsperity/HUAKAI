package sensitiveobfuscate_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/sensitiveobfuscate"
)

const zwsp = "\u200b"

// ---- Matcher / obfuscateString 单元测试 ----

func TestObfuscateSingleWord(t *testing.T) {
	m := sensitiveobfuscate.BuildSensitiveWordMatcher([]string{"banned"})
	body := makeBody(`"this is banned content"`, nil)
	out := sensitiveobfuscate.ObfuscateSensitiveWords(body, m)
	got := extractSystemString(t, out)
	want := "this is b" + zwsp + "anned content"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestCaseInsensitive(t *testing.T) {
	m := sensitiveobfuscate.BuildSensitiveWordMatcher([]string{"banned"})
	body := makeBody(`"this is BANNED here"`, nil)
	out := sensitiveobfuscate.ObfuscateSensitiveWords(body, m)
	got := extractSystemString(t, out)
	// BANNED -> B<zwsp>ANNED
	if !strings.Contains(got, "B"+zwsp+"ANNED") {
		t.Fatalf("case-insensitive failed; got %q", got)
	}
}

func TestLongestMatchFirst(t *testing.T) {
	// 词表 ["ban","banned"]："banned" 应只被插入一次，而非两次。
	m := sensitiveobfuscate.BuildSensitiveWordMatcher([]string{"ban", "banned"})
	body := makeBody(`"banned text"`, nil)
	out := sensitiveobfuscate.ObfuscateSensitiveWords(body, m)
	got := extractSystemString(t, out)
	// 单词 "banned" 中恰好一个 ZWSP
	if strings.Count(got, zwsp) != 1 {
		t.Fatalf("expected exactly 1 ZWSP insertion, got %d in %q", strings.Count(got, zwsp), got)
	}
	// 插入位置应在 'b' 之后
	if !strings.Contains(got, "b"+zwsp+"anned") {
		t.Fatalf("expected b<zwsp>anned, got %q", got)
	}
}

func TestEmptyWordList_ByteIdentical(t *testing.T) {
	m := sensitiveobfuscate.BuildSensitiveWordMatcher([]string{})
	body := makeBody(`"this is banned content"`, nil)
	out := sensitiveobfuscate.ObfuscateSensitiveWords(body, m)
	if !bytes.Equal(body, out) {
		t.Fatal("empty word list must return byte-identical output")
	}
}

func TestNilWordList_ByteIdentical(t *testing.T) {
	m := sensitiveobfuscate.BuildSensitiveWordMatcher(nil)
	body := makeBody(`"hello world"`, nil)
	out := sensitiveobfuscate.ObfuscateSensitiveWords(body, m)
	if !bytes.Equal(body, out) {
		t.Fatal("nil word list must return byte-identical output")
	}
}

func TestNoMatch_ByteIdentical(t *testing.T) {
	m := sensitiveobfuscate.BuildSensitiveWordMatcher([]string{"banned"})
	body := makeBody(`"completely clean text"`, nil)
	out := sensitiveobfuscate.ObfuscateSensitiveWords(body, m)
	if !bytes.Equal(body, out) {
		t.Fatalf("no-match body must be byte-identical; got %q", string(out))
	}
}

func TestMalformedJSON_ByteIdentical(t *testing.T) {
	m := sensitiveobfuscate.BuildSensitiveWordMatcher([]string{"banned"})
	body := []byte(`{not valid json`)
	out := sensitiveobfuscate.ObfuscateSensitiveWords(body, m)
	if !bytes.Equal(body, out) {
		t.Fatal("malformed JSON must return byte-identical output")
	}
}

func TestWalksSystemStringAndMessages(t *testing.T) {
	m := sensitiveobfuscate.BuildSensitiveWordMatcher([]string{"banned"})
	body := []byte(`{
		"model":"claude-3-5-sonnet-20241022",
		"system":"banned system text",
		"messages":[
			{"role":"user","content":"user banned message"},
			{"role":"assistant","content":"clean response"}
		]
	}`)
	out := sensitiveobfuscate.ObfuscateSensitiveWords(body, m)

	var root map[string]json.RawMessage
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	var sys string
	if err := json.Unmarshal(root["system"], &sys); err != nil {
		t.Fatalf("system not string: %v", err)
	}
	if !strings.Contains(sys, "b"+zwsp+"anned") {
		t.Errorf("system not obfuscated: %q", sys)
	}

	var msgs []json.RawMessage
	json.Unmarshal(root["messages"], &msgs)
	var msg0 map[string]json.RawMessage
	json.Unmarshal(msgs[0], &msg0)
	var content0 string
	json.Unmarshal(msg0["content"], &content0)
	if !strings.Contains(content0, "b"+zwsp+"anned") {
		t.Errorf("messages[0] not obfuscated: %q", content0)
	}
	// 干净的消息应保持不变
	var msg1 map[string]json.RawMessage
	json.Unmarshal(msgs[1], &msg1)
	var content1 string
	json.Unmarshal(msg1["content"], &content1)
	if strings.Contains(content1, zwsp) {
		t.Errorf("clean message should not have ZWSP: %q", content1)
	}
}

func TestWalksContentBlocks(t *testing.T) {
	m := sensitiveobfuscate.BuildSensitiveWordMatcher([]string{"banned"})
	body := []byte(`{
		"model":"claude-3-5-sonnet-20241022",
		"system":[{"type":"text","text":"banned block system"}],
		"messages":[
			{"role":"user","content":[
				{"type":"text","text":"user banned block"},
				{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aaa"}}
			]}
		]
	}`)
	out := sensitiveobfuscate.ObfuscateSensitiveWords(body, m)
	var root map[string]json.RawMessage
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	// system 块
	var sysBlocks []map[string]json.RawMessage
	json.Unmarshal(root["system"], &sysBlocks)
	var sysText string
	json.Unmarshal(sysBlocks[0]["text"], &sysText)
	if !strings.Contains(sysText, "b"+zwsp+"anned") {
		t.Errorf("system block not obfuscated: %q", sysText)
	}
	// messages 内容块
	var msgs []json.RawMessage
	json.Unmarshal(root["messages"], &msgs)
	var msg map[string]json.RawMessage
	json.Unmarshal(msgs[0], &msg)
	var blks []map[string]json.RawMessage
	json.Unmarshal(msg["content"], &blks)
	var blkText string
	json.Unmarshal(blks[0]["text"], &blkText)
	if !strings.Contains(blkText, "b"+zwsp+"anned") {
		t.Errorf("message content block not obfuscated: %q", blkText)
	}
}

// ---- 变异测试 ----
//（参见 mutation_test.go 里的 TestMutation —— 这里内联是为了保持单文件）

// TestMutation_Red 记录这样一点：若 BuildSensitiveWordMatcher 是 no-op
//（恒等映射），混淆测试就会失败。本测试验证生产实现确实是活跃的。
// 验证方式是检查输出确实包含 ZWSP。
func TestMutation_ActiveImplementation(t *testing.T) {
	m := sensitiveobfuscate.BuildSensitiveWordMatcher([]string{"banned"})
	body := makeBody(`"this is banned content"`, nil)
	out := sensitiveobfuscate.ObfuscateSensitiveWords(body, m)
	if bytes.Equal(body, out) {
		t.Fatal("MUTATION WOULD PASS (implementation is identity — production code broken)")
	}
	got := extractSystemString(t, out)
	if !strings.Contains(got, zwsp) {
		t.Fatalf("expected ZWSP in output, got %q", got)
	}
}

// ---- 辅助函数 ----

// makeBody 用给定的 system raw 值和可选的 messages 列表构造一个最小的
// Anthropic Messages JSON。
func makeBody(systemRawJSON string, messages []map[string]string) []byte {
	buf := strings.Builder{}
	buf.WriteString(`{"model":"claude-3-5-sonnet-20241022","system":`)
	buf.WriteString(systemRawJSON)
	buf.WriteString(`,"messages":[`)
	for i, msg := range messages {
		if i > 0 {
			buf.WriteByte(',')
		}
		b, _ := json.Marshal(msg)
		buf.Write(b)
	}
	buf.WriteString(`]}`)
	return []byte(buf.String())
}

func extractSystemString(t *testing.T, body []byte) string {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	var s string
	if err := json.Unmarshal(root["system"], &s); err != nil {
		t.Fatalf("system not a string: %v", err)
	}
	return s
}
