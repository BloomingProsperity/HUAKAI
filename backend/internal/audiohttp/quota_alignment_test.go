package audiohttp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/quota"
)

type audioQuotaReserverSpy struct {
	calls  int
	last   quota.ReserveRequest
	result quota.ReserveResult
	err    error
}

func (s *audioQuotaReserverSpy) Reserve(_ context.Context, req quota.ReserveRequest) (quota.ReserveResult, error) {
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

// TestAudioTokenSchemeQuotaReserveCarriesInputEstimate 守同型 C-5:audio token
// 方案已有 reserveTokenUsage 输入估算,必须传入 quota token 窗预检。
// 变异:billing.go 的 ReservedTokens 改回 0 → ReservedTokens 断言红。
func TestAudioTokenSchemeQuotaReserveCarriesInputEstimate(t *testing.T) {
	env := newAudioTestEnv(t, audioEndpointTranscriptions, upstreamResponse{
		status: http.StatusOK,
		body:   `{"text":"done","usage":{"input_tokens":7,"output_tokens":11}}`,
	})
	quotaSpy := &audioQuotaReserverSpy{}
	env.deps.QuotaReserver = quotaSpy
	body, contentType := multipartAudioBody(t, "file", "clip.wav", "audio/wav", wavPCM16Fixture(16000, 16000), map[string]string{"model": "gpt-4o-transcribe"})

	rec := env.invokeMultipart(t, body, contentType)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if quotaSpy.calls != 1 {
		t.Fatalf("quota reserve calls=%d want 1", quotaSpy.calls)
	}
	if quotaSpy.last.ReservedTokens <= 0 {
		t.Fatalf("ReservedTokens=%d want >0 from audio token estimate", quotaSpy.last.ReservedTokens)
	}
}

// TestAudioQuotaReserveInfraErrorFailsOpenWithMetric 守同型 C-4a:quota 后端故障
// 保持 fail-open 放行,并增加 audio 自己的观测计数。
// 变异:fail-open 分支去掉 audioQuotaReserveFailedOpenTotal.Add → 计数差值断言红。
func TestAudioQuotaReserveInfraErrorFailsOpenWithMetric(t *testing.T) {
	env := newAudioTestEnv(t, audioEndpointSpeech, upstreamResponse{
		status:  http.StatusOK,
		body:    "audio-bytes",
		headers: http.Header{"Content-Type": []string{"audio/mpeg"}},
	})
	env.deps.QuotaReserver = &audioQuotaReserverSpy{err: errors.New("quota store down")}
	before := audioQuotaReserveFailedOpenTotal.Value()

	rec := env.invokeJSON(t, `{"model":"tts-1","input":"hello","voice":"alloy","response_format":"mp3"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200 fail-open", rec.Code, rec.Body.String())
	}
	if !env.transport.called {
		t.Fatal("upstream was not called after quota fail-open")
	}
	if after := audioQuotaReserveFailedOpenTotal.Value(); after != before+1 {
		t.Fatalf("audio quota failed-open metric before/after=%d/%d want +1", before, after)
	}
}

// TestAudioQuotaDenyEmitsRetryAfter 守同型 quota deny 响应:窗口拒绝必须透出
// Retry-After、window_resets_at 与 window_kind。
// 变异:deny 分支改回不带退避字段的 writeInsufficientQuotaErrorRetryable(w,0,"") → Retry-After 与 body 字段断言红。
func TestAudioQuotaDenyEmitsRetryAfter(t *testing.T) {
	env := newAudioTestEnv(t, audioEndpointSpeech, upstreamResponse{status: http.StatusOK, body: "audio"})
	env.deps.QuotaReserver = &audioQuotaReserverSpy{result: quota.ReserveResult{
		Allowed: false,
		Decision: quota.Decision{
			Kind:       quota.DecisionDeny,
			RetryAfter: 2*time.Minute + 3*time.Second,
			WindowKind: quota.WindowCalendarWeek,
		},
	}}

	rec := env.invokeJSON(t, `{"model":"tts-1","input":"quota deny","voice":"alloy"}`)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d body=%s want 429", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "123" {
		t.Fatalf("Retry-After=%q want 123", got)
	}
	body := rec.Body.String()
	for _, want := range []string{`"window_resets_at"`, `"window_kind":"calendar_week"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body=%s want %s", body, want)
		}
	}
	if env.transport.called {
		t.Fatal("upstream should not be called on quota deny")
	}
}
