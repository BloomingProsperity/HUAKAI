package rerankhttp

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

func TestRerankBindingFallbackClass(t *testing.T) {
	t.Run("normal 成功不碰目标类", func(t *testing.T) {
		env := newRerankFallbackEnv(t, false, false)
		rec := env.base.invoke(t, rerankBody(1))
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		assertRerankFallbackMoney(t, env, 1, 0, 1)
		if len(env.selector.requests) != 1 || env.selector.requests[0].PoolGroupID != 101 {
			t.Fatalf("selector requests=%+v，期望仅 normal", env.selector.requests)
		}
	})

	t.Run("normal 耗尽恰一次转移 manual", func(t *testing.T) {
		env := newRerankFallbackEnv(t, true, false)
		rec := env.base.invoke(t, rerankBody(1))
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		assertRerankFallbackMoney(t, env, 3, 2, 1)
		target := env.selector.requests[2]
		if target.PoolGroupID != 201 || target.BindingID != 3201 || target.MaxParallelRequests != 8 || target.SelectionMode != "priority_weighted" {
			t.Fatalf("目标 request=%+v，未使用自身元数据", target)
		}
		if got := string(env.base.settler.settles[0].Draft.RoutingReason); !strings.Contains(got, `"to":"manual"`) {
			t.Fatalf("RoutingReason=%s，缺少 manual 转移", got)
		}
	})

	t.Run("本地 key 限额终态零转移", func(t *testing.T) {
		env := newRerankFallbackEnv(t, false, true)
		rec := env.base.invoke(t, rerankBody(1))
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("status=%d body=%s，期望 429", rec.Code, rec.Body.String())
		}
		assertRerankFallbackMoney(t, env, 1, 1, 0)
		if len(env.selector.requests) != 1 || len(env.dispatcher.calls) != 0 {
			t.Fatalf("终态 selector/dispatch=%d/%d，目标类不得执行", len(env.selector.requests), len(env.dispatcher.calls))
		}
	})
}

type rerankFallbackEnv struct {
	base       *rerankTestEnv
	claims     *rerankFallbackClaims
	selector   *rerankFallbackSelector
	dispatcher *rerankFallbackDispatcher
}

func newRerankFallbackEnv(t *testing.T, normalFails, terminal bool) *rerankFallbackEnv {
	base := newRerankTestEnv(t)
	claims := &rerankFallbackClaims{next: 14000}
	selector := &rerankFallbackSelector{terminal: terminal}
	dispatcher := &rerankFallbackDispatcher{normalFails: normalFails}
	base.deps.Registry = rerankFallbackRegistry{}
	base.deps.Router = router.NewDefaultRouter()
	base.deps.ClaimGate = claims
	base.deps.Selector = selector
	base.deps.CredentialVault = rerankFallbackVault{}
	base.deps.Dispatcher = dispatcher
	return &rerankFallbackEnv{base: base, claims: claims, selector: selector, dispatcher: dispatcher}
}

func assertRerankFallbackMoney(t *testing.T, env *rerankFallbackEnv, reserves, aborts, settles int) {
	t.Helper()
	if len(env.claims.reserves) != reserves || len(env.base.settler.aborts) != aborts || len(env.base.settler.settles) != settles {
		t.Fatalf("reserves/aborts/settles=%d/%d/%d，期望 %d/%d/%d",
			len(env.claims.reserves), len(env.base.settler.aborts), len(env.base.settler.settles), reserves, aborts, settles)
	}
}

type rerankFallbackRegistry struct{}

func (rerankFallbackRegistry) ResolveModel(context.Context, string, int64) (registry.Resolved, error) {
	targetMax := int32(8)
	return registry.Resolved{
		PublicAlias: "rerank-public", CanonicalModelID: "rerank/canonical",
		DefaultProviderModelID: "rerank-upstream", ProviderModelID: "rerank-upstream",
		ProtocolFamily: "openai_chat", Capabilities: []string{"rerank"}, PoolCandidates: []int64{101, 201},
		BindingMetadata: []registry.BindingMetadata{
			{PoolGroupID: 101, BindingID: 3101, Priority: 10, Weight: 1, SelectionMode: "strict_priority", FallbackClass: string(bindingfallback.ClassNormal)},
			{PoolGroupID: 201, BindingID: 3201, Priority: 20, Weight: 1, SelectionMode: "priority_weighted", MaxParallelRequests: &targetMax, FallbackClass: string(bindingfallback.ClassManual)},
		},
		SnapshotVersion: "registry:7:1",
	}, nil
}

type rerankFallbackClaims struct {
	next     int64
	reserves []billing.ReserveRequest
}

func (g *rerankFallbackClaims) Reserve(_ context.Context, req billing.ReserveRequest) (*billing.ReserveResult, error) {
	g.next++
	g.reserves = append(g.reserves, req)
	return &billing.ReserveResult{ClaimID: g.next}, nil
}

type rerankFallbackSelector struct {
	terminal bool
	requests []pool.SelectionRequest
}

func (s *rerankFallbackSelector) Select(_ context.Context, req pool.SelectionRequest) (*pool.SelectionResult, error) {
	s.requests = append(s.requests, req)
	if s.terminal {
		return nil, pool.ErrKeyRateLimited
	}
	return &pool.SelectionResult{AccountID: req.PoolGroupID, AcquisitionToken: uuid.New(), RoutingReasonJSON: []byte(`{"reason":"test"}`)}, nil
}

type rerankFallbackVault struct{}

func (rerankFallbackVault) Resolve(_ context.Context, tenantID, accountID int64) (provider.Credential, provider.AccountInfo, error) {
	return provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-test"}, provider.AccountInfo{TenantID: tenantID, AccountID: accountID, Platform: "openai", AccountType: "api_key"}, nil
}

type rerankFallbackDispatcher struct {
	normalFails bool
	calls       []gateway.DispatchInput
}

func (d *rerankFallbackDispatcher) Dispatch(_ context.Context, in gateway.DispatchInput) (*gateway.DispatchResult, error) {
	d.calls = append(d.calls, in)
	if d.normalFails && in.Account.AccountID != 201 {
		return &gateway.DispatchResult{StatusCode: http.StatusInternalServerError, Headers: http.Header{"Content-Type": []string{"application/json"}}, UpstreamReader: io.NopCloser(strings.NewReader(`{"error":{"type":"server_error"}}`))}, nil
	}
	return &gateway.DispatchResult{StatusCode: http.StatusOK, Headers: http.Header{"Content-Type": []string{"application/json"}}, UpstreamReader: io.NopCloser(strings.NewReader(`{"results":[{"index":0,"relevance_score":0.99}]}`))}, nil
}
