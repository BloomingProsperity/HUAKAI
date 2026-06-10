package audiohttp

import (
	"testing"

	"github.com/shopspring/decimal"
)

// 判别测试:token 方案的预留估算绝不再把文件字节数当 token 数。
// 此前 1MB 文件 = 100 万幻影 token(25MB≈2600 万 ≈ 157 USD hold,真实几毛),
// mandatory 余额模式下余额充足的用户被假 402 拒绝。
// Mutation guard: reserveTokenUsage 改回 len(File.Data) → 两个断言都红。
func TestReserveTokenUsage_DurationBasedNotByteCount(t *testing.T) {
	ex := &execution{}
	ex.req.File.Data = make([]byte, 1_000_000) // 1MB 上传

	// 有时长估算:60s × 30 token/s(≈15 真实 ×2 安全)= 1800,而不是 1,000,000
	ex.estimatedDuration = durationEstimate{Seconds: decimal.NewFromInt(60)}
	got := ex.reserveTokenUsage()
	if got.InputTokens != 1800 {
		t.Fatalf("duration-based reserve = %d want 1800 (60s x %d tok/s)", got.InputTokens, audioReserveTokensPerSecond)
	}

	// 无时长兜底:1MB / 1000 B/token = 1000,仍远小于字节数
	ex.estimatedDuration = durationEstimate{}
	got = ex.reserveTokenUsage()
	if got.InputTokens != 1000 {
		t.Fatalf("byte-fallback reserve = %d want 1000", got.InputTokens)
	}
	if got.InputTokens >= len(ex.req.File.Data) {
		t.Fatalf("预留 token 数(%d)不得回退到字节量级(%d)", got.InputTokens, len(ex.req.File.Data))
	}

	// 极小文件下界 1
	ex.req.File.Data = make([]byte, 10)
	got = ex.reserveTokenUsage()
	if got.InputTokens != 1 {
		t.Fatalf("tiny-file reserve = %d want 1", got.InputTokens)
	}
}
