package gateway

import (
	"bytes"
	"strings"
	"testing"
)

// TestDispatchBodyControls_EmptyObfuscateWords 验证空的 ObfuscateWords 列表会
// 让 body 逐字节保持不变——这是 cloaking 特性的默认关闭不变量。
func TestDispatchBodyControls_EmptyObfuscateWords(t *testing.T) {
	body := []byte(`{"model":"claude-3-5-sonnet-20241022","system":"banned word here","messages":[{"role":"user","content":"hello"}]}`)
	controls := DispatchBodyControls{
		ObfuscateWords: nil, // 空 — 必须是严格的 no-op
	}
	out, err := ApplyDispatchBodyControls(body, controls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(body, out) {
		t.Fatalf("empty ObfuscateWords must produce byte-identical output\ngot:  %s\nwant: %s", out, body)
	}
}

// TestDispatchBodyControls_WithObfuscateWords 验证非空的 ObfuscateWords 列表会
// 经由完整的 controls apply 路径触发 cloaking。
func TestDispatchBodyControls_WithObfuscateWords(t *testing.T) {
	body := []byte(`{"model":"claude-3-5-sonnet-20241022","system":"banned word here","messages":[{"role":"user","content":"hello"}]}`)
	controls := DispatchBodyControls{
		ObfuscateWords: []string{"banned"},
	}
	out, err := ApplyDispatchBodyControls(body, controls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bytes.Equal(body, out) {
		t.Fatal("non-empty ObfuscateWords must transform the body")
	}
	if !strings.Contains(string(out), "b\u200banned") {
		t.Fatalf("expected ZWSP-cloaked word in output, got: %s", out)
	}
}
