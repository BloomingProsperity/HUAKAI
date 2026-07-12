package gatewayhttp

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
	"github.com/BloomingProsperity/HUAKAI/internal/servingcapability"
)

// TestCacheExclusiveInputFamiliesCoverAnthropicParser 防复发(B2 / R1A):任何 serving
// 契约的 ResponseParseShape 是 anthropic_messages 的族(即复用 anthropic.Adapter、
// input_tokens 不含 cache 的 Anthropic 用量约定),都必须登记在 cacheExclusiveInputFamilies,
// 否则对缓存请求会二次减 cache→少计费。历史漏项:vertex_anthropic、anthropic_claude_session。
// 变异:从 cacheExclusiveInputFamilies 删掉任一 anthropic 族 → 本测试红。
func TestCacheExclusiveInputFamiliesCoverAnthropicParser(t *testing.T) {
	for _, c := range servingcapability.DefaultContracts() {
		if c.ResponseParseShape != registrydefault.ProtocolAnthropicMessages {
			continue
		}
		if !inputTokensExcludeCache(c.Family) {
			t.Errorf("family %s 用 Anthropic parser(input 不含 cache)却不在 cacheExclusiveInputFamilies,缓存请求会少计费", c.Family)
		}
	}
}
