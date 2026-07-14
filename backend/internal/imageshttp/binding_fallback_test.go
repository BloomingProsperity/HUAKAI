package imageshttp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
)

func TestImagesBindingFallbackClass(t *testing.T) {
	t.Run("normal 成功不碰目标类", func(t *testing.T) {
		env := newImagesFallbackEnv(t, false)
		rec := env.base.invoke(t, imageFallbackBody)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		assertImagesFallbackMoney(t, env, 1, 0, 1)
		if len(env.selector.requests) != 1 || env.selector.requests[0].PoolGroupID != 101 {
			t.Fatalf("selector requests=%+v，期望仅 normal", env.selector.requests)
		}
	})

	t.Run("normal 耗尽恰一次转移 manual 且安全重放", func(t *testing.T) {
		env := newImagesFallbackEnv(t, true)
		rec := env.base.invoke(t, imageFallbackBody)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		assertImagesFallbackMoney(t, env, 3, 2, 1)
		target := env.selector.requests[2]
		if target.PoolGroupID != 201 || target.BindingID != 5201 || target.MaxParallelRequests != 5 || target.SelectionMode != "priority_weighted" {
			t.Fatalf("目标 request=%+v，未使用自身元数据", target)
		}
		for i, call := range env.dispatcher.calls {
			if !bytes.Contains(call.InboundBody, []byte(`"prompt":"fallback image"`)) {
				t.Fatalf("第 %d 次重放丢失 prompt：%s", i+1, call.InboundBody)
			}
		}
		if got := string(env.base.settler.settles[0].Draft.RoutingReason); !strings.Contains(got, `"to":"manual"`) {
			t.Fatalf("RoutingReason=%s，缺少 manual 转移", got)
		}
	})

	t.Run("multipart 编辑体在跨类时完整重放", func(t *testing.T) {
		env := newImagesFallbackEnvForEndpoint(t, imageEndpointEdits, true)
		contentType, body := buildImageEditMultipart(t, map[string]string{
			"model": "dall-e-2", "prompt": "make it blue", "n": "1", "size": "512x512",
		}, true)
		req := httptest.NewRequest(http.MethodPost, imageEndpointEdits.Path(), bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer hk-test")
		req.Header.Set("Content-Type", contentType)
		rec := httptest.NewRecorder()
		middleware.RequestID(NewEditsHandler(env.base.deps)).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		assertImagesFallbackMoney(t, env, 3, 2, 1)
		for i, call := range env.dispatcher.calls {
			if !strings.HasPrefix(call.InboundContentType, "multipart/form-data;") || !bytes.Contains(call.InboundBody, []byte("fake png bytes")) {
				t.Fatalf("第 %d 次 multipart 重放不完整：content_type=%q body_bytes=%d", i+1, call.InboundContentType, len(call.InboundBody))
			}
		}
	})

	t.Run("响应已交付后 settle 失败零转移", func(t *testing.T) {
		env := newImagesFallbackEnv(t, false)
		env.base.settler.settleErr = errors.New("settle backend unavailable")
		rec := env.base.invoke(t, imageFallbackBody)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ok.png") {
			t.Fatalf("status=%d body=%s，期望图片响应已交付", rec.Code, rec.Body.String())
		}
		if len(env.selector.requests) != 1 || len(env.dispatcher.calls) != 1 {
			t.Fatalf("交付后 selector/dispatch=%d/%d，目标类不得执行", len(env.selector.requests), len(env.dispatcher.calls))
		}
		if len(env.base.settler.settles) != 1 || len(env.base.settler.aborts) != 0 {
			t.Fatalf("交付后 settles/aborts=%d/%d，期望 1/0", len(env.base.settler.settles), len(env.base.settler.aborts))
		}
	})
}

const imageFallbackBody = `{"model":"dall-e-2","prompt":"fallback image","size":"512x512","n":1}`

type imagesFallbackEnv struct {
	base       *imagesTestEnv
	claims     *imagesFallbackClaims
	selector   *imagesFallbackSelector
	dispatcher *imagesFallbackDispatcher
}

func newImagesFallbackEnv(t *testing.T, normalFails bool) *imagesFallbackEnv {
	return newImagesFallbackEnvForEndpoint(t, imageEndpointGenerations, normalFails)
}

func newImagesFallbackEnvForEndpoint(t *testing.T, endpoint imageEndpoint, normalFails bool) *imagesFallbackEnv {
	base := newImagesTestEnv(t, endpoint, upstreamResponse{status: http.StatusOK, body: "unused"})
	claims := &imagesFallbackClaims{next: 16000}
	selector := &imagesFallbackSelector{}
	dispatcher := &imagesFallbackDispatcher{normalFails: normalFails}
	base.deps.Registry = imagesFallbackRegistry{}
	base.deps.Router = router.NewDefaultRouter()
	base.deps.ClaimGate = claims
	base.deps.Selector = selector
	base.deps.CredentialVault = imagesFallbackVault{}
	base.deps.Dispatcher = dispatcher
	return &imagesFallbackEnv{base: base, claims: claims, selector: selector, dispatcher: dispatcher}
}

func assertImagesFallbackMoney(t *testing.T, env *imagesFallbackEnv, reserves, aborts, settles int) {
	t.Helper()
	if len(env.claims.reserves) != reserves || len(env.base.settler.aborts) != aborts || len(env.base.settler.settles) != settles {
		t.Fatalf("reserves/aborts/settles=%d/%d/%d，期望 %d/%d/%d",
			len(env.claims.reserves), len(env.base.settler.aborts), len(env.base.settler.settles), reserves, aborts, settles)
	}
}

type imagesFallbackRegistry struct{}

func (imagesFallbackRegistry) ResolveModel(_ context.Context, model string, _ int64) (registry.Resolved, error) {
	targetMax := int32(5)
	return registry.Resolved{
		PublicAlias: model, CanonicalModelID: "image/" + model,
		DefaultProviderModelID: model, ProviderModelID: model,
		ProtocolFamily: "openai_chat", Capabilities: []string{"image_output"}, PoolCandidates: []int64{101, 201},
		BindingMetadata: []registry.BindingMetadata{
			{PoolGroupID: 101, BindingID: 5101, Priority: 10, Weight: 1, SelectionMode: "strict_priority", FallbackClass: string(bindingfallback.ClassNormal)},
			{PoolGroupID: 201, BindingID: 5201, Priority: 20, Weight: 1, SelectionMode: "priority_weighted", MaxParallelRequests: &targetMax, FallbackClass: string(bindingfallback.ClassManual)},
		},
		SnapshotVersion: "registry:7:1",
	}, nil
}

type imagesFallbackClaims struct {
	next     int64
	reserves []billing.ReserveRequest
}

func (g *imagesFallbackClaims) Reserve(_ context.Context, req billing.ReserveRequest) (*billing.ReserveResult, error) {
	g.next++
	g.reserves = append(g.reserves, req)
	return &billing.ReserveResult{ClaimID: g.next}, nil
}

type imagesFallbackSelector struct {
	requests []pool.SelectionRequest
}

func (s *imagesFallbackSelector) Select(_ context.Context, req pool.SelectionRequest) (*pool.SelectionResult, error) {
	s.requests = append(s.requests, req)
	return &pool.SelectionResult{AccountID: req.PoolGroupID, AcquisitionToken: uuid.New(), RoutingReasonJSON: []byte(`{"reason":"test"}`)}, nil
}

type imagesFallbackVault struct{}

func (imagesFallbackVault) Resolve(_ context.Context, tenantID, accountID int64) (provider.Credential, provider.AccountInfo, error) {
	return provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-test"}, provider.AccountInfo{TenantID: tenantID, AccountID: accountID, Platform: "openai"}, nil
}

type imagesFallbackDispatcher struct {
	normalFails bool
	calls       []gateway.DispatchInput
}

func (d *imagesFallbackDispatcher) Dispatch(_ context.Context, in gateway.DispatchInput) (*gateway.DispatchResult, error) {
	d.calls = append(d.calls, in)
	if d.normalFails && in.Account.AccountID != 201 {
		return &gateway.DispatchResult{StatusCode: http.StatusInternalServerError, Headers: http.Header{"Content-Type": []string{"application/json"}}, UpstreamReader: io.NopCloser(strings.NewReader(`{"error":{"type":"server_error"}}`))}, nil
	}
	return &gateway.DispatchResult{StatusCode: http.StatusOK, Headers: http.Header{"Content-Type": []string{"application/json"}}, UpstreamReader: io.NopCloser(strings.NewReader(`{"created":1,"data":[{"url":"https://img.test/ok.png"}]}`))}, nil
}
