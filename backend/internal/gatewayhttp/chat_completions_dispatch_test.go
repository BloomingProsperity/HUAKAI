package gatewayhttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

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
	// toward pool/provider handling, producing a non-429 and no quota_denied
	// billing abort; both assertions below must turn red.
	claimGate := &recordingClaimGate{claimID: 99001}
	quotaReserver := &recordingQuotaReserver{
		err: &quota.DenyError{Decision: quota.Decision{
			Kind:   quota.DecisionDeny,
			Code:   "quota_limit_exceeded",
			Reason: "unit test deny",
		}},
	}
	settler := &stubSettler{}
	d := minimalDeps()
	d.ClaimGate = claimGate
	d.QuotaReserver = quotaReserver
	d.Settler = settler

	rec := invokeHandler(t, d, validBody())

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d body=%s; want 429 quota denial", rec.Code, rec.Body.String())
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
