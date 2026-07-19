package rerankhttp

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

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/rate"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/upstreamfeedback"
)

func TestRerank500RetriesSecondAccountAndSettlesOnce(t *testing.T) {
	env := newRerankTestEnv(t)
	claims := &rerankRetryClaimLifecycle{claimID: 8201}
	selector := &rerankRetrySelector{accounts: []int64{44, 45}}
	dispatcher := &rerankRetryDispatcher{steps: []rerankRetryResponse{
		{status: http.StatusInternalServerError, body: `{"error":"upstream busy"}`},
		{status: http.StatusOK, body: successfulRerankBody()},
	}}
	health := &rerankHealthSpy{}
	env.deps.Router = rerankRetryRouter{}
	env.deps.Selector = selector
	env.deps.CredentialVault = rerankRetryVault(t, 44, 45)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims
	env.deps.Feedback = upstreamfeedback.NewObserver(upstreamfeedback.Dependencies{ChannelHealth: health})

	rec := env.invoke(t, rerankBody(2))

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
	if len(claims.aborts) != 1 || claims.aborts[0].reason != "upstream_5xx" {
		t.Fatalf("aborts=%+v want one upstream_5xx abort", claims.aborts)
	}
	if len(claims.settles) != 1 || claims.settles[0].AccountID != 45 || claims.settles[0].AttemptSeq != 2 {
		t.Fatalf("settles=%+v want account 45 attempt 2", claims.settles)
	}
	if len(health.signals) != 2 || health.signals[0].Class != channelhealth.SignalUpstream5xx || health.signals[1].Class != channelhealth.SignalSuccess {
		t.Fatalf("health signals=%+v want upstream_5xx then success", health.signals)
	}
}

func TestRerankCredentialMismatchSkipsDispatchAndUsesNextAccount(t *testing.T) {
	env := newRerankTestEnv(t)
	claims := &rerankRetryClaimLifecycle{claimID: 8210}
	selector := &rerankRetrySelector{accounts: []int64{44, 45}}
	dispatcher := &rerankRetryDispatcher{steps: []rerankRetryResponse{{status: http.StatusOK, body: successfulRerankBody()}}}
	env.deps.Router = rerankRetryRouter{}
	env.deps.Selector = selector
	env.deps.CredentialVault = rerankCompatibilityVault(t)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims

	rec := env.invoke(t, rerankBody(2))

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
	if strings.Contains(rec.Body.String(), "wrong-rerank-secret") {
		t.Fatal("response leaked mismatched credential")
	}
}

func TestRerank400DoesNotRetry(t *testing.T) {
	env := newRerankTestEnv(t)
	claims := &rerankRetryClaimLifecycle{claimID: 8202}
	selector := &rerankRetrySelector{accounts: []int64{44, 45}}
	dispatcher := &rerankRetryDispatcher{steps: []rerankRetryResponse{{
		status: http.StatusBadRequest,
		body:   `{"error":{"message":"policy-match"}}`,
	}}}
	env.deps.Router = rerankRetryRouter{}
	env.deps.Selector = selector
	env.deps.CredentialVault = rerankRetryVault(t, 44, 45)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims
	env.deps.Feedback = rerankProjectionObserver()

	rec := env.invoke(t, rerankBody(2))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s want 422", rec.Code, rec.Body.String())
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"error":{"code":"account_busy","message":"账号暂不可用"}}` {
		t.Fatalf("投影错误响应=%s", got)
	}
	if len(selector.requests) != 1 || len(dispatcher.accounts) != 1 || len(claims.reserves) != 1 {
		t.Fatalf("selector/dispatcher/reserve calls=%d/%d/%d want 1/1/1", len(selector.requests), len(dispatcher.accounts), len(claims.reserves))
	}
	if len(claims.aborts) != 1 || claims.aborts[0].reason != "upstream_client_4xx" {
		t.Fatalf("客户端投影不得改写终态分类: %+v", claims.aborts)
	}
}

type rerankErrorPolicyProvider struct{}

func (rerankErrorPolicyProvider) GetAccountErrorPolicy(int64) rate.AccountErrorPolicy {
	clientStatus := http.StatusUnprocessableEntity
	affectHealth := false
	return rate.AccountErrorPolicy{Rules: []rate.TempUnschedulableRule{{
		RuleID: "busy-400", ErrorCode: http.StatusBadRequest, Keywords: []string{"policy-match"},
		DurationMinutes: 5, ClientStatus: &clientStatus, ClientCode: "account_busy",
		MessageMode: "custom", ClientMessage: "账号暂不可用", AffectHealth: &affectHealth,
	}}}
}

func rerankProjectionObserver() *upstreamfeedback.Observer {
	return upstreamfeedback.NewObserver(upstreamfeedback.Dependencies{
		RateService: rate.NewUpstreamRateService(time.Now, time.Minute,
			rate.WithAccountErrorRulesProvider(rerankErrorPolicyProvider{})),
	})
}

func TestRerankAbortFailureStopsBeforeRetry(t *testing.T) {
	env := newRerankTestEnv(t)
	claims := &rerankRetryClaimLifecycle{claimID: 8203, abortErr: context.DeadlineExceeded}
	selector := &rerankRetrySelector{accounts: []int64{44, 45}}
	dispatcher := &rerankRetryDispatcher{steps: []rerankRetryResponse{
		{status: http.StatusInternalServerError, body: `{"error":"upstream busy"}`},
		{status: http.StatusOK, body: successfulRerankBody()},
	}}
	env.deps.Router = rerankRetryRouter{}
	env.deps.Selector = selector
	env.deps.CredentialVault = rerankRetryVault(t, 44, 45)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims

	rec := env.invoke(t, rerankBody(2))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s want 502", rec.Code, rec.Body.String())
	}
	if len(selector.requests) != 1 || len(dispatcher.accounts) != 1 || len(claims.reserves) != 1 {
		t.Fatalf("selector/dispatcher/reserve calls=%d/%d/%d want 1/1/1", len(selector.requests), len(dispatcher.accounts), len(claims.reserves))
	}
	if rec.Header().Get("X-Huakai-Abort-Failed") == "" {
		t.Fatal("abort failure must remain observable in response headers")
	}
}

func TestRerank401UsesSingleAuthFailoverBeyondAttemptBudget(t *testing.T) {
	env := newRerankTestEnv(t)
	claims := &rerankRetryClaimLifecycle{claimID: 8205}
	selector := &rerankRetrySelector{accounts: []int64{44, 45}}
	dispatcher := &rerankRetryDispatcher{steps: []rerankRetryResponse{
		{status: http.StatusUnauthorized, body: ""},
		{status: http.StatusOK, body: successfulRerankBody()},
	}}
	env.deps.Router = rerankSingleAttemptRouter{}
	env.deps.Selector = selector
	env.deps.CredentialVault = rerankRetryVault(t, 44, 45)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims

	rec := env.invoke(t, rerankBody(2))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200 after auth failover", rec.Code, rec.Body.String())
	}
	if len(selector.requests) != 2 || len(dispatcher.accounts) != 2 || len(claims.reserves) != 2 {
		t.Fatalf("selector/dispatcher/reserve calls=%d/%d/%d want 2/2/2", len(selector.requests), len(dispatcher.accounts), len(claims.reserves))
	}
	if len(claims.aborts) != 1 || claims.aborts[0].reason != "upstream_auth_failure" {
		t.Fatalf("aborts=%+v want one upstream_auth_failure", claims.aborts)
	}
}

func TestRerankTenantRetryBudgetStopsSecondAttempt(t *testing.T) {
	env := newRerankTestEnv(t)
	claims := &rerankRetryClaimLifecycle{claimID: 8206}
	selector := &rerankRetrySelector{accounts: []int64{44, 45}}
	dispatcher := &rerankRetryDispatcher{steps: []rerankRetryResponse{
		{status: http.StatusInternalServerError, body: `{"error":"upstream busy"}`},
		{status: http.StatusOK, body: successfulRerankBody()},
	}}
	budget := &rerankDenyRetryBudget{}
	env.deps.Router = rerankRetryRouter{}
	env.deps.Selector = selector
	env.deps.CredentialVault = rerankRetryVault(t, 44, 45)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims
	env.deps.RetryBudget = budget

	rec := env.invoke(t, rerankBody(2))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s want 502", rec.Code, rec.Body.String())
	}
	if len(selector.requests) != 1 || len(dispatcher.accounts) != 1 || len(claims.reserves) != 1 {
		t.Fatalf("selector/dispatcher/reserve calls=%d/%d/%d want 1/1/1", len(selector.requests), len(dispatcher.accounts), len(claims.reserves))
	}
	if len(budget.tenants) != 1 || budget.tenants[0] != 7 {
		t.Fatalf("retry budget tenants=%v want [7]", budget.tenants)
	}
}

func TestRerankUsesReReservedAttemptSequenceAcrossRequests(t *testing.T) {
	env := newRerankTestEnv(t)
	claims := &rerankRetryClaimLifecycle{claimID: 8204, status: "aborted", attemptSeq: 6}
	selector := &rerankRetrySelector{accounts: []int64{44}}
	dispatcher := &rerankRetryDispatcher{steps: []rerankRetryResponse{{
		status: http.StatusOK,
		body:   successfulRerankBody(),
	}}}
	env.deps.Selector = selector
	env.deps.CredentialVault = rerankRetryVault(t, 44)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims

	rec := env.invoke(t, rerankBody(2))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if len(selector.requests) != 1 || selector.requests[0].AttemptSeq != 7 {
		t.Fatalf("selector requests=%+v want authoritative attempt_seq 7", selector.requests)
	}
	if len(claims.settles) != 1 || claims.settles[0].AttemptSeq != 7 {
		t.Fatalf("settles=%+v want authoritative attempt_seq 7", claims.settles)
	}
}

func TestRerankUpstreamSuccessRecordedBeforeSettleFailure(t *testing.T) {
	env := newRerankTestEnv(t)
	health := &rerankHealthSpy{}
	env.settler.settleErr = errors.New("settle backend down")
	env.deps.Feedback = upstreamfeedback.NewObserver(upstreamfeedback.Dependencies{ChannelHealth: health})
	env.deps.CredentialVault = rerankRetryVault(t, 44)

	rec := env.invoke(t, rerankBody(2))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s want 500", rec.Code, rec.Body.String())
	}
	if len(health.signals) != 1 || health.signals[0].Class != channelhealth.SignalSuccess {
		t.Fatalf("health signals=%+v want one upstream success", health.signals)
	}
}

func successfulRerankBody() string {
	return `{"results":[{"index":0,"relevance_score":0.99}]}`
}

type rerankRetryRouter struct{}

func (rerankRetryRouter) Plan(context.Context, router.PlanInput) (router.RoutePlan, error) {
	return router.RoutePlan{
		Attempts: []router.AttemptPlan{
			{Index: 0, PoolGroupID: 101, UpstreamModelID: "rerank-upstream", Reason: "primary"},
			{Index: 1, PoolGroupID: 101, UpstreamModelID: "rerank-upstream", Reason: "account_failover"},
		},
		AttemptBudget: 2,
		RetryableEndClasses: []string{
			string(gateway.UpstreamError5xx),
			string(gateway.UpstreamRateLimit),
			string(gateway.FirstTokenTimeout),
			string(gateway.InterEventTimeout),
		},
		SnapshotVersion: "registry:7:1;router:retry-test",
	}, nil
}

type rerankSingleAttemptRouter struct{}

func (rerankSingleAttemptRouter) Plan(context.Context, router.PlanInput) (router.RoutePlan, error) {
	return router.RoutePlan{
		Attempts: []router.AttemptPlan{{
			Index: 0, PoolGroupID: 101, UpstreamModelID: "rerank-upstream", Reason: "primary",
		}},
		AttemptBudget:   1,
		SnapshotVersion: "registry:7:1;router:auth-retry-test",
	}, nil
}

type rerankDenyRetryBudget struct {
	tenants []int64
}

func (b *rerankDenyRetryBudget) Allow(tenantID int64) bool {
	b.tenants = append(b.tenants, tenantID)
	return false
}

type rerankRetrySelector struct {
	accounts []int64
	requests []pool.SelectionRequest
}

func (s *rerankRetrySelector) Select(_ context.Context, req pool.SelectionRequest) (*pool.SelectionResult, error) {
	s.requests = append(s.requests, req)
	for _, accountID := range s.accounts {
		if _, excluded := req.ExcludedAccounts[accountID]; excluded {
			continue
		}
		return &pool.SelectionResult{
			AccountID:         accountID,
			AcquisitionToken:  uuid.New(),
			RoutingReasonJSON: []byte(`{"reason":"retry-test"}`),
		}, nil
	}
	return nil, pool.ErrNoEligibleAccount
}

func rerankRetryVault(t *testing.T, accountIDs ...int64) provider.CredentialVault {
	t.Helper()
	vault := provider.NewStaticVault()
	for _, accountID := range accountIDs {
		if err := vault.Set(accountID, provider.Credential{
			Type:  provider.CredentialTypeAPIKey,
			Value: "sk-retry-test",
		}, provider.AccountInfo{
			AccountID:           accountID,
			TenantID:            7,
			Platform:            "openai",
			AccountType:         "api_key",
			AccountCredentialID: 9000 + accountID,
			CredentialVersion:   1,
		}); err != nil {
			t.Fatalf("vault.Set(%d): %v", accountID, err)
		}
	}
	return vault
}

func rerankCompatibilityVault(t *testing.T) provider.CredentialVault {
	t.Helper()
	vault := provider.NewStaticVault()
	rows := []struct {
		id, credentialID int64
		platform, secret string
	}{{44, 9044, "gemini", "wrong-rerank-secret"}, {45, 9045, "openai", "right-rerank-secret"}}
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

type rerankRetryResponse struct {
	status int
	body   string
}

type rerankRetryDispatcher struct {
	steps    []rerankRetryResponse
	accounts []int64
}

func (d *rerankRetryDispatcher) Dispatch(_ context.Context, in gateway.DispatchInput) (*gateway.DispatchResult, error) {
	d.accounts = append(d.accounts, in.Account.AccountID)
	step := rerankRetryResponse{status: http.StatusOK, body: successfulRerankBody()}
	if len(d.accounts) <= len(d.steps) {
		step = d.steps[len(d.accounts)-1]
	}
	return &gateway.DispatchResult{
		StatusCode:     step.status,
		Headers:        http.Header{"Content-Type": []string{"application/json"}},
		UpstreamReader: io.NopCloser(strings.NewReader(step.body)),
		Close:          func() error { return nil },
	}, nil
}

type rerankHealthSpy struct {
	signals []channelhealth.Signal
}

func (s *rerankHealthSpy) ApplySignal(_ context.Context, signal channelhealth.Signal) (channelhealth.Record, error) {
	s.signals = append(s.signals, signal)
	return channelhealth.Record{Key: signal.Key}, nil
}

func (s *rerankHealthSpy) ForceCooldown(_ context.Context, key channelhealth.ChannelKey, _ time.Time, _ string) (channelhealth.Record, error) {
	return channelhealth.Record{Key: key}, nil
}

type rerankRetryClaimLifecycle struct {
	claimID     int64
	status      string
	attemptSeq  int32
	fingerprint string
	abortErr    error
	reserves    []billing.ReserveRequest
	aborts      []rerankAbortCall
	settles     []billing.SettleRequest
}

func (s *rerankRetryClaimLifecycle) Reserve(_ context.Context, req billing.ReserveRequest) (*billing.ReserveResult, error) {
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

func (s *rerankRetryClaimLifecycle) Settle(_ context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
	if s.status != "reserving" || req.ClaimID != s.claimID || req.AttemptSeq != s.attemptSeq {
		return nil, errors.New("test claim lifecycle: settle identity mismatch")
	}
	s.settles = append(s.settles, req)
	s.status = "committed"
	return &billing.SettleResult{}, nil
}

func (s *rerankRetryClaimLifecycle) Abort(_ context.Context, tenantID, claimID int64, reason, _ string, _ int64, _ json.RawMessage) error {
	if s.abortErr != nil {
		return s.abortErr
	}
	if s.status != "reserving" || claimID != s.claimID {
		return errors.New("test claim lifecycle: abort identity mismatch")
	}
	s.aborts = append(s.aborts, rerankAbortCall{tenantID: tenantID, claimID: claimID, reason: reason})
	s.status = "aborted"
	return nil
}

func (s *rerankRetryClaimLifecycle) CommitCacheHit(context.Context, billing.SettleRequest) error {
	s.status = "committed"
	return nil
}

func (s *rerankRetryClaimLifecycle) Refund(context.Context, billing.RefundRequest) (*billing.RefundResult, error) {
	return nil, nil
}
