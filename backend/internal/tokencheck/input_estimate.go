package tokencheck

import (
	"encoding/json"
	"strings"
)

// blobTokenCap 限定单个二进制/base64 大块(如 data-URI 图片)折算的 token 上限,
// 对齐成熟网关对图片输入 patch/tile 计数的上限量级(≈1.5k token)。原始字节数/4
// 对 1MB base64 图片会折出 ~26 万 token,作为终局计费基数不可接受。
const blobTokenCap = 1536

// blobMinLen 低于该长度的字符串不做大块判定,直接按文本估算。
const blobMinLen = 512

// EstimateRequestInputTokens 对客户端原始请求体做协议无关的输入 token 估算:
// 解析 JSON 后走查所有字符串叶子——文本走 CJK 感知启发式(estimateText),
// base64/data-URI 大块按 blobTokenCap 封顶;数字/布尔标量计 1;键名不计。
// 协议无关(openai messages / anthropic content blocks / gemini contents 同样
// 覆盖),仅供「上游全程未报告 usage」的估算计费兜底使用。非 JSON 体退回
// 字节数/4。结果下限 1(请求必有输入)。
func EstimateRequestInputTokens(body []byte) int {
	return EstimateRequestInputTokensWith(body, estimateText)
}

// EstimateRequestInputTokensWith 是 EstimateRequestInputTokens 的可注入版本,
// 允许为纯文本字符串叶子传入自定义计数器(BILL-086/TOK-008)。可以为
// OpenAI 系列文本注入真实 tokenizer,同时 base64/二进制大块仍按默认启发式
// 的上限封顶。传入 nil 计数器时回退到 CJK 启发式。
func EstimateRequestInputTokensWith(body []byte, textCounter func(string) int) int {
	if textCounter == nil {
		textCounter = estimateText
	}
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return 1
	}
	var root any
	if err := json.Unmarshal([]byte(trimmed), &root); err != nil {
		return floorOne((len(trimmed) + 3) / 4)
	}
	return floorOne(estimateJSONValue(root, textCounter))
}

func floorOne(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

func estimateJSONValue(v any, textCounter func(string) int) int {
	switch value := v.(type) {
	case string:
		return estimateStringLeaf(value, textCounter)
	case []any:
		total := 0
		for _, item := range value {
			total += estimateJSONValue(item, textCounter)
		}
		return total
	case map[string]any:
		total := 0
		for _, item := range value {
			total += estimateJSONValue(item, textCounter)
		}
		return total
	case nil:
		return 0
	default:
		// number / bool 标量:计 1,贴近其文本化后的 token 量级。
		return 1
	}
}

func estimateStringLeaf(s string, textCounter func(string) int) int {
	if len(s) >= blobMinLen && looksLikeBinaryBlob(s) {
		// base64/二进制大块绝不喂给真实 tokenizer:对 1MB 的 data-URI 做编码
		// 既慢又毫无意义。作为启发式直接封顶。
		estimate := (len(s) + 3) / 4
		if estimate > blobTokenCap {
			return blobTokenCap
		}
		return estimate
	}
	return textCounter(s)
}

// looksLikeBinaryBlob 识别 data-URI 或纯 base64 长串。采样前 256 字符:全部落在
// base64/URL-safe 字符集即视为大块;容忍 \r/\n(标准 60/76 列换行包裹 base64——
// Ruby Base64.encode64 / MIME 形态——必须命中封顶,否则 1MB 包裹 blob 按文本口径
// 折 ~26 万 token 终局超收)。空格不容忍:真实长文本含空格,误封顶会系统性少收。
func looksLikeBinaryBlob(s string) bool {
	sample := s
	if len(sample) > 256 {
		sample = sample[:256]
	}
	if strings.HasPrefix(s, "data:") && strings.Contains(sample, ";base64,") {
		return true
	}
	for _, r := range sample {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '+', r == '/', r == '=', r == '-', r == '_':
		case r == '\n', r == '\r':
		default:
			return false
		}
	}
	return true
}
