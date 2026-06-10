package voucher

import (
	"context"
	"testing"
	"time"
)

// 判别测试:PrivacyLogAuditSink 必须真校验防泄漏(CMB-5),泄漏 payload 拒发;
// 对照 NoopAuditSink 对任何输入都返回 nil = 无防护,所以本测试同时区分
// 「真 sink」与「Noop 休眠」两种状态。
// Mutation guard: 去掉 ValidateAuditEvent 调用 → 第一个断言红。
func TestPrivacyLogAuditSink_BlocksCodeLeakAndEmitsClean(t *testing.T) {
	sink := PrivacyLogAuditSink{}

	err := sink.EmitVoucherAudit(context.Background(), AuditEvent{
		EventType: AuditVoucherRedeemed,
		TenantID:  1,
		Payload:   map[string]any{"voucher_code": "HK-SECRET"},
	})
	// 双层防护:sanitize 层(privacy.SafePayloadOrBlocked)或 validate 层
	// (ErrAuditCodeLeakBlocked)任一拒发都算防住;Noop 则永远 nil。
	if err == nil {
		t.Fatal("泄漏 payload 必须被拒发(CMB-5);got nil")
	}

	if err := sink.EmitVoucherAudit(context.Background(), AuditEvent{
		EventType:       AuditVoucherRedeemed,
		TenantID:        1,
		VoucherID:       7,
		CodeFingerprint: "fp_abc",
		OccurredAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("干净事件发射失败: %v", err)
	}
}
