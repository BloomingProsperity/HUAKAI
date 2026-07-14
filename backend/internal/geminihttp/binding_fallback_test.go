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

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	providergemini "github.com/BloomingProsperity/HUAKAI/internal/provider/gemini"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
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
		if target.PoolGroupID != 201 || target.BindingID != 6201 || target.MaxParallelRequests != 4 || target.SelectionMode != "priority_weighted" {
			t.Fatalf("目标 request=%+v，未使用自身元数据", target)
		}
		if env.transport.keys[2] != "key-201" {
			t.Fatalf("目标凭据=%q，期望 key-201", env.transport.keys[2])
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
	normalModel := "gemini-normal-a"
	targetModel := "gemini-manual"
	return registry.Resolved{
		PublicAlias: "gemini-pro", CanonicalModelID: "gemini/canonical",
		DefaultProviderModelID: "gemini-normal-a", ProviderModelID: "gemini-normal-a",
		ProtocolFamily: "gemini_messages", PoolCandidates: []int64{101, 201},
		BindingMetadata: []registry.BindingMetadata{
			{PoolGroupID: 101, BindingID: 6101, Priority: 10, Weight: 1, SelectionMode: "strict_priority", ProviderModelIDOverride: &normalModel, FallbackClass: string(bindingfallback.ClassNormal)},
			{PoolGroupID: 201, BindingID: 6201, Priority: 20, Weight: 1, SelectionMode: "priority_weighted", MaxParallelRequests: &targetMax, ProviderModelIDOverride: &targetModel, FallbackClass: string(bindingfallback.ClassManual)},
		},
		SnapshotVersion: "registry:7:1",
	}, nil
}

type geminiFallbackSelector struct {
	requests []pool.SelectionRequest
	releases int
}

func (s *geminiFallbackSelector) Select(_ context.Context, req pool.SelectionRequest) (*pool.SelectionResult, error) {
	s.requests = append(s.requests, req)
	return &pool.SelectionResult{
		AccountID: req.PoolGroupID, AcquisitionToken: uuid.New(), RoutingReasonJSON: []byte(`{"reason":"test"}`),
		Release: func(context.Context) error { s.releases++; return nil },
	}, nil
}

type geminiFallbackVault struct{}

func (geminiFallbackVault) Resolve(_ context.Context, tenantID, accountID int64) (provider.Credential, provider.AccountInfo, error) {
	return provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "key-" + strconv.FormatInt(accountID, 10)}, provider.AccountInfo{TenantID: tenantID, AccountID: accountID, Platform: "gemini"}, nil
}

type geminiFallbackTransport struct {
	normalFails bool
	keys        []string
}

func (rt *geminiFallbackTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	key := req.Header.Get("X-Goog-Api-Key")
	rt.keys = append(rt.keys, key)
	status := http.StatusOK
	body := `{"totalTokens":7}`
	if rt.normalFails && key != "key-201" {
		status = http.StatusInternalServerError
		body = `{"error":{"type":"server_error"}}`
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}
