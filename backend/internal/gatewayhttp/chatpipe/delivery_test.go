package chatpipe

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
)

// TestDeliveredStreamAttempt_DoesNotInferClientDeliveryFromUpstreamUsage 守住交付与计量分离：
// 上游权威 usage 仍保留给结算金额计算，但不能反推出客户端收到过完整业务帧。
func TestDeliveredStreamAttempt_DoesNotInferClientDeliveryFromUpstreamUsage(t *testing.T) {
	draft := gateway.UsageRecordDraft{
		BusinessFrameDelivered: false,
		DeliveredTokenCount:    7,
		TokensOutput:           11,
		EndClass:               gateway.ClientDisconnect,
	}

	attempt, delivered := DeliveredStreamAttempt(draft)
	if delivered {
		t.Fatal("上游 token 非零但无整帧写入时 delivered=true，误把生成当成交付")
	}
	if attempt.DeliveredTokenCount != 11 {
		t.Fatalf("DeliveredTokenCount=%d want 11，上游权威 usage 不应被交付判定改写", attempt.DeliveredTokenCount)
	}
}
