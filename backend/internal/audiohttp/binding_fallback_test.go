package audiohttp

import (
	"bytes"
	"context"
	"errors"
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

func TestAudioBindingFallbackClass(t *testing.T) {
	t.Run("normal 成功不碰目标类", func(t *testing.T) {
		env := newAudioFallbackEnv(t, false, false)
		rec := env.base.invokeJSON(t, `{"model":"tts-1","input":"fallback","voice":"alloy"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		assertAudioFallbackMoney(t, env, 1, 0, 1)
		if len(env.selector.requests) != 1 || env.selector.requests[0].PoolGroupID != 101 {
			t.Fatalf("selector requests=%+v，期望仅 normal", env.selector.requests)
		}
	})

	t.Run("normal 耗尽恰一次转移 manual 且安全重放", func(t *testing.T) {
		env := newAudioFallbackEnv(t, true, false)
		rec := env.base.invokeJSON(t, `{"model":"tts-1","input":"fallback","voice":"alloy"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		assertAudioFallbackMoney(t, env, 3, 2, 1)
		target := env.selector.requests[2]
		if target.PoolGroupID != 201 || target.BindingID != 4201 || target.MaxParallelRequests != 6 || target.SelectionMode != "priority_weighted" {
			t.Fatalf("目标 request=%+v，未使用自身元数据", target)
		}
		for i, call := range env.dispatcher.calls {
			if !bytes.Contains(call.InboundBody, []byte(`"input":"fallback"`)) {
				t.Fatalf("第 %d 次重放丢失 input：%s", i+1, call.InboundBody)
			}
		}
		if got := string(env.base.settler.settles[0].Draft.RoutingReason); !strings.Contains(got, `"to":"manual"`) {
			t.Fatalf("RoutingReason=%s，缺少 manual 转移", got)
		}
	})

	t.Run("multipart 转写体在跨类时完整重放", func(t *testing.T) {
		env := newAudioFallbackEnvForEndpoint(t, audioEndpointTranscriptions, true, false)
		file := wavPCM16Fixture(8000, 8000)
		body, contentType := multipartAudioBody(t, "file", "clip.wav", "audio/wav", file, map[string]string{"model": "whisper-1"})
		rec := env.base.invokeMultipart(t, body, contentType)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		assertAudioFallbackMoney(t, env, 3, 2, 1)
		for i, call := range env.dispatcher.calls {
			if !strings.HasPrefix(call.InboundContentType, "multipart/form-data;") || !bytes.Contains(call.InboundBody, file) {
				t.Fatalf("第 %d 次 multipart 重放不完整：content_type=%q body_bytes=%d", i+1, call.InboundContentType, len(call.InboundBody))
			}
		}
	})

	t.Run("首字节后断连零转移", func(t *testing.T) {
		env := newAudioFallbackEnv(t, false, true)
		rec := env.base.invokeJSON(t, `{"model":"tts-1","input":"fallback","voice":"alloy"}`)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "audio-part") {
			t.Fatalf("status=%d body=%q，期望已交付部分音频", rec.Code, rec.Body.String())
		}
		if len(env.selector.requests) != 1 || len(env.dispatcher.calls) != 1 {
			t.Fatalf("交付后 selector/dispatch=%d/%d，目标类不得执行", len(env.selector.requests), len(env.dispatcher.calls))
		}
		if len(env.base.settler.aborts) != 1 || len(env.base.settler.settles) != 0 {
			t.Fatalf("断流 abort/settle=%d/%d，期望 1/0", len(env.base.settler.aborts), len(env.base.settler.settles))
		}
	})
}

type audioFallbackEnv struct {
	base       *audioTestEnv
	claims     *audioFallbackClaims
	selector   *audioFallbackSelector
	dispatcher *audioFallbackDispatcher
}

func newAudioFallbackEnv(t *testing.T, normalFails, partial bool) *audioFallbackEnv {
	return newAudioFallbackEnvForEndpoint(t, audioEndpointSpeech, normalFails, partial)
}

func newAudioFallbackEnvForEndpoint(t *testing.T, endpoint audioEndpoint, normalFails, partial bool) *audioFallbackEnv {
	base := newAudioTestEnv(t, endpoint, upstreamResponse{status: http.StatusOK, body: "unused"})
	claims := &audioFallbackClaims{next: 15000}
	selector := &audioFallbackSelector{}
	dispatcher := &audioFallbackDispatcher{normalFails: normalFails, partial: partial}
	base.deps.Registry = audioFallbackRegistry{}
	base.deps.Router = router.NewDefaultRouter()
	base.deps.ClaimGate = claims
	base.deps.Selector = selector
	base.deps.CredentialVault = audioFallbackVault{}
	base.deps.Dispatcher = dispatcher
	return &audioFallbackEnv{base: base, claims: claims, selector: selector, dispatcher: dispatcher}
}

func assertAudioFallbackMoney(t *testing.T, env *audioFallbackEnv, reserves, aborts, settles int) {
	t.Helper()
	if len(env.claims.reserves) != reserves || len(env.base.settler.aborts) != aborts || len(env.base.settler.settles) != settles {
		t.Fatalf("reserves/aborts/settles=%d/%d/%d，期望 %d/%d/%d",
			len(env.claims.reserves), len(env.base.settler.aborts), len(env.base.settler.settles), reserves, aborts, settles)
	}
}

type audioFallbackRegistry struct{}

func (audioFallbackRegistry) ResolveModel(_ context.Context, model string, _ int64) (registry.Resolved, error) {
	targetMax := int32(6)
	normalModel := model
	targetModel := model
	if model == "tts-1" {
		normalModel = "tts-normal-a"
		targetModel = "tts-manual"
	}
	return registry.Resolved{
		PublicAlias: model, CanonicalModelID: "audio/" + model,
		DefaultProviderModelID: normalModel, ProviderModelID: normalModel,
		ProtocolFamily: "openai_chat", Capabilities: []string{"audio"}, PoolCandidates: []int64{101, 201},
		BindingMetadata: []registry.BindingMetadata{
			{PoolGroupID: 101, BindingID: 4101, Priority: 10, Weight: 1, SelectionMode: "strict_priority", ProviderModelIDOverride: &normalModel, FallbackClass: string(bindingfallback.ClassNormal)},
			{PoolGroupID: 201, BindingID: 4201, Priority: 20, Weight: 1, SelectionMode: "priority_weighted", MaxParallelRequests: &targetMax, ProviderModelIDOverride: &targetModel, FallbackClass: string(bindingfallback.ClassManual)},
		},
		SnapshotVersion: "registry:7:1",
	}, nil
}

type audioFallbackClaims struct {
	next     int64
	reserves []billing.ReserveRequest
}

func (g *audioFallbackClaims) Reserve(_ context.Context, req billing.ReserveRequest) (*billing.ReserveResult, error) {
	g.next++
	g.reserves = append(g.reserves, req)
	return &billing.ReserveResult{ClaimID: g.next}, nil
}

type audioFallbackSelector struct {
	requests []pool.SelectionRequest
}

func (s *audioFallbackSelector) Select(_ context.Context, req pool.SelectionRequest) (*pool.SelectionResult, error) {
	s.requests = append(s.requests, req)
	return &pool.SelectionResult{AccountID: req.PoolGroupID, AcquisitionToken: uuid.New(), RoutingReasonJSON: []byte(`{"reason":"test"}`)}, nil
}

type audioFallbackVault struct{}

func (audioFallbackVault) Resolve(_ context.Context, tenantID, accountID int64) (provider.Credential, provider.AccountInfo, error) {
	return provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-test"}, provider.AccountInfo{TenantID: tenantID, AccountID: accountID, Platform: "openai"}, nil
}

type audioFallbackDispatcher struct {
	normalFails bool
	partial     bool
	calls       []gateway.DispatchInput
}

func (d *audioFallbackDispatcher) Dispatch(_ context.Context, in gateway.DispatchInput) (*gateway.DispatchResult, error) {
	d.calls = append(d.calls, in)
	if d.normalFails && in.Account.AccountID != 201 {
		return &gateway.DispatchResult{StatusCode: http.StatusInternalServerError, Headers: http.Header{"Content-Type": []string{"application/json"}}, UpstreamReader: strings.NewReader(`{"error":{"type":"server_error"}}`)}, nil
	}
	if d.partial {
		return &gateway.DispatchResult{StatusCode: http.StatusOK, Headers: http.Header{"Content-Type": []string{"audio/mpeg"}}, UpstreamReader: &audioPartialErrReader{body: []byte("audio-part"), err: errors.New("client disconnected")}}, nil
	}
	if strings.Contains(in.EndpointPath, "transcriptions") {
		return &gateway.DispatchResult{StatusCode: http.StatusOK, Headers: http.Header{"Content-Type": []string{"application/json"}}, UpstreamReader: strings.NewReader(`{"text":"ok","duration":1}`)}, nil
	}
	return &gateway.DispatchResult{StatusCode: http.StatusOK, Headers: http.Header{"Content-Type": []string{"audio/mpeg"}}, UpstreamReader: strings.NewReader("audio-ok")}, nil
}

type audioPartialErrReader struct {
	body []byte
	err  error
	done bool
}

func (r *audioPartialErrReader) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		return copy(p, r.body), nil
	}
	if r.err == nil {
		return 0, io.EOF
	}
	return 0, r.err
}
