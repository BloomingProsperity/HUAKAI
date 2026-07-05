package imageshttp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/quota"
)

type imagesQuotaReserverSpy struct {
	calls  int
	last   quota.ReserveRequest
	result quota.ReserveResult
	err    error
}

func (s *imagesQuotaReserverSpy) Reserve(_ context.Context, req quota.ReserveRequest) (quota.ReserveResult, error) {
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

// TestImagesTokenSchemeQuotaReserveCarriesPromptEstimate 守同型 C-5:token-image
// 方案已有 prompt token 估算,必须传入 quota token 窗预检。
// 变异:billing.go 的 ReservedTokens 改回 0 → ReservedTokens 断言红。
func TestImagesTokenSchemeQuotaReserveCarriesPromptEstimate(t *testing.T) {
	env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{
		status: http.StatusOK,
		body:   `{"created":1,"data":[{"url":"https://img.test/g.png"}],"usage":{"input_tokens":7,"output_tokens":11,"input_tokens_details":{"image_tokens":3}}}`,
	})
	quotaSpy := &imagesQuotaReserverSpy{}
	env.deps.QuotaReserver = quotaSpy

	rec := env.invoke(t, `{"model":"gpt-image-1","prompt":"quota token image","size":"1024x1024"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if quotaSpy.calls != 1 {
		t.Fatalf("quota reserve calls=%d want 1", quotaSpy.calls)
	}
	if quotaSpy.last.ReservedTokens <= 0 {
		t.Fatalf("ReservedTokens=%d want >0 from token-image prompt estimate", quotaSpy.last.ReservedTokens)
	}
}

// TestImagesQuotaReserveInfraErrorFailsOpenWithMetric 守同型 C-4a:quota 后端故障
// 保持 fail-open 放行,并增加 images 自己的观测计数。
// 变异:fail-open 分支去掉 imagesQuotaReserveFailedOpenTotal.Add → 计数差值断言红。
func TestImagesQuotaReserveInfraErrorFailsOpenWithMetric(t *testing.T) {
	env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{
		status: http.StatusOK,
		body:   `{"created":1,"data":[{"url":"https://img.test/g.png"}]}`,
	})
	env.deps.QuotaReserver = &imagesQuotaReserverSpy{err: errors.New("quota store down")}
	before := imagesQuotaReserveFailedOpenTotal.Value()

	rec := env.invoke(t, `{"model":"dall-e-2","prompt":"fail open","size":"512x512"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200 fail-open", rec.Code, rec.Body.String())
	}
	if !env.transport.called {
		t.Fatal("upstream was not called after quota fail-open")
	}
	if after := imagesQuotaReserveFailedOpenTotal.Value(); after != before+1 {
		t.Fatalf("images quota failed-open metric before/after=%d/%d want +1", before, after)
	}
}

// TestImagesQuotaDenyEmitsRetryAfter 守同型 quota deny 响应:窗口拒绝必须透出
// Retry-After、window_resets_at 与 window_kind。
// 变异:deny 分支改回 writeInsufficientQuotaError(w) → Retry-After 与 body 字段断言红。
func TestImagesQuotaDenyEmitsRetryAfter(t *testing.T) {
	env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{status: http.StatusOK, body: `{}`})
	env.deps.QuotaReserver = &imagesQuotaReserverSpy{result: quota.ReserveResult{
		Allowed: false,
		Decision: quota.Decision{
			Kind:       quota.DecisionDeny,
			RetryAfter: 75 * time.Second,
			WindowKind: quota.WindowCalendarDay,
		},
	}}

	rec := env.invoke(t, `{"model":"dall-e-2","prompt":"quota deny","size":"512x512"}`)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d body=%s want 429", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "75" {
		t.Fatalf("Retry-After=%q want 75", got)
	}
	body := rec.Body.String()
	for _, want := range []string{`"window_resets_at"`, `"window_kind":"calendar_day"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body=%s want %s", body, want)
		}
	}
	if env.transport.called {
		t.Fatal("upstream should not be called on quota deny")
	}
}
