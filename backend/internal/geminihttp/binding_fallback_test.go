package geminihttp

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	providergemini "github.com/BloomingProsperity/HUAKAI/internal/provider/gemini"
	"github.com/BloomingProsperity/HUAKAI/internal/rate"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
	"github.com/BloomingProsperity/HUAKAI/internal/upstreamfeedback"
)

func TestGeminiCountTokensBindingFallbackClass(t *testing.T) {
	t.Run("normal 成功不碰目标类", func(t *testing.T) {
		env := newGeminiFallbackEnv(false, nil)
		rec := env.invoke(t)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		if len(env.selector.requests) != 1 || len(env.transport.keys) != 1 || env.selector.releases != 1 {
			t.Fatalf("selector/dispatch/release=%d/%d/%d，期望 1/1/1", len(env.selector.requests), len(env.transport.keys), env.selector.releases)
		}
		if env.selector.requests[0].PoolGroupID != 101 {
			t.Fatalf("首个 pool=%d，期望 normal 101", env.selector.requests[0].PoolGroupID)
		}
		if got := env.selector.requests[0].CapabilityFlags; len(got) != 1 || got[0] != countTokensCapability {
			t.Fatalf("选号能力=%v，期望 [%s]", got, countTokensCapability)
		}
	})

	t.Run("normal 耗尽恰一次转移 manual", func(t *testing.T) {
		env := newGeminiFallbackEnv(true, nil)
		rec := env.invoke(t)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		if len(env.selector.requests) != 3 || len(env.transport.keys) != 3 || env.selector.releases != 3 {
			t.Fatalf("selector/dispatch/release=%d/%d/%d，期望 3/3/3", len(env.selector.requests), len(env.transport.keys), env.selector.releases)
		}
		target := env.selector.requests[2]
		if target.PoolGroupID != 201 || target.BindingID != 6201 || target.BindingRPMLimit != 72 || target.BindingTPMLimit != 720 || target.MaxParallelRequests != 4 || target.SelectionMode != "priority_weighted" {
			t.Fatalf("目标 request=%+v，未使用自身元数据", target)
		}
		if target.EstimatedInputTokens <= 0 || target.ModelContextWindow != 1_048_576 {
			t.Fatalf("目标请求未携带输入估算或模型窗口: %+v", target)
		}
		if env.transport.keys[2] != "key-201" {
			t.Fatalf("目标凭据=%q，期望 key-201", env.transport.keys[2])
		}
		if _, excluded := target.ExcludedAccounts[101]; !excluded {
			t.Fatalf("目标 class exclusions=%v，必须继承 normal 失败账号 101", target.ExcludedAccounts)
		}
	})

	t.Run("本地权限拒绝终态零转移", func(t *testing.T) {
		env := newGeminiFallbackEnv(false, auth.ErrForbidden)
		rec := env.invoke(t)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d body=%s，期望 403", rec.Code, rec.Body.String())
		}
		if len(env.selector.requests) != 0 || len(env.transport.keys) != 0 || env.selector.releases != 0 {
			t.Fatalf("本地拒绝后 selector/dispatch/release=%d/%d/%d，均应为 0", len(env.selector.requests), len(env.transport.keys), env.selector.releases)
		}
	})
}

func TestGeminiCountTokensAccountPolicyProjectionIsTerminal(t *testing.T) {
	env := newGeminiFallbackEnv(false, nil)
	env.transport.steps = []geminiTransportStep{{
		status: http.StatusBadRequest,
		body:   `{"error":{"message":"policy-match"}}`,
	}}
	env.relay.feedback = geminiProjectionObserver()

	rec := env.invoke(t)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s want 422", rec.Code, rec.Body.String())
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"error":{"code":"account_busy","message":"账号暂不可用"}}` {
		t.Fatalf("投影错误响应=%s", got)
	}
	if len(env.selector.requests) != 1 || len(env.transport.keys) != 1 || env.selector.releases != 1 {
		t.Fatalf("客户端终态不得因投影重试: selector/dispatch/release=%d/%d/%d", len(env.selector.requests), len(env.transport.keys), env.selector.releases)
	}
}

func TestGeminiCountTokensEmpty401UsesOneAuthFailoverBeyondSingleAttemptBudget(t *testing.T) {
	env := newGeminiFallbackEnv(false, nil)
	env.relay.d.Router = geminiFixedRouter{attempts: 1}
	env.selector.accounts = []int64{101, 102}
	env.transport.steps = []geminiTransportStep{
		{status: http.StatusUnauthorized, body: ""},
		{status: http.StatusOK, body: `{"totalTokens":7}`},
	}

	rec := env.invoke(t)

	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"totalTokens":7}` {
		t.Fatalf("status=%d body=%s want 200 after one auth failover", rec.Code, rec.Body.String())
	}
	if got := env.transport.keys; len(got) != 2 || got[0] != "key-101" || got[1] != "key-102" {
		t.Fatalf("授权换号凭据=%v want [key-101 key-102]", got)
	}
	if env.selector.releases != 2 {
		t.Fatalf("短请求槽释放=%d want 2", env.selector.releases)
	}
}

func TestGeminiCountTokensConsumesInjectedTenantRetryBudget(t *testing.T) {
	env := newGeminiFallbackEnv(false, nil)
	env.relay.d.Router = geminiFixedRouter{attempts: 2}
	env.selector.accounts = []int64{101, 102}
	env.transport.steps = []geminiTransportStep{
		{status: http.StatusInternalServerError, body: `{"error":{"type":"server_error"}}`},
		{status: http.StatusOK, body: `{"totalTokens":7}`},
	}
	budget := &geminiDenyRetryBudget{}
	env.relay.retryBudget = budget

	rec := env.invoke(t)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s want 502 when tenant retry budget denies", rec.Code, rec.Body.String())
	}
	if len(env.transport.keys) != 1 || len(env.selector.requests) != 1 || env.selector.releases != 1 {
		t.Fatalf("预算拒绝后仍重试: dispatch/selector/release=%d/%d/%d", len(env.transport.keys), len(env.selector.requests), env.selector.releases)
	}
	if len(budget.tenants) != 1 || budget.tenants[0] != 7 {
		t.Fatalf("重试预算 tenant=%v want [7]", budget.tenants)
	}
}

type geminiFallbackEnv struct {
	relay     *countTokensRelay
	selector  *geminiFallbackSelector
	transport *geminiFallbackTransport
}

func newGeminiFallbackEnv(normalFails bool, authErr error) *geminiFallbackEnv {
	selector := &geminiFallbackSelector{}
	roundTripper := &geminiFallbackTransport{normalFails: normalFails}
	adapters := provider.NewStaticRegistry()
	adapters.MustRegister("gemini_messages", &providergemini.PassthroughAdapter{})
	factory := transport.NewFactory()
	factory.SetStandard(roundTripper)
	return &geminiFallbackEnv{
		selector: selector, transport: roundTripper,
		relay: &countTokensRelay{d: gatewayhttp.ChatHandlerDeps{
			Auth: geminiFallbackAuth{err: authErr}, Registry: geminiFallbackRegistry{}, Router: router.NewDefaultRouter(),
			Selector: selector, CredentialVault: geminiFallbackVault{},
			Dispatcher: &gateway.UpstreamDispatcher{Adapters: adapters, TransportFactory: factory},
		}},
	}
}

func (e *geminiFallbackEnv) invoke(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-pro:countTokens", bytes.NewBufferString(`{"contents":[{"parts":[{"text":"hello"}]}]}`))
	req.Header.Set("Authorization", "Bearer hk-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.relay.ServeGeminiCountTokens(rec, req, "gemini-pro")
	return rec
}

type geminiFallbackAuth struct{ err error }

func (s geminiFallbackAuth) Resolve(context.Context, *http.Request) (auth.Identity, error) {
	if s.err != nil {
		return auth.Identity{}, s.err
	}
	return auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 13, UserGroup: "pro"}, nil
}

type geminiFallbackRegistry struct{}

func (geminiFallbackRegistry) ResolveModel(context.Context, string, int64) (registry.Resolved, error) {
	targetMax := int32(4)
	targetRPM := int32(72)
	targetTPM := int32(720)
	normalModel := "gemini-normal-a"
	targetModel := "gemini-manual"
	return registry.Resolved{
		PublicAlias: "gemini-pro", CanonicalModelID: "gemini/canonical",
		DefaultProviderModelID: "gemini-normal-a", ProviderModelID: "gemini-normal-a",
		ProtocolFamily: "gemini_messages", ContextWindow: 1_048_576, PoolCandidates: []int64{101, 201},
		BindingMetadata: []registry.BindingMetadata{
			{PoolGroupID: 101, BindingID: 6101, Priority: 10, Weight: 1, SelectionMode: "strict_priority", ProviderModelIDOverride: &normalModel, FallbackClass: string(bindingfallback.ClassNormal)},
			{PoolGroupID: 201, BindingID: 6201, Priority: 20, Weight: 1, SelectionMode: "priority_weighted", RPMLimit: &targetRPM, TPMLimit: &targetTPM, MaxParallelRequests: &targetMax, ProviderModelIDOverride: &targetModel, FallbackClass: string(bindingfallback.ClassManual)},
		},
		SnapshotVersion: "registry:7:1",
	}, nil
}

type geminiFallbackSelector struct {
	requests []pool.SelectionRequest
	releases int
	accounts []int64
}

func (s *geminiFallbackSelector) Select(_ context.Context, req pool.SelectionRequest) (*pool.SelectionResult, error) {
	s.requests = append(s.requests, req)
	accountID := req.PoolGroupID
	if len(s.accounts) > 0 {
		accountID = 0
		for _, candidate := range s.accounts {
			if _, excluded := req.ExcludedAccounts[candidate]; excluded {
				continue
			}
			accountID = candidate
			break
		}
		if accountID == 0 {
			return nil, pool.ErrNoEligibleAccount
		}
	}
	return &pool.SelectionResult{
		AccountID: accountID, AcquisitionToken: uuid.New(), RoutingReasonJSON: []byte(`{"reason":"test"}`),
		Release: func(context.Context) error { s.releases++; return nil },
	}, nil
}

type geminiFallbackVault struct{}

func (geminiFallbackVault) Resolve(_ context.Context, tenantID, accountID int64) (provider.Credential, provider.AccountInfo, error) {
	return provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "key-" + strconv.FormatInt(accountID, 10)}, provider.AccountInfo{
		TenantID: tenantID, AccountID: accountID, Platform: "gemini", AccountType: credentialstore.AuthModeAIStudioAPIKey,
	}, nil
}

type geminiFallbackTransport struct {
	normalFails bool
	keys        []string
	steps       []geminiTransportStep
}

type geminiTransportStep struct {
	status int
	body   string
}

func (rt *geminiFallbackTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	key := req.Header.Get("X-Goog-Api-Key")
	rt.keys = append(rt.keys, key)
	status := http.StatusOK
	body := `{"totalTokens":7}`
	if len(rt.keys) <= len(rt.steps) {
		step := rt.steps[len(rt.keys)-1]
		status, body = step.status, step.body
	} else if rt.normalFails && key != "key-201" {
		status = http.StatusInternalServerError
		body = `{"error":{"type":"server_error"}}`
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

type geminiErrorPolicyProvider struct{}

func (geminiErrorPolicyProvider) GetAccountErrorPolicy(int64) rate.AccountErrorPolicy {
	clientStatus := http.StatusUnprocessableEntity
	affectHealth := false
	return rate.AccountErrorPolicy{Rules: []rate.TempUnschedulableRule{{
		RuleID: "busy-400", ErrorCode: http.StatusBadRequest, Keywords: []string{"policy-match"},
		DurationMinutes: 5, ClientStatus: &clientStatus, ClientCode: "account_busy",
		MessageMode: "custom", ClientMessage: "账号暂不可用", AffectHealth: &affectHealth,
	}}}
}

func geminiProjectionObserver() *upstreamfeedback.Observer {
	return upstreamfeedback.NewObserver(upstreamfeedback.Dependencies{
		RateService: rate.NewUpstreamRateService(time.Now, time.Minute,
			rate.WithAccountErrorRulesProvider(geminiErrorPolicyProvider{})),
	})
}

type geminiFixedRouter struct{ attempts int }

func (r geminiFixedRouter) Plan(context.Context, router.PlanInput) (router.RoutePlan, error) {
	attempts := r.attempts
	if attempts <= 0 {
		attempts = 1
	}
	plans := make([]router.AttemptPlan, attempts)
	for i := range plans {
		plans[i] = router.AttemptPlan{
			Index: i, PoolGroupID: 101, BindingID: 6101,
			UpstreamModelID: "gemini-normal-a", Reason: "test",
		}
	}
	return router.RoutePlan{
		Attempts: plans, AttemptBudget: attempts,
		RetryableEndClasses: []string{string(gateway.UpstreamError5xx)},
		SnapshotVersion:     "registry:7:1;router:test",
	}, nil
}

type geminiDenyRetryBudget struct{ tenants []int64 }

func (b *geminiDenyRetryBudget) Allow(tenantID int64) bool {
	b.tenants = append(b.tenants, tenantID)
	return false
}
