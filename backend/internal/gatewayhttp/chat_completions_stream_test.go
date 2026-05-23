package gatewayhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/anthropic"
	provideropenai "github.com/BloomingProsperity/HUAKAI/internal/provider/openai"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

func TestDeliveryTrackerMarksStartedOnWriteAndWriteHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	tracker := newDeliveryTracker(rec)
	if tracker.started() {
		t.Fatal("new tracker must start as not delivered")
	}
	if _, err := tracker.Write([]byte("data: hello\n\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !tracker.started() {
		t.Fatal("Write must mark delivery started")
	}
	if tracker.statusCode() != http.StatusOK {
		t.Fatalf("status=%d want 200 after implicit WriteHeader", tracker.statusCode())
	}

	rec = httptest.NewRecorder()
	tracker = newDeliveryTracker(rec)
	tracker.WriteHeader(http.StatusAccepted)
	if !tracker.started() {
		t.Fatal("WriteHeader must mark delivery started")
	}
	if tracker.statusCode() != http.StatusAccepted {
		t.Fatalf("status=%d want 202", tracker.statusCode())
	}
}

func TestHandleStreamingResponse_CrossProtocolTranslatesRequestAndResponse(t *testing.T) {
	upstream := &recordingStreamingDoer{responseBody: anthropicStreamingFixture()}
	adapterReg := provider.NewStaticRegistry()
	adapterReg.MustRegister("anthropic_messages", &anthropic.PassthroughAdapter{})
	reqBody := []byte(`{"model":"gpt-4o","stream":true,"max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(reqBody)))
	ex := &chatExecution{
		d: ChatHandlerDeps{
			Dispatcher: &gateway.UpstreamDispatcher{
				Adapters:         adapterReg,
				TransportFactory: transport.NewFactory(),
				HTTPClient:       upstream,
			},
			Forwarder: &gateway.StreamForwarder{
				ProtocolAdapters: gateway.BuildDefaultProtocolAdapterRegistry(),
				Scanners:         gateway.BuildDefaultStreamScannerRegistry(),
				ScannerBufferCap: 1 << 20,
			},
			Settler: &stubSettler{},
		},
		r:                 req,
		ctx:               req.Context(),
		startedAt:         time.Now(),
		ident:             auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 3},
		body:              reqBody,
		req:               chatRequest{Model: "gpt-4o", Stream: true},
		clientProtocol:    proto.ClientProtocolOpenAIChat,
		requestID:         "req_stream_cross_protocol",
		resolved:          registry.Resolved{ProtocolFamily: "anthropic_messages", ProviderModelID: "claude-3-5-haiku"},
		plan:              router.RoutePlan{SnapshotVersion: "test-snapshot"},
		attempt:           router.AttemptPlan{PoolGroupID: 42},
		routeID:           "route-1",
		reserveRes:        &billing.ReserveResult{ClaimID: 999},
		acquiredAccountID: 1,
		upstreamModelID:   "claude-3-5-haiku",
		cacheVendor:       "anthropic",
		cred:              provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-ant-test"},
		accInfo:           provider.AccountInfo{AccountID: 1, Platform: "anthropic", AccountType: "apikey"},
		forwardReq: gateway.ForwardRequest{
			TenantID:       7,
			AccountID:      1,
			RequestID:      "req_stream_cross_protocol",
			RouteID:        "route-1",
			PoolID:         "42",
			IngressPath:    "/v1/chat/completions",
			ProtocolFamily: "anthropic_messages",
			ClientProtocol: string(proto.ClientProtocolOpenAIChat),
			Model:          "claude-3-5-haiku",
			RequestedModel: "gpt-4o",
			Provider:       "anthropic",
		},
	}

	rec := httptest.NewRecorder()
	ex.handleStreamingResponse(rec)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	var sent map[string]any
	if err := json.Unmarshal(upstream.body, &sent); err != nil {
		t.Fatalf("upstream body json: %v\n%s", err, string(upstream.body))
	}
	if sent["model"] != "claude-3-5-haiku" {
		t.Fatalf("upstream model=%v want claude-3-5-haiku; body=%s", sent["model"], string(upstream.body))
	}
	if sent["stream"] != true {
		t.Fatalf("upstream stream=%v want true; body=%s", sent["stream"], string(upstream.body))
	}
	if sent["max_tokens"] != float64(16) {
		t.Fatalf("upstream max_tokens=%v want 16; body=%s", sent["max_tokens"], string(upstream.body))
	}
	messages, ok := sent["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("upstream messages=%T/%v; body=%s", sent["messages"], sent["messages"], string(upstream.body))
	}
	first, ok := messages[0].(map[string]any)
	if !ok {
		t.Fatalf("first message=%T", messages[0])
	}
	content, ok := first["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("translated content=%T/%v; body=%s", first["content"], first["content"], string(upstream.body))
	}
	block, ok := content[0].(map[string]any)
	if !ok || block["type"] != "text" || block["text"] != "hi" {
		t.Fatalf("translated block=%T/%v; body=%s", content[0], content[0], string(upstream.body))
	}
	if strings.Contains(string(upstream.body), `"content":"hi"`) {
		t.Fatalf("raw OpenAI body leaked to Anthropic upstream: %s", string(upstream.body))
	}
	clientBody := rec.Body.String()
	if strings.Contains(clientBody, "event: message_start") {
		t.Fatalf("raw Anthropic SSE leaked to OpenAI client: %s", clientBody)
	}
	if !strings.Contains(clientBody, `"object":"chat.completion.chunk"`) || !strings.Contains(clientBody, "data: [DONE]") {
		t.Fatalf("client body did not use OpenAI Chat SSE shape: %s", clientBody)
	}
}

type recordingStreamingDoer struct {
	body         []byte
	responseBody string
}

func (d *recordingStreamingDoer) Do(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	d.body = body
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(d.responseBody)),
	}, nil
}

type erroringStreamingDoer struct {
	body           []byte
	responsePrefix string
	err            error
}

func (d *erroringStreamingDoer) Do(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	d.body = body
	readErr := d.err
	if readErr == nil {
		readErr = io.ErrUnexpectedEOF
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       &failingReadCloser{remaining: []byte(d.responsePrefix), err: readErr},
	}, nil
}

type failingReadCloser struct {
	remaining []byte
	err       error
}

func (r *failingReadCloser) Read(p []byte) (int, error) {
	if len(r.remaining) > 0 {
		n := copy(p, r.remaining)
		r.remaining = r.remaining[n:]
		return n, nil
	}
	if r.err != nil {
		err := r.err
		r.err = nil
		return 0, err
	}
	return 0, io.EOF
}

func (r *failingReadCloser) Close() error {
	return nil
}

func anthropicStreamingFixture() string {
	return strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_1","model":"claude-3-5-haiku","usage":{"input_tokens":1}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"pong"}}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
		``,
	}, "\n")
}

func TestStreamingIdempotencyReplayRecordsSSEAndReplays(t *testing.T) {
	replayStore := billing.NewMemoryReplayStore()
	body := openAIStreamingRequestBody()
	claimID := int64(77701)
	settler := &recordingSettler{}

	firstDeps := streamingReplayDeps(t, claimID, false, openAIStreamingFixture(), replayStore)
	firstDeps.Settler = settler
	first := invokeWithIdempotencyKey(t, firstDeps, body, "stream-idem-main")
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d want 200; body=%s", first.Code, first.Body.String())
	}
	if len(settler.calls) != 1 {
		t.Fatalf("settle calls=%d want 1", len(settler.calls))
	}
	if settler.calls[0].StreamAttempt == nil || !settler.calls[0].StreamAttempt.State.Chargeable() {
		t.Fatalf("graceful stream must commit a chargeable attempt; StreamAttempt=%#v", settler.calls[0].StreamAttempt)
	}
	if len(settler.aborts) != 0 {
		t.Fatalf("abort calls=%d want 0", len(settler.aborts))
	}
	if got := first.Header().Get("Content-Type"); !strings.HasPrefix(got, idempotencyReplayContentTypeSSE) {
		t.Fatalf("first Content-Type=%q want %s", got, idempotencyReplayContentTypeSSE)
	}
	stored, ok, err := replayStore.Lookup(context.Background(), validIdentity().TenantID, claimID)
	if err != nil {
		t.Fatalf("lookup replay: %v", err)
	}
	if !ok {
		t.Fatal("stream replay record missing after successful settlement")
	}
	if stored.ContentType != idempotencyReplayContentTypeSSE {
		t.Fatalf("stored ContentType=%q want %q", stored.ContentType, idempotencyReplayContentTypeSSE)
	}
	if string(stored.ResponseBody) != first.Body.String() {
		t.Fatalf("stored body mismatch:\nstored=%s\nfirst=%s", string(stored.ResponseBody), first.Body.String())
	}

	secondDeps := streamingReplayDeps(t, claimID, true, "data: should-not-dispatch\n\n", replayStore)
	secondDeps.Dispatcher = &gateway.UpstreamDispatcher{}
	second := invokeWithIdempotencyKey(t, secondDeps, body, "stream-idem-main")
	if second.Code != http.StatusOK {
		t.Fatalf("replay status=%d want 200; body=%s", second.Code, second.Body.String())
	}
	if got := second.Header().Get("X-HUAKAI-Idempotency-Hit"); got != "true" {
		t.Fatalf("replay hit header=%q want true", got)
	}
	if got := second.Header().Get("Content-Type"); !strings.HasPrefix(got, idempotencyReplayContentTypeSSE) {
		t.Fatalf("replay Content-Type=%q want %s", got, idempotencyReplayContentTypeSSE)
	}
	if got := second.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("replay Cache-Control=%q want no-cache", got)
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("replay body mismatch:\nfirst=%s\nsecond=%s", first.Body.String(), second.Body.String())
	}
}

func TestStreamingLedgerAppendAndDLQFailureProductionDoesNotSettle(t *testing.T) {
	// Risk killed: streaming Append+DLQ double failure must not become a
	// chargeable 200 with no audit row. Mutation self-check: removing the
	// post-Forward ledger-result settle gate records a settle for this fixture;
	// removing trailer reconciliation leaves StreamState=partial instead of failed.
	t.Setenv("HUAKAI_RELEASE_MODE", "production")
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	settler := &recordingSettler{}
	dlqSink := &recordingGatewayAuditLedgerDLQ{id: 0, err: errors.New("dlq unavailable")}
	deps := streamingReplayDeps(t, 77708, false, openAIStreamingFixture(), nil)
	deps.AuditLedger = &failingAppendLedger{appendErr: errors.New("ledger unavailable")}
	deps.AuditLedgerDLQ = dlqSink
	deps.Signer = signer
	deps.Settler = settler

	rec := invokeHandler(t, deps, openAIStreamingRequestBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want stream response already delivered; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "pong") {
		t.Fatalf("stream body=%s want delivered fixture content", rec.Body.String())
	}
	if len(settler.calls) != 0 {
		t.Fatalf("settle calls=%d want 0 when streaming ledger has no durable result", len(settler.calls))
	}
	if len(settler.aborts) != 1 || settler.aborts[0].reason != "audit_ledger_error" {
		t.Fatalf("aborts=%+v want one audit_ledger_error abort", settler.aborts)
	}
	if len(dlqSink.events) != 1 {
		t.Fatalf("DLQ events=%d want 1 attempted enqueue", len(dlqSink.events))
	}
	result := rec.Result()
	if got := result.Trailer.Get(headerHUAKAIStreamState); got != "failed" {
		t.Fatalf("%s trailer=%q want failed for fail-closed ledger abort", headerHUAKAIStreamState, got)
	}
	if got := result.Trailer.Get(headerHUAKAIStreamState); got == "partial" {
		t.Fatalf("%s trailer must not stay chargeable partial after fail-closed abort", headerHUAKAIStreamState)
	}
}

func TestStreamingPersistedLedgerIDIsTrailerOnly(t *testing.T) {
	// Risk killed: C-13 moves streaming ledger emission after body bytes, so
	// LedgerID must be a declared trailer, not an ordinary header. Mutation
	// self-check: writing the old ordinary header before first byte leaves
	// Result().Header populated and Result().Trailer empty.
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	deps := streamingReplayDeps(t, 77711, false, openAIStreamingFixture(), nil)
	deps.AuditLedger = ledger
	deps.Signer = signer

	rec := invokeHandler(t, deps, openAIStreamingRequestBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	result := rec.Result()
	if got := result.Header.Get(headerHUAKAIAuditLedgerID); got != "" {
		t.Fatalf("%s ordinary header=%q want empty for streaming trailer", headerHUAKAIAuditLedgerID, got)
	}
	if got := result.Trailer.Get(headerHUAKAIAuditLedgerID); got == "" {
		t.Fatalf("%s trailer is empty for persisted streaming ledger", headerHUAKAIAuditLedgerID)
	}
	if got := result.Trailer.Get("X-HUAKAI-Ledger-DLQ-Ref"); got != "" {
		t.Fatalf("X-HUAKAI-Ledger-DLQ-Ref trailer=%q want empty for persisted ledger", got)
	}
}

func TestStreamingDeferredLedgerDLQRefIsTrailer(t *testing.T) {
	// Risk killed: C-13 rev2 requires Deferred streaming results to expose
	// DLQRef in its own trailer, never mixed into LedgerID. Mutation
	// self-check: omitting the Deferred trailer writer leaves DLQRef empty.
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	dlqSink := &recordingGatewayAuditLedgerDLQ{id: 728}
	deps := streamingReplayDeps(t, 77712, false, openAIStreamingFixture(), nil)
	deps.AuditLedger = &failingAppendLedger{appendErr: errors.New("ledger unavailable")}
	deps.AuditLedgerDLQ = dlqSink
	deps.Signer = signer

	rec := invokeHandler(t, deps, openAIStreamingRequestBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(dlqSink.events) != 1 {
		t.Fatalf("DLQ events=%d want 1", len(dlqSink.events))
	}
	result := rec.Result()
	if got := result.Trailer.Get(headerHUAKAIAuditLedgerID); got != "" {
		t.Fatalf("%s trailer=%q want empty for Deferred ledger", headerHUAKAIAuditLedgerID, got)
	}
	if got := result.Trailer.Get("X-HUAKAI-Ledger-DLQ-Ref"); got != "audit_ledger_dlq:728" {
		t.Fatalf("X-HUAKAI-Ledger-DLQ-Ref trailer=%q want audit_ledger_dlq:728", got)
	}
}

func TestStreamingLedgerDuplicateRequestIDProductionDoesNotSettleOrDLQ(t *testing.T) {
	// Risk killed: duplicate request_id is never recoverable by DLQ replay.
	// Mutation self-check: removing the duplicate special-case enqueues DLQ,
	// sends a Deferred callback, and the handler settles this chargeable stream.
	t.Setenv("HUAKAI_RELEASE_MODE", "production")
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	settler := &recordingSettler{}
	dlqSink := &recordingGatewayAuditLedgerDLQ{id: 315}
	deps := streamingReplayDeps(t, 77709, false, openAIStreamingFixture(), nil)
	deps.AuditLedger = &failingAppendLedger{appendErr: auditledger.ErrDuplicateRequestID}
	deps.AuditLedgerDLQ = dlqSink
	deps.Signer = signer
	deps.Settler = settler

	rec := invokeHandler(t, deps, openAIStreamingRequestBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want stream response already delivered; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "pong") {
		t.Fatalf("stream body=%s want delivered fixture content", rec.Body.String())
	}
	if len(settler.calls) != 0 {
		t.Fatalf("settle calls=%d want 0 for duplicate request_id", len(settler.calls))
	}
	if len(settler.aborts) != 1 || settler.aborts[0].reason != "audit_ledger_error" {
		t.Fatalf("aborts=%+v want one audit_ledger_error abort", settler.aborts)
	}
	if len(dlqSink.events) != 0 {
		t.Fatalf("DLQ events=%d want 0 for duplicate request_id", len(dlqSink.events))
	}
}

func TestStreamingIdempotencyReplayAbortsZeroChargeGracefulStream(t *testing.T) {
	replayStore := billing.NewMemoryReplayStore()
	body := openAIStreamingRequestBody()
	claimID := int64(77706)
	settler := &recordingSettler{}

	firstDeps := streamingReplayDeps(t, claimID, false, zeroTokenOpenAIStreamingFixture(), replayStore)
	firstDeps.Settler = settler
	first := invokeWithIdempotencyKey(t, firstDeps, body, "stream-idem-zero-charge")
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d want 200; body=%s", first.Code, first.Body.String())
	}
	if len(settler.calls) != 0 {
		t.Fatalf("settle calls=%d want 0 for zero-delivery stream", len(settler.calls))
	}
	if len(settler.aborts) != 1 {
		t.Fatalf("abort calls=%d want 1 for zero-delivery stream", len(settler.aborts))
	}
	if got := settler.aborts[0].claimID; got != claimID {
		t.Fatalf("abort claimID=%d want %d", got, claimID)
	}
	if got := settler.aborts[0].reason; got != "stream_no_billable_delivery" {
		t.Fatalf("abort reason=%q want stream_no_billable_delivery", got)
	}
	if _, ok, err := replayStore.Lookup(context.Background(), validIdentity().TenantID, claimID); err != nil {
		t.Fatalf("lookup replay: %v", err)
	} else if ok {
		t.Fatal("zero-delivery stream must not record idempotency replay")
	}

	retryClaimID := claimID + 1
	retrySettler := &recordingSettler{}
	secondDeps := streamingReplayDeps(t, retryClaimID, false, openAIStreamingFixture(), replayStore)
	secondDeps.Settler = retrySettler
	second := invokeWithIdempotencyKey(t, secondDeps, body, "stream-idem-zero-charge")
	if second.Code != http.StatusOK {
		t.Fatalf("retry status=%d want 200; body=%s", second.Code, second.Body.String())
	}
	if got := second.Header().Get("X-HUAKAI-Idempotency-Hit"); got != "" {
		t.Fatalf("retry idempotency-hit header=%q want empty after aborted first claim", got)
	}
	if !strings.Contains(second.Body.String(), "pong") {
		t.Fatalf("retry should dispatch upstream and return fresh stream; body=%s", second.Body.String())
	}
	if len(retrySettler.calls) != 1 {
		t.Fatalf("retry settle calls=%d want 1", len(retrySettler.calls))
	}
	if retrySettler.calls[0].ClaimID != retryClaimID {
		t.Fatalf("retry settle claimID=%d want %d", retrySettler.calls[0].ClaimID, retryClaimID)
	}
	stored, ok, err := replayStore.Lookup(context.Background(), validIdentity().TenantID, retryClaimID)
	if err != nil {
		t.Fatalf("lookup replay: %v", err)
	}
	if !ok {
		t.Fatal("retry chargeable stream must record replay after successful settlement")
	}
	if stored.ContentType != idempotencyReplayContentTypeSSE {
		t.Fatalf("stored ContentType=%q want %q", stored.ContentType, idempotencyReplayContentTypeSSE)
	}
	if string(stored.ResponseBody) != second.Body.String() {
		t.Fatalf("stored retry body mismatch:\nstored=%s\nretry=%s", string(stored.ResponseBody), second.Body.String())
	}
	if first.Body.String() == second.Body.String() {
		t.Fatalf("fixture sanity: zero-delivery first body should differ from fresh chargeable retry\nfirst=%s\nsecond=%s", first.Body.String(), second.Body.String())
	}
}

func TestStreamingCaseCDefaultNoBillAbortsWithZeroObservedInput(t *testing.T) {
	settler := &recordingSettler{}
	deps := inputOnlyInterruptedStreamDeps(t, 77801, 17, billing.NewPolicyResolver(&streamPolicyStore{}, time.Minute))
	deps.Settler = settler

	rec := invokeHandler(t, deps, openAIStreamingRequestBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(settler.calls) != 0 {
		t.Fatalf("settle calls=%d want 0 for case C no_bill", len(settler.calls))
	}
	if len(settler.aborts) != 1 {
		t.Fatalf("abort calls=%d want 1 for case C no_bill", len(settler.aborts))
	}
	if got := settler.aborts[0].reason; got != "upstream_5xx" {
		t.Fatalf("abort reason=%q want upstream_5xx", got)
	}
	if got := settler.aborts[0].observedInputTokens; got != 0 {
		t.Fatalf("observed input tokens=%d want 0 for default no_bill", got)
	}
}

func TestStreamingCaseCNoBillRecordAbortsWithObservedInput(t *testing.T) {
	settler := &recordingSettler{}
	deps := inputOnlyInterruptedStreamDeps(t, 77802, 23, streamPolicyResolver(billing.StreamInputOnlyInterruptedPolicyNoBillRecord))
	deps.Settler = settler

	rec := invokeHandler(t, deps, openAIStreamingRequestBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(settler.calls) != 0 {
		t.Fatalf("settle calls=%d want 0 for case C no_bill_record", len(settler.calls))
	}
	if len(settler.aborts) != 1 {
		t.Fatalf("abort calls=%d want 1 for case C no_bill_record", len(settler.aborts))
	}
	if got := settler.aborts[0].observedInputTokens; got != 23 {
		t.Fatalf("observed input tokens=%d want 23 for no_bill_record", got)
	}
}

func TestStreamingNoBillRecordTrueZeroDeliveryKeepsZeroObservedInput(t *testing.T) {
	settler := &recordingSettler{}
	deps := streamingReplayDeps(t, 77803, false, zeroTokenOpenAIStreamingFixture(), nil)
	deps.BillingPolicyResolver = streamPolicyResolver(billing.StreamInputOnlyInterruptedPolicyNoBillRecord)
	deps.Settler = settler

	rec := invokeHandler(t, deps, openAIStreamingRequestBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(settler.calls) != 0 {
		t.Fatalf("settle calls=%d want 0 for true zero-delivery stream", len(settler.calls))
	}
	if len(settler.aborts) != 1 {
		t.Fatalf("abort calls=%d want 1 for true zero-delivery stream", len(settler.aborts))
	}
	if got := settler.aborts[0].observedInputTokens; got != 0 {
		t.Fatalf("observed input tokens=%d want 0 for true zero-delivery stream", got)
	}
}

func TestStreamingCaseCNilResolverDefaultsNoBill(t *testing.T) {
	settler := &recordingSettler{}
	deps := inputOnlyInterruptedStreamDeps(t, 77804, 31, nil)
	deps.Settler = settler

	rec := invokeHandler(t, deps, openAIStreamingRequestBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(settler.aborts) != 1 {
		t.Fatalf("abort calls=%d want 1 for nil resolver case C", len(settler.aborts))
	}
	if got := settler.aborts[0].observedInputTokens; got != 0 {
		t.Fatalf("observed input tokens=%d want 0 for nil resolver", got)
	}
}

func TestStreamingIdempotencyReplayRecordsEOFNoTerminalWhenForwardAndSettleSucceed(t *testing.T) {
	replayStore := billing.NewMemoryReplayStore()
	body := openAIStreamingRequestBody()
	claimID := int64(77707)
	settler := &recordingSettler{}

	firstDeps := streamingReplayDeps(t, claimID, false, openAIStreamingEOFNoTerminalFixture(), replayStore)
	firstDeps.Settler = settler
	first := invokeWithIdempotencyKey(t, firstDeps, body, "stream-idem-eof-no-terminal")
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d want 200; body=%s", first.Code, first.Body.String())
	}
	if got := first.Header().Get("X-Huakai-Forward-Error"); got != "" {
		t.Fatalf("no-terminal fixture fwdErr=%q want nil; body=%s", got, first.Body.String())
	}
	if len(settler.calls) != 1 {
		t.Fatalf("settle calls=%d want 1", len(settler.calls))
	}
	draft := settler.calls[0].Draft
	if draft.EndClass != gateway.UpstreamEOFNoTerminal {
		t.Fatalf("EndClass=%q want %q", draft.EndClass, gateway.UpstreamEOFNoTerminal)
	}
	if !draft.PendingReconciliation {
		t.Fatal("EOF without terminal marker must set PendingReconciliation")
	}
	if settler.calls[0].StreamAttempt == nil || !settler.calls[0].StreamAttempt.State.Chargeable() {
		t.Fatalf("fixture must deliver a chargeable partial stream; StreamAttempt=%#v", settler.calls[0].StreamAttempt)
	}
	if len(settler.aborts) != 0 {
		t.Fatalf("abort calls=%d want 0", len(settler.aborts))
	}
	stored, ok, err := replayStore.Lookup(context.Background(), validIdentity().TenantID, claimID)
	if err != nil {
		t.Fatalf("lookup replay: %v", err)
	}
	if !ok {
		t.Fatal("EOF without terminal marker must still record replay after successful forwarding and settlement")
	}
	if stored.ContentType != idempotencyReplayContentTypeSSE {
		t.Fatalf("stored ContentType=%q want %q", stored.ContentType, idempotencyReplayContentTypeSSE)
	}
	if string(stored.ResponseBody) != first.Body.String() {
		t.Fatalf("stored body mismatch:\nstored=%s\nfirst=%s", string(stored.ResponseBody), first.Body.String())
	}

	secondDeps := streamingReplayDeps(t, claimID, true, openAIStreamingFixture(), replayStore)
	second := invokeWithIdempotencyKey(t, secondDeps, body, "stream-idem-eof-no-terminal")
	if second.Code != http.StatusOK {
		t.Fatalf("replay status=%d want 200; body=%s", second.Code, second.Body.String())
	}
	if got := second.Header().Get("X-HUAKAI-Idempotency-Hit"); got != "true" {
		t.Fatalf("replay hit header=%q want true", got)
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("replay body mismatch:\nfirst=%s\nsecond=%s", first.Body.String(), second.Body.String())
	}
}

func TestStreamingIdempotencyReplaySkipsOverLimit(t *testing.T) {
	replayStore := billing.NewMemoryReplayStore()
	body := openAIStreamingRequestBody()
	claimID := int64(77702)

	firstDeps := streamingReplayDeps(t, claimID, false, oversizedOpenAIStreamingFixture(maxIdempotencyReplayBodyBytes), replayStore)
	first := invokeWithIdempotencyKey(t, firstDeps, body, "stream-idem-over-limit")
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d want 200; body len=%d body prefix=%q", first.Code, first.Body.Len(), first.Body.String()[:min(first.Body.Len(), 256)])
	}
	if first.Body.Len() <= maxIdempotencyReplayBodyBytes {
		t.Fatalf("first body len=%d must exceed replay limit %d", first.Body.Len(), maxIdempotencyReplayBodyBytes)
	}
	if _, ok, err := replayStore.Lookup(context.Background(), validIdentity().TenantID, claimID); err != nil {
		t.Fatalf("lookup replay: %v", err)
	} else if ok {
		t.Fatal("over-limit stream response must not be recorded for replay")
	}

	secondDeps := streamingReplayDeps(t, claimID, true, openAIStreamingFixture(), replayStore)
	second := invokeWithIdempotencyKey(t, secondDeps, body, "stream-idem-over-limit")
	if second.Code != http.StatusConflict {
		t.Fatalf("replay miss status=%d want 409; body=%s", second.Code, second.Body.String())
	}
	if !strings.Contains(second.Body.String(), "replay_without_cache") {
		t.Fatalf("body=%q want replay_without_cache", second.Body.String())
	}
}

func TestStreamingIdempotencyReplayRecordsForwardErrorPartialStream(t *testing.T) {
	replayStore := billing.NewMemoryReplayStore()
	claimID := int64(77703)
	settler := &recordingSettler{}

	deps := streamingReplayDeps(t, claimID, false, "", replayStore)
	deps.Settler = settler
	scanners := gateway.NewStaticStreamScannerRegistry()
	scanners.MustRegister("openai_chat", scannerThenError{
		event: partialOpenAIStreamingEventBeforeReadError(),
		err:   io.ErrUnexpectedEOF,
	})
	deps.Forwarder.Scanners = scanners
	rec := invokeWithIdempotencyKey(t, deps, openAIStreamingRequestBody(), "stream-idem-forward-error")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Huakai-Forward-Error"); got != "" {
		t.Fatalf("X-Huakai-Forward-Error=%q want empty post-Forward dead header", got)
	}
	if len(settler.calls) != 1 {
		t.Fatalf("settle calls=%d want 1 for partial delivery with forward error", len(settler.calls))
	}
	if settler.calls[0].StreamAttempt == nil || !settler.calls[0].StreamAttempt.State.Chargeable() {
		t.Fatalf("partial delivery with forward error must commit a chargeable attempt; StreamAttempt=%#v", settler.calls[0].StreamAttempt)
	}
	if len(settler.aborts) != 0 {
		t.Fatalf("abort calls=%d want 0", len(settler.aborts))
	}
	stored, ok, err := replayStore.Lookup(context.Background(), validIdentity().TenantID, claimID)
	if err != nil {
		t.Fatalf("lookup replay: %v", err)
	}
	if !ok {
		t.Fatal("forward-error partial stream must be recorded after successful settlement")
	}
	if stored.ContentType != idempotencyReplayContentTypeSSE {
		t.Fatalf("stored ContentType=%q want %q", stored.ContentType, idempotencyReplayContentTypeSSE)
	}
	if string(stored.ResponseBody) != rec.Body.String() {
		t.Fatalf("stored body mismatch:\nstored=%s\nclient=%s", string(stored.ResponseBody), rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "partial") || strings.Contains(rec.Body.String(), "data: [DONE]") {
		t.Fatalf("fixture must deliver only partial SSE bytes; body=%s", rec.Body.String())
	}
}

func TestStreamingIdempotencyReplaySettlesAmbiguousUsageWithDeliveredContent(t *testing.T) {
	replayStore := billing.NewMemoryReplayStore()
	claimID := int64(77710)
	settler := &recordingSettler{}

	deps := streamingReplayDeps(t, claimID, false, "", replayStore)
	deps.Settler = settler
	scanners := gateway.NewStaticStreamScannerRegistry()
	scanners.MustRegister("openai_chat", scannerThenError{
		event: partialOpenAIStreamingEventBeforeReadError(),
		err:   errors.New("unknown stream termination"),
	})
	deps.Forwarder.Scanners = scanners

	rec := invokeWithIdempotencyKey(t, deps, openAIStreamingRequestBody(), "stream-idem-ambiguous-delivered")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Huakai-Forward-Error"); got != "" {
		t.Fatalf("X-Huakai-Forward-Error=%q want empty post-Forward dead header", got)
	}
	if len(settler.calls) != 1 {
		t.Fatalf("settle calls=%d want 1 for delivered ambiguous usage stream", len(settler.calls))
	}
	if len(settler.aborts) != 0 {
		t.Fatalf("abort calls=%d want 0 for delivered ambiguous usage stream", len(settler.aborts))
	}
	draft := settler.calls[0].Draft
	if draft.EndClass != gateway.AmbiguousUsage {
		t.Fatalf("EndClass=%q want %q", draft.EndClass, gateway.AmbiguousUsage)
	}
	if draft.DeliveredTokenCount <= 0 {
		t.Fatalf("DeliveredTokenCount=%d want >0 for delivered ambiguous usage stream", draft.DeliveredTokenCount)
	}
	if settler.calls[0].StreamAttempt == nil {
		t.Fatal("StreamAttempt missing")
	}
	if settler.calls[0].StreamAttempt.State.Chargeable() {
		t.Fatalf("ambiguous usage attempt must stay non-chargeable; StreamAttempt=%#v", settler.calls[0].StreamAttempt)
	}
	if settler.calls[0].StreamAttempt.DeliveredTokenCount <= 0 {
		t.Fatalf("StreamAttempt DeliveredTokenCount=%d want >0", settler.calls[0].StreamAttempt.DeliveredTokenCount)
	}
	stored, ok, err := replayStore.Lookup(context.Background(), validIdentity().TenantID, claimID)
	if err != nil {
		t.Fatalf("lookup replay: %v", err)
	}
	if !ok {
		t.Fatal("delivered ambiguous usage stream must record replay after settlement")
	}
	if stored.ContentType != idempotencyReplayContentTypeSSE {
		t.Fatalf("stored ContentType=%q want %q", stored.ContentType, idempotencyReplayContentTypeSSE)
	}
	if string(stored.ResponseBody) != rec.Body.String() {
		t.Fatalf("stored body mismatch:\nstored=%s\nclient=%s", string(stored.ResponseBody), rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "partial") || strings.Contains(rec.Body.String(), "data: [DONE]") {
		t.Fatalf("fixture must deliver only partial SSE bytes; body=%s", rec.Body.String())
	}
}

func TestStreamingForwardSettleAndAbortErrorsAreLoggedNotHeaders(t *testing.T) {
	t.Run("forward error after delivery", func(t *testing.T) {
		const marker = "SENSITIVE_STREAM_FORWARD_MARKER"
		logs := captureSlogForTest(t)
		deps := streamingReplayDeps(t, 77901, false, "", nil)
		scanners := gateway.NewStaticStreamScannerRegistry()
		scanners.MustRegister("openai_chat", scannerThenError{
			event: partialOpenAIStreamingEventBeforeReadError(),
			err:   errors.New(marker),
		})
		deps.Forwarder.Scanners = scanners

		rec := invokeHandler(t, deps, openAIStreamingRequestBody())
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("X-Huakai-Forward-Error"); got != "" {
			t.Fatalf("X-Huakai-Forward-Error=%q want empty", got)
		}
		assertLogContains(t, logs, "forward_failed", marker)
	})

	t.Run("settle error after delivery", func(t *testing.T) {
		const marker = "SENSITIVE_STREAM_SETTLE_MARKER"
		logs := captureSlogForTest(t)
		deps := streamingReplayDeps(t, 77902, false, openAIStreamingFixture(), nil)
		deps.Settler = &failingSettleSettler{err: errors.New(marker)}

		rec := invokeHandler(t, deps, openAIStreamingRequestBody())
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("X-Huakai-Settle-Error"); got != "" {
			t.Fatalf("X-Huakai-Settle-Error=%q want empty", got)
		}
		assertLogContains(t, logs, "settle_failed", marker)
	})

	t.Run("abort error after delivery", func(t *testing.T) {
		const marker = "SENSITIVE_STREAM_ABORT_MARKER"
		logs := captureSlogForTest(t)
		deps := streamingReplayDeps(t, 77903, false, zeroTokenOpenAIStreamingFixture(), nil)
		deps.Settler = &failingAbortSettler{err: errors.New(marker)}

		rec := invokeHandler(t, deps, openAIStreamingRequestBody())
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("X-Huakai-Abort-Failed"); got != "" {
			t.Fatalf("X-Huakai-Abort-Failed=%q want empty post-Forward dead header", got)
		}
		assertLogContains(t, logs, "abort_failed", marker)
	})
}

func TestStreamingIdempotencyReplayAbortsZeroByteForwardError(t *testing.T) {
	replayStore := billing.NewMemoryReplayStore()
	claimID := int64(77709)
	settler := &recordingSettler{}

	deps := streamingReplayDeps(t, claimID, false, "", replayStore)
	deps.Settler = settler
	scanners := gateway.NewStaticStreamScannerRegistry()
	scanners.MustRegister("openai_chat", scannerImmediateError{err: io.ErrUnexpectedEOF})
	deps.Forwarder.Scanners = scanners

	first := invokeWithIdempotencyKey(t, deps, openAIStreamingRequestBody(), "stream-idem-zero-byte-forward-error")
	if first.Code != http.StatusBadGateway {
		t.Fatalf("first status=%d want 502; body=%s", first.Code, first.Body.String())
	}
	if !strings.Contains(first.Body.String(), "stream_forward_error") {
		t.Fatalf("first body=%s want stream_forward_error", first.Body.String())
	}
	if got := first.Header().Get("X-Huakai-Forward-Error"); got != "" {
		t.Fatalf("X-Huakai-Forward-Error=%q want empty post-Forward dead header", got)
	}
	if len(settler.calls) != 0 {
		t.Fatalf("settle calls=%d want 0 for zero-byte forward error", len(settler.calls))
	}
	if len(settler.aborts) != 1 {
		t.Fatalf("abort calls=%d want 1 for zero-byte forward error", len(settler.aborts))
	}
	if got := settler.aborts[0].claimID; got != claimID {
		t.Fatalf("abort claimID=%d want %d", got, claimID)
	}
	if got := settler.aborts[0].reason; got != "upstream_5xx" {
		t.Fatalf("abort reason=%q want upstream_5xx", got)
	}
	if _, ok, err := replayStore.Lookup(context.Background(), validIdentity().TenantID, claimID); err != nil {
		t.Fatalf("lookup replay: %v", err)
	} else if ok {
		t.Fatal("zero-byte forward error must not record idempotency replay")
	}

	retryClaimID := claimID + 1
	retrySettler := &recordingSettler{}
	secondDeps := streamingReplayDeps(t, retryClaimID, false, openAIStreamingFixture(), replayStore)
	secondDeps.Settler = retrySettler
	second := invokeWithIdempotencyKey(t, secondDeps, openAIStreamingRequestBody(), "stream-idem-zero-byte-forward-error")
	if second.Code != http.StatusOK {
		t.Fatalf("retry status=%d want 200; body=%s", second.Code, second.Body.String())
	}
	if got := second.Header().Get("X-HUAKAI-Idempotency-Hit"); got != "" {
		t.Fatalf("retry idempotency-hit header=%q want empty after aborted first claim", got)
	}
	if !strings.Contains(second.Body.String(), "pong") {
		t.Fatalf("retry should dispatch upstream and return fresh stream; body=%s", second.Body.String())
	}
	if len(retrySettler.calls) != 1 {
		t.Fatalf("retry settle calls=%d want 1", len(retrySettler.calls))
	}
	if retrySettler.calls[0].ClaimID != retryClaimID {
		t.Fatalf("retry settle claimID=%d want %d", retrySettler.calls[0].ClaimID, retryClaimID)
	}
	if _, ok, err := replayStore.Lookup(context.Background(), validIdentity().TenantID, retryClaimID); err != nil {
		t.Fatalf("lookup retry replay: %v", err)
	} else if !ok {
		t.Fatal("retry chargeable stream must record replay after successful settlement")
	}
}

func TestStreamingIdempotencyReplayRecordsAfterClientCancelPostFlush(t *testing.T) {
	replayStore := &ctxSensitiveReplayStore{inner: billing.NewMemoryReplayStore()}
	body := openAIStreamingRequestBody()
	claimID := int64(77708)

	deps := streamingReplayDeps(t, claimID, false, openAIStreamingFixture(), replayStore)
	settler := &ctxSensitiveSettler{}
	deps.Settler = settler
	h := NewChatCompletionsHandler(deps)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "stream-idem-cancel-after-flush")
	rec := &cancelOnDoneFlushRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		cancel:           cancel,
	}

	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !rec.canceled {
		t.Fatal("fixture did not cancel request context after final SSE flush")
	}
	if got := rec.Header().Get("X-Huakai-Settle-Error"); got != "" {
		t.Fatalf("settle error=%q; settle context must outlive client cancellation", got)
	}
	if len(settler.calls) != 1 {
		t.Fatalf("settle calls=%d want 1", len(settler.calls))
	}
	stored, ok, err := replayStore.Lookup(context.Background(), validIdentity().TenantID, claimID)
	if err != nil {
		t.Fatalf("lookup replay: %v", err)
	}
	if !ok {
		t.Fatal("replay must be recorded even when client cancels after flushed SSE body")
	}
	if stored.ContentType != idempotencyReplayContentTypeSSE {
		t.Fatalf("stored ContentType=%q want %q", stored.ContentType, idempotencyReplayContentTypeSSE)
	}
	if string(stored.ResponseBody) != rec.Body.String() {
		t.Fatalf("stored body mismatch:\nstored=%s\nclient=%s", string(stored.ResponseBody), rec.Body.String())
	}
}

func TestStreamingIdempotencyReplaySkipsWithoutKeyOrStore(t *testing.T) {
	body := openAIStreamingRequestBody()

	t.Run("without key", func(t *testing.T) {
		replayStore := billing.NewMemoryReplayStore()
		claimID := int64(77704)
		deps := streamingReplayDeps(t, claimID, false, openAIStreamingFixture(), replayStore)
		rec := invokeHandler(t, deps, body)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
		}
		if _, ok, err := replayStore.Lookup(context.Background(), validIdentity().TenantID, claimID); err != nil {
			t.Fatalf("lookup replay: %v", err)
		} else if ok {
			t.Fatal("stream response without Idempotency-Key must not be recorded")
		}
	})

	t.Run("without store", func(t *testing.T) {
		deps := streamingReplayDeps(t, 77705, false, openAIStreamingFixture(), nil)
		rec := invokeWithIdempotencyKey(t, deps, body, "stream-idem-no-store")
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
		}
	})
}

func streamingReplayDeps(t *testing.T, claimID int64, hit bool, responseBody string, replayStore billing.ReplayStore) ChatHandlerDeps {
	t.Helper()
	deps := clientAdapterDeps(t)
	adapters := provider.NewStaticRegistry()
	adapters.MustRegister("openai_chat", &provideropenai.PassthroughAdapter{})
	deps.Dispatcher = &gateway.UpstreamDispatcher{
		Adapters:         adapters,
		TransportFactory: transport.NewFactory(),
		HTTPClient:       &recordingStreamingDoer{responseBody: responseBody},
	}
	deps.Forwarder = &gateway.StreamForwarder{
		ProtocolAdapters: gateway.BuildDefaultProtocolAdapterRegistry(),
		Scanners:         gateway.BuildDefaultStreamScannerRegistry(),
		Timeouts: gateway.TimeoutConfig{
			FirstTokenTimeout:  500 * time.Millisecond,
			InterEventTimeout:  500 * time.Millisecond,
			TotalStreamTimeout: 5 * time.Second,
			DrainMaxSeconds:    100 * time.Millisecond,
		},
		ScannerBufferCap: 1 << 20,
	}
	deps.ClaimGate = replayClaimGate{claimID: claimID, hit: hit}
	deps.ReplayStore = replayStore
	deps.Settler = &recordingSettler{}
	return deps
}

func inputOnlyInterruptedStreamDeps(t *testing.T, claimID int64, inputTokens int, resolver *billing.PolicyResolver) ChatHandlerDeps {
	t.Helper()
	deps := streamingReplayDeps(t, claimID, false, "", nil)
	deps.BillingPolicyResolver = resolver
	scanners := gateway.NewStaticStreamScannerRegistry()
	scanners.MustRegister("openai_chat", scannerThenError{
		event: inputOnlyOpenAIUsageEvent(inputTokens),
		err:   io.ErrUnexpectedEOF,
	})
	deps.Forwarder.Scanners = scanners
	return deps
}

func streamPolicyResolver(policy billing.StreamInputOnlyInterruptedPolicy) *billing.PolicyResolver {
	return billing.NewPolicyResolver(&streamPolicyStore{
		ok:     true,
		policy: policy,
	}, time.Minute)
}

type streamPolicyStore struct {
	ok     bool
	policy billing.StreamInputOnlyInterruptedPolicy
}

func (s *streamPolicyStore) Get(_ context.Context, tenantID int64, key string) (billing.StoredBillingSetting, bool, error) {
	if !s.ok || key != billing.StreamInputOnlyInterruptedPolicyKey {
		return billing.StoredBillingSetting{}, false, nil
	}
	return billing.StoredBillingSetting{
		ID:        1,
		TenantID:  tenantID,
		Key:       key,
		Value:     s.policy.String(),
		UpdatedAt: time.Now().UTC(),
		UpdatedBy: "test",
	}, true, nil
}

func (s *streamPolicyStore) UpsertStreamInputOnlyInterruptedPolicy(_ context.Context, tenantID int64, policy billing.StreamInputOnlyInterruptedPolicy, updatedBy string) (billing.StoredBillingSetting, error) {
	s.ok = true
	s.policy = policy
	return billing.StoredBillingSetting{
		ID:        1,
		TenantID:  tenantID,
		Key:       billing.StreamInputOnlyInterruptedPolicyKey,
		Value:     policy.String(),
		UpdatedAt: time.Now().UTC(),
		UpdatedBy: updatedBy,
	}, nil
}

func (s *streamPolicyStore) List(_ context.Context, tenantID int64) ([]billing.StoredBillingSetting, error) {
	if !s.ok {
		return nil, nil
	}
	return []billing.StoredBillingSetting{{
		ID:        1,
		TenantID:  tenantID,
		Key:       billing.StreamInputOnlyInterruptedPolicyKey,
		Value:     s.policy.String(),
		UpdatedAt: time.Now().UTC(),
		UpdatedBy: "test",
	}}, nil
}

func openAIStreamingRequestBody() string {
	return `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`
}

func inputOnlyOpenAIUsageEvent(inputTokens int) gateway.SSEEvent {
	tokens := strconv.Itoa(inputTokens)
	return gateway.SSEEvent{Data: []byte(`{"id":"chatcmpl-case-c","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}],"usage":{"prompt_tokens":` + tokens + `,"completion_tokens":0,"total_tokens":` + tokens + `}}`)}
}

func openAIStreamingFixture() string {
	return strings.Join([]string{
		`data: {"id":"chatcmpl-stream","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"pong"},"finish_reason":null}]}`,
		``,
		`data: [DONE]`,
		``,
		``,
	}, "\n")
}

func zeroTokenOpenAIStreamingFixture() string {
	return strings.Join([]string{
		`data: {"id":"chatcmpl-zero","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		``,
		`data: {"id":"chatcmpl-zero","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		``,
		`data: [DONE]`,
		``,
		``,
	}, "\n")
}

func openAIStreamingEOFNoTerminalFixture() string {
	return strings.Join([]string{
		`data: {"id":"chatcmpl-eof","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"partial"},"finish_reason":null}]}`,
		``,
		``,
	}, "\n")
}

func partialOpenAIStreamingFixtureBeforeReadError() string {
	return strings.Join([]string{
		`data: {"id":"chatcmpl-error","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"partial"},"finish_reason":null}]}`,
		``,
	}, "\n")
}

func partialOpenAIStreamingEventBeforeReadError() gateway.SSEEvent {
	return gateway.SSEEvent{
		Data: []byte(`{"id":"chatcmpl-error","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"partial"},"finish_reason":null}]}`),
	}
}

func oversizedOpenAIStreamingFixture(minBytes int) string {
	var b strings.Builder
	b.WriteString(`data: {"id":"chatcmpl-big","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}` + "\n\n")
	content := strings.Repeat("x", 2048)
	for b.Len() <= minBytes {
		b.WriteString(`data: {"id":"chatcmpl-big","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":`)
		b.WriteString(strconv.Quote(content))
		b.WriteString(`},"finish_reason":null}]}` + "\n\n")
	}
	b.WriteString(`data: {"id":"chatcmpl-big","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n")
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

var _ gateway.HTTPDoer = (*recordingStreamingDoer)(nil)
var _ gateway.HTTPDoer = (*erroringStreamingDoer)(nil)

type scannerThenError struct {
	event gateway.SSEEvent
	err   error
}

func (s scannerThenError) Scan(context.Context, io.Reader, int) iter.Seq2[gateway.SSEEvent, error] {
	return func(yield func(gateway.SSEEvent, error) bool) {
		if !yield(s.event, nil) {
			return
		}
		yield(gateway.SSEEvent{}, s.err)
	}
}

type scannerImmediateError struct {
	err error
}

func (s scannerImmediateError) Scan(context.Context, io.Reader, int) iter.Seq2[gateway.SSEEvent, error] {
	return func(yield func(gateway.SSEEvent, error) bool) {
		err := s.err
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		yield(gateway.SSEEvent{}, err)
	}
}

type cancelOnDoneFlushRecorder struct {
	*httptest.ResponseRecorder
	cancel   context.CancelFunc
	canceled bool
}

func (w *cancelOnDoneFlushRecorder) Flush() {
	w.ResponseRecorder.Flush()
	if !w.canceled && strings.Contains(w.Body.String(), "data: [DONE]") {
		w.canceled = true
		w.cancel()
	}
}

type ctxSensitiveReplayStore struct {
	inner *billing.MemoryReplayStore
}

func (s *ctxSensitiveReplayStore) Record(ctx context.Context, tenantID, claimID int64, status int, contentType string, body []byte, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.inner.Record(ctx, tenantID, claimID, status, contentType, body, ttl)
}

func (s *ctxSensitiveReplayStore) Lookup(ctx context.Context, tenantID, claimID int64) (*billing.ReplayRecord, bool, error) {
	return s.inner.Lookup(ctx, tenantID, claimID)
}

func (s *ctxSensitiveReplayStore) DeleteExpired(ctx context.Context) (int64, error) {
	return s.inner.DeleteExpired(ctx)
}

var _ billing.ReplayStore = (*ctxSensitiveReplayStore)(nil)

type ctxSensitiveSettler struct {
	recordingSettler
}

func (s *ctxSensitiveSettler) Settle(ctx context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.recordingSettler.Settle(ctx, req)
}

type failingSettleSettler struct {
	recordingSettler
	err error
}

func (s *failingSettleSettler) Settle(ctx context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
	_, _ = s.recordingSettler.Settle(ctx, req)
	return nil, s.err
}

func captureSlogForTest(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() {
		slog.SetDefault(prev)
	})
	return &buf
}

func assertLogContains(t *testing.T, logs *bytes.Buffer, wants ...string) {
	t.Helper()
	got := logs.String()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("log output=%s missing %q", got, want)
		}
	}
}
