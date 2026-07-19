package quotaprobe

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/accountquota"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func TestAntigravityAdapterProjectsPerModelQuota(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer token-a" {
			t.Fatalf("请求合同错误：%s auth=%q", r.Method, r.Header.Get("Authorization"))
		}
		if r.Header.Get("User-Agent") == "" || r.Header.Get("X-Goog-Api-Client") == "" {
			t.Fatalf("缺少正式客户端身份头：ua=%q api_client=%q", r.Header.Get("User-Agent"), r.Header.Get("X-Goog-Api-Client"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"models":{"model-a":{"quotaInfo":{"remainingFraction":0.25,"resetTime":"2026-07-19T12:00:00Z"}},"model-b":{"quotaInfo":{"remainingFraction":0}}}}`)
	}))
	defer server.Close()

	adapter := NewAntigravityAdapter(server.Client(), nil)
	adapter.endpoint = server.URL
	result, err := adapter.Fetch(context.Background(), 101,
		provider.Credential{Value: "token-a", Extra: map[string]string{"project_id": "project-a"}},
		provider.AccountInfo{Platform: "antigravity"}, time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if calls.Load() != 1 || !result.Complete || result.Source != accountquota.SourceUpstreamModelCatalog || len(result.Facts) != 2 {
		t.Fatalf("result=%+v calls=%d", result, calls.Load())
	}
	facts := map[string]accountquota.Fact{}
	for _, fact := range result.Facts {
		facts[fact.ModelKey] = fact
	}
	if facts["model-a"].RemainingPercent == nil || *facts["model-a"].RemainingPercent != 25 || facts["model-a"].State != accountquota.StateAvailable {
		t.Fatalf("model-a=%+v", facts["model-a"])
	}
	if facts["model-b"].State != accountquota.StateExhausted {
		t.Fatalf("model-b=%+v", facts["model-b"])
	}
}

func TestAntigravityAdapterFallsBackOnlyForRecoverableFailure(t *testing.T) {
	var primaryCalls, fallbackCalls atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryCalls.Add(1)
		http.Error(w, "temporary", http.StatusServiceUnavailable)
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fallbackCalls.Add(1)
		_, _ = fmt.Fprint(w, `{"models":{"model-a":{"quotaInfo":{"remainingFraction":0.75}}}}`)
	}))
	defer fallback.Close()
	adapter := NewAntigravityAdapter(primary.Client(), nil)
	adapter.endpoint, adapter.fallbackEndpoint = primary.URL, fallback.URL
	result, err := adapter.Fetch(context.Background(), 101,
		provider.Credential{Value: "token-a", Extra: map[string]string{"project_id": "project-a"}},
		provider.AccountInfo{Platform: "antigravity"}, time.Now())
	if err != nil || len(result.Facts) != 1 || primaryCalls.Load() != 1 || fallbackCalls.Load() != 1 {
		t.Fatalf("可恢复故障未安全切换：result=%+v err=%v calls=%d/%d", result, err, primaryCalls.Load(), fallbackCalls.Load())
	}

	primaryCalls.Store(0)
	fallbackCalls.Store(0)
	authFailure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryCalls.Add(1)
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer authFailure.Close()
	adapter.endpoint = authFailure.URL
	_, err = adapter.Fetch(context.Background(), 101,
		provider.Credential{Value: "token-a", Extra: map[string]string{"project_id": "project-a"}},
		provider.AccountInfo{Platform: "antigravity"}, time.Now())
	if err == nil || primaryCalls.Load() != 1 || fallbackCalls.Load() != 0 {
		t.Fatalf("鉴权失败不得切备用端点：err=%v calls=%d/%d", err, primaryCalls.Load(), fallbackCalls.Load())
	}
}

func TestGrokBillingAdapterPartialResultDoesNotClaimComplete(t *testing.T) {
	var postCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			postCalls.Add(1)
		}
		if r.URL.Query().Get("format") == "credits" {
			http.Error(w, "temporary", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"config":{"monthlyLimit":1000,"used":250,"billingPeriodEnd":"2026-08-01T00:00:00Z"}}`)
	}))
	defer server.Close()

	adapter := NewGrokBillingAdapter(server.Client(), nil)
	adapter.base = server.URL
	result, err := adapter.Fetch(context.Background(), 202,
		provider.Credential{Value: "token-g"},
		provider.AccountInfo{Platform: "grok", AccountType: "xai_oauth"}, time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if result.Complete || result.ErrorClass != ErrorClassUpstreamPartialResponse || len(result.Facts) != 1 || result.Facts[0].MetricKey != "monthly_spend" {
		t.Fatalf("部分结果合同=%+v", result)
	}
	if postCalls.Load() != 0 {
		t.Fatalf("只读额度采集不得发送付费模型请求：post=%d", postCalls.Load())
	}
}

func TestGeminiAdapterReportsExplicitUnknown(t *testing.T) {
	result, err := (GeminiUnknownAdapter{}).Fetch(context.Background(), 303, provider.Credential{}, provider.AccountInfo{Platform: "gemini"}, time.Now())
	if err != nil || !result.Complete || len(result.Facts) != 1 || result.Facts[0].State != accountquota.StateUnknown || result.Facts[0].RemainingPercent != nil {
		t.Fatalf("Gemini unknown 合同=%+v err=%v", result, err)
	}
}
