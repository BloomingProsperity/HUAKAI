package pricingcatalog

import (
	"context"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/sign"
)

// TestSignPricingRatioAuditEntry_OccurredAtTruncatedToMicrosecondSurvivesRoundTrip
// 守护一个易被忽略的资金审计完整性缺陷:签名时钟里若残留亚微秒级的纳秒部分,而
// timestamptz 存储只能保存到微秒,读回后取到的 occurred_at 与签名所基于的值就会不同,
// 进而让这条本应完好的链在自身的 VerifyChain 里被误报为篡改(假阳),侵蚀防篡改证据的可信度。
//
// 写入路径必须在"计算 entry_hash / 签名之前"先把 occurred_at 截断到微秒,签名才会落在
// 读回后仍然成立的值上。
//
// 判别力(变异):删掉签名路径里对 occurred_at 的微秒截断 -> 签名落在纳秒值上,而下方
// 模拟读回时被截到微秒 -> 重算 canonical/entry_hash 与签名不一致 -> VerifyChain OK=false
// -> 本测试两处断言都变红。夹具特意带 ...789 ns 的亚微秒尾数,这是判别的前提。
func TestSignPricingRatioAuditEntry_OccurredAtTruncatedToMicrosecondSurvivesRoundTrip(t *testing.T) {
	ctx := context.Background()
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	// 携带亚微秒级纳秒(...789 ns):timestamptz 读回会把这 789ns 丢掉。
	occurredAt := time.Date(2026, 6, 4, 12, 0, 0, 123456789, time.UTC)
	// 夹具自检:若尾数能被 1000 整除则没有亚微秒部分,本测试会退化为不可判别。
	if occurredAt.Nanosecond()%1000 == 0 {
		t.Fatalf("夹具 OccurredAt 缺少亚微秒纳秒尾数,无法判别截断是否缺失")
	}

	entry, err := signPricingRatioAuditEntry(ctx, signer, pricingRatioAuditEvent{
		OccurredAt:  occurredAt,
		ActorID:     "admin_token:99",
		ActorRole:   "platform_admin",
		TenantID:    7,
		PoolGroupID: 42,
		Action:      RatioAuditActionUpsert,
		NewRatio:    stringPtrForRatioAuditTest("1.25000000"),
	}, nil)
	if err != nil {
		t.Fatalf("sign audit entry: %v", err)
	}

	// 直接断言:签名后的 OccurredAt 已被截断到微秒(纳秒尾数能被 1000 整除)。
	if entry.OccurredAt.Nanosecond()%1000 != 0 {
		t.Fatalf("entry.OccurredAt=%v 仍残留亚微秒纳秒;写路径未在签名前截断到微秒", entry.OccurredAt)
	}

	// 端到端断言:模拟 timestamptz 读回,把 OccurredAt 截断到微秒(数据库精度上限),
	// 再用纯校验器重算 canonical/entry_hash 并验签。截断若发生在签名之前,stored 与
	// 签名值一致 => 校验成立;若写路径漏截断 => 此处变红。
	stored := withAuditRowIDForTest(entry, 1)
	stored.OccurredAt = entry.OccurredAt.Truncate(time.Microsecond)

	if result := VerifyPricingRatioAuditEntries(ctx, signer.PublicKey(), []PricingRatioAuditEntry{stored}); !result.OK {
		t.Fatalf("occurred_at 经微秒读回后链校验失败 result=%+v;写路径需在签名前把 occurred_at 截断到微秒", result)
	}
}
