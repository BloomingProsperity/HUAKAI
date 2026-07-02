package embeddingshttp

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
)

// claimRaceClaimGate 恒返 billing.ErrClaimRace,模拟"幂等竞争 / Serializable 重试
// 耗尽"这类可重试的 reserve 失败。
type claimRaceClaimGate struct{}

func (claimRaceClaimGate) Reserve(context.Context, billing.ReserveRequest) (*billing.ReserveResult, error) {
	return nil, billing.ErrClaimRace
}

// TestEmbeddingsHandler_ReserveClaimRaceReturns409RetryAfter 守 reserve 阶段的
// ErrClaimRace 分支:命中可重试竞争必须回 409 + Retry-After:1 + code=claim_race,
// 而非通用 500 reserve_error(把可重试竞争误报成服务端错误)。
// 变异契约:删掉 billing.go reserve() 里 `if errors.Is(err, billing.ErrClaimRace)`
// 分支后,请求会落到下方通用 500 reserve_error,本测试随即变红。
func TestEmbeddingsHandler_ReserveClaimRaceReturns409RetryAfter(t *testing.T) {
	env := newEmbeddingsTestEnv(t, upstreamResponse{status: http.StatusOK, body: `{}`})
	env.deps.ClaimGate = claimRaceClaimGate{}

	rec := env.invoke(t, `{"model":"embed-public","input":"hello world"}`)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s want 409 claim_race, not reserve_error 500", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After=%q want 1", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"code":"claim_race"`) {
		t.Fatalf("body=%s want claim_race code", body)
	}
	for _, bad := range []string{"reserve_error", "request reservation failed"} {
		if strings.Contains(body, bad) {
			t.Fatalf("body=%s leaked generic reserve error marker %q", body, bad)
		}
	}
}
