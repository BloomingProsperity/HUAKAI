package gemini

import (
	"context"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// TestGeminiBufferedResponseRecordsMultiCandidateLoss 守护 #5:非流式(buffered)多候选
// (candidateCount>1)时,非主候选被丢弃必须记一条 multi_candidate lossy 损失(与流式路对称),
// 而非静默丢弃、无审计痕迹。主候选答案仍完整返回。
// 判别:删去 sse.go 中 len(resp.Candidates)>1 的 loss 追加 → losses 不含 multi_candidate → 本测试红。
func TestGeminiBufferedResponseRecordsMultiCandidateLoss(t *testing.T) {
	adapter := &Adapter{}
	// 两个候选:主候选(index 0)文本 "primary",非主候选(index 1)文本 "secondary"。
	raw := []byte(`{"candidates":[` +
		`{"content":{"parts":[{"text":"primary"}],"role":"model"},"index":0,"finishReason":"STOP"},` +
		`{"content":{"parts":[{"text":"secondary"}],"role":"model"},"index":1,"finishReason":"STOP"}` +
		`],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":2,"totalTokenCount":6}}`)

	env, losses, err := adapter.ProviderResponseToCanonical(context.Background(), raw)
	if err != nil {
		t.Fatalf("ProviderResponseToCanonical: %v", err)
	}

	// (1) 必须记录一条 multi_candidate lossy 损失。
	found := false
	for _, l := range losses {
		if l.Feature == string(proto.FeatureMultiCandidate) {
			found = true
			if l.Verdict != proto.VerdictLossy {
				t.Fatalf("multi_candidate 损失 verdict 应为 lossy, 实际 %v", l.Verdict)
			}
		}
	}
	if !found {
		t.Fatalf("非流式多候选必须记 multi_candidate lossy 损失, 实际 losses=%+v", losses)
	}

	// (2) 主候选答案完整返回,非主候选内容不泄入 canonical 响应。
	if env == nil || env.BufferedResponse == nil {
		t.Fatal("BufferedResponse 缺失")
	}
	gotPrimary := false
	for _, blk := range env.BufferedResponse.Content {
		if blk.Type == "text" && blk.Text == "primary" {
			gotPrimary = true
		}
		if blk.Type == "text" && blk.Text == "secondary" {
			t.Fatal("非主候选内容不应进入 canonical 响应")
		}
	}
	if !gotPrimary {
		t.Fatalf("主候选文本必须完整返回, content=%+v", env.BufferedResponse.Content)
	}
}

// TestGeminiBufferedResponseSingleCandidateNoLoss 守护边界:单候选(常态)不得误记 multi_candidate 损失。
// 判别:把追加条件从 >1 改成 >=1 → 单候选也记损失 → 本测试红。
func TestGeminiBufferedResponseSingleCandidateNoLoss(t *testing.T) {
	adapter := &Adapter{}
	raw := []byte(`{"candidates":[{"content":{"parts":[{"text":"only"}],"role":"model"},"index":0,"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":1,"totalTokenCount":5}}`)

	_, losses, err := adapter.ProviderResponseToCanonical(context.Background(), raw)
	if err != nil {
		t.Fatalf("ProviderResponseToCanonical: %v", err)
	}
	for _, l := range losses {
		if l.Feature == string(proto.FeatureMultiCandidate) {
			t.Fatalf("单候选不应记 multi_candidate 损失, losses=%+v", losses)
		}
	}
}
