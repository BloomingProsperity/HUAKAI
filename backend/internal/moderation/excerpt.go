package moderation

import (
	"encoding/json"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

// DefaultExcerptMaxRunes 是违规摘录的字符上限。摘录只用于让运营判断「这次请求
// 大致在做什么」，不是完整取证材料，故取一个能看懂意图又不至于把长对话整段落库
// 的长度。
const DefaultExcerptMaxRunes = 240

// BuildExcerpt 由原始请求体生成可供运营判读的违规摘录。
//
// 处理顺序固定为：提取用户消息 -> 凭证脱敏 -> 按 rune 截断，三步不可调换。
//   - 先提取再截断：请求体首部是协议字段(模型名、流式开关、system 提示等)，
//     直接截断整个 JSON 只会得到协议噪音，运营看不出用户在请求什么。
//   - 先脱敏再截断：凭证塌缩成一个占位符后才计长度，凭证后面用户真正说的话
//     才留得住；反过来一条长凭证就能吃光整个字符预算，摘录等于白留。
//     (脱敏按 token 前缀锚定，截断后的残片仍带前缀，故顺序颠倒不会漏出明文，
//     但会让摘录失去可读内容。)
//
// 无法解析出用户消息时返回空串，不回退成原始请求体：宁可没有摘录，也不把
// 未经提取的整包内容落库。
func BuildExcerpt(body []byte, maxRunes int) string {
	if maxRunes <= 0 {
		maxRunes = DefaultExcerptMaxRunes
	}
	text := extractUserText(body)
	if text == "" {
		return ""
	}
	return truncateRunes(privacy.RedactCredentialTokens(text), maxRunes)
}

// extractUserText 取请求体中最后一条用户消息的文本。
//
// 刻意不依赖客户端协议标识：审核入口被多个协议的 handler 共用，把协议判定引进来
// 会让本包跟着协议清单漂移。这里按已知请求体形态依次尝试，取第一个解析成功的。
// 取最后一条而非全部拼接，是因为触发拦截的通常是本轮新增的那条，且长对话全量
// 拼接后截断反而会把最相关的内容挤掉。
func extractUserText(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	if text := extractFromMessages(body); text != "" {
		return text
	}
	if text := extractFromContents(body); text != "" {
		return text
	}
	return extractFromInput(body)
}

// extractFromMessages 处理 messages 数组形态(OpenAI chat 及同形协议)。
func extractFromMessages(body []byte) string {
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return ""
	}
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if !strings.EqualFold(strings.TrimSpace(req.Messages[i].Role), "user") {
			continue
		}
		if text := contentText(req.Messages[i].Content); text != "" {
			return text
		}
	}
	return ""
}

// extractFromContents 处理 contents 数组形态(Gemini 系)。
func extractFromContents(body []byte) string {
	var req struct {
		Contents []struct {
			Role  string `json:"role"`
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return ""
	}
	for i := len(req.Contents) - 1; i >= 0; i-- {
		role := strings.TrimSpace(req.Contents[i].Role)
		// Gemini 侧 role 可省略，省略即视为用户输入。
		if role != "" && !strings.EqualFold(role, "user") {
			continue
		}
		var parts []string
		for _, p := range req.Contents[i].Parts {
			if t := strings.TrimSpace(p.Text); t != "" {
				parts = append(parts, t)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, " ")
		}
	}
	return ""
}

// extractFromInput 处理 input 形态(OpenAI responses 系)：可能是裸字符串，
// 也可能是带 role 的条目数组。
func extractFromInput(body []byte) string {
	var req struct {
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(body, &req); err != nil || len(req.Input) == 0 {
		return ""
	}
	var raw string
	if json.Unmarshal(req.Input, &raw) == nil {
		return strings.TrimSpace(raw)
	}
	var items []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(req.Input, &items); err != nil {
		return ""
	}
	for i := len(items) - 1; i >= 0; i-- {
		if !strings.EqualFold(strings.TrimSpace(items[i].Role), "user") {
			continue
		}
		if text := contentText(items[i].Content); text != "" {
			return text
		}
	}
	return ""
}

// contentText 把一条消息的 content 归一为纯文本：content 可能是字符串，
// 也可能是多模态分片数组；分片里只取文本，图片等二进制来源不进摘录。
func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strings.TrimSpace(text)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	var collected []string
	for _, p := range parts {
		if t := strings.TrimSpace(p.Text); t != "" {
			collected = append(collected, t)
		}
	}
	return strings.Join(collected, " ")
}

// truncateRunes 按字符数截断，避免按字节切断多字节字符产生乱码。
//
// 截断点若落在脱敏占位符中间，会在结果尾部留下 "[已脱敏" 这样的残片，看起来像
// 是内容被破坏。这里把尾部的不完整占位符整体去掉：宁可少显示几个字符，也不给
// 运营看一个断掉的标记。
func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return trimPartialPlaceholder(string(runes[:maxRunes]))
}

// trimPartialPlaceholder 去掉结果尾部被截断的占位符残片。
func trimPartialPlaceholder(s string) string {
	placeholder := privacy.CredentialPlaceholder
	// 从最长的残片开始试：完整占位符本身不算残片，故从 len-1 起。
	for n := len([]rune(placeholder)) - 1; n > 0; n-- {
		fragment := string([]rune(placeholder)[:n])
		if strings.HasSuffix(s, fragment) {
			return strings.TrimSuffix(s, fragment)
		}
	}
	return s
}
