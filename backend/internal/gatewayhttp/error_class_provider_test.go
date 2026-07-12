package gatewayhttp

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

// TestErrorClassProviderF2 咬住 F2:bedrock 账号 credential vendor 归一为 anthropic,但
// 错误分类必须用 bedrock,否则 bedrock 专用 429/503(限流/过载)规则不命中、退化为通用
// 规则,影响 cooldown/health/failover。其它族仍用 credential vendor(accInfo.Platform)。
// 变异:errorClassProvider 恒 return accInfo.Platform → bedrock 用例得 anthropic → 红。
func TestErrorClassProviderF2(t *testing.T) {
	cases := []struct {
		family   string
		platform string
		want     string
	}{
		{"bedrock_invoke", "anthropic", "bedrock"},
		{"openai_chat", "openai", "openai"},
		{"anthropic_messages", "anthropic", "anthropic"},
		{"gemini_messages", "gemini", "gemini"},
	}
	for _, c := range cases {
		ex := &chatExecution{
			resolved: registry.Resolved{ProtocolFamily: c.family},
			accInfo:  provider.AccountInfo{Platform: c.platform},
		}
		if got := ex.errorClassProvider(); got != c.want {
			t.Fatalf("family=%s platform=%s errorClassProvider=%q want %q", c.family, c.platform, got, c.want)
		}
	}
}
