// R7.3：Anthropic Messages API system 字段重写引擎（强伪装层 6 步 body
// 变换的第 1 步）。Spec：docs/specs/upstream-credential-management.md §Phase C
// 第 27 步。
//
// 纯 JSON 变换，不做 IO/网络/凭据接触。覆盖 system 字段的三种合法形态：
// 缺省/null、字符串、内容块数组（block 形如 {"type":"text","text":"...",
// "cache_control":{...}}）。EnsurePrefix 模式幂等：已经以 PrefixText
// 开头的请求第二次过会原样返回。
//
// HUAKAI 相对 sub2api 的差异：preamble 文本走配置（PrefixText 入参），不再
// 硬编码常量；rewrite 是纯函数，与具体 service 解耦；mode 用枚举切换三种
// 策略；数组形态下用 raw block 重新拼接，保住已有块上的 cache_control 与
// 任何未知字段（cache_control 在 prefix 注入块上由 R7.4 决定，本步不放）。
//
// Code-parallel 双 lane 合成（CLAUDE.md #10 + 2026-05-04 directive）：取 Codex
// 紧凑结构 + Claude 文档化决策 + 全中文注释。
package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// 拼接前后缀时使用的分隔符（两个换行 = 段落级分隔）。
const systemRewriteSeparator = "\n\n"

// 出参 Reason 的封闭枚举值。改动这里时同步刷新 SystemRewriteResult 的注释。
const (
	reasonAlreadyPrefixed = "already_prefixed" // 头部已匹配 PrefixText，未变动
	reasonRewroteString   = "rewrote_string"   // 原值是字符串，已重写
	reasonRewroteArray    = "rewrote_array"    // 原值是内容块数组，已重写
	reasonInsertedString  = "inserted_string"  // 原本无 system 字段，按字符串形态注入
	reasonReplacedAll     = "replaced_all"     // ReplaceAll 模式覆写所有原内容
	reasonAppended        = "appended"         // AppendAfter 模式追加在尾部
	reasonUnsupported     = "unsupported_shape" // system 是数字/对象/布尔等不受支持形态
	reasonInvalidBody     = "invalid_body"     // body 解析失败
	reasonEmptyPrefix     = "empty_prefix"     // PrefixText 为空，不动
)

// SystemRewriteMode 选择 system 字段的重写策略。
type SystemRewriteMode int

const (
	// SystemRewriteEnsurePrefix 保证 PrefixText 出现在 system 头部；已经在头
	// 部时为 no-op。强伪装的默认模式。
	SystemRewriteEnsurePrefix SystemRewriteMode = iota
	// SystemRewriteReplaceAll 丢弃原有 system 内容，替换为 PrefixText 的字符
	// 串形态。
	SystemRewriteReplaceAll
	// SystemRewriteAppendAfter 把 PrefixText 追加到原 system 内容的末尾。
	SystemRewriteAppendAfter
)

// SystemRewritePlan 是单次重写的入参。
type SystemRewritePlan struct {
	// PrefixText 是要确保/追加/替换的文本；为空时 RewriteSystem 直接返回
	// reason="empty_prefix"。
	PrefixText string
	// Mode 选择三种策略之一。
	Mode SystemRewriteMode
}

// SystemRewriteResult 携带重写结果。
//
// Reason 取值（封闭枚举）：
//   - "already_prefixed"  头部已是 PrefixText，未动
//   - "rewrote_string"    原 system 是字符串，已改写
//   - "rewrote_array"     原 system 是数组，已改写
//   - "inserted_string"   原本无 system 字段，按字符串形态注入
//   - "replaced_all"      ReplaceAll 模式整体覆写
//   - "appended"          AppendAfter 模式已追加
//   - "unsupported_shape" 原 system 是不支持的 JSON 形态
//   - "invalid_body"      body 解析失败
//   - "empty_prefix"      PrefixText 为空，no-op
type SystemRewriteResult struct {
	Body    []byte
	Applied bool
	Reason  string
}

// systemTextBlock 是 Anthropic content block 中 type=text 的最小投影；
// 注入新块时仅写这两个字段，其它字段（cache_control/citations/...）由调
// 用方在后续步骤中追加。
type systemTextBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// RewriteSystem 按 plan 重写 body 中的 system 字段并返回结果。永不修改入参
// 切片：未变动时返回 body 的拷贝，变动时返回重新序列化的字节。
func RewriteSystem(body []byte, plan SystemRewritePlan) (SystemRewriteResult, error) {
	if plan.PrefixText == "" {
		return unchanged(body, reasonEmptyPrefix), nil
	}
	root, err := decodeSystemBody(body)
	if err != nil {
		return unchanged(body, reasonInvalidBody), err
	}
	switch plan.Mode {
	case SystemRewriteReplaceAll:
		root["system"] = mustMarshalRaw(plan.PrefixText)
		return finishRewrite(root, reasonReplacedAll)
	case SystemRewriteAppendAfter:
		return appendSystemText(root, plan.PrefixText, body)
	default:
		return ensureSystemPrefix(root, plan.PrefixText, body)
	}
}

// ensureSystemPrefix 实现 EnsurePrefix 策略：缺失/null 直接注入；字符串看头
// 部是否已包含 PrefixText；数组看首块文字是否已包含；其它形态返回
// unsupported_shape。
func ensureSystemPrefix(root map[string]json.RawMessage, prefix string, body []byte) (SystemRewriteResult, error) {
	raw, ok := root["system"]
	if !ok || isJSONNull(raw) {
		root["system"] = mustMarshalRaw(prefix)
		return finishRewrite(root, reasonInsertedString)
	}
	if text, ok := decodeJSONString(raw); ok {
		if strings.HasPrefix(text, prefix) {
			return unchanged(body, reasonAlreadyPrefixed), nil
		}
		root["system"] = mustMarshalRaw(joinSystemText(prefix, text))
		return finishRewrite(root, reasonRewroteString)
	}
	if blocks, ok := decodeJSONArray(raw); ok {
		if first, ok := firstBlockText(blocks); ok && strings.HasPrefix(first, prefix) {
			return unchanged(body, reasonAlreadyPrefixed), nil
		}
		// 把新 prefix 块拼到原 raw blocks 前面；原 blocks 以 raw 形式保留，
		// cache_control / citations / type=server_tool_use 等未知字段一律不丢。
		next := append([]json.RawMessage{textBlockRaw(prefix)}, blocks...)
		root["system"] = mustMarshalRaw(next)
		return finishRewrite(root, reasonRewroteArray)
	}
	return unchanged(body, reasonUnsupported), nil
}

// appendSystemText 实现 AppendAfter 策略：缺失/null → 单字符串；字符串 → 拼
// 接；数组 → 在尾部新增一个 text 块。
func appendSystemText(root map[string]json.RawMessage, prefix string, body []byte) (SystemRewriteResult, error) {
	raw, ok := root["system"]
	if !ok || isJSONNull(raw) {
		root["system"] = mustMarshalRaw(prefix)
		return finishRewrite(root, reasonAppended)
	}
	if text, ok := decodeJSONString(raw); ok {
		root["system"] = mustMarshalRaw(joinSystemText(text, prefix))
		return finishRewrite(root, reasonAppended)
	}
	if blocks, ok := decodeJSONArray(raw); ok {
		root["system"] = mustMarshalRaw(append(blocks, textBlockRaw(prefix)))
		return finishRewrite(root, reasonAppended)
	}
	return unchanged(body, reasonUnsupported), nil
}

// decodeSystemBody 把 body 解码成顶层 JSON 对象（保留每个字段为 raw）。
// 解析失败或非对象时返回错误。
func decodeSystemBody(body []byte) (map[string]json.RawMessage, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("system rewrite: invalid body: %w", err)
	}
	if root == nil {
		return nil, fmt.Errorf("system rewrite: invalid body: expected JSON object")
	}
	return root, nil
}

// finishRewrite 把变更后的 root 重新序列化为 body 字节。
func finishRewrite(root map[string]json.RawMessage, reason string) (SystemRewriteResult, error) {
	out, err := json.Marshal(root)
	if err != nil {
		return SystemRewriteResult{}, fmt.Errorf("system rewrite: re-serialize: %w", err)
	}
	return SystemRewriteResult{Body: out, Applied: true, Reason: reason}, nil
}

// unchanged 返回原 body 的拷贝（确保调用方对返回切片的修改不会回写到入参）。
func unchanged(body []byte, reason string) SystemRewriteResult {
	return SystemRewriteResult{Body: append([]byte(nil), body...), Reason: reason}
}

// decodeJSONString 尝试把 raw 解码为字符串；不是字符串则返回 ok=false。
func decodeJSONString(raw json.RawMessage) (string, bool) {
	var text string
	return text, json.Unmarshal(raw, &text) == nil
}

// decodeJSONArray 尝试把 raw 解码为 raw 元素切片；不是数组则返回 ok=false。
func decodeJSONArray(raw json.RawMessage) ([]json.RawMessage, bool) {
	var blocks []json.RawMessage
	return blocks, json.Unmarshal(raw, &blocks) == nil
}

// firstBlockText 读取数组首元素的 "text" 字段。当首元素不是对象 / 没有 text /
// text 不是字符串时返回 ok=false。
func firstBlockText(blocks []json.RawMessage) (string, bool) {
	if len(blocks) == 0 {
		return "", false
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(blocks[0], &obj) != nil {
		return "", false
	}
	return decodeJSONString(obj["text"])
}

// textBlockRaw 序列化一个最小 text 块。
func textBlockRaw(text string) json.RawMessage {
	return mustMarshalRaw(systemTextBlock{Type: "text", Text: text})
}

// joinSystemText 用段落分隔符拼接两段文本；任一为空时直接返回另一段。
func joinSystemText(head, tail string) string {
	switch {
	case tail == "":
		return head
	case head == "":
		return tail
	default:
		return head + systemRewriteSeparator + tail
	}
}

// isJSONNull 判断 raw 是否是 JSON null 字面量（兼容首尾空白）。
func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// mustMarshalRaw 把任意值序列化成 raw；只在 marshal 失败（基本不会）时
// panic，调用方都是已知形态的内置类型/字符串。
func mustMarshalRaw(v interface{}) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}
