package gatewayhttp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"expvar"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/affinityrules"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/cache_routing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	protoanthropic "github.com/BloomingProsperity/HUAKAI/internal/proto/anthropic"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	provideranthropic "github.com/BloomingProsperity/HUAKAI/internal/provider/anthropic"
	"github.com/BloomingProsperity/HUAKAI/internal/quota"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

type recordingClaimGate struct {
	endpointFamily string
	req            billing.ReserveRequest
	claimID        int64
}

func (g *recordingClaimGate) Reserve(_ context.Context, req billing.ReserveRequest) (*billing.ReserveResult, error) {
	g.endpointFamily = req.EndpointFamily
	g.req = req
	claimID := g.claimID
	if claimID == 0 {
		claimID = 999
	}
	return &billing.ReserveResult{ClaimID: claimID}, nil
}

type reserveClaimRaceClaimGate struct{}

func (reserveClaimRaceClaimGate) Reserve(context.Context, billing.ReserveRequest) (*billing.ReserveResult, error) {
	return nil, billing.ErrClaimRace
}

func TestHandler_NoStream(t *testing.T) {
	t.Setenv("HUAKAI_DISPATCH_HCSF", "0")
	d := clientAdapterDeps(t)
	rec := invokeHandler(t, d, `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d; want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "upstream_unknown_upstream") {
		t.Fatalf("body = %q; want normalized upstream error", rec.Body.String())
	}
}

func TestHandler_DefaultHCSFOn(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if dispatcher.calls != 1 {
		t.Fatalf("canonical dispatcher calls = %d; want 1", dispatcher.calls)
	}
}

func TestHandler_HCSFUpstreamHTTPErrorDoesNotLeakBody(t *testing.T) {
	enableHCSFDispatchForTest(t)
	const marker = "SENSITIVE_UPSTREAM_MARKER"
	dispatcher := &mockCanonicalBufferedDispatcher{
		err: &gateway.UpstreamHTTPError{
			StatusCode: http.StatusUnauthorized,
			Body:       []byte(`{"error":"invalid_grant","detail":"` + marker + `"}`),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		},
	}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), marker) {
		t.Fatalf("response leaked upstream marker: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "upstream_oauth_invalid_grant") {
		t.Fatalf("body=%s want oauth invalid grant classification code", rec.Body.String())
	}
}

func TestHandler_EnvOffHCSFOff(t *testing.T) {
	t.Setenv("HUAKAI_DISPATCH_HCSF", "0")
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusBadGateway || dispatcher.calls != 0 {
		t.Fatalf("status/calls = %d/%d; body = %s", rec.Code, dispatcher.calls, rec.Body.String())
	}
}

func TestHandler_AnthropicEndpointFamilySet(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := anthropicClientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	body := `{"model":"claude-3-5-sonnet","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`
	rec := invokeHandlerPath(t, d, "/v1/messages", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if dispatcher.observed == nil || dispatcher.observed.RequestMeta.EndpointFamily != "anthropic_messages" {
		t.Fatalf("EndpointFamily = %+v", dispatcher.observed)
	}
}

func TestHandler_AnthropicMessagesRawBufferedNoLonger501(t *testing.T) {
	t.Setenv("HUAKAI_DISPATCH_HCSF", "0")
	doer := &anthropicBufferedDoer{body: `{
			"id":"msg_raw_handler",
			"type":"message",
			"role":"assistant",
			"model":"claude-3-5-sonnet",
			"content":[{"type":"text","text":"hello from raw anthropic"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":4,"output_tokens":5}
		}`}

	d := anthropicClientAdapterDeps(t)
	adapters := provider.NewStaticRegistry()
	adapters.MustRegister("anthropic_messages", &provideranthropic.PassthroughAdapter{})
	d.Dispatcher = &gateway.UpstreamDispatcher{
		Adapters:         adapters,
		TransportFactory: transport.NewFactory(),
		HTTPClient:       doer,
	}
	d.Forwarder = &gateway.StreamForwarder{ProtocolAdapters: gateway.BuildDefaultProtocolAdapterRegistry()}

	body := `{"model":"claude-3-5-sonnet","stream":false,"max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`
	rec := invokeHandlerPath(t, d, "/v1/messages", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "buffered_anthropic_not_supported") {
		t.Fatalf("handler still returned old 501 marker: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "hello from raw anthropic") {
		t.Fatalf("body=%s want translated Anthropic response", rec.Body.String())
	}
	if doer.requestPath != "/v1/messages" {
		t.Fatalf("upstream path=%q want /v1/messages", doer.requestPath)
	}
}

func TestHandler_RawBufferedBodyOverLimitIsTypedError(t *testing.T) {
	t.Setenv("HUAKAI_DISPATCH_HCSF", "0")
	doer := &anthropicBufferedDoer{
		body: `{"id":"msg_big","type":"message","role":"assistant","model":"claude-3-5-sonnet","content":[{"type":"text","text":"` +
			strings.Repeat("x", 1<<20) +
			`"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`,
	}

	d := anthropicClientAdapterDeps(t)
	adapters := provider.NewStaticRegistry()
	adapters.MustRegister("anthropic_messages", &provideranthropic.PassthroughAdapter{})
	d.Dispatcher = &gateway.UpstreamDispatcher{
		Adapters:         adapters,
		TransportFactory: transport.NewFactory(),
		HTTPClient:       doer,
	}
	d.Forwarder = &gateway.StreamForwarder{ProtocolAdapters: gateway.BuildDefaultProtocolAdapterRegistry()}

	rec := invokeHandlerPath(t, d, "/v1/messages", `{"model":"claude-3-5-sonnet","stream":false,"max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d want 502; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "upstream_response_too_large") {
		t.Fatalf("body=%s want typed upstream_response_too_large error", rec.Body.String())
	}
}

func TestReadRawBufferedUpstreamBodyTooLargeReturnsTruncatedBody(t *testing.T) {
	raw, err := readRawBufferedUpstreamBody(strings.NewReader(strings.Repeat("x", maxRawBufferedUpstreamBodyBytes+1)))
	if !errors.Is(err, errRawBufferedUpstreamBodyTooLarge) {
		t.Fatalf("err=%v want errRawBufferedUpstreamBodyTooLarge", err)
	}
	if len(raw) != maxRawBufferedUpstreamBodyBytes {
		t.Fatalf("len(raw)=%d want %d", len(raw), maxRawBufferedUpstreamBodyBytes)
	}
}

func TestHandler_RawBufferedNon2xxBodyOverLimitUsesClassification(t *testing.T) {
	t.Setenv("HUAKAI_DISPATCH_HCSF", "0")
	const marker = "SENSITIVE_TRUNCATED_UPSTREAM_MARKER"
	doer := &anthropicBufferedDoer{
		status: http.StatusTooManyRequests,
		body:   marker + strings.Repeat("x", maxRawBufferedUpstreamBodyBytes+1),
	}

	d := anthropicClientAdapterDeps(t)
	adapters := provider.NewStaticRegistry()
	adapters.MustRegister("anthropic_messages", &provideranthropic.PassthroughAdapter{})
	d.Dispatcher = &gateway.UpstreamDispatcher{
		Adapters:         adapters,
		TransportFactory: transport.NewFactory(),
		HTTPClient:       doer,
	}
	d.Forwarder = &gateway.StreamForwarder{ProtocolAdapters: gateway.BuildDefaultProtocolAdapterRegistry()}

	rec := invokeHandlerPath(t, d, "/v1/messages", `{"model":"claude-3-5-sonnet","stream":false,"max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503 classified rate-limit response; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "upstream_response_too_large") {
		t.Fatalf("body=%s must not use terminal too-large error for non-2xx upstream response", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "upstream_rate_limited") {
		t.Fatalf("body=%s want rate-limit classification", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), marker) {
		t.Fatalf("body=%s must not leak truncated upstream marker", rec.Body.String())
	}
}

type anthropicBufferedDoer struct {
	body        string
	status      int
	requestPath string
}

func (d *anthropicBufferedDoer) Do(req *http.Request) (*http.Response, error) {
	d.requestPath = req.URL.Path
	status := d.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(d.body)),
	}, nil
}

func TestHandler_OpenAIEndpointFamilySet(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if dispatcher.observed == nil || dispatcher.observed.RequestMeta.EndpointFamily != "openai_chat" {
		t.Fatalf("EndpointFamily = %+v", dispatcher.observed)
	}
}

func TestResponsesRoute200RoundTrip(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := responsesClientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	rec := invokeResponsesHandlerPath(t, d, "/v1/responses", `{"model":"gpt-4o","stream":false,"input":"hi"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"object":"response"`) {
		t.Fatalf("body = %s; want OpenAI Responses response object", rec.Body.String())
	}
	if dispatcher.calls != 1 {
		t.Fatalf("canonical dispatcher calls = %d; want 1", dispatcher.calls)
	}
}

func TestCodexResponsesIngressRouted(t *testing.T) {
	// MUTATION: leave /backend-api/codex/responses out of
	// ClientProtocolByIngressPath; validateClientProtocol returns 404
	// unknown_route and the Responses dispatcher is never called.
	enableHCSFDispatchForTest(t)
	const codexPath = "/backend-api/codex/responses"
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := responsesClientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher

	rec := invokeResponsesHandlerPath(t, d, codexPath, `{
		"model":"gpt-4o",
		"instructions":"reply tersely",
		"input":[{"type":"message","role":"user","content":"hi"}],
		"store":false,
		"stream":false
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"object":"response"`) {
		t.Fatalf("body = %s; want OpenAI Responses response object", rec.Body.String())
	}
	if dispatcher.calls != 1 {
		t.Fatalf("canonical dispatcher calls = %d; want 1", dispatcher.calls)
	}
	if dispatcher.observed == nil {
		t.Fatal("canonical dispatcher did not observe Codex Responses request")
	}
	meta := dispatcher.observed.RequestMeta
	if string(meta.ClientProtocol) != "openai_responses" || meta.EndpointFamily != "openai_responses" {
		t.Fatalf("Codex Responses meta client/family=%q/%q; want openai_responses/openai_responses", meta.ClientProtocol, meta.EndpointFamily)
	}
	if meta.IngressPath != codexPath {
		t.Fatalf("IngressPath=%q want %q", meta.IngressPath, codexPath)
	}
}

func TestResponsesFamilySetEndpointFamily(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	dispatcher := &mockCanonicalBufferedDispatcher{}
	claimGate := &recordingClaimGate{}
	d := responsesClientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	d.ClaimGate = claimGate
	rec := invokeResponsesHandlerPath(t, d, "/v1/responses", `{"model":"gpt-4o","stream":false,"input":"hi"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if claimGate.endpointFamily != "openai_responses" {
		t.Fatalf("billing EndpointFamily=%q want openai_responses", claimGate.endpointFamily)
	}
	if dispatcher.observed == nil {
		t.Fatal("canonical dispatcher did not observe request")
	}
	if string(dispatcher.observed.RequestMeta.ClientProtocol) != "openai_responses" ||
		dispatcher.observed.RequestMeta.EndpointFamily != "openai_responses" {
		t.Fatalf("responses meta client/family=%q/%q", dispatcher.observed.RequestMeta.ClientProtocol, dispatcher.observed.RequestMeta.EndpointFamily)
	}
}

func TestCodexResponsesBilled(t *testing.T) {
	// MUTATION: bypass the normal Responses settle path for the Codex ingress;
	// the HTTP response can still be 200 but no SettleRequest is recorded.
	enableHCSFDispatchForTest(t)
	const codexPath = "/backend-api/codex/responses"
	dispatcher := &mockCanonicalBufferedDispatcher{}
	claimGate := &recordingClaimGate{claimID: 8123}
	settler := &recordingSettler{}
	d := responsesClientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	d.ClaimGate = claimGate
	d.Settler = settler

	rec := invokeResponsesHandlerPath(t, d, codexPath, `{"model":"gpt-4o","stream":false,"input":"hi"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if claimGate.endpointFamily != "openai_responses" {
		t.Fatalf("reserve EndpointFamily=%q want openai_responses", claimGate.endpointFamily)
	}
	if len(settler.calls) != 1 {
		t.Fatalf("settle calls=%d want 1", len(settler.calls))
	}
	if settler.calls[0].ClaimID != 8123 {
		t.Fatalf("settle ClaimID=%d want 8123 from reserve path", settler.calls[0].ClaimID)
	}
	if settler.calls[0].RequestedModel != "gpt-4o" {
		t.Fatalf("settle RequestedModel=%q want gpt-4o", settler.calls[0].RequestedModel)
	}
}

func TestCodexResponsesAuthRequired(t *testing.T) {
	// MUTATION: route Codex ingress around NewResponsesHandler auth; the mock
	// dispatcher would be called and the response would be 200 instead of 401.
	enableHCSFDispatchForTest(t)
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := responsesClientAdapterDeps(t)
	d.Auth = stubAuth{err: auth.ErrUnauthorized}
	d.CanonicalDispatcher = dispatcher

	rec := invokeResponsesHandlerPath(t, d, "/backend-api/codex/responses", `{"model":"gpt-4o","stream":false,"input":"hi"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401; body = %s", rec.Code, rec.Body.String())
	}
	if dispatcher.calls != 0 {
		t.Fatalf("dispatcher calls=%d want 0 when auth fails", dispatcher.calls)
	}
}

func TestResponsesPreviousResponseIDPreservesSessionHashThroughDispatch(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	dispatcher := &mockCanonicalBufferedDispatcher{}
	selector := &recordingSelectionRequestSelector{}
	d := responsesClientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	d.Selector = selector

	rec := invokeResponsesHandlerPath(t, d, "/v1/responses", `{"model":"gpt-4o","stream":false,"previous_response_id":"resp_abc","input":"hi"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if len(selector.requests) != 1 {
		t.Fatalf("selector requests = %d; want 1", len(selector.requests))
	}
	if got := selector.requests[0].SessionHash; got != "resp_abc" {
		t.Fatalf("selector SessionHash=%q want previous_response_id resp_abc", got)
	}
	if dispatcher.observed == nil {
		t.Fatal("canonical dispatcher did not observe request")
	}
	if got := dispatcher.observed.RequestMeta.SessionHash; got != "resp_abc" {
		t.Fatalf("SessionHash=%q want previous_response_id resp_abc; empty prompt hash must not overwrite sticky affinity", got)
	}
}

func TestResponsesPreviousResponseIDDoesNotReplacePromptHashAffinity(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	dispatcher := &mockCanonicalBufferedDispatcher{}
	selector := &recordingSelectionRequestSelector{}
	d := responsesClientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	d.Selector = selector

	body := `{"model":"gpt-4o","stream":false,"previous_response_id":"resp_abc","input":"hi","tools":[{"type":"function","name":"f1","description":"...","parameters":{}}]}`
	want := cache_routing.ComputePromptHash([]byte(body))
	if want == "" {
		t.Fatal("test fixture must produce a non-empty prompt hash")
	}
	rec := invokeResponsesHandlerPath(t, d, "/v1/responses", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if len(selector.requests) != 1 {
		t.Fatalf("selector requests = %d; want 1", len(selector.requests))
	}
	if got := selector.requests[0].SessionHash; got != want {
		t.Fatalf("selector SessionHash=%q want prompt hash %q", got, want)
	}
	if dispatcher.observed == nil {
		t.Fatal("canonical dispatcher did not observe request")
	}
	if got := dispatcher.observed.RequestMeta.SessionHash; got != want {
		t.Fatalf("RequestMeta.SessionHash=%q want prompt hash %q", got, want)
	}
}

func TestSessionHashHonorsExplicitClientSessionID(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	selector := &recordingSelectionRequestSelector{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.Selector = selector

	bodyA := `{"model":"gpt-4o","stream":false,"tools":[{"type":"function","function":{"name":"lookup_a","parameters":{"type":"object"}}}],"messages":[{"role":"user","content":"first prompt"}]}`
	bodyB := `{"model":"gpt-4o","stream":false,"tools":[{"type":"function","function":{"name":"lookup_b","parameters":{"type":"object"}}}],"messages":[{"role":"user","content":"second prompt"}]}`
	promptHashA := cache_routing.ComputePromptHash([]byte(bodyA))
	promptHashB := cache_routing.ComputePromptHash([]byte(bodyB))
	if promptHashA == "" || promptHashB == "" || promptHashA == promptHashB {
		t.Fatalf("test fixture must produce distinct non-empty prompt hashes: %q %q", promptHashA, promptHashB)
	}

	headers := map[string]string{"X-Session-ID": "thread-stable-1"}
	for _, body := range []string{bodyA, bodyB} {
		rec := invokeHandlerPathWithHeaders(t, d, "/v1/chat/completions", body, headers)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
		}
	}
	if len(selector.requests) != 2 {
		t.Fatalf("selector requests = %d; want 2", len(selector.requests))
	}
	gotA := selector.requests[0].SessionHash
	gotB := selector.requests[1].SessionHash
	if gotA == "" || gotB == "" {
		t.Fatalf("explicit session hash must be non-empty: %q %q", gotA, gotB)
	}
	// MUTATION: ignore the explicit id and always use the prefix hash; these
	// distinct prompt prefixes would produce different sticky session hashes.
	if gotA != gotB {
		t.Fatalf("same X-Session-ID produced different SessionHash values: %q vs %q", gotA, gotB)
	}
	if gotA == promptHashA || gotA == promptHashB {
		t.Fatalf("SessionHash=%q still used prompt hash despite explicit client session id", gotA)
	}
}

func TestSessionHashFallbackUnchanged(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	selector := &recordingSelectionRequestSelector{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.Selector = selector

	body := `{"model":"gpt-4o","stream":false,"tools":[{"type":"function","function":{"name":"fallback_lookup","parameters":{"type":"object"}}}],"messages":[{"role":"user","content":"no explicit session"}]}`
	want := cache_routing.ComputePromptHash([]byte(body))
	if want == "" {
		t.Fatal("test fixture must produce a non-empty prompt hash")
	}

	rec := invokeHandlerPathWithHeaders(t, d, "/v1/chat/completions", body, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if len(selector.requests) != 1 {
		t.Fatalf("selector requests = %d; want 1", len(selector.requests))
	}
	// MUTATION: always take the explicit-id hash path, even when the id is
	// absent; the empty-id hash would differ from the pre-change prompt hash.
	if got := selector.requests[0].SessionHash; got != want {
		t.Fatalf("selector SessionHash=%q want unchanged prompt hash %q", got, want)
	}
}

func TestAffinityRulesOverrideDefaultSessionHashWhenConfigured(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	selector := &recordingSelectionRequestSelector{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.Selector = selector
	d.AffinityRules = affinityrules.AffinityRuleSet{{
		Name:             "cache",
		ModelRegex:       []string{"^gpt-"},
		PathRegex:        []string{"^/v1/chat/completions$"},
		UserAgentInclude: []string{"affinity-client"},
		KeySources: []affinityrules.KeySource{{
			Type: affinityrules.KeySourceRequestHeader,
			Key:  "X-Affinity-Key",
		}},
		IncludeRuleName: true,
	}}
	body := `{"model":"gpt-4o","stream":false,"tools":[{"type":"function","function":{"name":"affinity_lookup","parameters":{"type":"object"}}}],"messages":[{"role":"user","content":"configured rule"}]}`
	promptHash := cache_routing.ComputePromptHash([]byte(body))
	if promptHash == "" {
		t.Fatal("test fixture must produce a non-empty prompt hash")
	}

	rec := invokeHandlerPathWithHeaders(t, d, "/v1/chat/completions", body, map[string]string{
		"User-Agent":     "huakai affinity-client",
		"X-Session-ID":   "legacy-thread",
		"X-Affinity-Key": "rule-key",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if len(selector.requests) != 1 {
		t.Fatalf("selector requests = %d; want 1", len(selector.requests))
	}
	// MUTATION: keep using the legacy requestSessionHash cascade before
	// affinity rules; this would return the client-session hash instead.
	if got := selector.requests[0].SessionHash; got != "cache:rule-key" {
		t.Fatalf("selector SessionHash=%q want cache:rule-key", got)
	}
}

func TestAffinityRulesNoMatchFallsBackToExistingSessionHash(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	selector := &recordingSelectionRequestSelector{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.Selector = selector
	d.AffinityRules = affinityrules.AffinityRuleSet{{
		Name:       "claude-only",
		ModelRegex: []string{"^claude-"},
		KeySources: []affinityrules.KeySource{{
			Type: affinityrules.KeySourceRequestHeader,
			Key:  "X-Affinity-Key",
		}},
	}}
	body := `{"model":"gpt-4o","stream":false,"tools":[{"type":"function","function":{"name":"fallback_lookup","parameters":{"type":"object"}}}],"messages":[{"role":"user","content":"rule no match"}]}`
	want := cache_routing.ComputePromptHash([]byte(body))
	if want == "" {
		t.Fatal("test fixture must produce a non-empty prompt hash")
	}

	rec := invokeHandlerPathWithHeaders(t, d, "/v1/chat/completions", body, map[string]string{
		"X-Affinity-Key": "rule-key",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if len(selector.requests) != 1 {
		t.Fatalf("selector requests = %d; want 1", len(selector.requests))
	}
	// MUTATION: treat a configured but unmatched rule set as authoritative;
	// this would drop or replace the old prompt-hash fallback.
	if got := selector.requests[0].SessionHash; got != want {
		t.Fatalf("selector SessionHash=%q want existing fallback %q", got, want)
	}
}

func TestSessionHashHeaderPriority(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")

	t.Run("x_session_id_beats_body_conversation_id", func(t *testing.T) {
		selector := &recordingSelectionRequestSelector{}
		d := clientAdapterDeps(t)
		d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
		d.Selector = selector
		body := `{"model":"gpt-4o","stream":false,"conversation_id":"body-thread","tools":[{"type":"function","function":{"name":"priority_lookup","parameters":{"type":"object"}}}],"messages":[{"role":"user","content":"priority"}]}`

		rec := invokeHandlerPathWithHeaders(t, d, "/v1/chat/completions", body, map[string]string{"X-Session-ID": "header-thread"})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
		}
		if len(selector.requests) != 1 {
			t.Fatalf("selector requests = %d; want 1", len(selector.requests))
		}
		want := expectedClientSessionHashForTest("header-thread")
		// MUTATION: prefer body conversation_id over X-Session-ID; the observed
		// sticky hash would equal the body-thread hash instead of header-thread.
		if got := selector.requests[0].SessionHash; got != want {
			t.Fatalf("selector SessionHash=%q want X-Session-ID hash %q", got, want)
		}
	})

	t.Run("invalid_control_header_falls_through_to_body_id", func(t *testing.T) {
		selector := &recordingSelectionRequestSelector{}
		d := clientAdapterDeps(t)
		d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
		d.Selector = selector
		body := `{"model":"gpt-4o","stream":false,"conversation_id":"body-thread","tools":[{"type":"function","function":{"name":"control_lookup","parameters":{"type":"object"}}}],"messages":[{"role":"user","content":"control"}]}`

		rec := invokeHandlerPathWithHeaders(t, d, "/v1/chat/completions", body, map[string]string{"X-Session-ID": "bad\x01thread"})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
		}
		if len(selector.requests) != 1 {
			t.Fatalf("selector requests = %d; want 1", len(selector.requests))
		}
		want := expectedClientSessionHashForTest("body-thread")
		// MUTATION: accept control characters in the header id; the observed
		// sticky hash would be derived from the invalid header value.
		if got := selector.requests[0].SessionHash; got != want {
			t.Fatalf("selector SessionHash=%q want body conversation_id hash %q", got, want)
		}
	})

	t.Run("metadata_user_id_claude_code_session_suffix_fills_session", func(t *testing.T) {
		selector := &recordingSelectionRequestSelector{}
		d := clientAdapterDeps(t)
		d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
		d.Selector = selector
		sessionID := "11111111-2222-3333-4444-555555555555"
		body := `{"model":"gpt-4o","stream":false,"metadata":{"user_id":"user_x__session_` + sessionID + `"},"tools":[{"type":"function","function":{"name":"metadata_lookup","parameters":{"type":"object"}}}],"messages":[{"role":"user","content":"metadata"}]}`

		rec := invokeHandlerPathWithHeaders(t, d, "/v1/chat/completions", body, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
		}
		if len(selector.requests) != 1 {
			t.Fatalf("selector requests = %d; want 1", len(selector.requests))
		}
		want := expectedClientSessionHashForTest(sessionID)
		// MUTATION: skip metadata.user_id or hash the full user id instead of
		// the Claude-Code session suffix; the session hash would fall back to
		// prompt hash or use a different client-session hash.
		if got := selector.requests[0].SessionHash; got != want {
			t.Fatalf("selector SessionHash=%q want metadata.user_id session hash %q", got, want)
		}
	})

	t.Run("x_client_request_id_is_lowest_header_priority", func(t *testing.T) {
		selector := &recordingSelectionRequestSelector{}
		d := clientAdapterDeps(t)
		d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
		d.Selector = selector
		body := `{"model":"gpt-4o","stream":false,"conversation_id":"body-thread","tools":[{"type":"function","function":{"name":"client_request_lookup","parameters":{"type":"object"}}}],"messages":[{"role":"user","content":"request id"}]}`

		rec := invokeHandlerPathWithHeaders(t, d, "/v1/chat/completions", body, map[string]string{
			"X-Client-Request-Id": "client-request-thread",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
		}
		if len(selector.requests) != 1 {
			t.Fatalf("selector requests = %d; want 1", len(selector.requests))
		}
		want := expectedClientSessionHashForTest("client-request-thread")
		if got := selector.requests[0].SessionHash; got != want {
			t.Fatalf("selector SessionHash=%q want X-Client-Request-Id hash %q", got, want)
		}

		selector.requests = nil
		rec = invokeHandlerPathWithHeaders(t, d, "/v1/chat/completions", body, map[string]string{
			"X-Session-ID":        "primary-thread",
			"X-Client-Request-Id": "client-request-thread",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
		}
		if len(selector.requests) != 1 {
			t.Fatalf("selector requests = %d; want 1", len(selector.requests))
		}
		want = expectedClientSessionHashForTest("primary-thread")
		// MUTATION: move X-Client-Request-Id ahead of existing session headers;
		// it would steal priority from X-Session-ID here.
		if got := selector.requests[0].SessionHash; got != want {
			t.Fatalf("selector SessionHash=%q want existing header priority hash %q", got, want)
		}
	})

	t.Run("too_long_body_id_falls_back_to_prompt_hash", func(t *testing.T) {
		selector := &recordingSelectionRequestSelector{}
		d := clientAdapterDeps(t)
		d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
		d.Selector = selector
		longSessionID := strings.Repeat("x", 201)
		body := `{"model":"gpt-4o","stream":false,"session_id":"` + longSessionID + `","tools":[{"type":"function","function":{"name":"long_lookup","parameters":{"type":"object"}}}],"messages":[{"role":"user","content":"long"}]}`
		want := cache_routing.ComputePromptHash([]byte(body))
		if want == "" {
			t.Fatal("test fixture must produce a non-empty prompt hash")
		}

		rec := invokeHandlerPathWithHeaders(t, d, "/v1/chat/completions", body, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
		}
		if len(selector.requests) != 1 {
			t.Fatalf("selector requests = %d; want 1", len(selector.requests))
		}
		// MUTATION: accept an over-length explicit id; the observed sticky hash
		// would be derived from session_id instead of the existing prompt hash.
		if got := selector.requests[0].SessionHash; got != want {
			t.Fatalf("selector SessionHash=%q want prompt hash fallback %q", got, want)
		}
	})
}

func TestHandler_WaitPlanReturnsQueueWait(t *testing.T) {
	settler := &stubSettler{}
	d := minimalDeps()
	d.Selector = waitPlanSelector{}
	d.Settler = settler

	rec := invokeHandler(t, d, `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d body=%s; want 429", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "3" {
		t.Fatalf("Retry-After=%q want 3", got)
	}
	if !strings.Contains(rec.Body.String(), "queue_wait") {
		t.Fatalf("body=%s; want queue_wait", rec.Body.String())
	}
	if settler.abortCalls != 1 || settler.lastAbortClaimID != 999 || settler.lastAbortReason != "queue_wait" {
		t.Fatalf("abort calls/id/reason=%d/%d/%q; want 1/999/queue_wait",
			settler.abortCalls, settler.lastAbortClaimID, settler.lastAbortReason)
	}
}

func TestHandler_ReserveClaimRaceReturns409RetryAfterWithoutAbort(t *testing.T) {
	// Mutation check: deleting the reserve-phase ErrClaimRace branch falls
	// through to the generic reserve_error path, yielding 500 without
	// Retry-After; the 409 assertion below must catch that regression.
	settler := &stubSettler{}
	d := minimalDeps()
	d.ClaimGate = reserveClaimRaceClaimGate{}
	d.Settler = settler

	rec := invokeHandler(t, d, validBody())
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s; want 409 claim_race, not reserve_error 500", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After=%q want 1", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"code":"claim_race"`) {
		t.Fatalf("body=%s; want claim_race code", body)
	}
	for _, bad := range []string{"reserve_error", "request reservation failed"} {
		if strings.Contains(body, bad) {
			t.Fatalf("body=%s leaked generic reserve error marker %q", body, bad)
		}
	}
	if settler.abortCalls != 0 {
		t.Fatalf("reserve-phase claim race must not abort a rolled-back Tx1 claim; abort calls=%d", settler.abortCalls)
	}
}

func TestHandler_QuotaDenyAbortsBillingClaimAndReturns429(t *testing.T) {
	// Mutation check: deleting the quota deny branch lets the request continue
	// through the buffered happy path, producing 200 and no quota_denied billing
	// abort; both assertions below must turn red.
	enableHCSFDispatchForTest(t)
	claimGate := &recordingClaimGate{claimID: 99001}
	quotaReserver := &recordingQuotaReserver{
		err: &quota.DenyError{Decision: quota.Decision{
			Kind:   quota.DecisionDeny,
			Code:   "quota_limit_exceeded",
			Reason: "unit test deny",
		}},
	}
	settler := &stubSettler{}
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := clientAdapterDeps(t)
	d.ClaimGate = claimGate
	d.QuotaReserver = quotaReserver
	d.Settler = settler
	d.CanonicalDispatcher = dispatcher

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d body=%s; want 429 quota denial", rec.Code, rec.Body.String())
	}
	if dispatcher.calls != 0 {
		t.Fatalf("dispatcher calls=%d want 0 because genuine quota deny must not proceed", dispatcher.calls)
	}
	body := rec.Body.String()
	for _, want := range []string{`"type":"insufficient_quota"`, `"code":"insufficient_balance"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body=%s missing %s", body, want)
		}
	}
	if settler.abortCalls != 1 || settler.lastAbortClaimID != 99001 || settler.lastAbortReason != "quota_denied" {
		t.Fatalf("abort calls/id/reason=%d/%d/%q; want 1/99001/quota_denied",
			settler.abortCalls, settler.lastAbortClaimID, settler.lastAbortReason)
	}
	if quotaReserver.calls != 1 {
		t.Fatalf("quota reserve calls=%d want 1", quotaReserver.calls)
	}
	if quotaReserver.req.TenantID != validIdentity().TenantID || quotaReserver.req.ClaimID != 99001 {
		t.Fatalf("quota reserve identity=%+v want tenant=%d claim=99001", quotaReserver.req, validIdentity().TenantID)
	}
	if !quotaReserver.req.PredictedCost.Equal(claimGate.req.PredictedCost) {
		t.Fatalf("quota predicted cost=%s want same billing predicted cost %s",
			quotaReserver.req.PredictedCost, claimGate.req.PredictedCost)
	}
	if !hasQuotaScope(quotaReserver.req.Scopes, quota.ScopeGlobal, "*") {
		t.Fatalf("quota scopes=%+v missing tenant-level global scope", quotaReserver.req.Scopes)
	}
	if !hasQuotaScope(quotaReserver.req.Scopes, quota.ScopeUser, "3") {
		t.Fatalf("quota scopes=%+v missing user scope", quotaReserver.req.Scopes)
	}
	if !hasQuotaScope(quotaReserver.req.Scopes, quota.ScopeAPIKey, "11") {
		t.Fatalf("quota scopes=%+v missing api-key scope", quotaReserver.req.Scopes)
	}
}

// TestHandler_QuotaReserveFeedsInputTokenEstimate W5:输入 token 估算必须喂进
// 配额预检的 ReservedTokens(否则 token-per-window 配额永远拿不到量、无法拦截)。
// MUTATION: 去掉 reserveQuota 的 ReservedTokens 接线 → req.ReservedTokens=0 →
// 本断言红。
func TestHandler_QuotaReserveFeedsInputTokenEstimate(t *testing.T) {
	enableHCSFDispatchForTest(t)
	claimGate := &recordingClaimGate{claimID: 99010}
	quotaReserver := &recordingQuotaReserver{} // allow
	d := clientAdapterDeps(t)
	d.ClaimGate = claimGate
	d.QuotaReserver = quotaReserver
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}

	body := `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`
	invokeHandlerPath(t, d, "/v1/chat/completions", body)

	want := int64(estimateInputTokens([]byte(body)))
	if want <= 0 {
		t.Fatalf("fixture non-discriminating: estimateInputTokens=%d must be >0", want)
	}
	if quotaReserver.req.ReservedTokens != want {
		t.Fatalf("quota ReservedTokens=%d want %d(输入 token 估算须喂进配额预检)", quotaReserver.req.ReservedTokens, want)
	}
}

// TestHandler_QuotaDenyEmitsRetryAfterAndWindowResetsAt "更强"delta:窗口配额
// 拒绝时,引擎算出的 RetryAfter 必须吐成 Retry-After 头 + body 的
// window_resets_at,让客户端按窗口边界智能退避(对齐 sub2api,强于 new-api)。
// MUTATION: 拒绝写回改回 writeInsufficientQuotaError(w)(不传 RetryAfter)→
// Retry-After 头缺失 + body 无 window_resets_at → 两断言红。
func TestHandler_QuotaDenyEmitsRetryAfterAndWindowResetsAt(t *testing.T) {
	enableHCSFDispatchForTest(t)
	claimGate := &recordingClaimGate{claimID: 99011}
	quotaReserver := &recordingQuotaReserver{
		err: &quota.DenyError{Decision: quota.Decision{
			Kind:       quota.DecisionDeny,
			Code:       "quota_limit_exceeded",
			Reason:     "token window exhausted",
			RetryAfter: 2 * time.Hour,
		}},
	}
	d := clientAdapterDeps(t)
	d.ClaimGate = claimGate
	d.QuotaReserver = quotaReserver
	d.Settler = &stubSettler{}
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "7200" {
		t.Fatalf("Retry-After=%q want 7200(2h=7200s)", got)
	}
	if !strings.Contains(rec.Body.String(), `"window_resets_at"`) {
		t.Fatalf("body=%s missing window_resets_at", rec.Body.String())
	}
}

func TestHandler_QuotaReserveInfraErrorFailsOpenAndKeepsBillingClaim(t *testing.T) {
	// Mutation check: restoring the old quota_reserve_error abort+500 branch
	// makes this return 500 and increments abortCalls, so the status and abort
	// assertions must turn red.
	enableHCSFDispatchForTest(t)
	before := quotaReserveFailedOpenCount(t)
	claimGate := &recordingClaimGate{claimID: 99004}
	quotaReserver := &recordingQuotaReserver{err: errors.New("quota store unavailable")}
	settler := &stubSettler{}
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := clientAdapterDeps(t)
	d.ClaimGate = claimGate
	d.QuotaReserver = quotaReserver
	d.Settler = settler
	d.CanonicalDispatcher = dispatcher

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s; want 200 fail-open on quota reserve infra error", rec.Code, rec.Body.String())
	}
	if settler.abortCalls != 0 {
		t.Fatalf("billing abort calls=%d reason=%q; want 0 so claim remains for money settlement",
			settler.abortCalls, settler.lastAbortReason)
	}
	if quotaReserver.calls != 1 {
		t.Fatalf("quota reserve calls=%d want 1", quotaReserver.calls)
	}
	if dispatcher.calls != 1 {
		t.Fatalf("dispatcher calls=%d want request to proceed after quota reserve infra error", dispatcher.calls)
	}
	after := quotaReserveFailedOpenCount(t)
	if after != before+1 {
		t.Fatalf("quota_reserve_failed_open_total before/after=%d/%d want +1", before, after)
	}
}

func TestHandler_QuotaAllowProceedsToPoolSelection(t *testing.T) {
	claimGate := &recordingClaimGate{claimID: 99002}
	quotaReserver := &recordingQuotaReserver{result: quota.ReserveResult{Allowed: true}}
	selector := &recordingSelectionRequestSelector{}
	d := minimalDeps()
	d.ClaimGate = claimGate
	d.QuotaReserver = quotaReserver
	d.Selector = selector

	rec := invokeHandler(t, d, validBody())

	if rec.Code == http.StatusTooManyRequests && strings.Contains(rec.Body.String(), "insufficient_quota") {
		t.Fatalf("status=%d body=%s; allowed quota must not render quota denial", rec.Code, rec.Body.String())
	}
	if quotaReserver.calls != 1 {
		t.Fatalf("quota reserve calls=%d want 1", quotaReserver.calls)
	}
	if selector.calls != 1 {
		t.Fatalf("selector calls=%d want request to proceed after quota allow", selector.calls)
	}
	if selector.requests[0].ClaimID != 99002 {
		t.Fatalf("selector ClaimID=%d want billing claim 99002", selector.requests[0].ClaimID)
	}
}

func TestHandler_IdempotencyReplaySkipsQuotaReserve(t *testing.T) {
	quotaReserver := &recordingQuotaReserver{result: quota.ReserveResult{Allowed: true}}
	d := minimalDeps()
	d.ClaimGate = replayClaimGate{claimID: 99003, hit: true}
	d.QuotaReserver = quotaReserver

	rec := invokeHandler(t, d, validBody())

	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "replay_without_cache") {
		t.Fatalf("status/body=%d/%s want replay_without_cache from idempotency path", rec.Code, rec.Body.String())
	}
	if quotaReserver.calls != 0 {
		t.Fatalf("quota reserve calls=%d want 0 for idempotency replay", quotaReserver.calls)
	}
}

func TestHandler_AttemptLoopPassesAttemptSeqAndEmptyExclusionsOnFirstSuccess(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	dispatcher := &mockCanonicalBufferedDispatcher{}
	selector := &recordingSelectionRequestSelector{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	d.Selector = selector
	d.Router = stubRouter{plan: router.RoutePlan{
		Attempts: []router.AttemptPlan{
			{Index: 0, PoolGroupID: 42, UpstreamModelID: "gpt-4o", Reason: "primary"},
			{Index: 1, PoolGroupID: 43, UpstreamModelID: "gpt-4o-backup", Reason: "cross_pool_fallback"},
		},
		AttemptBudget:   2,
		SnapshotVersion: "registry:7:1;router:multi",
	}}

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s; want 200", rec.Code, rec.Body.String())
	}
	if selector.calls != 1 {
		t.Fatalf("selector calls=%d want 1 because first attempt succeeds", selector.calls)
	}
	req := selector.requests[0]
	if req.PoolGroupID != 42 {
		t.Fatalf("PoolGroupID=%d want first planned pool 42", req.PoolGroupID)
	}
	if req.AttemptSeq != 1 {
		t.Fatalf("AttemptSeq=%d want 1", req.AttemptSeq)
	}
	if req.ExcludedAccounts == nil {
		t.Fatal("ExcludedAccounts must be a non-nil empty map in PR3")
	}
	if len(req.ExcludedAccounts) != 0 {
		t.Fatalf("ExcludedAccounts=%v want empty map in PR3", req.ExcludedAccounts)
	}
}

func TestRouterResolvedModelFromRegistryMapsPerPoolModelOverrides(t *testing.T) {
	poolAOverride := "pool-a-upstream"
	poolBOverride := "pool-b-upstream"
	resolved := registry.Resolved{
		PublicAlias:            "gpt-4o",
		CanonicalModelID:       "openai/gpt-4o",
		DefaultProviderModelID: "default-upstream",
		ProviderModelID:        "pool-a-upstream",
		ContextWindow:          128000,
		Capabilities:           []string{"stream"},
		PricingClass:           "standard",
		ProtocolFamily:         "openai_chat",
		PoolCandidates:         []int64{701, 702, 703},
		BindingMetadata: []registry.BindingMetadata{
			{BindingID: 1, PoolGroupID: 701, Priority: 10, Weight: 5, SelectionMode: "strict_priority", FallbackClass: "normal", ProviderModelIDOverride: &poolAOverride},
			{BindingID: 2, PoolGroupID: 702, Priority: 20, Weight: 3, SelectionMode: "strict_priority", FallbackClass: "quota", ProviderModelIDOverride: &poolBOverride},
			{BindingID: 3, PoolGroupID: 703, Priority: 30, Weight: 1, SelectionMode: "strict_priority", FallbackClass: "manual"},
		},
		SnapshotVersion: "registry:7:3",
	}

	got := routerResolvedModelFromRegistry(resolved)

	if got.ProviderModelID != "pool-a-upstream" {
		t.Fatalf("ProviderModelID=%q want primary override", got.ProviderModelID)
	}
	if len(got.PoolMetadata) != 3 {
		t.Fatalf("PoolMetadata len=%d want 3", len(got.PoolMetadata))
	}
	want := []router.PoolCandidateMeta{
		{PoolGroupID: 701, ProviderModelID: "pool-a-upstream"},
		{PoolGroupID: 702, ProviderModelID: "pool-b-upstream"},
		{PoolGroupID: 703, ProviderModelID: "default-upstream"},
	}
	for i := range want {
		if got.PoolMetadata[i] != want[i] {
			t.Fatalf("PoolMetadata[%d]=%+v want %+v", i, got.PoolMetadata[i], want[i])
		}
	}
}

func quotaReserveFailedOpenCount(t *testing.T) int64 {
	t.Helper()
	v := expvar.Get("quota_reserve_failed_open_total")
	if v == nil {
		return 0
	}
	iv, ok := v.(*expvar.Int)
	if !ok {
		t.Fatalf("quota_reserve_failed_open_total is %T want *expvar.Int", v)
	}
	return iv.Value()
}

type recordingQuotaReserver struct {
	calls  int
	req    quota.ReserveRequest
	result quota.ReserveResult
	err    error
}

func (r *recordingQuotaReserver) Reserve(_ context.Context, req quota.ReserveRequest) (quota.ReserveResult, error) {
	r.calls++
	r.req = req
	if r.err != nil {
		return r.result, r.err
	}
	if !r.result.Allowed {
		r.result.Allowed = true
	}
	return r.result, nil
}

func hasQuotaScope(scopes []quota.Scope, kind quota.ScopeKind, id string) bool {
	for _, scope := range scopes {
		if scope.Kind == kind && scope.ID == id {
			return true
		}
	}
	return false
}

type waitPlanSelector struct{}

func (waitPlanSelector) Select(context.Context, pool.SelectionRequest) (*pool.SelectionResult, error) {
	return &pool.SelectionResult{WaitPlan: &pool.WaitPlan{
		AccountID:      1,
		MaxConcurrency: 2,
		TimeoutMS:      2500,
		MaxWaiting:     8,
	}}, nil
}

type recordingSelectionRequestSelector struct {
	calls    int
	requests []pool.SelectionRequest
}

func (s *recordingSelectionRequestSelector) Select(_ context.Context, req pool.SelectionRequest) (*pool.SelectionResult, error) {
	s.calls++
	s.requests = append(s.requests, req)
	return &pool.SelectionResult{AccountID: 1}, nil
}

func invokeHandlerPathWithHeaders(t *testing.T, deps ChatHandlerDeps, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewChatCompletionsHandler(deps)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func expectedClientSessionHashForTest(id string) string {
	sum := sha256.Sum256([]byte("huakai:client-session:v1:" + id))
	return "client-session:" + hex.EncodeToString(sum[:])
}

// TestSelectPoolAccount_ThreadsUserGroupFromIdentity 守 R-SUB-WIRE-1 接线: 选号时
// SelectionRequest.UserGroup 必须从 auth.Identity.UserGroup 透传, 否则订阅分组路由 gate
// 永远收到空档 → 恒放行 → 限档失效。
// mutation: 删 selectPoolAccount 里 `UserGroup: ex.ident.UserGroup` → 透传为空串 → 红。
func TestSelectPoolAccount_ThreadsUserGroupFromIdentity(t *testing.T) {
	selector := &recordingSelectionRequestSelector{}
	ex := &chatExecution{
		ctx:        context.Background(),
		ident:      auth.Identity{TenantID: 7, UserID: 3, APIKeyID: 9, UserGroup: "premium"},
		d:          ChatHandlerDeps{Selector: selector},
		body:       []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`),
		req:        chatRequest{Model: "gpt-4o"},
		attempt:    router.AttemptPlan{PoolGroupID: 42},
		reserveRes: &billing.ReserveResult{},
		resolved:   registry.Resolved{ProtocolFamily: "openai_chat"},
	}

	if f := ex.selectPoolAccount(httptest.NewRecorder(), attemptInput{AttemptSeq: 1}); f != nil {
		t.Fatalf("selectPoolAccount returned failure: %+v", f)
	}
	if len(selector.requests) != 1 {
		t.Fatalf("selector calls=%d want 1", len(selector.requests))
	}
	if got := selector.requests[0].UserGroup; got != "premium" {
		t.Fatalf("SelectionRequest.UserGroup=%q want premium (must thread from auth.Identity.UserGroup)", got)
	}
}

func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()
	old, ok := os.LookupEnv(key)
	_ = os.Unsetenv(key)
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func anthropicClientAdapterDeps(t *testing.T) ChatHandlerDeps {
	t.Helper()
	d := minimalDeps()
	d.Registry = stubRegistry{resolved: registry.Resolved{
		PublicAlias:      "claude-3-5-sonnet",
		CanonicalModelID: "anthropic/claude-3-5-sonnet",
		ProviderModelID:  "claude-3-5-sonnet",
		ProtocolFamily:   "anthropic_messages",
		PoolCandidates:   []int64{42},
	}}
	vault := provider.NewStaticVault()
	if err := vault.Set(1, provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-ant-test"}, provider.AccountInfo{AccountID: 1, Platform: "anthropic", AccountType: "apikey", AccountCredentialID: 9002, CredentialVersion: 1}); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	d.CredentialVault = vault
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	protoReg := gateway.NewStaticProtocolAdapterRegistry()
	protoReg.MustRegister("anthropic_messages", &protoanthropic.Adapter{})
	d.Forwarder = &gateway.StreamForwarder{
		ProtocolAdapters: protoReg,
	}
	return d
}

func responsesClientAdapterDeps(t *testing.T) ChatHandlerDeps {
	t.Helper()
	d := clientAdapterDeps(t)
	d.Registry = stubRegistry{resolved: registry.Resolved{
		PublicAlias:      "gpt-4o",
		CanonicalModelID: "openai/gpt-4o",
		ProviderModelID:  "gpt-4o",
		ProtocolFamily:   "openai_responses",
		PoolCandidates:   []int64{42},
	}}
	return d
}

// recordingPlanInputRouter 记录最后一次 Plan 收到的 PlanInput，便于断言 :313
// 把 body-derived caps 接进了 RequestFeatures，而不是只接了 Stream。
type recordingPlanInputRouter struct {
	delegate router.Router
	last     router.PlanInput
}

func (r *recordingPlanInputRouter) Plan(ctx context.Context, in router.PlanInput) (router.RoutePlan, error) {
	r.last = in
	return r.delegate.Plan(ctx, in)
}

func capSet(caps []string) map[string]bool {
	m := make(map[string]bool, len(caps))
	for _, c := range caps {
		m[c] = true
	}
	return m
}

// TestPrepareRoute_ThreadsBodyDerivedCapabilities 是自证接线测试 (ROUTE-024 核心契约):
// 一个含 image part + tools + json_schema 的 streamed body 经 prepareRoute 后,
// 真 Router 产出的 AttemptPlan.RequiredCapabilities 必须 == {stream,vision,tools,json};
// baseline arm = 纯 text 非流 body,同代码路径只应得到 {} (没有 :313 接线两臂不会有差别)。
// 还断言 PlanInput.Features 三个 Wants* 位被 body 真正驱动。
// mutation: 还原 :313 为 `Features: router.RequestFeatures{Stream: ex.req.Stream}` ->
// rich arm 的 vision/tools/json 全丢 -> 转红。
func TestPrepareRoute_ThreadsBodyDerivedCapabilities(t *testing.T) {
	richBody := `{"model":"claude-3-5-sonnet","stream":true,` +
		`"messages":[{"role":"user","content":[` +
		`{"type":"text","text":"look"},` +
		`{"type":"image_url","image_url":{"url":"https://example.com/cat.png"}}]}],` +
		`"tools":[{"type":"function","function":{"name":"f"}}],` +
		`"response_format":{"type":"json_schema","json_schema":{"name":"x","schema":{}}}}`
	baselineBody := `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"plain text"}]}`

	run := func(t *testing.T, body string, stream bool) (router.PlanInput, []string) {
		t.Helper()
		rec := &recordingPlanInputRouter{delegate: router.NewDefaultRouter()}
		ex := &chatExecution{
			ctx:       context.Background(),
			ident:     auth.Identity{TenantID: 7, UserID: 3, APIKeyID: 9},
			d:         ChatHandlerDeps{Router: rec},
			body:      []byte(body),
			req:       chatRequest{Model: "claude-3-5-sonnet", Stream: stream},
			requestID: "r-route024",
		}
		ex.d.Registry = stubRegistry{resolved: registry.Resolved{
			PublicAlias:      "claude-3-5-sonnet",
			CanonicalModelID: "anthropic/claude-3-5-sonnet",
			ProviderModelID:  "claude-3-5-sonnet",
			ProtocolFamily:   "anthropic_messages",
			PoolCandidates:   []int64{42},
		}}
		if ok := ex.prepareRoute(httptest.NewRecorder()); !ok {
			t.Fatalf("prepareRoute returned false")
		}
		return rec.last, ex.attempt.RequiredCapabilities
	}

	richPlan, richCaps := run(t, richBody, true)
	if !richPlan.Features.WantsVision || !richPlan.Features.WantsToolUse || !richPlan.Features.WantsJSON {
		t.Fatalf("PlanInput.Features not driven by body: %+v", richPlan.Features)
	}
	if !richPlan.Features.Stream {
		t.Fatalf("PlanInput.Features.Stream regressed; want true for streamed request")
	}
	gotRich := capSet(richCaps)
	for _, want := range []string{"stream", "vision", "tools", "json"} {
		if !gotRich[want] {
			t.Fatalf("rich arm RequiredCapabilities missing %q; got %v", want, richCaps)
		}
	}
	if len(richCaps) != 4 {
		t.Fatalf("rich arm should carry exactly {stream,vision,tools,json}; got %v", richCaps)
	}

	basePlan, baseCaps := run(t, baselineBody, false)
	if basePlan.Features.WantsVision || basePlan.Features.WantsToolUse || basePlan.Features.WantsJSON || basePlan.Features.Stream {
		t.Fatalf("baseline arm should derive no capabilities; got %+v", basePlan.Features)
	}
	if len(baseCaps) != 0 {
		t.Fatalf("baseline arm RequiredCapabilities should be empty; got %v", baseCaps)
	}

	// Self-proving: the two arms MUST differ. Without the :313 wiring both
	// would collapse to {stream}/{} and this guard goes red.
	if len(richCaps) == len(baseCaps) {
		t.Fatalf("rich and baseline arms must differ; rich=%v baseline=%v", richCaps, baseCaps)
	}
}

// TestPrepareRoute_StreamOnlyWhenBodyHasNoCaps 守 stream 不被回归:
// 一个 streamed 但无 vision/tools/json 的 body 只应得到 {stream}。
// mutation: :313 误删 Stream 字段 -> 转红。
func TestPrepareRoute_StreamOnlyWhenBodyHasNoCaps(t *testing.T) {
	rec := &recordingPlanInputRouter{delegate: router.NewDefaultRouter()}
	ex := &chatExecution{
		ctx:       context.Background(),
		ident:     auth.Identity{TenantID: 7, UserID: 3, APIKeyID: 9},
		d:         ChatHandlerDeps{Router: rec},
		body:      []byte(`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`),
		req:       chatRequest{Model: "gpt-4o", Stream: true},
		requestID: "r-route024-stream",
	}
	ex.d.Registry = stubRegistry{resolved: registry.Resolved{
		PublicAlias:      "gpt-4o",
		CanonicalModelID: "openai/gpt-4o",
		ProviderModelID:  "gpt-4o",
		ProtocolFamily:   "openai_chat",
		PoolCandidates:   []int64{42},
	}}
	if ok := ex.prepareRoute(httptest.NewRecorder()); !ok {
		t.Fatalf("prepareRoute returned false")
	}
	caps := ex.attempt.RequiredCapabilities
	if len(caps) != 1 || caps[0] != "stream" {
		t.Fatalf("stream-only body should yield exactly [stream]; got %v", caps)
	}
}
