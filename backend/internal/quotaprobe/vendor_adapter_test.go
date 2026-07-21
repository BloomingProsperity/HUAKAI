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
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionprofile"
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
		provider.AccountInfo{Platform: credentialstore.VendorAntigravity, AccountType: credentialstore.AuthModeOAuth}, time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC))
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

func TestAntigravityAdapterOwnsCanonicalAndLegacyStorageModes(t *testing.T) {
	adapter := NewAntigravityAdapter(http.DefaultClient, nil)
	credential := provider.Credential{Value: "access-token"}
	for _, info := range []provider.AccountInfo{
		{Platform: credentialstore.VendorAntigravity, AccountType: credentialstore.AuthModeOAuth},
		{Platform: credentialstore.VendorGemini, AccountType: credentialstore.AuthModeAntigravity},
	} {
		if !adapter.Supports(credential, info) {
			t.Fatalf("Antigravity 适配器未识别账号：%+v", info)
		}
		if (GeminiUnknownAdapter{}).Supports(credential, info) {
			t.Fatalf("Antigravity 账号被 Gemini 未知额度适配器抢占：%+v", info)
		}
	}
	if adapter.Supports(credential, provider.AccountInfo{Platform: credentialstore.VendorGemini, AccountType: credentialstore.AuthModeOAuth}) {
		t.Fatal("普通 Gemini OAuth 不得进入 Antigravity 额度链")
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

func TestGrokBillingAdapterProjectsVerifiedSubscriptionFromMonthlyBilling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("format") == "credits" {
			_, _ = fmt.Fprint(w, `{"config":{"creditUsagePercent":20}}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"config":{"monthlyLimit":{"val":"150000"},"used":{"val":7500}}}`)
	}))
	defer server.Close()

	adapter := NewGrokBillingAdapter(server.Client(), nil)
	adapter.base = server.URL
	result, err := adapter.Fetch(context.Background(), 203,
		provider.Credential{Value: "token-g"},
		provider.AccountInfo{Platform: "grok", AccountType: "xai_oauth"}, time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !result.Complete || len(result.Facts) != 2 || result.Subscription.Label() != "grok:supergrok_heavy" ||
		result.Subscription.Source != subscriptionprofile.SourceProviderAPI ||
		result.Subscription.Trust != subscriptionprofile.TrustVerifiedAPI ||
		result.Subscription.Verification != subscriptionprofile.VerificationVerified {
		t.Fatalf("Grok 套餐与额度投影不完整：%+v", result)
	}
}

func TestGeminiAdapterReportsExplicitUnknown(t *testing.T) {
	result, err := (GeminiUnknownAdapter{}).Fetch(context.Background(), 303, provider.Credential{}, provider.AccountInfo{Platform: "gemini"}, time.Now())
	if err != nil || !result.Complete || len(result.Facts) != 1 || result.Facts[0].State != accountquota.StateUnknown || result.Facts[0].RemainingPercent != nil {
		t.Fatalf("Gemini unknown 合同=%+v err=%v", result, err)
	}
}

func TestCodexUsageAdapterProjectsWindowsAndVerifiedPlan(t *testing.T) {
	observedAt := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer codex-access" ||
			r.Header.Get("ChatGPT-Account-Id") != "workspace-7" || r.Header.Get("OpenAI-Beta") != "codex-1" {
			t.Fatalf("Codex 额度请求合同错误：method=%s auth=%q account=%q beta=%q", r.Method, r.Header.Get("Authorization"), r.Header.Get("ChatGPT-Account-Id"), r.Header.Get("OpenAI-Beta"))
		}
		_, _ = fmt.Fprint(w, `{
			"user_id":"user-7","account_id":"workspace-7","plan_type":"Pro",
			"rate_limit":{
				"primary_window":{"used_percent":76,"limit_window_seconds":604800,"reset_after_seconds":500000},
				"secondary_window":{"used_percent":24,"limit_window_seconds":18000,"reset_after_seconds":7200}
			}
		}`)
	}))
	defer server.Close()

	adapter := NewCodexUsageAdapter(server.Client(), nil)
	adapter.endpoint = server.URL
	credential := provider.Credential{
		Type: provider.CredentialTypeSessionToken, Value: "codex-access",
		Extra: map[string]string{"chatgpt_account_id": "workspace-7", "codex_version": "1.2.3"},
	}
	info := provider.AccountInfo{Platform: "openai", AccountType: "codex_cli_oauth"}
	result, err := adapter.Fetch(context.Background(), 404, credential, info, observedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || result.Session == nil || len(result.Facts) != 2 || result.Subscription.Label() != "openai:pro" ||
		result.Subscription.Trust != subscriptionprofile.TrustVerifiedAPI {
		t.Fatalf("Codex 投影不完整：%+v", result)
	}
	if result.Session.FiveHour.Utilization == nil || *result.Session.FiveHour.Utilization != 24 ||
		result.Session.FiveHour.ResetsAt == nil || !result.Session.FiveHour.ResetsAt.Equal(observedAt.Add(2*time.Hour)) {
		t.Fatalf("Codex 5h 窗口错误：%+v", result.Session.FiveHour)
	}
	if result.Session.SevenDay.Utilization == nil || *result.Session.SevenDay.Utilization != 76 ||
		result.Session.SevenDay.ResetsAt == nil || !result.Session.SevenDay.ResetsAt.Equal(observedAt.Add(500000*time.Second)) {
		t.Fatalf("Codex 7d 窗口错误：%+v", result.Session.SevenDay)
	}
	if result.Subscription.SubjectRef != "user-7" || result.Subscription.WorkspaceRef != "" {
		t.Fatalf("个人套餐不得误写工作区归属：%+v", result.Subscription)
	}
}

func TestCodexUsageProjectionMissingDurationKeepsPrimaryAsShortWindow(t *testing.T) {
	observedAt := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)
	snapshot, facts, complete := projectCodexUsage(&codexRateLimits{
		Primary:   &codexRateWindow{UsedPercent: 100, ResetAfterSeconds: 3600},
		Secondary: &codexRateWindow{UsedPercent: 17, ResetAfterSeconds: 500000},
	}, observedAt)
	if !complete || len(facts) != 2 || snapshot.FiveHour.Utilization == nil ||
		*snapshot.FiveHour.Utilization != 100 || snapshot.SevenDay.Utilization == nil ||
		*snapshot.SevenDay.Utilization != 17 {
		t.Fatalf("缺少窗口时长时不得颠倒主次窗口：snapshot=%+v facts=%+v complete=%v", snapshot, facts, complete)
	}
}

func TestCodexUsageAdapterRejectsOfficialAPIKey(t *testing.T) {
	adapter := NewCodexUsageAdapter(http.DefaultClient, nil)
	if adapter.Supports(
		provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-test"},
		provider.AccountInfo{Platform: "openai", AccountType: "api_key"},
	) {
		t.Fatal("OpenAI 官方 API Key 不得进入个人订阅与会话额度采集")
	}
}
