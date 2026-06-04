// clientid_test.go — U6-A 测试: client identity detector 决策树覆盖。
//
// fixture 来自真实客户端 User-Agent 字符串 (公开 GitHub issue / changelog
// 中观察到的形态)，不读 sub2api / 商业项目源码。
package clientid

import (
	"context"
	"net/http"
	"testing"
)

func TestDetect_ExplicitXCursorHeader(t *testing.T) {
	s := Signal{
		UserAgent: "irrelevant",
		XClient:   map[string]string{"x-cursor-version": "0.42.5"},
	}
	id, conf := Detect(s)
	if id != IdentityCursor {
		t.Errorf("X-Cursor-* 应识别为 Cursor，得 %q", id)
	}
	if conf != 1.0 {
		t.Errorf("显式 header confidence 应 1.0，得 %.2f", conf)
	}
}

func TestDetect_XClientNameExplicit(t *testing.T) {
	cases := []struct {
		name      string
		clientHdr string
		want      Identity
	}{
		{"cursor", "Cursor IDE", IdentityCursor},
		{"claude code", "Claude Code", IdentityClaudeCode},
		{"cody", "cody-cli", IdentityCody},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := Signal{XClient: map[string]string{"x-client-name": tc.clientHdr}}
			id, conf := Detect(s)
			if id != tc.want {
				t.Errorf("Detect(%q)=%q want %q", tc.clientHdr, id, tc.want)
			}
			if conf != 1.0 {
				t.Errorf("X-Client-Name confidence 应 1.0，得 %.2f", conf)
			}
		})
	}
}

func TestDetect_UserAgentCursor(t *testing.T) {
	s := Signal{UserAgent: "Cursor/0.42.5 (Mac; arm64)"}
	id, conf := Detect(s)
	if id != IdentityCursor {
		t.Errorf("UA Cursor/* 应识别为 Cursor，得 %q", id)
	}
	if conf < 0.8 || conf > 0.95 {
		t.Errorf("UA confidence 应约 0.9，得 %.2f", conf)
	}
}

func TestDetect_UserAgentClaudeCode(t *testing.T) {
	cases := []string{
		"claude-cli/1.0.45 (Anthropic; node)",
		"Claude-Code/0.99",
	}
	for _, ua := range cases {
		t.Run(ua, func(t *testing.T) {
			s := Signal{UserAgent: ua}
			id, _ := Detect(s)
			if id != IdentityClaudeCode {
				t.Errorf("UA=%q 应识别为 ClaudeCode，得 %q", ua, id)
			}
		})
	}
}

func TestDetect_UserAgentCody(t *testing.T) {
	cases := []string{"cody/1.2.3 (Sourcegraph)", "Cody-CLI/0.5"}
	for _, ua := range cases {
		s := Signal{UserAgent: ua}
		id, _ := Detect(s)
		if id != IdentityCody {
			t.Errorf("UA=%q 应识别为 Cody，得 %q", ua, id)
		}
	}
}

func TestDetect_ScriptUserAgent(t *testing.T) {
	cases := []string{
		"curl/7.81.0",
		"Wget/1.21.3",
		"python-requests/2.31.0",
		"Go-http-client/1.1",
		"node-fetch/3.3.0",
		"axios/1.6.2",
	}
	for _, ua := range cases {
		t.Run(ua, func(t *testing.T) {
			s := Signal{UserAgent: ua}
			id, _ := Detect(s)
			if id != IdentityCurlScript {
				t.Errorf("UA=%q 应识别为 CurlScript，得 %q", ua, id)
			}
		})
	}
}

func TestDetect_ChatUIByOriginAllowedDomain(t *testing.T) {
	cases := []struct {
		name   string
		origin string
		want   Identity
	}{
		{"openwebui exact", "https://openwebui.com", IdentityChatUI},
		{"openwebui sub", "https://app.openwebui.com:8443/path", IdentityChatUI},
		{"lobechat", "https://lobechat.com", IdentityChatUI},
		{"jan.ai", "https://jan.ai", IdentityChatUI},
		// F1 fix: 这两个含 "chat" 子串但不在 allowlist，不应误识别
		{"techsupport-chat NOT chat-ui", "https://techsupport-chat.com", IdentityUnknown},
		{"chat.openai.com NOT chat-ui", "https://chat.openai.com", IdentityUnknown},
		{"random domain", "https://example.com", IdentityUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := Signal{
				UserAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X)",
				Origin:    tc.origin,
			}
			id, _ := Detect(s)
			if id != tc.want {
				t.Errorf("Origin=%q 识别为 %q want %q", tc.origin, id, tc.want)
			}
		})
	}
}

// TestDetect_DualXClientHeaders_DeterministicPrecedence F3 修复
// 验证: 当 X-Cursor-* 与 X-Cody-* 同时出现时，X-Cursor 优先（不依赖 map
// 迭代顺序）。同样：X-Client-Name 优先于 prefix headers。
func TestDetect_DualXClientHeaders_DeterministicPrecedence(t *testing.T) {
	// 跑多次确认每次都是 cursor（不是 map iter random 决胜）
	for i := 0; i < 20; i++ {
		s := Signal{XClient: map[string]string{
			"x-cursor-version": "0.42",
			"x-cody-version":   "1.0",
		}}
		id, _ := Detect(s)
		if id != IdentityCursor {
			t.Fatalf("iter %d: 双 prefix header 应 cursor 优先，得 %q", i, id)
		}
	}
	// X-Client-Name 优先于 X-Cursor-* prefix
	s2 := Signal{XClient: map[string]string{
		"x-client-name":    "cody-cli",
		"x-cursor-version": "0.42",
	}}
	id, _ := Detect(s2)
	if id != IdentityCody {
		t.Errorf("X-Client-Name=cody 应优先于 x-cursor-* prefix，得 %q", id)
	}
}

// TestSignalFromRequest_HeaderCardinalityCap F4 修复 — 200+ header
// 不应把 XClient map 灌爆，命中 xClientCardinalityCap 后 break。
func TestSignalFromRequest_HeaderCardinalityCap(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "https://api.example.com/v1/chat/completions", nil)
	for i := 0; i < 100; i++ {
		req.Header.Add("X-Client-Test-"+string(rune('A'+i%26)), "v")
	}
	s := SignalFromRequest(req)
	if len(s.XClient) > xClientCardinalityCap {
		t.Errorf("XClient cap=%d 但收到 %d 条；上限失效", xClientCardinalityCap, len(s.XClient))
	}
}

func TestDetect_UnknownFallsBackToIdentityUnknown(t *testing.T) {
	s := Signal{
		UserAgent: "Mozilla/5.0 random browser string",
		Path:      "/v1/chat/completions",
	}
	id, _ := Detect(s)
	if id != IdentityUnknown {
		t.Errorf("无识别信号应 IdentityUnknown，得 %q", id)
	}
}

func TestDetect_EmptySignal(t *testing.T) {
	id, _ := Detect(Signal{})
	if id != IdentityUnknown {
		t.Errorf("空 signal 应 IdentityUnknown，得 %q", id)
	}
}

func TestDetect_PrefersXClientOverUserAgent(t *testing.T) {
	// X-Cursor-* 显式信号即使 UA 是 curl 也应优先
	s := Signal{
		UserAgent: "curl/7.81.0",
		XClient:   map[string]string{"x-cursor-version": "0.42"},
	}
	id, conf := Detect(s)
	if id != IdentityCursor {
		t.Errorf("显式 header 应优先于 UA，得 %q", id)
	}
	if conf != 1.0 {
		t.Errorf("显式 confidence 应 1.0")
	}
}

func TestSignalFromRequest(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "https://api.example.com/v1/chat/completions", nil)
	req.Header.Set("User-Agent", "Cursor/0.42")
	req.Header.Set("X-Cursor-Version", "0.42.5")
	req.Header.Set("Origin", "https://cursor.com")

	s := SignalFromRequest(req)
	if s.UserAgent != "Cursor/0.42" {
		t.Errorf("UA=%q", s.UserAgent)
	}
	if s.Path != "/v1/chat/completions" {
		t.Errorf("Path=%q", s.Path)
	}
	if s.Origin != "https://cursor.com" {
		t.Errorf("Origin=%q", s.Origin)
	}
	if s.XClient["x-cursor-version"] != "0.42.5" {
		t.Errorf("XClient[x-cursor-version]=%q", s.XClient["x-cursor-version"])
	}
}

func TestIdentityContext_Roundtrip(t *testing.T) {
	ctx := WithIdentity(context.Background(), IdentityClaudeCode, 0.9)
	id, conf := IdentityFromContext(ctx)
	if id != IdentityClaudeCode {
		t.Errorf("ctx identity=%q want claude_code", id)
	}
	if conf != 0.9 {
		t.Errorf("ctx confidence=%.2f want 0.9", conf)
	}
}

func TestIdentityContext_NoValueReturnsUnknown(t *testing.T) {
	id, conf := IdentityFromContext(context.Background())
	if id != IdentityUnknown {
		t.Errorf("空 ctx 应 IdentityUnknown，得 %q", id)
	}
	if conf != 0 {
		t.Errorf("空 ctx confidence 应 0，得 %.2f", conf)
	}
}

func TestIdentityContext_NilCtxSafe(t *testing.T) {
	id, _ := IdentityFromContext(nil) //nolint:staticcheck // intentional nil test
	if id != IdentityUnknown {
		t.Errorf("nil ctx 应 IdentityUnknown，得 %q", id)
	}
}

// TestDetect_HotPathPerformance 简单基准: 1000 次 detect 应 < 10ms。
// （非 micro-benchmark；只是 hot path budget 守界）
func TestDetect_HotPathPerformance(t *testing.T) {
	s := Signal{
		UserAgent: "Cursor/0.42.5",
		XClient:   map[string]string{"x-cursor-version": "0.42"},
	}
	for i := 0; i < 1000; i++ {
		id, _ := Detect(s)
		if id != IdentityCursor {
			t.Fatalf("iter %d: id=%q", i, id)
		}
	}
}
