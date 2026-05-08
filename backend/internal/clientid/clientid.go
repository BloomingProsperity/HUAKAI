// Package clientid 检测进入 HUAKAI 的请求"真实客户端身份"——是
// Cursor / Claude Code / Cody / 自定义脚本 / chat UI 还是其它。
//
// 用途（识别后，下游可做的事）：
//   - per-client quota 切分（不同客户端按不同档计费）
//   - abuse detection（同一客户端短时间高频 = 可疑）
//   - 协议形态适配（Cursor 期望 OpenAI shape；Claude Code 期望 Anthropic shape）
//   - 强伪装层（暂停，execution_boundary_c memory rule）
//
// 设计：
//   - 输入: HTTP request 的 headers + path + 可选 body 字段名 hints
//   - 输出: Identity enum + confidence (0.0-1.0)
//   - 多信号融合: User-Agent / 自定义 header (X-Client-*) / Origin / path /
//     body fingerprint。任一信号都可能被 spoof，故多信号决策树投票
//
// 不做的事（U6-A 范围外）：
//   - 不直接拒绝请求（误识别 fallback 到 IdentityUnknown，让 policy 决定）
//   - 不实施反向行为模拟 / 强伪装层（execution_boundary_c memory rule）
//   - 不持久化（per-request 检测）
package clientid

import (
	"net/http"
	"strings"
)

// Identity 是已知客户端身份枚举。新增客户端时在此加 const + 在 Detect
// 中加识别规则 + 在测试中加 fixture。
type Identity string

const (
	// IdentityCursor: Cursor IDE（cursor.com）。User-Agent 含 "Cursor/"。
	IdentityCursor Identity = "cursor"

	// IdentityClaudeCode: Anthropic 官方 CLI claude-code。User-Agent 含
	// "claude-cli" 或 "Claude-Code/"。
	IdentityClaudeCode Identity = "claude_code"

	// IdentityCody: Sourcegraph Cody。User-Agent 含 "cody/" 或 "Cody-CLI/"。
	IdentityCody Identity = "cody"

	// IdentityChatUI: 通用 chat web UI（自建 / OpenWebUI / LobeChat 等）。
	// 信号: Origin / Referer 含 "chat" 或 UI 化的 Accept header。
	IdentityChatUI Identity = "chat_ui"

	// IdentityCurlScript: curl / wget / python-requests / 其它脚本类。
	// User-Agent 明显是脚本工具默认串。
	IdentityCurlScript Identity = "curl_script"

	// IdentityUnknown: 多信号无法识别，或冲突 → 标 unknown，不阻塞请求。
	IdentityUnknown Identity = "unknown"
)

// Signal 是 Detect 的输入抽象。从 *http.Request 提取（参 SignalFromRequest）
// 也可以测试时直接构造。
type Signal struct {
	UserAgent string
	Path      string
	Origin    string
	Referer   string
	// XClient: X-Client-* 系列自定义 header（如 Cursor 自带 X-Cursor-Version）
	XClient map[string]string
	// 可选：body 顶层字段名快照（不含值；防 PII 泄漏）
	BodyFieldNames []string
}

// xClientCardinalityCap 限制 SignalFromRequest 收集 X-Client-* 等 header
// 的数量上限，防异常请求 200+ header 导致 hot path 内存膨胀（sonnet
// debugger F4 finding）。命中上限后 break，损失精度可接受。
const xClientCardinalityCap = 16

// SignalFromRequest 从 *http.Request 抽取 Signal，便于 middleware 接入。
//
// 安全注：本函数遍历所有 request header 但只 picked up x-client-* /
// x-cursor-* / x-anthropic-* / x-cody-* 前缀；命中 xClientCardinalityCap
// 后停止收集（超过 16 个相关 header 是异常请求形态）。
func SignalFromRequest(r *http.Request) Signal {
	xc := map[string]string{}
	for name, vals := range r.Header {
		if len(xc) >= xClientCardinalityCap {
			break
		}
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "x-client-") || strings.HasPrefix(lower, "x-cursor-") ||
			strings.HasPrefix(lower, "x-anthropic-") || strings.HasPrefix(lower, "x-cody-") {
			if len(vals) > 0 {
				xc[lower] = vals[0]
			}
		}
	}
	return Signal{
		UserAgent: r.UserAgent(),
		Path:      r.URL.Path,
		Origin:    r.Header.Get("Origin"),
		Referer:   r.Header.Get("Referer"),
		XClient:   xc,
	}
}

// Detect 根据 signal 推断 Identity + confidence (0.0-1.0)。
// 算法: 决策树（不是权重投票，便于测试 + 可读）：
//  1. 检查显式 X-Client-* / X-Cursor-* 等自定义 header（最强信号）
//  2. 检查 User-Agent 已知 token 子串（次强）
//  3. 检查 Origin / Referer（弱信号，仅 chat UI 类）
//  4. 检查 User-Agent 是否典型脚本默认串
//  5. fallback IdentityUnknown
//
// confidence 含义:
//   - 1.0: 显式 X-Client-Name 或多信号一致
//   - 0.8-0.9: User-Agent 单信号匹配
//   - 0.6-0.7: Origin/Referer 软推断
//   - 0.5: 启发式 fallback
func Detect(s Signal) (Identity, float64) {
	// --- 1. 显式自定义 header（最强）---
	if id, conf, ok := detectFromXClient(s.XClient); ok {
		return id, conf
	}

	// --- 2. User-Agent 子串匹配 ---
	if id, conf, ok := detectFromUserAgent(s.UserAgent); ok {
		return id, conf
	}

	// --- 3. Origin / Referer 推断 chat UI ---
	if isChatUIByOrigin(s.Origin) || isChatUIByOrigin(s.Referer) {
		return IdentityChatUI, 0.6
	}

	// --- 4. 脚本工具默认 UA ---
	if isScriptUserAgent(s.UserAgent) {
		return IdentityCurlScript, 0.7
	}

	return IdentityUnknown, 0.5
}

// detectFromXClient 检查 X-Client-* / X-Cursor-* 等自定义 header。
// 这是最可信的信号——客户端主动声明自己。
// 返回 (identity, confidence, matched)。
//
// 显式优先级（从高到低，sonnet debugger F3 finding 对应修复——避免 Go map
// 迭代顺序非确定性下的"哪个 prefix 先看到谁就赢"）：
//  1. X-Client-Name 显式声明（最强；客户端自报）
//  2. X-Cursor-*  prefix（Cursor 特有 header）
//  3. X-Cody-*    prefix（Cody 特有 header）
func detectFromXClient(xc map[string]string) (Identity, float64, bool) {
	if xc == nil {
		return "", 0, false
	}
	// 1. X-Client-Name 显式声明
	if name := strings.ToLower(xc["x-client-name"]); name != "" {
		switch {
		case strings.Contains(name, "cursor"):
			return IdentityCursor, 1.0, true
		case strings.Contains(name, "claude") && strings.Contains(name, "code"):
			return IdentityClaudeCode, 1.0, true
		case strings.Contains(name, "cody"):
			return IdentityCody, 1.0, true
		}
	}
	// 2. X-Cursor-* 任何 key（独立 loop 避免与 cody 同时出现时迭代顺序赢家不定）
	for k := range xc {
		if strings.HasPrefix(k, "x-cursor-") {
			return IdentityCursor, 1.0, true
		}
	}
	// 3. X-Cody-* 任何 key
	for k := range xc {
		if strings.HasPrefix(k, "x-cody-") {
			return IdentityCody, 1.0, true
		}
	}
	return "", 0, false
}

// detectFromUserAgent 检查 User-Agent 已知 token 子串。
func detectFromUserAgent(ua string) (Identity, float64, bool) {
	if ua == "" {
		return "", 0, false
	}
	lower := strings.ToLower(ua)
	switch {
	case strings.Contains(lower, "cursor/"):
		return IdentityCursor, 0.9, true
	case strings.Contains(lower, "claude-cli") || strings.Contains(lower, "claude-code/"):
		return IdentityClaudeCode, 0.9, true
	case strings.Contains(lower, "cody/") || strings.Contains(lower, "cody-cli"):
		return IdentityCody, 0.9, true
	}
	return "", 0, false
}

// chatUIDomainSuffixes 是已知 chat UI 项目的固定域名尾缀。
// 用 HasSuffix 而非 substring contains，避免误伤 "techsupport-chat.com" /
// "chat.openai.com" 等含 "chat" 子串但实际不是 chat UI 项目的域名。
// （sonnet debugger F1 BLOCKING finding 对应修复）
var chatUIDomainSuffixes = []string{
	"openwebui.com",
	"lobechat.com",
	"anywebui.com",
	"chatboxai.app",
	"librechat.ai",
	"jan.ai",
}

// isChatUIByOrigin 检查 Origin/Referer 是否落在 chatUIDomainSuffixes 列表。
// 输入接受完整 URL（含 scheme）或纯 host。
func isChatUIByOrigin(origin string) bool {
	if origin == "" {
		return false
	}
	lower := strings.ToLower(origin)
	// 砍掉 scheme + 任何 path 部分，得纯 host
	if idx := strings.Index(lower, "://"); idx >= 0 {
		lower = lower[idx+3:]
	}
	if idx := strings.IndexByte(lower, '/'); idx >= 0 {
		lower = lower[:idx]
	}
	// 砍 port
	if idx := strings.IndexByte(lower, ':'); idx >= 0 {
		lower = lower[:idx]
	}
	for _, suffix := range chatUIDomainSuffixes {
		if lower == suffix || strings.HasSuffix(lower, "."+suffix) {
			return true
		}
	}
	return false
}

// isScriptUserAgent 检查 UA 是否典型脚本工具默认串。
func isScriptUserAgent(ua string) bool {
	if ua == "" {
		return false
	}
	lower := strings.ToLower(ua)
	for _, prefix := range []string{
		"curl/",
		"wget/",
		"python-requests/",
		"python-urllib/",
		"go-http-client/",
		"node-fetch/",
		"axios/",
		"okhttp/",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}
