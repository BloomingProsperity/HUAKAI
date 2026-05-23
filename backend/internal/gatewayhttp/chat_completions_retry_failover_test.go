package gatewayhttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	l2cache "github.com/BloomingProsperity/HUAKAI/internal/cache"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	provideropenai "github.com/BloomingProsperity/HUAKAI/internal/provider/openai"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

func TestPR5NonStream500RetriesSecondAccountAndSettlesOnce(t *testing.T) {
	enableHCSFDispatchForTest(t)
	selector := newPR5Selector(t, 101, 102)
	claimGate := &pr5ClaimGate{claimID: 88001}
	settler := &recordingSettler{}
	dispatcher := &pr5CanonicalSequenceDispatcher{
		steps: []pr5CanonicalStep{
			{status: http.StatusInternalServerError, body: `{"error":"upstream busy"}`},
			{successText: "success after failover"},
		},
	}
	deps := pr5NonStreamDeps(t, selector, claimGate, settler, dispatcher)

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s; want 200 after second account success", rec.Code, rec.Body.String())
	}
	if selector.calls != 2 {
		t.Fatalf("selector calls=%d want 2", selector.calls)
	}
	if got := []int{selector.requests[0].AttemptSeq, selector.requests[1].AttemptSeq}; got[0] != 1 || got[1] != 2 {
		t.Fatalf("selector attempt seq=%v want [1 2]", got)
	}
	if _, excluded := selector.requests[1].ExcludedAccounts[101]; !excluded {
		t.Fatalf("second attempt exclusions=%v want failed account 101 excluded", selector.requests[1].ExcludedAccounts)
	}
	if len(claimGate.requests) != 2 {
		t.Fatalf("reserve calls=%d want 2 (initial reserve + re-reserve)", len(claimGate.requests))
	}
	if len(settler.aborts) != 1 || settler.aborts[0].reason != "upstream_5xx" {
		t.Fatalf("aborts=%+v want one upstream_5xx abort", settler.aborts)
	}
	if len(settler.calls) != 1 {
		t.Fatalf("positive settle calls=%d want 1", len(settler.calls))
	}
	if settler.calls[0].AccountID != 102 || settler.calls[0].AttemptSeq != 2 {
		t.Fatalf("settle account/seq=%d/%d want 102/2", settler.calls[0].AccountID, settler.calls[0].AttemptSeq)
	}
	if dispatcher.calls != 2 || dispatcher.accounts[0] != 101 || dispatcher.accounts[1] != 102 {
		t.Fatalf("dispatcher calls/accounts=%d/%v want 2/[101 102]", dispatcher.calls, dispatcher.accounts)
	}
}

func TestPR5AbortFailureStopsRetryBeforeReReserve(t *testing.T) {
	enableHCSFDispatchForTest(t)
	selector := newPR5Selector(t, 111, 112)
	settler := &failingAbortSettler{err: errors.New("abort unavailable")}
	dispatcher := &pr5CanonicalSequenceDispatcher{
		steps: []pr5CanonicalStep{
			{status: http.StatusInternalServerError, body: `{"error":"upstream busy"}`},
			{successText: "must not retry while claim may still be reserving"},
		},
	}
	deps := pr5NonStreamDeps(t, selector, &pr5ClaimGate{claimID: 88101}, settler, dispatcher)

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s; want terminal upstream failure when Abort fails", rec.Code, rec.Body.String())
	}
	if selector.calls != 1 || dispatcher.calls != 1 {
		t.Fatalf("selector/dispatcher calls=%d/%d want 1/1; Abort failure must not retry", selector.calls, dispatcher.calls)
	}
	if len(settler.aborts) != 1 {
		t.Fatalf("abort calls=%d want 1", len(settler.aborts))
	}
}

func TestPR5NonStream429RecordsCooldownAndRetriesNextAccount(t *testing.T) {
	enableHCSFDispatchForTest(t)
	selector := newPR5Selector(t, 201, 202)
	health := &recordingChannelHealth{}
	dispatcher := &pr5CanonicalSequenceDispatcher{
		steps: []pr5CanonicalStep{
			{status: http.StatusTooManyRequests, body: `{"error":"rate limited"}`, headers: http.Header{"Retry-After": []string{"7"}}},
			{successText: "success after rate limit"},
		},
	}
	deps := pr5NonStreamDeps(t, selector, &pr5ClaimGate{claimID: 88002}, &recordingSettler{}, dispatcher)
	deps.ChannelHealth = health

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s; want 200 after 429 failover", rec.Code, rec.Body.String())
	}
	if selector.calls != 2 {
		t.Fatalf("selector calls=%d want 2", selector.calls)
	}
	if len(health.signals) < 1 {
		t.Fatalf("health signals=%+v want rate limit signal", health.signals)
	}
	if sig := health.signals[0]; sig.Class != channelhealth.SignalRateLimit || sig.StatusCode != http.StatusTooManyRequests || sig.RateLimitResetAt == nil {
		t.Fatalf("first health signal=%+v want 429 rate-limit cooldown", sig)
	}
}

func TestPR5NonStream401ConsumesOneAuthFailoverOnlyAndDoesNotRecordHealth(t *testing.T) {
	enableHCSFDispatchForTest(t)
	selector := newPR5Selector(t, 301, 302, 303)
	settler := &recordingSettler{}
	health := &recordingChannelHealth{}
	dispatcher := &pr5CanonicalSequenceDispatcher{
		steps: []pr5CanonicalStep{
			{status: http.StatusUnauthorized, body: `{"error":"invalid_grant"}`},
			{status: http.StatusUnauthorized, body: `{"error":"invalid_grant"}`},
			{successText: "must not reach third account"},
		},
	}
	deps := pr5NonStreamDeps(t, selector, &pr5ClaimGate{claimID: 88003}, settler, dispatcher)
	deps.ChannelHealth = health
	deps.Router = stubRouter{plan: pr5RoutePlan(
		router.AttemptPlan{Index: 0, PoolGroupID: 42, UpstreamModelID: "gpt-4o", Reason: "primary"},
		router.AttemptPlan{Index: 1, PoolGroupID: 43, UpstreamModelID: "gpt-4o", Reason: "auth_failover"},
		router.AttemptPlan{Index: 2, PoolGroupID: 44, UpstreamModelID: "gpt-4o", Reason: "must_not_use"},
	)}

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s; want final 401 after one auth failover", rec.Code, rec.Body.String())
	}
	if selector.calls != 2 || dispatcher.calls != 2 {
		t.Fatalf("selector/dispatcher calls=%d/%d want 2/2", selector.calls, dispatcher.calls)
	}
	if len(settler.calls) != 0 {
		t.Fatalf("positive settles=%d want 0", len(settler.calls))
	}
	if len(settler.aborts) != 2 {
		t.Fatalf("aborts=%+v want both failed attempts released", settler.aborts)
	}
	if len(health.signals) != 0 {
		t.Fatalf("401 health signals=%+v want none", health.signals)
	}
}

func TestPR5NonStreamAllAttemptsFailReturnsLastClassifiedError(t *testing.T) {
	enableHCSFDispatchForTest(t)
	selector := newPR5Selector(t, 401, 402)
	dispatcher := &pr5CanonicalSequenceDispatcher{
		steps: []pr5CanonicalStep{
			{status: http.StatusInternalServerError, body: `{"error":"first failed"}`},
			{status: http.StatusTooManyRequests, body: `{"error":"last failed"}`, headers: http.Header{"Retry-After": []string{"11"}}},
		},
	}
	deps := pr5NonStreamDeps(t, selector, &pr5ClaimGate{claimID: 88004}, &recordingSettler{}, dispatcher)

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s; want final 503 from last 429 classification", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "11" {
		t.Fatalf("Retry-After=%q want 11 from last failure", got)
	}
	if !strings.Contains(rec.Body.String(), "upstream_upstream_rate_limited") {
		t.Fatalf("body=%s want last classified rate-limit error", rec.Body.String())
	}
}

func TestPR5IdempotentReplayAfterRetriedSuccessWritesOneTerminalResponse(t *testing.T) {
	enableHCSFDispatchForTest(t)
	replayStore := billing.NewMemoryReplayStore()
	settler := &recordingSettler{}
	dispatcher := &pr5CanonicalSequenceDispatcher{
		steps: []pr5CanonicalStep{
			{status: http.StatusInternalServerError, body: `{"error":"first failed"}`},
			{successText: "stored after retry"},
		},
	}
	body := pr5NonStreamBody()
	firstDeps := pr5NonStreamDeps(t, newPR5Selector(t, 501, 502), &pr5ClaimGate{claimID: 88005}, settler, dispatcher)
	firstDeps.ReplayStore = replayStore

	first := invokeWithIdempotencyKey(t, firstDeps, body, "pr5-retry-idem")
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s; want retried success", first.Code, first.Body.String())
	}
	if len(settler.calls) != 1 {
		t.Fatalf("positive settles=%d want 1", len(settler.calls))
	}
	if stored, ok, err := replayStore.Lookup(context.Background(), validIdentity().TenantID, 88005); err != nil {
		t.Fatalf("lookup replay: %v", err)
	} else if !ok || string(stored.ResponseBody) != first.Body.String() {
		t.Fatalf("stored replay ok/body=%v/%q want first response", ok, string(stored.ResponseBody))
	}

	secondDeps := pr5NonStreamDeps(t, newPR5Selector(t, 503), replayClaimGate{claimID: 88005, hit: true}, &recordingSettler{}, &pr5CanonicalSequenceDispatcher{
		steps: []pr5CanonicalStep{{successText: "must not dispatch replay"}},
	})
	secondDeps.ReplayStore = replayStore
	second := invokeWithIdempotencyKey(t, secondDeps, body, "pr5-retry-idem")
	if second.Code != http.StatusOK {
		t.Fatalf("second status=%d body=%s; want replay hit", second.Code, second.Body.String())
	}
	if got := second.Header().Get("X-HUAKAI-Idempotency-Hit"); got != "true" {
		t.Fatalf("idempotency hit header=%q want true", got)
	}
	if second.Body.String() != first.Body.String() {
		t.Fatalf("replay body mismatch:\nfirst=%s\nsecond=%s", first.Body.String(), second.Body.String())
	}
}

func TestPR5StreamRetryFinalJSONErrorClearsAttemptScopedHeaders(t *testing.T) {
	replayStore := billing.NewMemoryReplayStore()
	settler := &recordingSettler{}
	streamDoer := &pr5SequentialStreamingDoer{
		steps: []pr5StreamStep{
			{body: &delayedReadCloser{delay: 50 * time.Millisecond}},
			{status: http.StatusTooManyRequests, body: io.NopCloser(strings.NewReader(`{"error":"rate limited"}`))},
		},
	}
	deps := streamingReplayDeps(t, 88008, false, "", replayStore)
	deps.Dispatcher.HTTPClient = streamDoer
	deps.Forwarder.Timeouts.FirstTokenTimeout = 5 * time.Millisecond
	deps.Forwarder.Timeouts.InterEventTimeout = 200 * time.Millisecond
	deps.ResponseCache = l2cache.NewMemoryStore(1<<20, time.Minute)
	deps.Router = stubRouter{plan: pr5RoutePlan(
		router.AttemptPlan{Index: 0, PoolGroupID: 42, UpstreamModelID: "gpt-4o", Reason: "primary"},
		router.AttemptPlan{Index: 1, PoolGroupID: 42, UpstreamModelID: "gpt-4o", Reason: "same_pool_account_failover"},
	)}
	deps.Selector = newPR5Selector(t, 801, 802)
	deps.CredentialVault = pr5CredentialVault(t, 801, 802)
	deps.Settler = settler

	rec := invokeWithIdempotencyKey(t, deps, openAIStreamingRequestBody(), "pr5-stream-stale-header")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s; want final JSON 503 from second attempt 429", rec.Code, rec.Body.String())
	}
	if streamDoer.calls != 2 {
		t.Fatalf("stream dispatch calls=%d want 2", streamDoer.calls)
	}
	if len(settler.aborts) != 2 {
		t.Fatalf("aborts=%+v want first pre-byte failure and final 429 failure", settler.aborts)
	}
	for _, header := range []string{
		"Trailer",
		"Cache-Control",
		"Connection",
		"X-HUAKAI-Stream-State",
		"X-HUAKAI-Delivered-Tokens",
		"X-HUAKAI-Cache-L2",
		"X-HUAKAI-Ledger-ID",
		"X-HUAKAI-Verify",
		"X-HUAKAI-Sig-Fingerprint",
		"X-Huakai-Forward-Error",
		"X-Huakai-Abort-Failed",
		"X-Huakai-Settle-Error",
	} {
		if got := rec.Header().Values(header); len(got) > 0 {
			t.Fatalf("final JSON error leaked stale %s=%v from failed stream attempt", header, got)
		}
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("Content-Type=%q want application/json for final JSON error", got)
	}
}

func TestPR5StreamDispatchTimeoutAbortReasonMatchesRetryDecision(t *testing.T) {
	settler := &recordingSettler{}
	dispatchErr := context.DeadlineExceeded
	deps := streamingReplayDeps(t, 88009, false, "", nil)
	deps.Dispatcher.HTTPClient = &pr5SequentialStreamingDoer{
		steps: []pr5StreamStep{{err: dispatchErr}},
	}
	deps.Settler = settler

	rec := invokeHandler(t, deps, openAIStreamingRequestBody())
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s; want dispatch timeout client status", rec.Code, rec.Body.String())
	}
	if len(settler.aborts) != 1 {
		t.Fatalf("aborts=%+v want one dispatch-timeout abort", settler.aborts)
	}
	want := gateway.ClassifyAttemptDispatchError(dispatchErr).AbortReason
	if got := settler.aborts[0].reason; got != want {
		t.Fatalf("abort reason=%q want %q from dispatch retry decision", got, want)
	}
}

func TestPR5StreamRetryClearsDeferredLedgerDLQTrailerBeforeSuccess(t *testing.T) {
	// Risk killed: a Deferred ledger result from a pre-delivery failed attempt
	// must not leak X-HUAKAI-Ledger-DLQ-Ref into the later successful streamed
	// response. Mutation self-check: removing the DLQ trailer from retry
	// cleanup leaves audit_ledger_dlq:729 in the final response trailer.
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	inner, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	ledger := &firstAppendFailsThenPersistsLedger{
		inner: inner,
		err:   errors.New("ledger unavailable on first attempt"),
	}
	dlqSink := &recordingGatewayAuditLedgerDLQ{id: 729}
	streamDoer := &pr5SequentialStreamingDoer{
		steps: []pr5StreamStep{
			{body: &delayedReadCloser{delay: 50 * time.Millisecond}},
			{body: io.NopCloser(strings.NewReader(openAIStreamingFixture()))},
		},
	}
	deps := streamingReplayDeps(t, 88010, false, "", nil)
	deps.Dispatcher.HTTPClient = streamDoer
	deps.Forwarder.Timeouts.FirstTokenTimeout = 5 * time.Millisecond
	deps.Forwarder.Timeouts.InterEventTimeout = 200 * time.Millisecond
	deps.AuditLedger = ledger
	deps.AuditLedgerDLQ = dlqSink
	deps.Signer = signer
	deps.Router = stubRouter{plan: pr5RoutePlan(
		router.AttemptPlan{Index: 0, PoolGroupID: 42, UpstreamModelID: "gpt-4o", Reason: "primary"},
		router.AttemptPlan{Index: 1, PoolGroupID: 42, UpstreamModelID: "gpt-4o", Reason: "same_pool_account_failover"},
	)}
	deps.Selector = newPR5Selector(t, 901, 902)
	deps.CredentialVault = pr5CredentialVault(t, 901, 902)

	rec := invokeHandler(t, deps, openAIStreamingRequestBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s; want 200 after retry success", rec.Code, rec.Body.String())
	}
	if streamDoer.calls != 2 {
		t.Fatalf("stream dispatch calls=%d want 2", streamDoer.calls)
	}
	if ledger.calls != 2 {
		t.Fatalf("ledger append calls=%d want first Deferred then second Persisted", ledger.calls)
	}
	if len(dlqSink.events) != 1 {
		t.Fatalf("DLQ events=%d want 1 from first attempt", len(dlqSink.events))
	}
	result := rec.Result()
	if got := result.Trailer.Get(headerHUAKAIAuditLedgerDLQRef); got != "" {
		t.Fatalf("%s trailer=%q want empty after retry success", headerHUAKAIAuditLedgerDLQRef, got)
	}
	if got := result.Header.Get(headerHUAKAIAuditLedgerDLQRef); got != "" {
		t.Fatalf("%s ordinary header=%q want empty after retry success", headerHUAKAIAuditLedgerDLQRef, got)
	}
	if got := result.Trailer.Get(headerHUAKAIAuditLedgerID); got == "" {
		t.Fatalf("%s trailer is empty; fixture must prove second attempt persisted", headerHUAKAIAuditLedgerID)
	}
}

func TestPR5RawBufferedDispatchTimeoutAbortReasonMatchesRetryDecision(t *testing.T) {
	t.Setenv("HUAKAI_DISPATCH_HCSF", "0")
	settler := &recordingSettler{}
	dispatchErr := context.DeadlineExceeded
	adapters := provider.NewStaticRegistry()
	adapters.MustRegister("openai_chat", &provideropenai.PassthroughAdapter{})
	deps := clientAdapterDeps(t)
	deps.Dispatcher = &gateway.UpstreamDispatcher{
		Adapters:         adapters,
		TransportFactory: transport.NewFactory(),
		HTTPClient:       &pr5SequentialStreamingDoer{steps: []pr5StreamStep{{err: dispatchErr}}},
	}
	deps.Settler = settler

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s; want dispatch timeout client status", rec.Code, rec.Body.String())
	}
	if len(settler.aborts) != 1 {
		t.Fatalf("aborts=%+v want one raw dispatch-timeout abort", settler.aborts)
	}
	want := gateway.ClassifyAttemptDispatchError(dispatchErr).AbortReason
	if got := settler.aborts[0].reason; got != want {
		t.Fatalf("abort reason=%q want %q from dispatch retry decision", got, want)
	}
}

func TestPR5ClaimRaceAbortFailureSurfacesSafeHeader(t *testing.T) {
	const marker = "SENSITIVE_ABORT_MARKER"
	settler := &failingAbortSettler{err: errors.New("abort unavailable: " + marker)}
	deps := clientAdapterDeps(t)
	deps.Selector = claimRaceSelector{}
	deps.Settler = settler

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s; want 409 claim_race", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After=%q want 1", got)
	}
	if got := rec.Header().Get("X-Huakai-Abort-Failed"); got != "abort_failed" {
		t.Fatalf("X-Huakai-Abort-Failed=%q want abort_failed", got)
	} else if strings.Contains(got, marker) {
		t.Fatalf("X-Huakai-Abort-Failed leaked marker: %q", got)
	}
	if len(settler.aborts) != 1 || settler.aborts[0].reason != "claim_race" {
		t.Fatalf("aborts=%+v want one claim_race abort", settler.aborts)
	}
}

func TestPR5StreamPreFirstByteTimeoutRetriesAndPostFirstByteErrorDoesNotRetry(t *testing.T) {
	replayStore := billing.NewMemoryReplayStore()
	settler := &recordingSettler{}
	streamDoer := &pr5SequentialStreamingDoer{
		steps: []pr5StreamStep{
			{body: &delayedReadCloser{delay: 50 * time.Millisecond}},
			{body: io.NopCloser(strings.NewReader(openAIStreamingFixture()))},
		},
	}
	deps := streamingReplayDeps(t, 88006, false, "", replayStore)
	deps.Dispatcher.HTTPClient = streamDoer
	deps.Forwarder.Timeouts.FirstTokenTimeout = 5 * time.Millisecond
	deps.Forwarder.Timeouts.InterEventTimeout = 200 * time.Millisecond
	deps.Router = stubRouter{plan: pr5RoutePlan(
		router.AttemptPlan{Index: 0, PoolGroupID: 42, UpstreamModelID: "gpt-4o", Reason: "primary"},
		router.AttemptPlan{Index: 1, PoolGroupID: 42, UpstreamModelID: "gpt-4o", Reason: "same_pool_account_failover"},
	)}
	deps.Selector = newPR5Selector(t, 601, 602)
	deps.CredentialVault = pr5CredentialVault(t, 601, 602)
	deps.Settler = settler

	rec := invokeWithIdempotencyKey(t, deps, openAIStreamingRequestBody(), "pr5-stream-pre-byte")
	if rec.Code != http.StatusOK {
		t.Fatalf("pre-byte retry status=%d body=%s; want 200", rec.Code, rec.Body.String())
	}
	if streamDoer.calls != 2 {
		t.Fatalf("stream dispatch calls=%d want 2 after pre-first-byte timeout retry", streamDoer.calls)
	}
	if len(settler.aborts) != 1 || settler.aborts[0].reason != "upstream_timeout" {
		t.Fatalf("stream pre-byte aborts=%+v want one upstream_timeout abort", settler.aborts)
	}
	if len(settler.calls) != 1 {
		t.Fatalf("stream positive settles=%d want 1", len(settler.calls))
	}
	if got := rec.Header().Get("X-Huakai-Forward-Error"); got != "" {
		t.Fatalf("pre-byte retry success leaked X-Huakai-Forward-Error=%q from failed attempt", got)
	}
	if got := rec.Header().Get("X-Huakai-Abort-Failed"); got != "" {
		t.Fatalf("pre-byte retry success leaked X-Huakai-Abort-Failed=%q from failed attempt", got)
	}

	postByteDoer := &pr5SequentialStreamingDoer{
		steps: []pr5StreamStep{
			{body: io.NopCloser(strings.NewReader(openAIStreamingFixture()))},
			{body: io.NopCloser(strings.NewReader(openAIStreamingFixture()))},
		},
	}
	postSettler := &recordingSettler{}
	postDeps := streamingReplayDeps(t, 88007, false, "", nil)
	postDeps.Dispatcher.HTTPClient = postByteDoer
	postDeps.Router = deps.Router
	postDeps.Selector = newPR5Selector(t, 701, 702)
	postDeps.CredentialVault = pr5CredentialVault(t, 701, 702)
	scanners := gateway.NewStaticStreamScannerRegistry()
	scanners.MustRegister("openai_chat", scannerThenError{
		event: partialOpenAIStreamingEventBeforeReadError(),
		err:   errors.New("body idle timeout"),
	})
	postDeps.Forwarder.Scanners = scanners
	postDeps.Settler = postSettler

	post := invokeWithIdempotencyKey(t, postDeps, openAIStreamingRequestBody(), "pr5-stream-post-byte")
	if post.Code != http.StatusOK {
		t.Fatalf("post-byte status=%d body=%s; want current stream response", post.Code, post.Body.String())
	}
	if postByteDoer.calls != 1 {
		t.Fatalf("post-byte dispatch calls=%d want 1 because delivery started", postByteDoer.calls)
	}
	if len(postSettler.calls) != 1 {
		t.Fatalf("post-byte positive settles=%d want 1 partial settle", len(postSettler.calls))
	}
	if len(postSettler.aborts) != 0 {
		t.Fatalf("post-byte aborts=%+v want none after delivered partial stream", postSettler.aborts)
	}
}

func pr5NonStreamBody() string {
	return `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`
}

func pr5NonStreamDeps(t *testing.T, selector pool.Selector, claimGate billing.ClaimGate, settler billing.Settler, dispatcher HCSFDispatcher) ChatHandlerDeps {
	t.Helper()
	deps := clientAdapterDeps(t)
	deps.Router = stubRouter{plan: pr5RoutePlan(
		router.AttemptPlan{Index: 0, PoolGroupID: 42, UpstreamModelID: "gpt-4o", Reason: "primary"},
		router.AttemptPlan{Index: 1, PoolGroupID: 42, UpstreamModelID: "gpt-4o", Reason: "same_pool_account_failover"},
	)}
	deps.Selector = selector
	deps.ClaimGate = claimGate
	deps.Settler = settler
	deps.CanonicalDispatcher = dispatcher
	if pr5, ok := selector.(*pr5Selector); ok {
		deps.CredentialVault = pr5CredentialVault(t, pr5.accounts...)
	}
	return deps
}

func pr5RoutePlan(attempts ...router.AttemptPlan) router.RoutePlan {
	return router.RoutePlan{
		Attempts:      attempts,
		AttemptBudget: len(attempts),
		RetryableEndClasses: []string{
			string(gateway.UpstreamError5xx),
			string(gateway.UpstreamRateLimit),
			string(gateway.FirstTokenTimeout),
			string(gateway.InterEventTimeout),
		},
		SnapshotVersion: "registry:7:1;router:pr5-test",
	}
}

type pr5ClaimGate struct {
	claimID  int64
	requests []billing.ReserveRequest
}

func (g *pr5ClaimGate) Reserve(_ context.Context, req billing.ReserveRequest) (*billing.ReserveResult, error) {
	g.requests = append(g.requests, req)
	return &billing.ReserveResult{ClaimID: g.claimID}, nil
}

type pr5Selector struct {
	t        *testing.T
	accounts []int64
	calls    int
	requests []pool.SelectionRequest
}

func newPR5Selector(t *testing.T, accounts ...int64) *pr5Selector {
	t.Helper()
	vaultAccounts := append([]int64(nil), accounts...)
	if len(vaultAccounts) == 0 {
		t.Fatal("newPR5Selector requires at least one account")
	}
	return &pr5Selector{t: t, accounts: vaultAccounts}
}

func (s *pr5Selector) Select(_ context.Context, req pool.SelectionRequest) (*pool.SelectionResult, error) {
	s.calls++
	s.requests = append(s.requests, req)
	for _, accountID := range s.accounts {
		if _, excluded := req.ExcludedAccounts[accountID]; excluded {
			continue
		}
		return &pool.SelectionResult{
			AccountID:         accountID,
			AcquisitionToken:  uuid.New(),
			RoutingReasonJSON: []byte(`{"test":"pr5"}`),
		}, nil
	}
	return nil, pool.ErrNoEligibleAccount
}

type claimRaceSelector struct{}

func (claimRaceSelector) Select(context.Context, pool.SelectionRequest) (*pool.SelectionResult, error) {
	return nil, pool.ErrClaimRace
}

func pr5CredentialVault(t *testing.T, accountIDs ...int64) provider.CredentialVault {
	t.Helper()
	vault := provider.NewStaticVault()
	for _, accountID := range accountIDs {
		if err := vault.Set(accountID, provider.Credential{
			Type:  provider.CredentialTypeAPIKey,
			Value: "sk-pr5-test",
		}, provider.AccountInfo{
			AccountID:           accountID,
			Platform:            "openai",
			AccountType:         "apikey",
			AccountCredentialID: 9000 + accountID,
			CredentialVersion:   1,
		}); err != nil {
			t.Fatalf("vault.Set(%d): %v", accountID, err)
		}
	}
	return vault
}

type pr5CanonicalStep struct {
	status      int
	body        string
	headers     http.Header
	successText string
}

type pr5CanonicalSequenceDispatcher struct {
	calls    int
	steps    []pr5CanonicalStep
	accounts []int64
}

type failingAbortSettler struct {
	recordingSettler
	err error
}

func (s *failingAbortSettler) Abort(ctx context.Context, tenantID, claimID int64, reason, auditRequestID string, observedInputTokens int64) error {
	_ = s.recordingSettler.Abort(ctx, tenantID, claimID, reason, auditRequestID, observedInputTokens)
	return s.err
}

func (d *pr5CanonicalSequenceDispatcher) DispatchHCSF(_ context.Context, requestEnvelope *proto.HCSF) (*proto.HCSF, error) {
	d.calls++
	d.accounts = append(d.accounts, requestEnvelope.RequestMeta.AccountID)
	step := pr5CanonicalStep{successText: "ok"}
	if d.calls <= len(d.steps) {
		step = d.steps[d.calls-1]
	}
	if step.status != 0 && (step.status < 200 || step.status >= 300) {
		return nil, &gateway.UpstreamHTTPError{
			StatusCode: step.status,
			Body:       []byte(step.body),
			Header:     step.headers,
		}
	}
	env := proto.NewEmptyEnvelope()
	env.RequestMeta = requestEnvelope.RequestMeta
	env.BufferedResponse = &proto.CanonicalResponse{
		ID:         "chatcmpl-pr5-retry",
		Model:      requestEnvelope.RequestMeta.UpstreamModel,
		Content:    []proto.CanonicalContentBlock{{Type: "text", Text: step.successText}},
		Usage:      proto.CanonicalUsage{InputTokens: 2, OutputTokens: 3},
		StopReason: proto.CanonicalStopEndTurn,
	}
	env.Accounting.Usage = env.BufferedResponse.Usage
	env.Accounting.EvidenceLabel = proto.EvidenceMock
	return env, nil
}

type pr5StreamStep struct {
	status int
	body   io.ReadCloser
	err    error
}

type pr5SequentialStreamingDoer struct {
	calls int
	steps []pr5StreamStep
}

func (d *pr5SequentialStreamingDoer) Do(req *http.Request) (*http.Response, error) {
	_, _ = io.ReadAll(req.Body)
	d.calls++
	step := pr5StreamStep{status: http.StatusOK, body: io.NopCloser(strings.NewReader(openAIStreamingFixture()))}
	if d.calls <= len(d.steps) {
		step = d.steps[d.calls-1]
	}
	if step.err != nil {
		return nil, step.err
	}
	if step.status == 0 {
		step.status = http.StatusOK
	}
	if step.body == nil {
		step.body = io.NopCloser(strings.NewReader(""))
	}
	return &http.Response{
		StatusCode: step.status,
		Header:     make(http.Header),
		Body:       step.body,
	}, nil
}

type firstAppendFailsThenPersistsLedger struct {
	inner *auditledger.MemoryLedger
	err   error
	calls int
}

func (l *firstAppendFailsThenPersistsLedger) Append(ctx context.Context, entry auditledger.PreparedEntry) (auditledger.LedgerEntry, error) {
	l.calls++
	if l.calls == 1 {
		return auditledger.LedgerEntry{}, l.err
	}
	return l.inner.Append(ctx, entry)
}

func (l *firstAppendFailsThenPersistsLedger) GetByRequestID(ctx context.Context, requestID string) (auditledger.LedgerEntry, error) {
	return l.inner.GetByRequestID(ctx, requestID)
}

func (l *firstAppendFailsThenPersistsLedger) GetByRequestIDAndTenantScope(ctx context.Context, requestID, tenantScopeRef string) (auditledger.LedgerEntry, error) {
	return l.inner.GetByRequestIDAndTenantScope(ctx, requestID, tenantScopeRef)
}

func (l *firstAppendFailsThenPersistsLedger) LatestMerkleRoot(ctx context.Context) ([32]byte, error) {
	return l.inner.LatestMerkleRoot(ctx)
}

func (l *firstAppendFailsThenPersistsLedger) Size(ctx context.Context) int {
	return l.inner.Size(ctx)
}

type delayedReadCloser struct {
	delay time.Duration
	done  bool
}

func (r *delayedReadCloser) Read(_ []byte) (int, error) {
	if !r.done {
		time.Sleep(r.delay)
		r.done = true
	}
	return 0, io.EOF
}

func (r *delayedReadCloser) Close() error {
	return nil
}
