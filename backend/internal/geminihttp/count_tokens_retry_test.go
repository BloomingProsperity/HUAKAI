package geminihttp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/openai"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
	"github.com/BloomingProsperity/HUAKAI/internal/upstreamfeedback"
)

func TestGeminiCountTokens500RetriesSecondAccountWithoutClaim(t *testing.T) {
	env := newGeminiCountTokensTestEnv(t, geminiCountTokensRetryRouter{})
	health := &geminiCountTokensHealthSpy{}
	env.relay.feedback = upstreamfeedback.NewObserver(upstreamfeedback.Dependencies{ChannelHealth: health})
	env.transport.steps = []geminiCountTokensResponse{
		{status: http.StatusInternalServerError, body: `{"error":"upstream busy"}`},
		{status: http.StatusOK, body: `{"totalTokens":7}`},
	}

	rec := env.invoke(t)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"totalTokens":7`) {
		t.Fatalf("status/body=%d/%s want 200 totalTokens=7", rec.Code, rec.Body.String())
	}
	if len(env.selector.requests) != 2 {
		t.Fatalf("selector calls=%d want 2", len(env.selector.requests))
	}
	if env.selector.requests[0].ClaimID != 0 || env.selector.requests[1].ClaimID != 0 {
		t.Fatalf("countTokens must not create billing claims: %+v", env.selector.requests)
	}
	if _, excluded := env.selector.requests[1].ExcludedAccounts[44]; !excluded {
		t.Fatalf("second attempt exclusions=%v want failed account 44", env.selector.requests[1].ExcludedAccounts)
	}
	if got := env.transport.authorization; len(got) != 2 || got[0] != "Bearer gemini-count-44" || got[1] != "Bearer gemini-count-45" {
		t.Fatalf("authorization sequence=%v want account 44 then 45", got)
	}
	if len(health.signals) != 2 ||
		health.signals[0].Class != channelhealth.SignalUpstream5xx ||
		health.signals[1].Class != channelhealth.SignalSuccess {
		t.Fatalf("health signals=%+v want upstream_5xx then success", health.signals)
	}
}

func TestGeminiCountTokens400DoesNotRetry(t *testing.T) {
	env := newGeminiCountTokensTestEnv(t, geminiCountTokensRetryRouter{})
	env.transport.steps = []geminiCountTokensResponse{{
		status: http.StatusBadRequest,
		body:   `{"error":"invalid contents"}`,
	}}

	rec := env.invoke(t)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s want 400", rec.Code, rec.Body.String())
	}
	if len(env.selector.requests) != 1 || len(env.transport.authorization) != 1 {
		t.Fatalf("selector/transport=%d/%d want 1/1", len(env.selector.requests), len(env.transport.authorization))
	}
}

func TestGeminiCountTokens401UsesSingleAuthFailoverBeyondAttemptBudget(t *testing.T) {
	env := newGeminiCountTokensTestEnv(t, geminiCountTokensSingleAttemptRouter{})
	env.transport.steps = []geminiCountTokensResponse{
		{status: http.StatusUnauthorized, body: `{"error":"invalid_grant"}`},
		{status: http.StatusOK, body: `{"totalTokens":9}`},
	}

	rec := env.invoke(t)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if len(env.selector.requests) != 2 || len(env.transport.authorization) != 2 {
		t.Fatalf("selector/transport=%d/%d want 2/2", len(env.selector.requests), len(env.transport.authorization))
	}
}

func TestGeminiCountTokensTenantRetryBudgetStopsSecondAttempt(t *testing.T) {
	env := newGeminiCountTokensTestEnv(t, geminiCountTokensRetryRouter{})
	budget := &geminiCountTokensDenyBudget{}
	env.relay.retryBudget = budget
	env.transport.steps = []geminiCountTokensResponse{{
		status: http.StatusInternalServerError,
		body:   `{"error":"upstream busy"}`,
	}}

	rec := env.invoke(t)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s want 502", rec.Code, rec.Body.String())
	}
	if len(budget.tenants) != 1 || budget.tenants[0] != 7 {
		t.Fatalf("retry budget tenants=%v want [7]", budget.tenants)
	}
	if len(env.transport.authorization) != 1 {
		t.Fatalf("transport calls=%d want 1", len(env.transport.authorization))
	}
}

func TestGeminiCountTokensWaitPlanDoesNotDispatch(t *testing.T) {
	env := newGeminiCountTokensTestEnv(t, geminiCountTokensRetryRouter{})
	env.selector.wait = true

	rec := env.invoke(t)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503", rec.Code, rec.Body.String())
	}
	if len(env.selector.requests) != 1 {
		t.Fatalf("selector calls=%d want 1", len(env.selector.requests))
	}
	if len(env.transport.authorization) != 0 {
		t.Fatalf("transport calls=%d want 0", len(env.transport.authorization))
	}
}

func TestGeminiDepsRetainSharedFeedbackAndRetryBudget(t *testing.T) {
	feedback := upstreamfeedback.NewObserver(upstreamfeedback.Dependencies{})
	budget := &geminiCountTokensDenyBudget{}
	deps := NewDeps(gatewayhttp.ChatHandlerDeps{}, nil, nil, feedback, budget)
	relay, ok := deps.CountTokens.(*countTokensRelay)
	if !ok {
		t.Fatalf("CountTokens type=%T want *countTokensRelay", deps.CountTokens)
	}
	if relay.feedback != feedback || relay.retryBudget != budget {
		t.Fatal("Gemini countTokens did not retain shared feedback and retry budget")
	}
}

type geminiCountTokensTestEnv struct {
	relay     *countTokensRelay
	handler   http.Handler
	selector  *geminiCountTokensSelector
	transport *geminiCountTokensTransport
}

func newGeminiCountTokensTestEnv(t *testing.T, route router.Router) *geminiCountTokensTestEnv {
	t.Helper()
	selector := &geminiCountTokensSelector{accounts: []int64{44, 45}}
	vault := provider.NewStaticVault()
	for _, accountID := range selector.accounts {
		if err := vault.Set(accountID, provider.Credential{
			Type:  provider.CredentialTypeAPIKey,
			Value: "gemini-count-" + strconv.FormatInt(accountID, 10),
		}, provider.AccountInfo{
			AccountID:           accountID,
			TenantID:            7,
			Platform:            "openai",
			AccountType:         "apikey",
			AccountCredentialID: 7000 + accountID,
			CredentialVersion:   1,
		}); err != nil {
			t.Fatalf("vault.Set(%d): %v", accountID, err)
		}
	}
	rt := &geminiCountTokensTransport{}
	adapters := provider.NewStaticRegistry()
	adapters.MustRegister("openai_chat", &openai.PassthroughAdapter{})
	tf := transport.NewFactory()
	tf.SetStandard(rt)
	dispatcher := &gateway.UpstreamDispatcher{Adapters: adapters, TransportFactory: tf}
	relay := &countTokensRelay{d: gatewayhttp.ChatHandlerDeps{
		Auth:            geminiCountTokensAuthStub{},
		Registry:        geminiCountTokensRegistryStub{},
		Router:          route,
		Selector:        selector,
		CredentialVault: vault,
		Dispatcher:      dispatcher,
	}}
	handler := NewGenerateContentHandler(Deps{CountTokens: relay})
	return &geminiCountTokensTestEnv{relay: relay, handler: handler, selector: selector, transport: rt}
}

func (e *geminiCountTokensTestEnv) invoke(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(
		http.MethodPost,
		"/v1beta/models/gemini-pro:countTokens",
		strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`),
	)
	req.Header.Set("Authorization", "Bearer hk-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return rec
}

type geminiCountTokensAuthStub struct{}

func (geminiCountTokensAuthStub) Resolve(context.Context, *http.Request) (auth.Identity, error) {
	return auth.Identity{TenantID: 7, UserID: 13, APIKeyID: 11, UserGroup: "pro"}, nil
}

type geminiCountTokensRegistryStub struct{}

func (geminiCountTokensRegistryStub) ResolveModel(context.Context, string, int64) (registry.Resolved, error) {
	return registry.Resolved{
		PublicAlias:      "gemini-pro",
		CanonicalModelID: "gemini/gemini-pro",
		ProviderModelID:  "gemini-pro",
		ProtocolFamily:   "openai_chat",
		Capabilities:     []string{"count_tokens"},
		PoolCandidates:   []int64{101, 202},
		SnapshotVersion:  "registry:7:1",
	}, nil
}

type geminiCountTokensRetryRouter struct{}

func (geminiCountTokensRetryRouter) Plan(context.Context, router.PlanInput) (router.RoutePlan, error) {
	return router.RoutePlan{
		Attempts: []router.AttemptPlan{
			{Index: 0, PoolGroupID: 101, UpstreamModelID: "gemini-pro", Reason: "primary"},
			{Index: 1, PoolGroupID: 202, UpstreamModelID: "gemini-pro", Reason: "account_failover"},
		},
		AttemptBudget: 2,
		RetryableEndClasses: []string{
			string(gateway.UpstreamError5xx),
			string(gateway.UpstreamRateLimit),
			string(gateway.FirstTokenTimeout),
			string(gateway.InterEventTimeout),
		},
		SnapshotVersion: "registry:7:1;router:gemini-count-retry",
	}, nil
}

type geminiCountTokensSingleAttemptRouter struct{}

func (geminiCountTokensSingleAttemptRouter) Plan(context.Context, router.PlanInput) (router.RoutePlan, error) {
	return router.RoutePlan{
		Attempts: []router.AttemptPlan{{
			Index: 0, PoolGroupID: 101, UpstreamModelID: "gemini-pro", Reason: "primary",
		}},
		AttemptBudget:   1,
		SnapshotVersion: "registry:7:1;router:gemini-count-auth",
	}, nil
}

type geminiCountTokensSelector struct {
	accounts []int64
	requests []pool.SelectionRequest
	wait     bool
}

func (s *geminiCountTokensSelector) Select(_ context.Context, req pool.SelectionRequest) (*pool.SelectionResult, error) {
	s.requests = append(s.requests, req)
	if s.wait {
		return &pool.SelectionResult{
			AccountID:        s.accounts[0],
			AcquisitionToken: uuid.New(),
			WaitPlan:         &pool.WaitPlan{},
		}, nil
	}
	for _, accountID := range s.accounts {
		if _, excluded := req.ExcludedAccounts[accountID]; excluded {
			continue
		}
		return &pool.SelectionResult{
			AccountID:         accountID,
			AcquisitionToken:  uuid.New(),
			RoutingReasonJSON: []byte(`{"reason":"gemini-count-test"}`),
		}, nil
	}
	return nil, pool.ErrNoEligibleAccount
}

type geminiCountTokensResponse struct {
	status int
	body   string
}

type geminiCountTokensTransport struct {
	mu            sync.Mutex
	steps         []geminiCountTokensResponse
	authorization []string
}

func (rt *geminiCountTokensTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.authorization = append(rt.authorization, req.Header.Get("Authorization"))
	step := geminiCountTokensResponse{status: http.StatusOK, body: `{"totalTokens":1}`}
	if len(rt.authorization) <= len(rt.steps) {
		step = rt.steps[len(rt.authorization)-1]
	}
	return &http.Response{
		StatusCode: step.status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(step.body)),
		Request:    req,
	}, nil
}

type geminiCountTokensHealthSpy struct {
	signals []channelhealth.Signal
}

func (s *geminiCountTokensHealthSpy) ApplySignal(_ context.Context, signal channelhealth.Signal) (channelhealth.Record, error) {
	s.signals = append(s.signals, signal)
	return channelhealth.Record{Key: signal.Key}, nil
}

func (s *geminiCountTokensHealthSpy) ForceCooldown(_ context.Context, key channelhealth.ChannelKey, _ time.Time, _ string) (channelhealth.Record, error) {
	return channelhealth.Record{Key: key}, nil
}

type geminiCountTokensDenyBudget struct {
	tenants []int64
}

func (b *geminiCountTokensDenyBudget) Allow(tenantID int64) bool {
	b.tenants = append(b.tenants, tenantID)
	return false
}
