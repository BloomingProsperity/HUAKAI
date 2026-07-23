package embeddingshttp

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

func TestEmbeddingsBindingFallbackClass(t *testing.T) {
	t.Run("normal 成功不碰目标类", func(t *testing.T) {
		env := newEmbeddingsFallbackEnv(false, false)
		rec := env.invoke(t)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		assertEmbeddingsFallbackMoney(t, env, 1, 0, 1)
		if len(env.selector.requests) != 1 || env.selector.requests[0].PoolGroupID != 101 {
			t.Fatalf("selector requests=%+v，期望仅 normal", env.selector.requests)
		}
	})

	t.Run("normal 耗尽恰一次转移 manual", func(t *testing.T) {
		env := newEmbeddingsFallbackEnv(true, false)
		rec := env.invoke(t)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		assertEmbeddingsFallbackMoney(t, env, 3, 2, 1)
		if len(env.selector.requests) != 3 {
			t.Fatalf("selector 次数=%d，期望 3", len(env.selector.requests))
		}
		target := env.selector.requests[2]
		if target.PoolGroupID != 201 || target.BindingID != 2201 || target.BindingRPMLimit != 71 || target.BindingTPMLimit != 710 || target.MaxParallelRequests != 7 || target.SelectionMode != "priority_weighted" || target.EstimatedInputTokens <= 0 {
			t.Fatalf("目标 request=%+v，未使用自身元数据", target)
		}
		if got := string(env.settler.settles[0].Draft.RoutingReason); !strings.Contains(got, `"to":"manual"`) {
			t.Fatalf("RoutingReason=%s，缺少 manual 转移", got)
		}
	})

	t.Run("本地 key 限额终态零转移", func(t *testing.T) {
		env := newEmbeddingsFallbackEnv(false, true)
		rec := env.invoke(t)
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("status=%d body=%s，期望 429", rec.Code, rec.Body.String())
		}
		assertEmbeddingsFallbackMoney(t, env, 1, 1, 0)
		if len(env.selector.requests) != 1 || len(env.dispatcher.calls) != 0 {
			t.Fatalf("终态 selector/dispatch=%d/%d，目标类不得执行", len(env.selector.requests), len(env.dispatcher.calls))
		}
	})
}

type embeddingsFallbackEnv struct {
	deps       Deps
	claims     *embeddingsFallbackClaims
	selector   *embeddingsFallbackSelector
	dispatcher *embeddingsFallbackDispatcher
	settler    *recordingSettler
}

func newEmbeddingsFallbackEnv(normalFails, terminal bool) *embeddingsFallbackEnv {
	claims := &embeddingsFallbackClaims{next: 13000}
	selector := &embeddingsFallbackSelector{terminal: terminal}
	dispatcher := &embeddingsFallbackDispatcher{normalFails: normalFails}
	settler := &recordingSettler{}
	return &embeddingsFallbackEnv{
		claims: claims, selector: selector, dispatcher: dispatcher, settler: settler,
		deps: Deps{
			Auth:     authStub{ident: auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 13, UserGroup: "pro"}},
			Registry: embeddingsFallbackRegistry{}, Router: router.NewDefaultRouter(), ClaimGate: claims,
			RateTables: rateTableStub{}, Selector: selector, CredentialVault: embeddingsFallbackVault{},
			Dispatcher: dispatcher, Settler: settler,
			BillingPolicyResolver: billing.NewPolicyResolver(nil, 0), BillingPolicyVersion: "test-policy", RequestClass: "standard",
		},
	}
}

func (e *embeddingsFallbackEnv) invoke(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", bytes.NewBufferString(`{"model":"embed-public","input":"fallback"}`))
	req.Header.Set("Authorization", "Bearer hk-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	middleware.RequestID(NewEmbeddingsHandler(e.deps)).ServeHTTP(rec, req)
	return rec
}

func assertEmbeddingsFallbackMoney(t *testing.T, env *embeddingsFallbackEnv, reserves, aborts, settles int) {
	t.Helper()
	if len(env.claims.reserves) != reserves || len(env.settler.aborts) != aborts || len(env.settler.settles) != settles {
		t.Fatalf("reserves/aborts/settles=%d/%d/%d，期望 %d/%d/%d",
			len(env.claims.reserves), len(env.settler.aborts), len(env.settler.settles), reserves, aborts, settles)
	}
}

type embeddingsFallbackRegistry struct{}

func (embeddingsFallbackRegistry) ResolveModel(context.Context, string, int64) (registry.Resolved, error) {
	targetMax := int32(7)
	targetRPM := int32(71)
	targetTPM := int32(710)
	return registry.Resolved{
		PublicAlias: "embed-public", CanonicalModelID: "embedding/canonical",
		DefaultProviderModelID: "text-embedding-3-small", ProviderModelID: "text-embedding-3-small",
		ProtocolFamily: "openai_chat", Capabilities: []string{"embeddings"}, PoolCandidates: []int64{101, 201},
		BindingMetadata: []registry.BindingMetadata{
			{PoolGroupID: 101, BindingID: 1101, Priority: 10, Weight: 1, SelectionMode: "strict_priority", FallbackClass: string(bindingfallback.ClassNormal)},
			{PoolGroupID: 201, BindingID: 2201, Priority: 20, Weight: 1, SelectionMode: "priority_weighted", RPMLimit: &targetRPM, TPMLimit: &targetTPM, MaxParallelRequests: &targetMax, FallbackClass: string(bindingfallback.ClassManual)},
		},
		SnapshotVersion: "registry:7:1",
	}, nil
}

type embeddingsFallbackClaims struct {
	next     int64
	reserves []billing.ReserveRequest
}

func (g *embeddingsFallbackClaims) Reserve(_ context.Context, req billing.ReserveRequest) (*billing.ReserveResult, error) {
	g.next++
	g.reserves = append(g.reserves, req)
	return &billing.ReserveResult{ClaimID: g.next}, nil
}

type embeddingsFallbackSelector struct {
	terminal bool
	requests []pool.SelectionRequest
}

func (s *embeddingsFallbackSelector) Select(_ context.Context, req pool.SelectionRequest) (*pool.SelectionResult, error) {
	s.requests = append(s.requests, req)
	if s.terminal {
		return nil, pool.ErrKeyRateLimited
	}
	return &pool.SelectionResult{AccountID: req.PoolGroupID, AcquisitionToken: uuid.New(), RoutingReasonJSON: []byte(`{"reason":"test"}`)}, nil
}

type embeddingsFallbackVault struct{}

func (embeddingsFallbackVault) Resolve(_ context.Context, tenantID, accountID int64) (provider.Credential, provider.AccountInfo, error) {
	return provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-test"}, provider.AccountInfo{TenantID: tenantID, AccountID: accountID, Platform: "openai", AccountType: "api_key"}, nil
}

type embeddingsFallbackDispatcher struct {
	normalFails bool
	calls       []gateway.DispatchInput
}

func (d *embeddingsFallbackDispatcher) Dispatch(_ context.Context, in gateway.DispatchInput) (*gateway.DispatchResult, error) {
	d.calls = append(d.calls, in)
	if d.normalFails && in.Account.AccountID != 201 {
		return &gateway.DispatchResult{StatusCode: http.StatusInternalServerError, Headers: http.Header{"Content-Type": []string{"application/json"}}, UpstreamReader: io.NopCloser(strings.NewReader(`{"error":{"type":"server_error"}}`))}, nil
	}
	body := `{"object":"list","data":[{"embedding":[0.1],"index":0}],"usage":{"prompt_tokens":3,"total_tokens":3}}`
	return &gateway.DispatchResult{StatusCode: http.StatusOK, Headers: http.Header{"Content-Type": []string{"application/json"}}, UpstreamReader: io.NopCloser(strings.NewReader(body))}, nil
}
