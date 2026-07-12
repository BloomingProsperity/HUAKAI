package embeddingshttp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/quota"
)

type embeddingsQuotaReserverSpy struct {
	calls  int
	last   quota.ReserveRequest
	result quota.ReserveResult
	err    error
}

func (s *embeddingsQuotaReserverSpy) Reserve(_ context.Context, req quota.ReserveRequest) (quota.ReserveResult, error) {
	s.calls++
	s.last = req
	if s.err != nil {
		return quota.ReserveResult{}, s.err
	}
	if !s.result.Allowed && s.result.Decision.Kind == "" {
		return quota.ReserveResult{Allowed: true, Decision: quota.Decision{Kind: quota.DecisionAllow}}, nil
	}
	return s.result, nil
}

// TestEmbeddingsQuotaReserveInfraErrorFailsOpenWithMetric 守同型 C-4a:quota 后端故障
// 继续 fail-open 放行,但必须增加 embeddings 自己的观测计数。
// 变异:fail-open 分支去掉 embeddingsQuotaReserveFailedOpenTotal.Add → 计数差值断言红。
func TestEmbeddingsQuotaReserveInfraErrorFailsOpenWithMetric(t *testing.T) {
	env := newEmbeddingsTestEnv(t, upstreamResponse{
		status: http.StatusOK,
		body:   `{"object":"list","data":[{"object":"embedding","embedding":[0.1],"index":0}],"model":"text-embedding-3-small","usage":{"prompt_tokens":5,"total_tokens":5}}`,
	})
	env.deps.QuotaReserver = &embeddingsQuotaReserverSpy{err: errors.New("quota store down")}
	before := embeddingsQuotaReserveFailedOpenTotal.Value()

	rec := env.invoke(t, `{"model":"embed-public","input":"fail open"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200 fail-open", rec.Code, rec.Body.String())
	}
	if !env.transport.called {
		t.Fatal("upstream was not called after quota fail-open")
	}
	if after := embeddingsQuotaReserveFailedOpenTotal.Value(); after != before+1 {
		t.Fatalf("embeddings quota failed-open metric before/after=%d/%d want +1", before, after)
	}
}

// TestEmbeddingsQuotaDenyEmitsRetryAfter 守同型 quota deny 响应:窗口拒绝必须透出
// Retry-After、window_resets_at 与 window_kind。
// 变异:deny 分支改回不带 retryAfter 的写法 → Retry-After 与 body 字段断言红。
func TestEmbeddingsQuotaDenyEmitsRetryAfter(t *testing.T) {
	env := newEmbeddingsTestEnv(t, upstreamResponse{status: http.StatusOK, body: `{}`})
	env.deps.QuotaReserver = &embeddingsQuotaReserverSpy{result: quota.ReserveResult{
		Allowed: false,
		Decision: quota.Decision{
			Kind:       quota.DecisionDeny,
			RetryAfter: 90 * time.Second,
			WindowKind: quota.WindowFixed,
		},
	}}

	rec := env.invoke(t, `{"model":"embed-public","input":"quota deny"}`)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d body=%s want 429", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "90" {
		t.Fatalf("Retry-After=%q want 90", got)
	}
	body := rec.Body.String()
	for _, want := range []string{`"window_resets_at"`, `"window_kind":"fixed"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body=%s want %s", body, want)
		}
	}
	if env.transport.called {
		t.Fatal("upstream should not be called on quota deny")
	}
}
