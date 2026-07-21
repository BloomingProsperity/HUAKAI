package completionshttp

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

func TestCompletionsBindingFallbackClass(t *testing.T) {
	t.Run("normal 成功不碰目标类", func(t *testing.T) {
		env := newCompletionsFallbackEnv(false)
		rec := env.invoke(t, false)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		assertCompletionsFallbackCounts(t, env, 1, 0, 1)
		if env.selector.requests[0].PoolGroupID != 101 {
			t.Fatalf("首个 pool=%d，期望 normal 101", env.selector.requests[0].PoolGroupID)
		}
	})

	t.Run("normal 耗尽恰一次转移 manual", func(t *testing.T) {
		env := newCompletionsFallbackEnv(true)
		rec := env.invoke(t, false)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		assertCompletionsFallbackCounts(t, env, 3, 2, 1)
		target := env.selector.requests[2]
		if target.PoolGroupID != 201 || target.BindingID != 2001 || target.BindingRPMLimit != 72 || target.BindingTPMLimit != 720 || target.MaxParallelRequests != 9 || target.SelectionMode != "priority_weighted" || target.EstimatedInputTokens <= 0 || target.MaxOutputTokens != 8 {
			t.Fatalf("目标 request=%+v，未使用自身 binding/K/selection_mode", target)
		}
		if got := string(env.settler.settles[0].Draft.RoutingReason); !strings.Contains(got, `"to":"manual"`) {
			t.Fatalf("RoutingReason=%s，缺少 manual 转移", got)
		}
	})

	t.Run("首字节后断连零转移", func(t *testing.T) {
		env := newCompletionsFallbackEnv(false)
		env.dispatcher.streamAfterByteError = true
		rec := env.invoke(t, true)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "partial") {
			t.Fatalf("status=%d body=%q，期望已交付部分流", rec.Code, rec.Body.String())
		}
		if len(env.selector.requests) != 1 || env.selector.requests[0].PoolGroupID != 101 {
			t.Fatalf("已交付后 selector requests=%+v，目标类不得执行", env.selector.requests)
		}
		if len(env.settler.aborts) != 0 || len(env.settler.settles) != 1 {
			t.Fatalf("交付后 abort/settle=%d/%d，期望 0/1", len(env.settler.aborts), len(env.settler.settles))
		}
	})

	t.Run("count_tokens 跨类仍保持零钱账", func(t *testing.T) {
		env := newCompletionsFallbackEnv(true)
		req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", bytes.NewBufferString(`{"model":"legacy-public","messages":[{"role":"user","content":"hello"}]}`))
		req.Header.Set("Authorization", "Bearer hk-test")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		middleware.RequestID(NewCountTokensHandler(env.deps)).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		if len(env.selector.requests) != 3 || len(env.dispatcher.calls) != 3 {
			t.Fatalf("selector/dispatch=%d/%d，期望 normal 两次 + target 一次", len(env.selector.requests), len(env.dispatcher.calls))
		}
		if len(env.claims.reserves) != 0 || len(env.settler.aborts) != 0 || len(env.settler.settles) != 0 {
			t.Fatalf("免费端点触碰钱账 reserves/aborts/settles=%d/%d/%d", len(env.claims.reserves), len(env.settler.aborts), len(env.settler.settles))
		}
	})
}

type completionsFallbackEnv struct {
	deps       Deps
	claims     *fallbackClaimGate
	selector   *fallbackSelector
	dispatcher *fallbackDispatcher
	settler    *recordingSettler
}

func newCompletionsFallbackEnv(normalFails bool) *completionsFallbackEnv {
	claims := &fallbackClaimGate{next: 12000}
	selector := &fallbackSelector{}
	dispatcher := &fallbackDispatcher{normalFails: normalFails}
	settler := &recordingSettler{}
	return &completionsFallbackEnv{
		claims: claims, selector: selector, dispatcher: dispatcher, settler: settler,
		deps: Deps{
			Auth:     authStub{ident: auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 13, UserGroup: "pro"}},
			Registry: completionsFallbackRegistry{}, Router: router.NewDefaultRouter(), ClaimGate: claims,
			RateTables: rateTableStub{}, Selector: selector, CredentialVault: fallbackVault{},
			Dispatcher: dispatcher, Settler: settler,
			BillingPolicyResolver: billing.NewPolicyResolver(nil, 0), BillingPolicyVersion: "test-policy", RequestClass: "standard",
		},
	}
}

func (e *completionsFallbackEnv) invoke(t *testing.T, stream bool) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"model":"legacy-public","prompt":"fallback","max_tokens":8}`
	if stream {
		body = `{"model":"legacy-public","prompt":"fallback","max_tokens":8,"stream":true}`
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/completions", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer hk-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	middleware.RequestID(NewCompletionsHandler(e.deps)).ServeHTTP(rec, req)
	return rec
}

func assertCompletionsFallbackCounts(t *testing.T, env *completionsFallbackEnv, reserves, aborts, settles int) {
	t.Helper()
	if len(env.claims.reserves) != reserves || len(env.settler.aborts) != aborts || len(env.settler.settles) != settles {
		t.Fatalf("reserves/aborts/settles=%d/%d/%d，期望 %d/%d/%d",
			len(env.claims.reserves), len(env.settler.aborts), len(env.settler.settles), reserves, aborts, settles)
	}
}

type completionsFallbackRegistry struct{}

func (completionsFallbackRegistry) ResolveModel(context.Context, string, int64) (registry.Resolved, error) {
	normalMax := int32(2)
	targetMax := int32(9)
	targetRPM := int32(72)
	targetTPM := int32(720)
	normalModel := "normal-a"
	targetModel := "manual-target"
	return registry.Resolved{
		PublicAlias: "legacy-public", CanonicalModelID: "completion/canonical",
		DefaultProviderModelID: "normal-a", ProviderModelID: "normal-a",
		ProtocolFamily: "openai_chat", PoolCandidates: []int64{101, 201},
		BindingMetadata: []registry.BindingMetadata{
			{PoolGroupID: 101, BindingID: 1001, Priority: 10, Weight: 1, SelectionMode: "strict_priority", MaxParallelRequests: &normalMax, ProviderModelIDOverride: &normalModel, FallbackClass: string(bindingfallback.ClassNormal)},
			{PoolGroupID: 201, BindingID: 2001, Priority: 20, Weight: 1, SelectionMode: "priority_weighted", RPMLimit: &targetRPM, TPMLimit: &targetTPM, MaxParallelRequests: &targetMax, ProviderModelIDOverride: &targetModel, FallbackClass: string(bindingfallback.ClassManual)},
		},
		SnapshotVersion: "registry:7:1",
	}, nil
}

type fallbackClaimGate struct {
	next     int64
	reserves []billing.ReserveRequest
}

func (g *fallbackClaimGate) Reserve(_ context.Context, req billing.ReserveRequest) (*billing.ReserveResult, error) {
	g.next++
	g.reserves = append(g.reserves, req)
	return &billing.ReserveResult{ClaimID: g.next}, nil
}

type fallbackSelector struct {
	requests []pool.SelectionRequest
}

func (s *fallbackSelector) Select(_ context.Context, req pool.SelectionRequest) (*pool.SelectionResult, error) {
	s.requests = append(s.requests, req)
	return &pool.SelectionResult{
		AccountID: req.PoolGroupID, AcquisitionToken: uuid.New(), RoutingReasonJSON: []byte(`{"reason":"fallback-test"}`),
	}, nil
}

type fallbackVault struct{}

func (fallbackVault) Resolve(_ context.Context, tenantID, accountID int64) (provider.Credential, provider.AccountInfo, error) {
	return provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-test"}, provider.AccountInfo{
		TenantID: tenantID, AccountID: accountID, Platform: "openai", AccountType: "api_key",
	}, nil
}

type fallbackDispatcher struct {
	normalFails          bool
	streamAfterByteError bool
	calls                []gateway.DispatchInput
}

func (d *fallbackDispatcher) Dispatch(_ context.Context, in gateway.DispatchInput) (*gateway.DispatchResult, error) {
	d.calls = append(d.calls, in)
	if d.streamAfterByteError {
		return &gateway.DispatchResult{
			StatusCode: http.StatusOK, Headers: http.Header{"Content-Type": []string{"text/event-stream"}},
			UpstreamReader: &partialThenErrReader{body: []byte("data: partial\n\n"), err: io.ErrUnexpectedEOF}, Close: func() error { return nil },
		}, nil
	}
	if d.normalFails && in.Account.AccountID != 201 {
		return &gateway.DispatchResult{
			StatusCode: http.StatusInternalServerError, Headers: http.Header{"Content-Type": []string{"application/json"}},
			UpstreamReader: strings.NewReader(`{"error":{"type":"server_error"}}`), Close: func() error { return nil },
		}, nil
	}
	body := `{"id":"cmpl-ok","choices":[{"text":"ok"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`
	return &gateway.DispatchResult{
		StatusCode: http.StatusOK, Headers: http.Header{"Content-Type": []string{"application/json"}},
		UpstreamReader: strings.NewReader(body), Close: func() error { return nil },
	}, nil
}
