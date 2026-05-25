// cache_demo_test.go — 端到端 demo: 喂合成 Anthropic SSE 流给 adapter,
// 打印 cachemetrics counter 变化。让 Owner 直接看到命中率管道通了。
//
// 跑法:
//
//	cd backend && go test ./internal/proto/... -run TestDemo_CacheMetrics -v
//
// 不连任何真 vendor / 不需 API key, 纯 in-process 模拟。
//
// 用 proto_test 外部测试包避免循环 (cachemetrics 已被 proto 内部 import)。
package proto_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/cachemetrics"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/anthropic"
)

// TestDemo_CacheMetrics 模拟两次请求（一 miss 一 hit）走完整 adapter,
// 打印 expvar counter 变化。展示 production 起来后 /debug/vars 看到的形态。
func TestDemo_CacheMetrics(t *testing.T) {
	adapter := &anthropic.Adapter{}

	// 取 baseline (其它 test 可能已增过, 用 delta 来比)
	c0, r0, req0 := cachemetrics.Snapshot()

	// === Request 1: cache miss (vendor 写入新 prefix 1500 tokens) ===
	state1 := &anthropic.UpstreamState{}
	feedDemo(t, adapter, state1, []string{
		`{"type":"message_start","message":{"id":"r1","model":"claude-3-5","usage":{"input_tokens":50,"cache_creation_input_tokens":1500,"cache_read_input_tokens":0}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2,"cache_creation_input_tokens":1500,"cache_read_input_tokens":0}}`,
		`{"type":"message_stop"}`,
	})
	c1, r1, req1 := cachemetrics.Snapshot()
	dC1, dR1, dReq1 := c1-c0, r1-r0, req1-req0

	fmt.Printf("\n=== Request 1: cache miss ===\n")
	fmt.Printf("  Δ creation_total = %d  (vendor 写入 1500 token 新缓存)\n", dC1)
	fmt.Printf("  Δ read_total     = %d  (无命中)\n", dR1)
	fmt.Printf("  Δ request_count  = %d\n", dReq1)
	if dC1 != 1500 || dR1 != 0 || dReq1 != 1 {
		t.Errorf("R1 delta 期望 1500/0/1, 得 %d/%d/%d", dC1, dR1, dReq1)
	}

	// === Request 2: cache hit (相同 prefix) ===
	state2 := &anthropic.UpstreamState{}
	feedDemo(t, adapter, state2, []string{
		`{"type":"message_start","message":{"id":"r2","model":"claude-3-5","usage":{"input_tokens":50,"cache_creation_input_tokens":0,"cache_read_input_tokens":1500}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2,"cache_creation_input_tokens":0,"cache_read_input_tokens":1500}}`,
		`{"type":"message_stop"}`,
	})
	c2, r2, req2 := cachemetrics.Snapshot()
	dC2, dR2, dReq2 := c2-c1, r2-r1, req2-req1

	fmt.Printf("\n=== Request 2: cache hit ===\n")
	fmt.Printf("  Δ creation_total = %d  (无新增)\n", dC2)
	fmt.Printf("  Δ read_total     = %d  (vendor 命中读出 1500 token)\n", dR2)
	fmt.Printf("  Δ request_count  = %d\n", dReq2)
	if dC2 != 0 || dR2 != 1500 || dReq2 != 1 {
		t.Errorf("R2 delta 期望 0/1500/1, 得 %d/%d/%d", dC2, dR2, dReq2)
	}

	// 打印命中率公式
	totalC := c2 - c0
	totalR := r2 - r0
	if totalC+totalR > 0 {
		hitRatio := float64(totalR) / float64(totalC+totalR)
		fmt.Printf("\n=== 总命中率 ===\n")
		fmt.Printf("  hit_ratio = read_total / (creation_total + read_total)\n")
		fmt.Printf("            = %d / (%d + %d) = %.1f%%\n",
			totalR, totalC, totalR, hitRatio*100)
	}
	fmt.Printf("\nproduction 起 gateway 后:\n")
	fmt.Printf("  curl /debug/vars | jq '.cache_token_count'\n")
	fmt.Printf("会看到 vendor 真请求按上述形态累计.\n\n")
}

func feedDemo(t *testing.T, ad *anthropic.Adapter, st *anthropic.UpstreamState, raws []string) {
	t.Helper()
	for i, raw := range raws {
		_, _, err := ad.ProviderEventToCanonicalEvents(context.Background(), []byte(raw), st)
		if err != nil {
			t.Fatalf("event[%d] err=%v raw=%s", i, err, raw)
		}
	}
}
