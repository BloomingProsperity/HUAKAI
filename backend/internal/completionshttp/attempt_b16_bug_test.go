package completionshttp

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestStreamAndCapture_B16_LargeStreamRetainsTrailingUsage 判别测试 (bug B16 [S3]).
//
// 机理: OpenAI 兼容 SSE 流的权威 usage 帧位于流末最后一个 data: chunk。
// 当整条流累计 > maxUpstreamBodyBytes(16MiB) 时,若捕获缓冲在到达阈值后
// 整块丢弃后续 chunk,末尾 usage 帧就落在阈值之外不被捕获 → usageFromSSE
// 解不出 usage → found=false → CompletionTokens 退回 0 → 欠费(undercharge)。
//
// 正确行为: 无论流多大,末尾的 usage 帧都必须被捕获,usageFromSSE 能解出真实
// 的 completion tokens。该测试在有缺陷代码下应 RED。
func TestStreamAndCapture_B16_LargeStreamRetainsTrailingUsage(t *testing.T) {
	const (
		fillerLineCount  = 5000
		fillerContentLen = 4096 // 每行 ~4KiB → 总 filler ~20MiB,稳超 16MiB 阈值
		wantCompletion   = 2000
		wantPrompt       = 137
	)

	var src bytes.Buffer
	filler := strings.Repeat("x", fillerContentLen)
	for i := 0; i < fillerLineCount; i++ {
		// 不含 usage 的正常增量帧: usageFromJSON 解不出 usage → 不会误置 found。
		fmt.Fprintf(&src, "data: {\"choices\":[{\"delta\":{\"content\":\"%s\"}}]}\n\n", filler)
	}
	if src.Len() <= maxUpstreamBodyBytes {
		t.Fatalf("测试前置失败: filler 总量 %d 未超过阈值 %d,无法触发截断", src.Len(), maxUpstreamBodyBytes)
	}
	// 流末权威 usage 帧 + [DONE]。
	fmt.Fprintf(&src, "data: {\"usage\":{\"prompt_tokens\":%d,\"completion_tokens\":%d,\"total_tokens\":%d}}\n\n",
		wantPrompt, wantCompletion, wantPrompt+wantCompletion)
	src.WriteString("data: [DONE]\n\n")

	w := httptest.NewRecorder()
	var captured bytes.Buffer
	if err := streamAndCapture(w, bytes.NewReader(src.Bytes()), &captured); err != nil {
		t.Fatalf("streamAndCapture 返回错误: %v", err)
	}

	// 客户端交付必须无损: 完整字节照发。
	if w.Body.Len() != src.Len() {
		t.Fatalf("客户端交付字节数 %d != 上游 %d (交付不应被截断)", w.Body.Len(), src.Len())
	}

	usage, ok := usageFromSSE(captured.Bytes())
	if !ok {
		t.Fatalf("usageFromSSE 未解出 usage: 末尾 usage 帧在超大流下被丢弃 → 会走 pending_reconciliation 且 output token 记 0 = 欠费")
	}
	if usage.CompletionTokens != wantCompletion {
		t.Fatalf("CompletionTokens = %d, 期望 %d (末尾 usage 帧必须被捕获)", usage.CompletionTokens, wantCompletion)
	}
	if usage.PromptTokens != wantPrompt {
		t.Fatalf("PromptTokens = %d, 期望 %d", usage.PromptTokens, wantPrompt)
	}
}
