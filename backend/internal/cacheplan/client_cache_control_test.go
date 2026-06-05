package cacheplan

import "testing"

func TestHasAnyCacheControl_PlainBodyNoControl(t *testing.T) {
	body := []byte(`{"model":"claude-opus-4-5","system":"you are helpful","messages":[{"role":"user","content":"hi"}]}`)
	if HasAnyCacheControl(body) {
		t.Fatal("plain body without cache_control should report false")
	}
}

func TestHasAnyCacheControl_SystemArrayBlock(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"hi"}],"system":[{"type":"text","text":"x","cache_control":{"type":"ephemeral"}}]}`)
	if !HasAnyCacheControl(body) {
		t.Fatal("cache_control on a system block must be detected")
	}
}

func TestHasAnyCacheControl_SystemSingleObject(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"hi"}],"system":{"type":"text","text":"x","cache_control":{"type":"ephemeral"}}}`)
	if !HasAnyCacheControl(body) {
		t.Fatal("cache_control on a single system object must be detected")
	}
}

func TestHasAnyCacheControl_MessageContentBlock(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}]}]}`)
	if !HasAnyCacheControl(body) {
		t.Fatal("cache_control on a message content block must be detected")
	}
}

func TestHasAnyCacheControl_ToolBlock(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"t","cache_control":{"type":"ephemeral"}}]}`)
	if !HasAnyCacheControl(body) {
		t.Fatal("cache_control on a tool definition must be detected")
	}
}

func TestHasAnyCacheControl_StringContentNeverControl(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"plain string content"}],"system":"plain"}`)
	if HasAnyCacheControl(body) {
		t.Fatal("string-form system/content cannot carry cache_control")
	}
}

func TestHasAnyCacheControl_InvalidOrEmpty(t *testing.T) {
	if HasAnyCacheControl(nil) {
		t.Fatal("nil body must report false")
	}
	if HasAnyCacheControl([]byte(`{not json`)) {
		t.Fatal("invalid JSON must report false")
	}
}
