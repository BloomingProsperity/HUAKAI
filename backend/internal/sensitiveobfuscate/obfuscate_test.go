package sensitiveobfuscate_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/sensitiveobfuscate"
)

const zwsp = "​"

// ---- Matcher / obfuscateString unit tests ----

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
	// words ["ban","banned"]: "banned" should get ONE insertion, not two.
	m := sensitiveobfuscate.BuildSensitiveWordMatcher([]string{"ban", "banned"})
	body := makeBody(`"banned text"`, nil)
	out := sensitiveobfuscate.ObfuscateSensitiveWords(body, m)
	got := extractSystemString(t, out)
	// Exactly one ZWSP in the word "banned"
	if strings.Count(got, zwsp) != 1 {
		t.Fatalf("expected exactly 1 ZWSP insertion, got %d in %q", strings.Count(got, zwsp), got)
	}
	// The insertion should be after 'b'
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
	// clean message should be unchanged
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
	// system block
	var sysBlocks []map[string]json.RawMessage
	json.Unmarshal(root["system"], &sysBlocks)
	var sysText string
	json.Unmarshal(sysBlocks[0]["text"], &sysText)
	if !strings.Contains(sysText, "b"+zwsp+"anned") {
		t.Errorf("system block not obfuscated: %q", sysText)
	}
	// messages content block
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

// ---- Mutation test ----
// (see TestMutation in mutation_test.go — inline here to keep one file)

// TestMutation_Red documents that if BuildSensitiveWordMatcher were a no-op
// (identity), the obfuscation test would fail. This test verifies the
// production implementation IS active. We verify by checking the output does
// contain the ZWSP.
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

// ---- helpers ----

// makeBody builds a minimal Anthropic Messages JSON with the given system raw
// value and optional messages list.
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
