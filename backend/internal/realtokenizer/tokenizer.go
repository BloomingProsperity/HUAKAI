// Package realtokenizer 用真正的 BPE 分词器(tiktoken)为 OpenAI 系模型给出更精确的
// 输入 token *估算*,对其它厂商(Anthropic / Gemini / 未知)则回退到 internal/tokencheck
// 里共享的、对 CJK 敏感的启发式算法。它只用于请求前的估算——预测费用与配额预留余量——
// 绝不充当权威计费,后者永远以上游上报的 usage 为准。BILL-086/TOK-008。
package realtokenizer

import (
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/BloomingProsperity/HUAKAI/internal/tokencheck"
	"github.com/tiktoken-go/tokenizer"
)

// EnabledEnv 用于开关真实分词器估算。默认开启;设为 false 时回退到旧的按字节数估算。
const EnabledEnv = "HUAKAI_REAL_TOKENIZER_ENABLED"

var (
	enabledOnce sync.Once
	enabledVal  bool

	// codecCache 缓存每个模型的 codec 解析结果(包括"无 codec"这一否定结果),
	// 这样热路径上的每个请求都不必再付出 ForModel/Get 的开销。
	codecCache sync.Map // model string -> codecEntry
)

type codecEntry struct {
	codec tokenizer.Codec
	ok    bool
}

// Enabled 报告真实分词器估算是否启用。默认开启;无法解析的取值仍保持开启
//(向更精确的默认行为容错)。
func Enabled() bool {
	enabledOnce.Do(func() { enabledVal = parseEnabled(os.Getenv(EnabledEnv)) })
	return enabledVal
}

// parseEnabled 是纯粹的开关策略:空值默认开启,显式的 false 关闭,
// 无法解析的取值仍保持开启(向更优的默认行为容错)。
func parseEnabled(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true
	}
	v, err := strconv.ParseBool(raw)
	return err != nil || v
}

// InputTokens 为原始请求体返回更精确的输入 token 估算。
// 对 OpenAI 系模型,纯文本 JSON 叶子节点用 tiktoken 计数;
// 对其它厂商、未知模型,或遇到任何分词器错误时,使用共享的 CJK 启发式算法。
// 无论哪种情况,base64/二进制大块都会被 tokencheck 截断,这样多模态请求
// 就不会按其 base64 体积来估算。
func InputTokens(model string, body []byte) int {
	if counter, ok := textCounter(model); ok {
		return tokencheck.EstimateRequestInputTokensWith(body, counter)
	}
	return tokencheck.EstimateRequestInputTokens(body)
}

// textCounter 为该模型解析出一个带缓存的 tiktoken 计数器,若无适用的编码器则返回
//(nil,false)。返回的计数器是软容错的:遇到分词器错误时,该叶子节点按零计数,
// 而不是污染整个估算。
func textCounter(model string) (func(string) int, bool) {
	codec, ok := codecForModel(model)
	if !ok {
		return nil, false
	}
	return func(s string) int {
		if s == "" {
			return 0
		}
		n, err := codec.Count(s)
		if err != nil || n < 0 {
			return 0
		}
		return n
	}, true
}

func codecForModel(model string) (tokenizer.Codec, bool) {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil, false
	}
	if cached, ok := codecCache.Load(model); ok {
		entry := cached.(codecEntry)
		return entry.codec, entry.ok
	}
	codec, err := tokenizer.ForModel(tokenizer.Model(model))
	entry := codecEntry{codec: codec, ok: err == nil && codec != nil}
	codecCache.Store(model, entry)
	return entry.codec, entry.ok
}
