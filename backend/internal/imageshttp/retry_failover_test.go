package imageshttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/rate"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/upstreamfeedback"
)

func TestImages500RetriesSecondAccountAndSettlesOnce(t *testing.T) {
	env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{})
	claims := &imageRetryClaimLifecycle{claimID: 8301}
	selector := &imageRetrySelector{accounts: []int64{44, 45}}
	dispatcher := &imageRetryDispatcher{steps: []imageRetryResponse{
		{status: http.StatusInternalServerError, body: `{"error":"upstream busy"}`},
		{status: http.StatusOK, body: successfulImageBody()},
	}}
	health := &imageHealthSpy{}
	env.deps.Router = imageRetryRouter{}
	env.deps.Selector = selector
	env.deps.CredentialVault = imageRetryVault(t, "openai", 44, 45)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims
	env.deps.Feedback = upstreamfeedback.NewObserver(upstreamfeedback.Dependencies{ChannelHealth: health})
	env.deps.PricingRatioResolver = imagePoolRatioResolver{}
	claims.beforeSettle = func() {
		if len(health.signals) != 2 || health.signals[1].Class != channelhealth.SignalSuccess {
			t.Fatalf("settle 前 health signals=%+v want final success already recorded", health.signals)
		}
	}

	rec := env.invoke(t, `{"model":"dall-e-3","prompt":"fail over safely","size":"1024x1024"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200 after second account succeeds", rec.Code, rec.Body.String())
	}
	if len(selector.requests) != 2 {
		t.Fatalf("selector calls=%d want 2", len(selector.requests))
	}
	if _, excluded := selector.requests[1].ExcludedAccounts[44]; !excluded {
		t.Fatalf("second attempt exclusions=%v want failed account 44", selector.requests[1].ExcludedAccounts)
	}
	if got := dispatcher.accounts; len(got) != 2 || got[0] != 44 || got[1] != 45 {
		t.Fatalf("dispatcher accounts=%v want [44 45]", got)
	}
	if len(claims.reserves) != 2 || claims.reserves[0].LogicalRequestID != claims.reserves[1].LogicalRequestID {
		t.Fatalf("reserves=%+v want two calls with one logical request", claims.reserves)
	}
	if claims.reserves[0].PoolingGroupID != 101 || claims.reserves[1].PoolingGroupID != 202 ||
		!claims.reserves[0].PredictedCost.Equal(claims.reserves[1].PredictedCost.Mul(decimal.NewFromInt(2))) {
		t.Fatalf("reserve pool/cost=%d/%s then %d/%s want second attempt pool ratio repricing",
			claims.reserves[0].PoolingGroupID, claims.reserves[0].PredictedCost,
			claims.reserves[1].PoolingGroupID, claims.reserves[1].PredictedCost)
	}
	if len(claims.aborts) != 1 || claims.aborts[0].reason != "upstream_5xx" {
		t.Fatalf("aborts=%+v want one upstream_5xx abort", claims.aborts)
	}
	if len(claims.settles) != 1 || claims.settles[0].AccountID != 45 || claims.settles[0].AttemptSeq != 2 {
		t.Fatalf("settles=%+v want account 45 attempt 2", claims.settles)
	}
	if len(health.signals) != 2 ||
		health.signals[0].Class != channelhealth.SignalUpstream5xx ||
		health.signals[1].Class != channelhealth.SignalSuccess {
		t.Fatalf("health signals=%+v want upstream_5xx then success", health.signals)
	}
}

func TestImagesCredentialMismatchSkipsDispatchAndUsesNextAccount(t *testing.T) {
	env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{})
	claims := &imageRetryClaimLifecycle{claimID: 9110}
	selector := &imageRetrySelector{accounts: []int64{44, 45}}
	dispatcher := &imageRetryDispatcher{steps: []imageRetryResponse{{status: http.StatusOK, body: successfulImageBody()}}}
	env.deps.Router = imageRetryRouter{}
	env.deps.Selector = selector
	env.deps.CredentialVault = imageCompatibilityVault(t)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims

	rec := env.invoke(t, `{"model":"dall-e-3","prompt":"compatibility failover","n":1,"size":"1024x1024"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if len(selector.requests) != 2 {
		t.Fatalf("selector calls=%d want 2", len(selector.requests))
	}
	if _, excluded := selector.requests[1].ExcludedAccounts[44]; !excluded {
		t.Fatalf("second exclusions=%v want account 44", selector.requests[1].ExcludedAccounts)
	}
	if len(dispatcher.accounts) != 1 || dispatcher.accounts[0] != 45 {
		t.Fatalf("dispatcher accounts=%v want [45]", dispatcher.accounts)
	}
	if len(claims.aborts) != 1 || claims.aborts[0].reason != "credential_protocol_incompatible" {
		t.Fatalf("aborts=%+v want one credential_protocol_incompatible", claims.aborts)
	}
	if len(claims.settles) != 1 || claims.settles[0].AccountID != 45 {
		t.Fatalf("settles=%+v want only account 45", claims.settles)
	}
	if strings.Contains(rec.Body.String(), "wrong-images-secret") {
		t.Fatal("response leaked mismatched credential")
	}
}

func TestImagesEmpty400DoesNotRetry(t *testing.T) {
	env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{})
	claims := &imageRetryClaimLifecycle{claimID: 8302}
	selector := &imageRetrySelector{accounts: []int64{44, 45}}
	dispatcher := &imageRetryDispatcher{steps: []imageRetryResponse{{
		status: http.StatusBadRequest,
		body:   `{"error":{"message":"policy-match"}}`,
	}}}
	env.deps.Router = imageRetryRouter{}
	env.deps.Selector = selector
	env.deps.CredentialVault = imageRetryVault(t, "openai", 44, 45)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims
	env.deps.Feedback = imageProjectionObserver()

	rec := env.invoke(t, `{"model":"dall-e-3","prompt":"terminal client error","size":"1024x1024"}`)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s want 422", rec.Code, rec.Body.String())
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"error":{"code":"account_busy","message":"账号暂不可用"}}` {
		t.Fatalf("投影错误响应=%s", got)
	}
	if len(selector.requests) != 1 || len(dispatcher.accounts) != 1 || len(claims.reserves) != 1 {
		t.Fatalf("selector/dispatcher/reserve calls=%d/%d/%d want 1/1/1",
			len(selector.requests), len(dispatcher.accounts), len(claims.reserves))
	}
	if len(claims.aborts) != 1 || claims.aborts[0].reason != "upstream_client_4xx" {
		t.Fatalf("客户端投影不得改写终态分类: %+v", claims.aborts)
	}
}

type imageErrorPolicyProvider struct{}

func (imageErrorPolicyProvider) GetAccountErrorPolicy(int64) rate.AccountErrorPolicy {
	clientStatus := http.StatusUnprocessableEntity
	affectHealth := false
	return rate.AccountErrorPolicy{Rules: []rate.TempUnschedulableRule{{
		RuleID: "busy-400", ErrorCode: http.StatusBadRequest, Keywords: []string{"policy-match"},
		DurationMinutes: 5, ClientStatus: &clientStatus, ClientCode: "account_busy",
		MessageMode: "custom", ClientMessage: "账号暂不可用", AffectHealth: &affectHealth,
	}}}
}

func imageProjectionObserver() *upstreamfeedback.Observer {
	return upstreamfeedback.NewObserver(upstreamfeedback.Dependencies{
		RateService: rate.NewUpstreamRateService(time.Now, time.Minute,
			rate.WithAccountErrorRulesProvider(imageErrorPolicyProvider{})),
	})
}

func TestImagesAbortFailureStopsBeforeRetry(t *testing.T) {
	env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{})
	claims := &imageRetryClaimLifecycle{claimID: 8303, abortErr: context.DeadlineExceeded}
	selector := &imageRetrySelector{accounts: []int64{44, 45}}
	dispatcher := &imageRetryDispatcher{steps: []imageRetryResponse{
		{status: http.StatusInternalServerError, body: `{"error":"upstream busy"}`},
		{status: http.StatusOK, body: successfulImageBody()},
	}}
	env.deps.Router = imageRetryRouter{}
	env.deps.Selector = selector
	env.deps.CredentialVault = imageRetryVault(t, "openai", 44, 45)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims

	rec := env.invoke(t, `{"model":"dall-e-3","prompt":"do not overlap claims","size":"1024x1024"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s want 502", rec.Code, rec.Body.String())
	}
	if len(selector.requests) != 1 || len(dispatcher.accounts) != 1 || len(claims.reserves) != 1 {
		t.Fatalf("selector/dispatcher/reserve calls=%d/%d/%d want 1/1/1",
			len(selector.requests), len(dispatcher.accounts), len(claims.reserves))
	}
	if rec.Header().Get("X-Huakai-Abort-Failed") == "" {
		t.Fatal("abort failure must remain observable in response headers")
	}
}

func TestImages401UsesSingleAuthFailoverBeyondAttemptBudget(t *testing.T) {
	env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{})
	claims := &imageRetryClaimLifecycle{claimID: 8304}
	selector := &imageRetrySelector{accounts: []int64{44, 45}}
	dispatcher := &imageRetryDispatcher{steps: []imageRetryResponse{
		{status: http.StatusUnauthorized, body: ""},
		{status: http.StatusOK, body: successfulImageBody()},
	}}
	env.deps.Router = imageSingleAttemptRouter{}
	env.deps.Selector = selector
	env.deps.CredentialVault = imageRetryVault(t, "openai", 44, 45)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims

	rec := env.invoke(t, `{"model":"dall-e-3","prompt":"refresh and fail over once","size":"1024x1024"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200 after auth failover", rec.Code, rec.Body.String())
	}
	if len(selector.requests) != 2 || len(dispatcher.accounts) != 2 || len(claims.reserves) != 2 {
		t.Fatalf("selector/dispatcher/reserve calls=%d/%d/%d want 2/2/2",
			len(selector.requests), len(dispatcher.accounts), len(claims.reserves))
	}
	if len(claims.aborts) != 1 || claims.aborts[0].reason != "upstream_credential_rejected" {
		t.Fatalf("aborts=%+v want one upstream_credential_rejected", claims.aborts)
	}
}

func TestImagesTenantRetryBudgetStopsSecondAttempt(t *testing.T) {
	env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{})
	claims := &imageRetryClaimLifecycle{claimID: 8309}
	selector := &imageRetrySelector{accounts: []int64{44, 45}}
	dispatcher := &imageRetryDispatcher{steps: []imageRetryResponse{
		{status: http.StatusInternalServerError, body: `{"error":"upstream busy"}`},
		{status: http.StatusOK, body: successfulImageBody()},
	}}
	budget := &imageDenyRetryBudget{}
	env.deps.Router = imageRetryRouter{}
	env.deps.Selector = selector
	env.deps.CredentialVault = imageRetryVault(t, "openai", 44, 45)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims
	env.deps.RetryBudget = budget

	rec := env.invoke(t, `{"model":"dall-e-3","prompt":"tenant budget exhausted","size":"1024x1024"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s want 502", rec.Code, rec.Body.String())
	}
	if len(selector.requests) != 1 || len(dispatcher.accounts) != 1 || len(claims.reserves) != 1 {
		t.Fatalf("selector/dispatcher/reserve calls=%d/%d/%d want 1/1/1",
			len(selector.requests), len(dispatcher.accounts), len(claims.reserves))
	}
	if len(budget.tenants) != 1 || budget.tenants[0] != 7 {
		t.Fatalf("retry budget tenants=%v want [7]", budget.tenants)
	}
}

func TestImagesKeepaliveWritePreventsAutomaticRetry(t *testing.T) {
	env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{})
	claims := &imageRetryClaimLifecycle{claimID: 8305}
	selector := &imageRetrySelector{accounts: []int64{44, 45}}
	dispatcher := &imageRetryDispatcher{steps: []imageRetryResponse{
		{status: http.StatusInternalServerError, body: `{"error":"slow failure"}`, delay: 20 * time.Millisecond},
		{status: http.StatusOK, body: successfulImageBody()},
	}}
	env.deps.Router = imageRetryRouter{}
	env.deps.Selector = selector
	env.deps.CredentialVault = imageRetryVault(t, "openai", 44, 45)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims
	env.deps.NonStreamKeepAliveInterval = time.Millisecond

	rec := env.invoke(t, `{"model":"dall-e-3","prompt":"keepalive boundary","size":"1024x1024"}`)

	if len(dispatcher.accounts) != 1 || len(selector.requests) != 1 || len(claims.reserves) != 1 {
		t.Fatalf("keepalive 后仍发生换号: dispatcher/selector/reserve=%d/%d/%d want 1/1/1",
			len(dispatcher.accounts), len(selector.requests), len(claims.reserves))
	}
	if !strings.Contains(rec.Body.String(), `"error"`) {
		t.Fatalf("body=%q want terminal error body after keepalive", rec.Body.String())
	}
}

func TestReplicateImages5xxDoesNotDuplicatePaidTask(t *testing.T) {
	env := newReplicateImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{})
	claims := &imageRetryClaimLifecycle{claimID: 8306}
	selector := &imageRetrySelector{accounts: []int64{44, 45}}
	dispatcher := &imageRetryDispatcher{steps: []imageRetryResponse{
		{status: http.StatusInternalServerError, body: `{"id":"pred-5xx","status":"processing","error":"provider failed after submission"}`},
		{status: http.StatusOK, body: `{"status":"succeeded","output":"https://r.test/out.webp"}`},
	}}
	env.deps.Router = imageRetryRouterWithManualFallback{model: replicateTestModel}
	env.deps.Selector = selector
	env.deps.CredentialVault = imageRetryVault(t, "replicate", 44, 45)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims

	rec := env.invoke(t, `{"model":"flux-pro","prompt":"do not duplicate task","size":"1024x1024"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s want 502", rec.Code, rec.Body.String())
	}
	if len(dispatcher.accounts) != 1 || len(claims.reserves) != 1 {
		t.Fatalf("replicate 5xx duplicated upstream task: dispatch/reserve=%d/%d want 1/1",
			len(dispatcher.accounts), len(claims.reserves))
	}
	if len(dispatcher.cancelInputs) != 1 {
		t.Fatalf("replicate 5xx cancel requests=%d want 1", len(dispatcher.cancelInputs))
	}
	if len(claims.aborts) != 1 ||
		!strings.Contains(string(claims.aborts[0].protocolLoss), "pred-5xx") ||
		!strings.Contains(string(claims.aborts[0].protocolLoss), "cancel_issued") {
		t.Fatalf("replicate 5xx aborts=%+v want prediction id and cancel outcome", claims.aborts)
	}
}

func TestReplicateImages429CanFailOverBeforeTaskCreation(t *testing.T) {
	env := newReplicateImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{})
	claims := &imageRetryClaimLifecycle{claimID: 8307}
	selector := &imageRetrySelector{accounts: []int64{44, 45}}
	dispatcher := &imageRetryDispatcher{steps: []imageRetryResponse{
		{status: http.StatusTooManyRequests, body: `{"error":"rate limited"}`},
		{status: http.StatusOK, body: `{"status":"succeeded","output":"https://r.test/out.webp"}`},
	}}
	env.deps.Router = imageRetryRouterForModel(replicateTestModel)
	env.deps.Selector = selector
	env.deps.CredentialVault = imageRetryVault(t, "replicate", 44, 45)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims

	rec := env.invoke(t, `{"model":"flux-pro","prompt":"rate limit failover","size":"1024x1024"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200 after 429 failover", rec.Code, rec.Body.String())
	}
	if len(dispatcher.accounts) != 2 || len(claims.settles) != 1 || claims.settles[0].AccountID != 45 {
		t.Fatalf("dispatch/settle=%v/%+v want two accounts and final account 45", dispatcher.accounts, claims.settles)
	}
}

func TestReplicateImages429WithPredictionStopsWhenCancelFails(t *testing.T) {
	env := newReplicateImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{})
	claims := &imageRetryClaimLifecycle{claimID: 8310}
	selector := &imageRetrySelector{accounts: []int64{44, 45}}
	dispatcher := &imageRetryDispatcher{cancelErr: errors.New("cancel timeout"), steps: []imageRetryResponse{
		{status: http.StatusTooManyRequests, body: `{"id":"pred-429-failed-cancel","status":"processing","error":"rate limited"}`},
		{status: http.StatusOK, body: `{"status":"succeeded","output":"https://r.test/out.webp"}`},
	}}
	env.deps.Router = imageRetryRouterForModel(replicateTestModel)
	env.deps.Selector = selector
	env.deps.CredentialVault = imageRetryVault(t, "replicate", 44, 45)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims

	rec := env.invoke(t, `{"model":"flux-pro","prompt":"cancel before failover","size":"1024x1024"}`)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503", rec.Code, rec.Body.String())
	}
	if len(dispatcher.accounts) != 1 || len(claims.reserves) != 1 {
		t.Fatalf("cancel 失败后仍换号: dispatch/reserve=%d/%d want 1/1",
			len(dispatcher.accounts), len(claims.reserves))
	}
	if len(claims.aborts) != 1 ||
		!strings.Contains(string(claims.aborts[0].protocolLoss), "pred-429-failed-cancel") ||
		!strings.Contains(string(claims.aborts[0].protocolLoss), "cancel_send_failed") {
		t.Fatalf("aborts=%+v want failed cancel evidence", claims.aborts)
	}
}

func TestReplicateImages429WithPredictionCanFailOverAfterCancel(t *testing.T) {
	env := newReplicateImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{})
	claims := &imageRetryClaimLifecycle{claimID: 8311}
	selector := &imageRetrySelector{accounts: []int64{44, 45}}
	dispatcher := &imageRetryDispatcher{steps: []imageRetryResponse{
		{status: http.StatusTooManyRequests, body: `{"id":"pred-429-canceled","status":"processing","error":"rate limited"}`},
		{status: http.StatusOK, body: `{"status":"succeeded","output":"https://r.test/out.webp"}`},
	}}
	env.deps.Router = imageRetryRouterForModel(replicateTestModel)
	env.deps.Selector = selector
	env.deps.CredentialVault = imageRetryVault(t, "replicate", 44, 45)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims

	rec := env.invoke(t, `{"model":"flux-pro","prompt":"cancel then fail over","size":"1024x1024"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if len(dispatcher.cancelInputs) != 1 || len(dispatcher.accounts) != 2 ||
		len(claims.settles) != 1 || claims.settles[0].AccountID != 45 {
		t.Fatalf("cancel/dispatch/settle=%d/%v/%+v want 1/two/final account 45",
			len(dispatcher.cancelInputs), dispatcher.accounts, claims.settles)
	}
}

func TestImagesUsesReReservedAttemptSequenceAcrossRequests(t *testing.T) {
	env := newImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{})
	claims := &imageRetryClaimLifecycle{claimID: 8308, status: "aborted", attemptSeq: 4}
	selector := &imageRetrySelector{accounts: []int64{44}}
	dispatcher := &imageRetryDispatcher{steps: []imageRetryResponse{{
		status: http.StatusOK,
		body:   successfulImageBody(),
	}}}
	env.deps.Selector = selector
	env.deps.CredentialVault = imageRetryVault(t, "openai", 44)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims

	rec := env.invoke(t, `{"model":"dall-e-3","prompt":"resume aborted claim","size":"1024x1024"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if len(selector.requests) != 1 || selector.requests[0].AttemptSeq != 5 {
		t.Fatalf("selector requests=%+v want authoritative attempt_seq 5", selector.requests)
	}
	if len(claims.settles) != 1 || claims.settles[0].AttemptSeq != 5 {
		t.Fatalf("settles=%+v want authoritative attempt_seq 5", claims.settles)
	}
}

func successfulImageBody() string {
	return `{"created":1,"data":[{"url":"https://img.test/out.png"}]}`
}

type imageRetryRouter struct{}

func (imageRetryRouter) Plan(ctx context.Context, in router.PlanInput) (router.RoutePlan, error) {
	return imageRetryRouterForModel("dall-e-3").Plan(ctx, in)
}

type imageRetryRouterForModel string

func (m imageRetryRouterForModel) Plan(context.Context, router.PlanInput) (router.RoutePlan, error) {
	return router.RoutePlan{
		Attempts: []router.AttemptPlan{
			{Index: 0, PoolGroupID: 101, UpstreamModelID: string(m), Reason: "primary"},
			{Index: 1, PoolGroupID: 202, UpstreamModelID: string(m), Reason: "account_failover"},
		},
		AttemptBudget: 2,
		RetryableEndClasses: []string{
			string(gateway.UpstreamError5xx),
			string(gateway.UpstreamRateLimit),
			string(gateway.FirstTokenTimeout),
			string(gateway.InterEventTimeout),
		},
		SnapshotVersion: "registry:7:1;router:image-retry-test",
	}, nil
}

type imageRetryRouterWithManualFallback struct{ model string }

func (r imageRetryRouterWithManualFallback) Plan(ctx context.Context, in router.PlanInput) (router.RoutePlan, error) {
	plan, err := imageRetryRouterForModel(r.model).Plan(ctx, in)
	if err != nil {
		return router.RoutePlan{}, err
	}
	plan.FallbackPhases = []router.FallbackPhasePlan{{
		FallbackClass: bindingfallback.ClassManual,
		Attempts: []router.AttemptPlan{{
			Index: 0, PoolGroupID: 303, UpstreamModelID: r.model, Reason: "manual_fallback",
		}},
		AttemptBudget: 1,
	}}
	return plan, nil
}

type imageSingleAttemptRouter struct{}

func (imageSingleAttemptRouter) Plan(context.Context, router.PlanInput) (router.RoutePlan, error) {
	return router.RoutePlan{
		Attempts: []router.AttemptPlan{{
			Index: 0, PoolGroupID: 101, UpstreamModelID: "dall-e-3", Reason: "primary",
		}},
		AttemptBudget:   1,
		SnapshotVersion: "registry:7:1;router:image-auth-test",
	}, nil
}

type imageRetrySelector struct {
	accounts []int64
	requests []pool.SelectionRequest
}

type imageDenyRetryBudget struct {
	tenants []int64
}

func (b *imageDenyRetryBudget) Allow(tenantID int64) bool {
	b.tenants = append(b.tenants, tenantID)
	return false
}

type imagePoolRatioResolver struct{}

func (imagePoolRatioResolver) Resolve(_ context.Context, _, poolGroupID int64) (decimal.Decimal, error) {
	if poolGroupID == 202 {
		return decimal.RequireFromString("0.5"), nil
	}
	return decimal.NewFromInt(1), nil
}

func (s *imageRetrySelector) Select(_ context.Context, req pool.SelectionRequest) (*pool.SelectionResult, error) {
	s.requests = append(s.requests, req)
	for _, accountID := range s.accounts {
		if _, excluded := req.ExcludedAccounts[accountID]; excluded {
			continue
		}
		return &pool.SelectionResult{
			AccountID:         accountID,
			AcquisitionToken:  uuid.New(),
			RoutingReasonJSON: []byte(`{"reason":"image-retry-test"}`),
		}, nil
	}
	return nil, pool.ErrNoEligibleAccount
}

func imageRetryVault(t *testing.T, platform string, accountIDs ...int64) provider.CredentialVault {
	t.Helper()
	vault := provider.NewStaticVault()
	for _, accountID := range accountIDs {
		if err := vault.Set(accountID, provider.Credential{
			Type:  provider.CredentialTypeAPIKey,
			Value: "image-retry-secret",
		}, provider.AccountInfo{
			AccountID:           accountID,
			TenantID:            7,
			Platform:            platform,
			AccountType:         "api_key",
			AccountCredentialID: 9000 + accountID,
			CredentialVersion:   1,
		}); err != nil {
			t.Fatalf("vault.Set(%d): %v", accountID, err)
		}
	}
	return vault
}

func imageCompatibilityVault(t *testing.T) provider.CredentialVault {
	t.Helper()
	vault := provider.NewStaticVault()
	rows := []struct {
		id, credentialID int64
		platform, secret string
	}{{44, 9044, "gemini", "wrong-images-secret"}, {45, 9045, "openai", "right-images-secret"}}
	for _, row := range rows {
		if err := vault.Set(row.id, provider.Credential{Type: provider.CredentialTypeAPIKey, Value: row.secret}, provider.AccountInfo{
			AccountID: row.id, TenantID: 7, Platform: row.platform, AccountType: "api_key",
			AccountCredentialID: row.credentialID, CredentialVersion: 1,
		}); err != nil {
			t.Fatalf("vault.Set(%d): %v", row.id, err)
		}
	}
	return vault
}

type imageRetryResponse struct {
	status int
	body   string
	delay  time.Duration
	err    error
}

type imageRetryDispatcher struct {
	steps        []imageRetryResponse
	accounts     []int64
	cancelInputs []gateway.DispatchInput
	cancelStatus int
	cancelErr    error
}

func (d *imageRetryDispatcher) Dispatch(ctx context.Context, in gateway.DispatchInput) (*gateway.DispatchResult, error) {
	if strings.HasSuffix(in.EndpointPath, "/cancel") {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		d.cancelInputs = append(d.cancelInputs, in)
		if d.cancelErr != nil {
			return nil, d.cancelErr
		}
		status := d.cancelStatus
		if status == 0 {
			status = http.StatusOK
		}
		return &gateway.DispatchResult{
			StatusCode: status, Headers: make(http.Header), UpstreamReader: http.NoBody,
			Close: func() error { return nil },
		}, nil
	}
	d.accounts = append(d.accounts, in.Account.AccountID)
	step := imageRetryResponse{status: http.StatusOK, body: successfulImageBody()}
	if len(d.accounts) <= len(d.steps) {
		step = d.steps[len(d.accounts)-1]
	}
	if step.delay > 0 {
		time.Sleep(step.delay)
	}
	if step.err != nil {
		return nil, step.err
	}
	return &gateway.DispatchResult{
		StatusCode:     step.status,
		Headers:        http.Header{"Content-Type": []string{"application/json"}},
		UpstreamReader: io.NopCloser(strings.NewReader(step.body)),
		Close:          func() error { return nil },
	}, nil
}

type imageHealthSpy struct {
	signals        []channelhealth.Signal
	forceCooldowns int
}

func (s *imageHealthSpy) ApplySignal(_ context.Context, signal channelhealth.Signal) (channelhealth.Record, error) {
	s.signals = append(s.signals, signal)
	return channelhealth.Record{Key: signal.Key}, nil
}

func (s *imageHealthSpy) ForceCooldown(_ context.Context, key channelhealth.ChannelKey, _ time.Time, _ string) (channelhealth.Record, error) {
	s.forceCooldowns++
	return channelhealth.Record{Key: key}, nil
}

type imageRetryClaimLifecycle struct {
	claimID      int64
	status       string
	attemptSeq   int32
	fingerprint  string
	abortErr     error
	beforeSettle func()
	reserves     []billing.ReserveRequest
	aborts       []abortCall
	settles      []billing.SettleRequest
}

func (s *imageRetryClaimLifecycle) Reserve(_ context.Context, req billing.ReserveRequest) (*billing.ReserveResult, error) {
	fingerprint := billing.ComputeIdempotencyFingerprint(req)
	if s.fingerprint != "" && s.fingerprint != fingerprint {
		return &billing.ReserveResult{FingerprintConflict: true}, billing.ErrFingerprintConflict
	}
	s.fingerprint = fingerprint
	s.reserves = append(s.reserves, req)
	switch s.status {
	case "committed":
		return &billing.ReserveResult{ClaimID: s.claimID, AttemptSeq: s.attemptSeq, IdempotencyHit: true}, nil
	case "reserving":
		return nil, billing.ErrClaimRace
	case "aborted":
		s.attemptSeq++
	default:
		if s.attemptSeq <= 0 {
			s.attemptSeq = 1
		}
	}
	s.status = "reserving"
	return &billing.ReserveResult{ClaimID: s.claimID, AttemptSeq: s.attemptSeq}, nil
}

func (s *imageRetryClaimLifecycle) Settle(_ context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
	if s.status != "reserving" || req.ClaimID != s.claimID || req.AttemptSeq != s.attemptSeq {
		return nil, errors.New("test claim lifecycle: settle identity mismatch")
	}
	if s.beforeSettle != nil {
		s.beforeSettle()
	}
	s.settles = append(s.settles, req)
	s.status = "committed"
	return &billing.SettleResult{}, nil
}

func (s *imageRetryClaimLifecycle) Abort(_ context.Context, tenantID, claimID int64, reason, _ string, _ int64, protocolLoss json.RawMessage) error {
	if s.abortErr != nil {
		return s.abortErr
	}
	if s.status != "reserving" || claimID != s.claimID {
		return errors.New("test claim lifecycle: abort identity mismatch")
	}
	s.aborts = append(s.aborts, abortCall{
		tenantID:     tenantID,
		claimID:      claimID,
		reason:       reason,
		protocolLoss: protocolLoss,
	})
	s.status = "aborted"
	return nil
}

func (s *imageRetryClaimLifecycle) CommitCacheHit(context.Context, billing.SettleRequest) error {
	s.status = "committed"
	return nil
}

func (s *imageRetryClaimLifecycle) Refund(context.Context, billing.RefundRequest) (*billing.RefundResult, error) {
	return nil, nil
}
