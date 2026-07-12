package gateway

import (
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// TestFinishDraftPropagatesToolCallCountsToDraft 守流式工具附加费恒 $0 的漏钱 S1。
//
// 背景:服务端工具调用(web_search/file_search/image_generation)在流式 SSE 里由
// content_block_start 逐次发出,UsageAccumulator.Update 按次 += 累加进 acc.Usage。
// 但 finishDraft 此前逐字段拷 token/cache 用量却独漏这三个工具计数 → draft 恒 0 →
// usageFromDraft 取到 ToolCallCounts 全零 → ApplyToolCallSurcharge 判 IsZero 直接跳过 →
// 流式工具调用的上游按次附加费永不向租户计收(我方付了上游成本却对客户收 $0)。
//
// 本测试用生产路径 acc.Update 累加工具次数,断言 finishDraft 把三计数落入 draft。
// 变异:删 finishDraft 里 d.WebSearchCalls=acc.Usage.WebSearchCalls 等三行赋值 →
// 下方三个 draft 断言全部 RED。
func TestFinishDraftPropagatesToolCallCountsToDraft(t *testing.T) {
	acc := UsageAccumulator{}
	// 模拟上游分两个增量帧逐次累加:共 2 次 web_search、3 次 file_search、1 次 image_generation。
	// 分两帧是为了同时验证 Update 对工具计数是 += 累加(非 set-to-latest 覆盖)。
	acc.Update(UsageSourceReported, proto.CanonicalUsage{
		WebSearchCalls:       1,
		FileSearchCalls:      2,
		ImageGenerationCalls: 1,
	})
	acc.Update(UsageSourceReported, proto.CanonicalUsage{
		WebSearchCalls:  1,
		FileSearchCalls: 1,
	})

	// 前置自检:坐实累加器确实按次累加(避免把判别测试建在错误前提上)。
	if acc.Usage.WebSearchCalls != 2 || acc.Usage.FileSearchCalls != 3 || acc.Usage.ImageGenerationCalls != 1 {
		t.Fatalf("累加器未按次累加工具计数:web=%d file=%d img=%d(期望 2/3/1)",
			acc.Usage.WebSearchCalls, acc.Usage.FileSearchCalls, acc.Usage.ImageGenerationCalls)
	}

	draft, err := (&StreamForwarder{}).finishDraft(UsageRecordDraft{}, acc, time.Now(), nil)
	if err != nil {
		t.Fatalf("finishDraft 返回意外错误:%v", err)
	}

	// 判别核心:finishDraft 必须把三个服务端工具计数落入 draft,否则流式工具附加费全漏。
	if draft.WebSearchCalls != 2 {
		t.Fatalf("draft.WebSearchCalls=%d,期望 2 —— 流式 web_search 附加费漏计(finishDraft 未拷工具计数,$0 漏钱)", draft.WebSearchCalls)
	}
	if draft.FileSearchCalls != 3 {
		t.Fatalf("draft.FileSearchCalls=%d,期望 3 —— 流式 file_search 附加费漏计", draft.FileSearchCalls)
	}
	if draft.ImageGenerationCalls != 1 {
		t.Fatalf("draft.ImageGenerationCalls=%d,期望 1 —— 流式 image_generation 附加费漏计", draft.ImageGenerationCalls)
	}
}
