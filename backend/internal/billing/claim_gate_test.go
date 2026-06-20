package billing

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// TestClaimLeaseWindowExceedsMaxRequestLifetime 守"claim 孤儿回收租约必须显著大于
// 单个请求的最大生命周期"。claim 的 lease_expires_at 在 reserve 时设为 now+window
// 且请求期内不续租;LeaseSweeper 会 Abort 任何 lease 过期仍 reserving 的 claim。
// 若窗口短于请求时长,跑得久的合法流式请求(可达 HUAKAI_STREAM_TOTAL_TIMEOUT 默认
// 600s)会在仍在传输时被误 Abort —— 已交付内容永不计费(亏钱)+ in_flight 提前减
// 致上游账号超并发。
// 变异判据:把 DefaultClaimLeaseWindow 还原成旧值 90s → 90s <= 600s → 本测试 RED。
func TestClaimLeaseWindowExceedsMaxRequestLifetime(t *testing.T) {
	const maxStreamTimeout = 600 * time.Second // HUAKAI_STREAM_TOTAL_TIMEOUT 默认值
	if DefaultClaimLeaseWindow <= maxStreamTimeout {
		t.Fatalf("claim 租约 %v 必须 > 最大流时长 %v,否则长流会被 LeaseSweeper 中途 abort(亏钱+超并发)",
			DefaultClaimLeaseWindow, maxStreamTimeout)
	}
	// 额外留 settle/DLQ 重放余量:至少 15min。
	if DefaultClaimLeaseWindow < 15*time.Minute {
		t.Fatalf("claim 租约 %v 偏小,需 >= 15min 以覆盖最大请求生命周期 + 结算/DLQ 余量", DefaultClaimLeaseWindow)
	}
}

func TestPR4ReReserveAbortedClaimSQLResetsRouteAndAcquisition(t *testing.T) {
	sqlText := readBillingClaimsSQL(t)
	for _, want := range []string{
		"pooling_group_id = $4",
		"provider_account_id = NULL",
		"acquisition_token = NULL",
		"tenant_id = $5",
	} {
		if !strings.Contains(sqlText, want) {
			t.Fatalf("ReReserveAbortedClaim SQL missing %q", want)
		}
	}
}

func TestComputeIdempotencyFingerprintIgnoresPoolingGroupID(t *testing.T) {
	req := ReserveRequest{
		TenantID:              101,
		APIKeyID:              202,
		UserID:                303,
		LogicalRequestID:      "same-logical-request",
		EndpointFamily:        "chat",
		NormalizedPayloadHash: "same-payload",
		RequestedModel:        "gpt-4.1-mini",
		PoolingGroupID:        10,
		BillingPolicyVersion:  "1.0",
		RequestClass:          "standard",
		PredictedCost:         decimal.RequireFromString("0.01000000"),
	}
	first := ComputeIdempotencyFingerprint(req)
	req.PoolingGroupID = 20
	second := ComputeIdempotencyFingerprint(req)
	if first != second {
		t.Fatalf("fingerprint changed across pool groups: first=%s second=%s", first, second)
	}
}

func readBillingClaimsSQL(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "sql", "queries", "billing_claims.sql"))
	if err != nil {
		t.Fatalf("read billing_claims.sql: %v", err)
	}
	return string(raw)
}
