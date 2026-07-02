package imageshttp

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

// TestImagesHandler_ReserveClaimRaceReturns409RetryAfter 守 reserve 阶段的
// ErrClaimRace 分支:命中可重试竞争必须回 409 + Retry-After:1 + code=claim_race,
// 而非通用 500 reserve_error。
// 变异契约:删掉 billing.go reserve() 里 `if errors.Is(err, billing.ErrClaimRace)`
// 分支后,请求会落到下方通用 500 reserve_error,本测试随即变红。
func TestImagesHandler_ReserveClaimRaceReturns409RetryAfter(t *testing.T) {
	env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{status: http.StatusOK, body: `{}`})
	env.deps.ClaimGate = claimRaceClaimGate{}

	rec := env.invoke(t, `{"model":"dall-e-2","prompt":"claim race","size":"512x512"}`)

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
