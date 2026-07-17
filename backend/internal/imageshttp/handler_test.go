package imageshttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/openai"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementrecovery"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

func TestImagesHandler_PerImageCostUsesSizeQualityAndNExactlyOnce(t *testing.T) {
	env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{
		status: http.StatusOK,
		body:   `{"created":1,"data":[{"url":"https://img.test/a.png"},{"url":"https://img.test/b.png"}]}`,
	})
	env.rateTable.raw = imageRateTableFixture(2, 10, 1000)

	rec := env.invoke(t, `{"model":"dall-e-3","prompt":"paint a precise ledger","size":"1024x1792","quality":"hd","n":2}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	env.assertNoHangingClaims(t)
	if got := env.transport.path; got != "/v1/images/generations" {
		t.Fatalf("upstream path=%q want /v1/images/generations", got)
	}
	if got := len(env.settler.settles); got != 1 {
		t.Fatalf("settle calls=%d want 1", got)
	}
	want := decimal.RequireFromString("0.006")
	assertImagesDecimal(t, "reserve PredictedCost", env.claims.reserves[0].req.PredictedCost, want)
	assertImagesDecimal(t, "settle ActualCost", env.settler.settles[0].ActualCost, want)
	if env.settler.settles[0].Draft.TokensInput != 0 || env.settler.settles[0].Draft.TokensOutput != 0 {
		t.Fatalf("per-image settle tokens input/output=%d/%d want 0/0",
			env.settler.settles[0].Draft.TokensInput, env.settler.settles[0].Draft.TokensOutput)
	}
}

func TestImagesHandler_SettleDraftCarriesImageAuditColumns(t *testing.T) {
	env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{
		status: http.StatusOK,
		body:   `{"created":1,"data":[{"url":"https://img.test/a.png"},{"url":"https://img.test/b.png"}]}`,
	})
	env.rateTable.raw = imageRateTableFixture(2, 10, 1000)

	rec := env.invoke(t, `{"model":"dall-e-3","prompt":"audit columns","size":"1024x1024","quality":"standard","n":2}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if got := len(env.settler.settles); got != 1 {
		t.Fatalf("settle calls=%d want 1", got)
	}
	draft := env.settler.settles[0].Draft
	if draft.ImageCount != 2 {
		t.Fatalf("Draft.ImageCount=%d want 2", draft.ImageCount)
	}
	if draft.ImageSize == nil || *draft.ImageSize != "1024x1024" {
		t.Fatalf("Draft.ImageSize=%v want 1024x1024", draft.ImageSize)
	}
	var breakdown map[string]int
	if err := json.Unmarshal(draft.ImageSizeBreakdown, &breakdown); err != nil {
		t.Fatalf("ImageSizeBreakdown invalid JSON: %v payload=%s", err, string(draft.ImageSizeBreakdown))
	}
	if breakdown["1024x1024"] != 2 {
		t.Fatalf("ImageSizeBreakdown=%v want 1024x1024=2", breakdown)
	}
}

func TestImagesHandler_TokenImageSettlesReportedUsageNotReserveEstimate(t *testing.T) {
	env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{
		status: http.StatusOK,
		body:   `{"created":1,"data":[{"url":"https://img.test/g.png"}],"usage":{"input_tokens":7,"output_tokens":11,"input_tokens_details":{"image_tokens":3}}}`,
	})

	rec := env.invoke(t, `{"model":"gpt-image-1","prompt":"transparent icon","size":"1024x1024","background":"transparent"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	env.assertNoHangingClaims(t)
	if got := len(env.claims.reserves); got != 1 {
		t.Fatalf("reserve calls=%d want 1", got)
	}
	if got := len(env.settler.settles); got != 1 {
		t.Fatalf("settle calls=%d want 1", got)
	}
	assertImagesDecimal(t, "actual usage cost", env.settler.settles[0].ActualCost, decimal.RequireFromString("0.029"))
	if env.claims.reserves[0].req.PredictedCost.Equal(env.settler.settles[0].ActualCost) {
		t.Fatalf("token-image fixture is non-discriminating: reserve=%s actual=%s",
			env.claims.reserves[0].req.PredictedCost, env.settler.settles[0].ActualCost)
	}
	settle := env.settler.settles[0]
	if settle.Draft.TokensInput != 7 || settle.Draft.TokensOutput != 11 {
		t.Fatalf("settled tokens input/output=%d/%d want 7/11", settle.Draft.TokensInput, settle.Draft.TokensOutput)
	}
	if !strings.Contains(env.transport.body, `"background":"transparent"`) {
		t.Fatalf("raw passthrough body lost gpt-image field: %s", env.transport.body)
	}
}

func TestImagesHandler_TokenImageMissingUsageAbortsWithoutZeroSettle(t *testing.T) {
	env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{
		status: http.StatusOK,
		body:   `{"created":1,"data":[{"url":"https://img.test/g.png"}]}`,
	})

	rec := env.invoke(t, `{"model":"gpt-image-1","prompt":"no usage","size":"1024x1024"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s want 502", rec.Code, rec.Body.String())
	}
	env.assertNoHangingClaims(t)
	if got := len(env.settler.settles); got != 0 {
		t.Fatalf("settle calls=%d want 0 when token-image usage missing", got)
	}
	if got := len(env.settler.aborts); got != 1 {
		t.Fatalf("abort calls=%d want 1 when token-image usage missing", got)
	}
	if env.settler.aborts[0].reason != "usage_missing" {
		t.Fatalf("abort reason=%q want usage_missing", env.settler.aborts[0].reason)
	}
}

func TestImagesHandler_ModelCatalogValidationHappensBeforeReserve(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantCode int
	}{
		{name: "dall-e3 n too high", body: `{"model":"dall-e-3","prompt":"x","size":"1024x1024","n":2}`, wantCode: http.StatusBadRequest},
		{name: "dall-e2 n max ok", body: `{"model":"dall-e-2","prompt":"x","size":"512x512","n":10}`, wantCode: http.StatusOK},
		{name: "dall-e2 n too high", body: `{"model":"dall-e-2","prompt":"x","size":"512x512","n":11}`, wantCode: http.StatusBadRequest},
		{name: "dall-e3 size rejected", body: `{"model":"dall-e-3","prompt":"x","size":"512x512","n":1}`, wantCode: http.StatusBadRequest},
		{name: "dall-e2 size ok", body: `{"model":"dall-e-2","prompt":"x","size":"512x512","n":1}`, wantCode: http.StatusOK},
		// stream:true 透传上游会回 SSE → token 计费解析失败 → abort 假 502 → 退款,
		// 但 vendor 已扣费 = 漏钱;入口必须 400 拒绝(reserve 之前,零成本)。
		{name: "stream true rejected", body: `{"model":"dall-e-2","prompt":"x","size":"512x512","stream":true}`, wantCode: http.StatusBadRequest},
		{name: "stream false ok", body: `{"model":"dall-e-2","prompt":"x","size":"512x512","stream":false}`, wantCode: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{status: http.StatusOK, body: `{"created":1,"data":[{"url":"https://img.test/ok.png"}]}`})

			rec := env.invoke(t, tt.body)

			if rec.Code != tt.wantCode {
				t.Fatalf("status=%d body=%s want %d", rec.Code, rec.Body.String(), tt.wantCode)
			}
			if tt.wantCode != http.StatusOK {
				if got := len(env.claims.reserves); got != 0 {
					t.Fatalf("reserve calls=%d want 0 for pre-reserve validation failure", got)
				}
				if env.transport.called {
					t.Fatal("upstream called before rejecting invalid request")
				}
				return
			}
			env.assertNoHangingClaims(t)
		})
	}
}

func TestImagesHandler_PromptRulesAreEndpointSpecificAndCatalogDriven(t *testing.T) {
	longPrompt := strings.Repeat("a", 1001)
	tests := []struct {
		name     string
		endpoint imageEndpoint
		body     string
		wantCode int
	}{
		{name: "generation empty prompt", endpoint: imageEndpointGenerations, body: `{"model":"dall-e-2","prompt":"","size":"512x512"}`, wantCode: http.StatusBadRequest},
		{name: "variation no prompt ok", endpoint: imageEndpointVariations, body: `{"model":"dall-e-2","image_url":"https://img.test/source.png","size":"512x512"}`, wantCode: http.StatusOK},
		{name: "dall-e2 prompt over max", endpoint: imageEndpointGenerations, body: `{"model":"dall-e-2","prompt":"` + longPrompt + `","size":"512x512"}`, wantCode: http.StatusBadRequest},
		{name: "dall-e3 prompt over dall-e2 max ok", endpoint: imageEndpointGenerations, body: `{"model":"dall-e-3","prompt":"` + longPrompt + `","size":"1024x1024"}`, wantCode: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newImagesTestEnv(t, tt.endpoint, upstreamResponse{status: http.StatusOK, body: `{"created":1,"data":[{"url":"https://img.test/ok.png"}]}`})

			rec := env.invoke(t, tt.body)

			if rec.Code != tt.wantCode {
				t.Fatalf("status=%d body=%s want %d", rec.Code, rec.Body.String(), tt.wantCode)
			}
			if tt.wantCode != http.StatusOK {
				if got := len(env.claims.reserves); got != 0 {
					t.Fatalf("reserve calls=%d want 0 for invalid prompt", got)
				}
				return
			}
			env.assertNoHangingClaims(t)
		})
	}
}

func TestImagesHandler_Upstream5xxAbortsReservedClaim(t *testing.T) {
	env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{
		status: http.StatusInternalServerError,
		body:   `{"error":{"message":"upstream down"}}`,
	})

	rec := env.invoke(t, `{"model":"dall-e-2","prompt":"bill only on success","size":"512x512"}`)

	if rec.Code == http.StatusOK {
		t.Fatalf("status=%d body=%s want normalized upstream failure", rec.Code, rec.Body.String())
	}
	env.assertNoHangingClaims(t)
	if got := len(env.settler.settles); got != 0 {
		t.Fatalf("settle calls=%d want 0 on upstream failure", got)
	}
	if got := len(env.settler.aborts); got != 1 {
		t.Fatalf("abort calls=%d want 1 on upstream failure", got)
	}
}

func TestImagesHandler_Upstream2xxEmptyBodyAbortsReservedClaim(t *testing.T) {
	env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{
		status: http.StatusOK,
		body:   ``,
	})

	rec := env.invoke(t, `{"model":"dall-e-2","prompt":"empty upstream body","size":"512x512"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s want 502", rec.Code, rec.Body.String())
	}
	env.assertNoHangingClaims(t)
	if got := len(env.settler.settles); got != 0 {
		t.Fatalf("settle calls=%d want 0 on empty upstream body", got)
	}
	if got := len(env.settler.aborts); got != 1 {
		t.Fatalf("abort calls=%d want 1 on empty upstream body", got)
	}
}

func TestImagesHandler_SettleErrorKeepsDeliveredJSONAndEnqueuesRecovery(t *testing.T) {
	// 该测试反转旧终局：图片 JSON 完整交付后，结算失败只能进入恢复，不能把响应改成 500。
	// 变异：恢复 settle-before-write 后，状态变 500、业务体消失且恢复断言失败。
	env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{
		status: http.StatusOK,
		body:   `{"created":1,"data":[{"url":"https://img.test/ok.png"}]}`,
	})
	env.settler.settleErr = errors.New("settle backend down")
	recovery := &imagesRecoveryEnqueuer{}
	env.deps.SettleRecoveryDLQ = recovery

	rec := env.invoke(t, `{"model":"dall-e-2","prompt":"settle fails","size":"512x512"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != `{"created":1,"data":[{"url":"https://img.test/ok.png"}]}` {
		t.Fatalf("body=%s want complete image JSON", got)
	}
	if got := len(env.settler.settles); got != 1 {
		t.Fatalf("settle calls=%d want 1", got)
	}
	if got := len(env.settler.aborts); got != 0 {
		t.Fatalf("abort calls=%d want 0 after full delivery", got)
	}
	if recovery.calls != 1 || recovery.event.EventKind != dlq.EventKindPostDeliverySettlement {
		t.Fatalf("recovery calls/kind=%d/%q want 1/%q", recovery.calls, recovery.event.EventKind, dlq.EventKindPostDeliverySettlement)
	}
	payload, err := settlementrecovery.Decode(recovery.event.Payload)
	if err != nil {
		t.Fatalf("decode recovery payload: %v", err)
	}
	if payload.Source != settlementrecovery.SourceImagesDelivered {
		t.Fatalf("recovery source=%q want %q", payload.Source, settlementrecovery.SourceImagesDelivered)
	}
}

// TestImagesHandler_SettleAndRecoveryDoubleFailureEmitsP0 守住图片 money-path
// 双故障外部信号。变异：删除 EnqueueFailure 的 critical P0 事件后，本测试变红。
func TestImagesHandler_SettleAndRecoveryDoubleFailureEmitsP0(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	const secret = "IMAGE_DOUBLE_FAULT_SECRET"
	env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{
		status: http.StatusOK,
		body:   `{"created":1,"data":[{"url":"https://img.test/ok.png"}]}`,
	})
	env.settler.settleErr = errors.New("settle failed " + secret)
	recovery := &imagesRecoveryEnqueuer{err: errors.New("recovery enqueue failed " + secret)}
	env.deps.SettleRecoveryDLQ = recovery

	rec := env.invoke(t, `{"model":"dall-e-2","prompt":"double fault","size":"512x512"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200，响应已交付不能反悔", rec.Code, rec.Body.String())
	}
	if recovery.calls != 1 {
		t.Fatalf("recovery calls=%d want 1", recovery.calls)
	}
	got := logs.String()
	for _, want := range []string{"money_lost_double_fault", "critical", "P0", "imageshttp.settle_recovery"} {
		if !strings.Contains(got, want) {
			t.Fatalf("P0 log missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, secret) {
		t.Fatalf("P0 log leaked raw failure detail: %s", got)
	}
}

func TestImagesHandler_PartialWriteAbortsWithoutSettlement(t *testing.T) {
	// 该测试守住图片未完整交付不得计费：部分写后报错必须 Abort，且不能创建 post-delivery 恢复。
	// 变异：忽略 Write 的 n/err 会产生一次 Settle 且 Abort 为零，本测试必红。
	env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{
		status: http.StatusOK,
		body:   `{"created":1,"data":[{"url":"https://img.test/ok.png"}]}`,
	})
	recovery := &imagesRecoveryEnqueuer{}
	env.deps.SettleRecoveryDLQ = recovery
	w := &imagesPartialWriteResponseWriter{header: make(http.Header), limit: 7, err: io.ErrClosedPipe}

	env.invokeWithWriter(t, w, `{"model":"dall-e-2","prompt":"write fails","size":"512x512"}`)

	if got := len(env.settler.settles); got != 0 {
		t.Fatalf("settle calls=%d want 0 on partial write", got)
	}
	if got := len(env.settler.aborts); got != 1 || env.settler.aborts[0].reason != "client_response_write_error" {
		t.Fatalf("aborts=%+v want one client_response_write_error", env.settler.aborts)
	}
	if recovery.calls != 0 {
		t.Fatalf("recovery calls=%d want 0 for incomplete image body", recovery.calls)
	}
	if w.writeHeaderCalls != 0 {
		t.Fatalf("WriteHeader calls=%d want 0 before fallible body write", w.writeHeaderCalls)
	}
	if w.flushes != 0 {
		t.Fatalf("flushes=%d want 0 after incomplete image body", w.flushes)
	}
}

func TestImagesHandler_FullLengthWriteErrorIsConservativelyDelivered(t *testing.T) {
	// 完整长度与错误同时返回时交付结果不确定，保守结算且不得释放 hold。
	env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{
		status: http.StatusOK,
		body:   `{"created":1,"data":[{"url":"https://img.test/ok.png"}]}`,
	})
	recovery := &imagesRecoveryEnqueuer{}
	env.deps.SettleRecoveryDLQ = recovery
	w := &imagesPartialWriteResponseWriter{header: make(http.Header), limit: -1, err: io.ErrUnexpectedEOF}
	settleBeforeFlush := false
	env.settler.beforeSettle = func() { settleBeforeFlush = w.flushes == 0 }

	env.invokeWithWriter(t, w, `{"model":"dall-e-2","prompt":"uncertain full write","size":"512x512"}`)

	if got := len(env.settler.settles); got != 1 {
		t.Fatalf("settle calls=%d want 1", got)
	}
	if got := len(env.settler.aborts); got != 0 {
		t.Fatalf("abort calls=%d want 0", got)
	}
	if recovery.calls != 0 {
		t.Fatalf("recovery calls=%d want 0 after successful settlement", recovery.calls)
	}
	if w.flushes != 1 || settleBeforeFlush {
		t.Fatalf("flushes/settleBeforeFlush=%d/%v want 1/false", w.flushes, settleBeforeFlush)
	}
}

type imagesRecoveryEnqueuer struct {
	calls int
	event dlq.Event
	err   error
}

func (q *imagesRecoveryEnqueuer) Enqueue(_ context.Context, event dlq.Event) (int64, error) {
	q.calls++
	q.event = event
	return 1, q.err
}

type imagesPartialWriteResponseWriter struct {
	header           http.Header
	limit            int
	err              error
	writeHeaderCalls int
	flushes          int
}

func (w *imagesPartialWriteResponseWriter) Header() http.Header { return w.header }

func (w *imagesPartialWriteResponseWriter) WriteHeader(int) { w.writeHeaderCalls++ }

func (w *imagesPartialWriteResponseWriter) Write(p []byte) (int, error) {
	n := w.limit
	if n < 0 || n > len(p) {
		n = len(p)
	}
	return n, w.err
}

func (w *imagesPartialWriteResponseWriter) Flush() { w.flushes++ }

func TestImagesHandler_GroupRatioDiscountsReserveAndSettle(t *testing.T) {
	base := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{status: http.StatusOK, body: `{"created":1,"data":[{"url":"https://img.test/ok.png"}]}`})
	baseRec := base.invoke(t, `{"model":"dall-e-2","prompt":"ratio","size":"512x512","n":2}`)
	if baseRec.Code != http.StatusOK {
		t.Fatalf("baseline status=%d body=%s want 200", baseRec.Code, baseRec.Body.String())
	}
	discounted := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{status: http.StatusOK, body: `{"created":1,"data":[{"url":"https://img.test/ok.png"}]}`})
	discounted.deps.PricingRatioResolver = &pricingRatioResolverStub{ratio: decimal.RequireFromString("0.8")}
	discountedRec := discounted.invoke(t, `{"model":"dall-e-2","prompt":"ratio","size":"512x512","n":2}`)
	if discountedRec.Code != http.StatusOK {
		t.Fatalf("discounted status=%d body=%s want 200", discountedRec.Code, discountedRec.Body.String())
	}

	ratio := decimal.RequireFromString("0.8")
	assertImagesDecimal(t, "discounted reserve", discounted.claims.reserves[0].req.PredictedCost, base.claims.reserves[0].req.PredictedCost.Mul(ratio))
	assertImagesDecimal(t, "discounted settle", discounted.settler.settles[0].ActualCost, base.settler.settles[0].ActualCost.Mul(ratio))
	if !strings.Contains(discounted.settler.settles[0].Draft.CostSnapshot, "group_ratio=0.8") {
		t.Fatalf("CostSnapshot=%q want group_ratio=0.8", discounted.settler.settles[0].Draft.CostSnapshot)
	}
}

func TestImagesHandler_ResponseIsUpstreamBytesWithAllowedHeaders(t *testing.T) {
	env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{
		status: http.StatusOK,
		body:   `{"created":1,"data":[{"b64_json":"abc"}]}`,
		headers: http.Header{
			"Content-Type":         []string{"application/json"},
			"X-Request-Id":         []string{"upstream-req"},
			"Openai-Processing-Ms": []string{"123"},
			"X-Internal-Secret":    []string{"must-not-pass"},
			"Openai-Organization":  []string{"must-not-pass"},
			"Openai-Version":       []string{"2026-01-01"},
		},
	})

	rec := env.invoke(t, `{"model":"dall-e-2","prompt":"headers","size":"512x512","response_format":"b64_json"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != `{"created":1,"data":[{"b64_json":"abc"}]}` {
		t.Fatalf("body=%s want exact upstream bytes", got)
	}
	if !headerContains(rec.Header(), "X-Request-Id", "upstream-req") || rec.Header().Get("Openai-Processing-Ms") != "123" {
		t.Fatalf("allowed headers missing: %v", rec.Header())
	}
	if got := rec.Header().Get("X-Internal-Secret"); got != "" {
		t.Fatalf("blocked header propagated: %q", got)
	}
}

type imagesTestEnv struct {
	deps      Deps
	claims    *recordingClaimGate
	settler   *recordingSettler
	transport *recordingRoundTripper
	rateTable *rateTableStub
	endpoint  imageEndpoint
}

type upstreamResponse struct {
	status  int
	body    string
	headers http.Header
}

func newImagesTestEnv(t *testing.T, endpoint imageEndpoint, resp upstreamResponse) *imagesTestEnv {
	t.Helper()
	claims := &recordingClaimGate{nextClaimID: 9101}
	settler := &recordingSettler{}
	rt := &recordingRoundTripper{resp: resp}
	rates := &rateTableStub{raw: imageRateTableFixture(1, 10, 1000)}
	adapters := provider.NewStaticRegistry()
	adapters.MustRegister("openai_chat", &openai.PassthroughAdapter{})
	tf := transport.NewFactory()
	tf.SetStandard(rt)
	deps := Deps{
		Auth:                  authStub{ident: auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 13, UserGroup: "pro"}},
		Registry:              registryStub{},
		Router:                routerStub{},
		ClaimGate:             claims,
		RateTables:            rates,
		Selector:              selectorStub{},
		CredentialVault:       vaultStub{},
		Dispatcher:            &gateway.UpstreamDispatcher{Adapters: adapters, TransportFactory: tf},
		Settler:               settler,
		BillingPolicyResolver: billing.NewPolicyResolver(nil, 0),
		BillingPolicyVersion:  "test-policy",
		RequestClass:          "standard",
	}
	return &imagesTestEnv{deps: deps, claims: claims, settler: settler, transport: rt, rateTable: rates, endpoint: endpoint}
}

func (e *imagesTestEnv) invoke(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	var h http.HandlerFunc
	switch e.endpoint {
	case imageEndpointEdits:
		h = NewEditsHandler(e.deps)
	case imageEndpointVariations:
		h = NewVariationsHandler(e.deps)
	default:
		h = NewGenerationsHandler(e.deps)
	}
	req := httptest.NewRequest(http.MethodPost, e.endpoint.Path(), bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer hk-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	middleware.RequestID(h).ServeHTTP(rec, req)
	return rec
}

func (e *imagesTestEnv) invokeWithWriter(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()
	var h http.HandlerFunc
	switch e.endpoint {
	case imageEndpointEdits:
		h = NewEditsHandler(e.deps)
	case imageEndpointVariations:
		h = NewVariationsHandler(e.deps)
	default:
		h = NewGenerationsHandler(e.deps)
	}
	req := httptest.NewRequest(http.MethodPost, e.endpoint.Path(), bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer hk-test")
	req.Header.Set("Content-Type", "application/json")
	middleware.RequestID(h).ServeHTTP(w, req)
}

func (e *imagesTestEnv) assertNoHangingClaims(t *testing.T) {
	t.Helper()
	closed := map[int64]string{}
	for _, req := range e.settler.settles {
		closed[req.ClaimID] = "settled"
	}
	for _, req := range e.settler.aborts {
		if prior := closed[req.claimID]; prior != "" {
			t.Fatalf("claim %d closed twice: %s and aborted", req.claimID, prior)
		}
		closed[req.claimID] = "aborted"
	}
	for _, req := range e.claims.reserves {
		if got := closed[req.claimID]; got == "" {
			t.Fatalf("reserved claim %d was not settled or aborted", req.claimID)
		}
	}
}

type authStub struct {
	ident auth.Identity
	err   error
}

func (s authStub) Resolve(context.Context, *http.Request) (auth.Identity, error) {
	return s.ident, s.err
}

type registryStub struct{}

func (registryStub) ResolveModel(_ context.Context, model string, _ int64) (registry.Resolved, error) {
	return registry.Resolved{
		PublicAlias:      model,
		CanonicalModelID: "image/" + model,
		ProviderModelID:  model,
		ProtocolFamily:   "openai_chat",
		Capabilities:     []string{"image_output"},
		PoolCandidates:   []int64{101},
		SnapshotVersion:  "registry:7:1",
	}, nil
}

type routerStub struct{}

func (routerStub) Plan(_ context.Context, in router.PlanInput) (router.RoutePlan, error) {
	return router.RoutePlan{
		Attempts: []router.AttemptPlan{{
			Index:           0,
			PoolGroupID:     101,
			Reason:          "primary",
			UpstreamModelID: in.Model.ProviderModelID,
		}},
		AttemptBudget:   1,
		SnapshotVersion: "registry:7:1;router:test",
	}, nil
}

type recordingClaimGate struct {
	nextClaimID int64
	reserves    []reservedClaim
}

type reservedClaim struct {
	claimID int64
	req     billing.ReserveRequest
}

func (g *recordingClaimGate) Reserve(_ context.Context, req billing.ReserveRequest) (*billing.ReserveResult, error) {
	g.reserves = append(g.reserves, reservedClaim{claimID: g.nextClaimID, req: req})
	return &billing.ReserveResult{ClaimID: g.nextClaimID}, nil
}

type rateTableStub struct {
	raw json.RawMessage
}

func (s *rateTableStub) GetRateTable(context.Context, string) (billing.RateTable, error) {
	return billing.RateTable{Version: "test-policy", PricingData: s.raw}, nil
}

func (s *rateTableStub) GetRateTableSnapshot(context.Context, int64) (billing.RateTable, error) {
	return billing.RateTable{}, billing.ErrRateTableNotFound
}

func (s *rateTableStub) ListRateTableSnapshots(context.Context) ([]billing.RateTableSnapshot, error) {
	return nil, nil
}

type selectorStub struct{}

func (selectorStub) Select(context.Context, pool.SelectionRequest) (*pool.SelectionResult, error) {
	return &pool.SelectionResult{
		AccountID:         44,
		AcquisitionToken:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		RoutingReasonJSON: []byte(`{"reason":"test"}`),
	}, nil
}

type vaultStub struct{}

func (vaultStub) Resolve(context.Context, int64, int64) (provider.Credential, provider.AccountInfo, error) {
	return provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-test"}, provider.AccountInfo{
		AccountID:   44,
		TenantID:    7,
		Platform:    "openai",
		AccountType: "api_key",
	}, nil
}

type recordingSettler struct {
	settles      []billing.SettleRequest
	aborts       []abortCall
	settleErr    error
	beforeSettle func()
}

type abortCall struct {
	tenantID     int64
	claimID      int64
	reason       string
	protocolLoss json.RawMessage
}

func (s *recordingSettler) Settle(_ context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
	if s.beforeSettle != nil {
		s.beforeSettle()
	}
	s.settles = append(s.settles, req)
	if s.settleErr != nil {
		return nil, s.settleErr
	}
	return &billing.SettleResult{}, nil
}

func (s *recordingSettler) Abort(_ context.Context, tenantID, claimID int64, reason, _ string, _ int64, protocolLoss json.RawMessage) error {
	s.aborts = append(s.aborts, abortCall{tenantID: tenantID, claimID: claimID, reason: reason, protocolLoss: protocolLoss})
	return nil
}

func (s *recordingSettler) CommitCacheHit(context.Context, billing.SettleRequest) error {
	return nil
}

func (s *recordingSettler) Refund(context.Context, billing.RefundRequest) (*billing.RefundResult, error) {
	return nil, nil
}

type pricingRatioResolverStub struct {
	ratio decimal.Decimal
}

func (s *pricingRatioResolverStub) Resolve(context.Context, int64, int64) (decimal.Decimal, error) {
	if s == nil || s.ratio.IsZero() {
		return decimal.NewFromInt(1), nil
	}
	return s.ratio, nil
}

type recordingRoundTripper struct {
	mu     sync.Mutex
	resp   upstreamResponse
	called bool
	path   string
	auth   string
	body   string
	header http.Header
}

func (rt *recordingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	raw, _ := io.ReadAll(req.Body)
	rt.called = true
	rt.path = req.URL.Path
	rt.auth = req.Header.Get("Authorization")
	rt.body = string(raw)
	rt.header = req.Header.Clone()
	status := rt.resp.status
	if status == 0 {
		status = http.StatusOK
	}
	headers := rt.resp.headers.Clone()
	if headers == nil {
		headers = http.Header{"Content-Type": []string{"application/json"}}
	}
	return &http.Response{
		StatusCode: status,
		Header:     headers,
		Body:       io.NopCloser(strings.NewReader(rt.resp.body)),
		Request:    req,
	}, nil
}

func imageRateTableFixture(dallE3Max, dallE2Max int, promptMaxDallE2 int) json.RawMessage {
	return json.RawMessage(`{
		"providers": {
			"openai": {
				"models": {
					"dall-e-3": {
						"pricing_scheme": "per_image",
						"image_base_micro_usd": "1000",
						"image_size_multipliers": {"1024x1024": "1", "1024x1792": "2.0"},
						"image_quality_multipliers": {"standard": "1", "hd": "1.25", "hd@1024x1792": "1.5"},
						"image_amount_range": {"min": 1, "max": ` + intString(dallE3Max) + `},
						"image_prompt_max_chars": 4000
					},
					"dall-e-2": {
						"pricing_scheme": "per_image",
						"image_base_micro_usd": "500",
						"image_size_multipliers": {"256x256": "0.5", "512x512": "1", "1024x1024": "2"},
						"image_quality_multipliers": {"standard": "1"},
						"image_amount_range": {"min": 1, "max": ` + intString(dallE2Max) + `},
						"image_prompt_max_chars": ` + intString(promptMaxDallE2) + `
					},
					"gpt-image-1": {
						"pricing_scheme": "token_image",
						"input_micro_usd": "1000",
						"output_micro_usd": "2000",
						"image_output_token_upper_bound": {"1024x1024": 100},
						"image_size_multipliers": {"1024x1024": "1"},
						"image_quality_multipliers": {"standard": "1", "hd": "1.2"},
						"image_amount_range": {"min": 1, "max": 4},
						"image_prompt_max_chars": 4000
					}
				}
			}
		}
	}`)
}

func intString(v int) string {
	return decimal.NewFromInt(int64(v)).String()
}

func assertImagesDecimal(t *testing.T, field string, got, want decimal.Decimal) {
	t.Helper()
	if !got.Equal(want) {
		t.Fatalf("%s=%s want %s", field, got, want)
	}
}

func headerContains(h http.Header, key, want string) bool {
	for _, got := range h.Values(key) {
		if got == want {
			return true
		}
	}
	return false
}
