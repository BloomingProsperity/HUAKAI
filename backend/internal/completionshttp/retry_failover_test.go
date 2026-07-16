package completionshttp

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
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/upstreamfeedback"
)

func TestCompletions500RetriesSecondAccountAndSettlesOnce(t *testing.T) {
	env := newCompletionsTestEnv(upstreamResponse{})
	claims := &retryClaimLifecycle{claimID: 8101}
	selector := &retrySelector{accounts: []int64{44, 45}}
	dispatcher := &retryDispatcher{steps: []upstreamResponse{
		{status: http.StatusInternalServerError, body: `{"error":"upstream busy"}`},
		{status: http.StatusOK, body: successfulCompletionBody()},
	}}
	env.deps.Router = retryRouter()
	env.deps.Selector = selector
	env.deps.CredentialVault = retryVault(t, 44, 45)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims

	rec := env.invokeCompletions(t, `{"model":"legacy-public","prompt":"fail over safely"}`)

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
	if len(claims.reserves) != 2 {
		t.Fatalf("reserve calls=%d want 2", len(claims.reserves))
	}
	if claims.reserves[0].LogicalRequestID != claims.reserves[1].LogicalRequestID {
		t.Fatalf("logical request changed across retry: %q != %q", claims.reserves[0].LogicalRequestID, claims.reserves[1].LogicalRequestID)
	}
	if len(claims.aborts) != 1 || claims.aborts[0].reason != "upstream_5xx" {
		t.Fatalf("aborts=%+v want one upstream_5xx abort", claims.aborts)
	}
	if len(claims.settles) != 1 {
		t.Fatalf("settle calls=%d want 1", len(claims.settles))
	}
	settle := claims.settles[0]
	if settle.AccountID != 45 || settle.AttemptSeq != 2 {
		t.Fatalf("settle account/attempt=%d/%d want 45/2", settle.AccountID, settle.AttemptSeq)
	}
	if claims.status != "committed" || claims.attemptSeq != 2 {
		t.Fatalf("claim status/attempt=%s/%d want committed/2", claims.status, claims.attemptSeq)
	}
}

func TestCompletionsCredentialMismatchSkipsDispatchAndUsesNextAccount(t *testing.T) {
	env := newCompletionsTestEnv(upstreamResponse{})
	claims := &retryClaimLifecycle{claimID: 8110}
	selector := &retrySelector{accounts: []int64{44, 45}}
	dispatcher := &retryDispatcher{steps: []upstreamResponse{{status: http.StatusOK, body: successfulCompletionBody()}}}
	env.deps.Router = retryRouter()
	env.deps.Selector = selector
	env.deps.CredentialVault = completionCompatibilityVault(t)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims

	rec := env.invokeCompletions(t, `{"model":"legacy-public","prompt":"compatibility failover"}`)

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
	if strings.Contains(rec.Body.String(), "wrong-completions-secret") {
		t.Fatal("response leaked mismatched credential")
	}
}

func TestCompletionsUsesReReservedAttemptSequenceAcrossRequests(t *testing.T) {
	env := newCompletionsTestEnv(upstreamResponse{
		status: http.StatusOK,
		body:   successfulCompletionBody(),
	})
	claims := &retryClaimLifecycle{
		claimID:    8101,
		status:     "aborted",
		attemptSeq: 1,
	}
	selector := &retrySelector{accounts: []int64{44}}
	env.deps.ClaimGate = claims
	env.deps.Settler = claims
	env.deps.Selector = selector
	env.deps.CredentialVault = retryVault(t, 44)

	rec := env.invokeCompletions(t, `{"model":"legacy-public","prompt":"resume an aborted claim"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if len(selector.requests) != 1 || selector.requests[0].AttemptSeq != 2 {
		t.Fatalf("selector requests=%+v want authoritative re-reserved attempt_seq 2", selector.requests)
	}
	if len(claims.settles) != 1 || claims.settles[0].AttemptSeq != 2 {
		t.Fatalf("settles=%+v want authoritative re-reserved attempt_seq 2", claims.settles)
	}
}

func TestCompletions400DoesNotRetry(t *testing.T) {
	env := newCompletionsTestEnv(upstreamResponse{})
	selector := &retrySelector{accounts: []int64{44, 45}}
	dispatcher := &retryDispatcher{steps: []upstreamResponse{{
		status: http.StatusBadRequest,
		body:   `{"error":"invalid prompt"}`,
	}}}
	env.deps.Router = retryRouter()
	env.deps.Selector = selector
	env.deps.CredentialVault = retryVault(t, 44, 45)
	env.deps.Dispatcher = dispatcher

	rec := env.invokeCompletions(t, `{"model":"legacy-public","prompt":"terminal client error"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s want 400", rec.Code, rec.Body.String())
	}
	if len(selector.requests) != 1 || len(dispatcher.accounts) != 1 {
		t.Fatalf("selector/dispatcher calls=%d/%d want 1/1", len(selector.requests), len(dispatcher.accounts))
	}
	if len(env.claims.reserves) != 1 {
		t.Fatalf("reserve calls=%d want 1", len(env.claims.reserves))
	}
	if len(env.settler.aborts) != 1 || env.settler.aborts[0].reason != "upstream_client_4xx" {
		t.Fatalf("aborts=%+v want one upstream_client_4xx abort", env.settler.aborts)
	}
	if len(env.settler.settles) != 0 {
		t.Fatalf("settle calls=%d want 0", len(env.settler.settles))
	}
}

func TestCompletionsAbortFailureStopsBeforeRetry(t *testing.T) {
	env := newCompletionsTestEnv(upstreamResponse{})
	selector := &retrySelector{accounts: []int64{44, 45}}
	dispatcher := &retryDispatcher{steps: []upstreamResponse{
		{status: http.StatusInternalServerError, body: `{"error":"upstream busy"}`},
		{status: http.StatusOK, body: successfulCompletionBody()},
	}}
	env.deps.Router = retryRouter()
	env.deps.Selector = selector
	env.deps.CredentialVault = retryVault(t, 44, 45)
	env.deps.Dispatcher = dispatcher
	env.settler.abortErr = context.DeadlineExceeded

	rec := env.invokeCompletions(t, `{"model":"legacy-public","prompt":"do not overlap claims"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s want 502", rec.Code, rec.Body.String())
	}
	if len(selector.requests) != 1 || len(dispatcher.accounts) != 1 {
		t.Fatalf("selector/dispatcher calls=%d/%d want 1/1 when abort fails", len(selector.requests), len(dispatcher.accounts))
	}
	if len(env.claims.reserves) != 1 {
		t.Fatalf("reserve calls=%d want 1 when abort fails", len(env.claims.reserves))
	}
	if rec.Header().Get("X-Huakai-Abort-Failed") == "" {
		t.Fatal("abort failure must remain observable in response headers")
	}
}

func TestCountTokens500RetriesWithoutTouchingMoney(t *testing.T) {
	env := newCompletionsTestEnv(upstreamResponse{})
	selector := &retrySelector{accounts: []int64{44, 45}}
	dispatcher := &retryDispatcher{steps: []upstreamResponse{
		{status: http.StatusInternalServerError, body: `{"error":"upstream busy"}`},
		{status: http.StatusOK, body: `{"input_tokens":42}`},
	}}
	env.deps.Router = retryRouter()
	env.deps.Selector = selector
	env.deps.CredentialVault = retryVault(t, 44, 45)
	env.deps.Dispatcher = dispatcher

	rec := env.invokeCountTokens(t, `{"model":"claude-public","messages":[{"role":"user","content":"hello"}]}`)

	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"input_tokens":42}` {
		t.Fatalf("status=%d body=%s want second-attempt count_tokens success", rec.Code, rec.Body.String())
	}
	if len(selector.requests) != 2 {
		t.Fatalf("selector calls=%d want 2", len(selector.requests))
	}
	if _, excluded := selector.requests[1].ExcludedAccounts[44]; !excluded {
		t.Fatalf("second attempt exclusions=%v want failed account 44", selector.requests[1].ExcludedAccounts)
	}
	if len(env.claims.reserves) != 0 || len(env.settler.aborts) != 0 || len(env.settler.settles) != 0 {
		t.Fatalf("count_tokens touched money path: reserves/aborts/settles=%d/%d/%d", len(env.claims.reserves), len(env.settler.aborts), len(env.settler.settles))
	}
}

func TestCompletionsUpstreamSuccessRecordedBeforeLocalPricingFailure(t *testing.T) {
	env := newCompletionsTestEnv(upstreamResponse{
		status: http.StatusOK,
		body:   successfulCompletionBody(),
	})
	health := &completionHealthSpy{}
	env.deps.Feedback = upstreamfeedback.NewObserver(upstreamfeedback.Dependencies{ChannelHealth: health})
	env.deps.CredentialVault = retryVault(t, 44)
	env.deps.RateTables = &flakyRateTableStub{failFrom: 2}

	rec := env.invokeCompletions(t, `{"model":"legacy-public","prompt":"upstream succeeds before pricing fails"}`)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503 pricing unavailable", rec.Code, rec.Body.String())
	}
	if len(health.signals) != 1 || health.signals[0].Class != channelhealth.SignalSuccess {
		t.Fatalf("health signals=%+v want one upstream success despite local pricing failure", health.signals)
	}
	if len(env.settler.aborts) != 1 || env.settler.aborts[0].reason != "pricing_unavailable" {
		t.Fatalf("aborts=%+v want pricing_unavailable", env.settler.aborts)
	}
}

func successfulCompletionBody() string {
	return `{"id":"cmpl_retry","object":"text_completion","model":"text-davinci-003","choices":[{"text":"ok","index":0}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`
}

type fixedRetryRouter struct{}

func (fixedRetryRouter) Plan(context.Context, router.PlanInput) (router.RoutePlan, error) {
	return retryRouterPlan(), nil
}

func retryRouter() router.Router {
	return fixedRetryRouter{}
}

func retryRouterPlan() router.RoutePlan {
	return router.RoutePlan{
		Attempts: []router.AttemptPlan{
			{Index: 0, PoolGroupID: 101, UpstreamModelID: "text-davinci-003", Reason: "primary"},
			{Index: 1, PoolGroupID: 101, UpstreamModelID: "text-davinci-003", Reason: "same_pool_account_failover"},
		},
		AttemptBudget: 2,
		RetryableEndClasses: []string{
			string(gateway.UpstreamError5xx),
			string(gateway.UpstreamRateLimit),
			string(gateway.FirstTokenTimeout),
			string(gateway.InterEventTimeout),
		},
		SnapshotVersion: "registry:7:1;router:retry-test",
	}
}

type retrySelector struct {
	accounts []int64
	requests []pool.SelectionRequest
}

func (s *retrySelector) Select(_ context.Context, req pool.SelectionRequest) (*pool.SelectionResult, error) {
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

func retryVault(t *testing.T, accountIDs ...int64) provider.CredentialVault {
	t.Helper()
	vault := provider.NewStaticVault()
	for _, accountID := range accountIDs {
		err := vault.Set(accountID, provider.Credential{
			Type:  provider.CredentialTypeAPIKey,
			Value: "sk-retry-test",
		}, provider.AccountInfo{
			AccountID:           accountID,
			TenantID:            7,
			Platform:            "openai",
			AccountType:         "api_key",
			AccountCredentialID: 9000 + accountID,
			CredentialVersion:   1,
		})
		if err != nil {
			t.Fatalf("vault.Set(%d): %v", accountID, err)
		}
	}
	return vault
}

func completionCompatibilityVault(t *testing.T) provider.CredentialVault {
	t.Helper()
	vault := provider.NewStaticVault()
	rows := []struct {
		id, credentialID int64
		platform, secret string
	}{{44, 9044, "gemini", "wrong-completions-secret"}, {45, 9045, "openai", "right-completions-secret"}}
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

type retryDispatcher struct {
	steps    []upstreamResponse
	accounts []int64
}

func (d *retryDispatcher) Dispatch(_ context.Context, in gateway.DispatchInput) (*gateway.DispatchResult, error) {
	d.accounts = append(d.accounts, in.Account.AccountID)
	step := upstreamResponse{status: http.StatusOK, body: successfulCompletionBody()}
	if len(d.accounts) <= len(d.steps) {
		step = d.steps[len(d.accounts)-1]
	}
	headers := http.Header{"Content-Type": []string{"application/json"}}
	return &gateway.DispatchResult{
		StatusCode:     step.status,
		Headers:        headers,
		UpstreamReader: io.NopCloser(strings.NewReader(step.body)),
		Close:          func() error { return nil },
	}, nil
}

type completionHealthSpy struct {
	signals []channelhealth.Signal
}

func (s *completionHealthSpy) ApplySignal(_ context.Context, signal channelhealth.Signal) (channelhealth.Record, error) {
	s.signals = append(s.signals, signal)
	return channelhealth.Record{Key: signal.Key}, nil
}

func (s *completionHealthSpy) ForceCooldown(_ context.Context, key channelhealth.ChannelKey, _ time.Time, _ string) (channelhealth.Record, error) {
	return channelhealth.Record{Key: key}, nil
}

type retryClaimLifecycle struct {
	claimID     int64
	status      string
	attemptSeq  int32
	fingerprint string
	reserves    []billing.ReserveRequest
	aborts      []abortCall
	settles     []billing.SettleRequest
}

func (s *retryClaimLifecycle) Reserve(_ context.Context, req billing.ReserveRequest) (*billing.ReserveResult, error) {
	fingerprint := billing.ComputeIdempotencyFingerprint(req)
	if s.fingerprint != "" && s.fingerprint != fingerprint {
		return &billing.ReserveResult{FingerprintConflict: true}, billing.ErrFingerprintConflict
	}
	s.fingerprint = fingerprint
	s.reserves = append(s.reserves, req)
	switch s.status {
	case "committed":
		return &billing.ReserveResult{
			ClaimID:        s.claimID,
			AttemptSeq:     s.attemptSeq,
			IdempotencyHit: true,
		}, nil
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

func (s *retryClaimLifecycle) Settle(_ context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
	if s.status != "reserving" || req.ClaimID != s.claimID || req.AttemptSeq != s.attemptSeq {
		return nil, errors.New("test claim lifecycle: settle identity mismatch")
	}
	s.settles = append(s.settles, req)
	s.status = "committed"
	return &billing.SettleResult{}, nil
}

func (s *retryClaimLifecycle) Abort(_ context.Context, tenantID, claimID int64, reason, _ string, _ int64, _ json.RawMessage) error {
	if s.status != "reserving" || claimID != s.claimID {
		return errors.New("test claim lifecycle: abort identity mismatch")
	}
	s.aborts = append(s.aborts, abortCall{tenantID: tenantID, claimID: claimID, reason: reason})
	s.status = "aborted"
	return nil
}

func (s *retryClaimLifecycle) CommitCacheHit(context.Context, billing.SettleRequest) error {
	s.status = "committed"
	return nil
}

func (s *retryClaimLifecycle) Refund(context.Context, billing.RefundRequest) (*billing.RefundResult, error) {
	return nil, nil
}

var _ billing.Settler = (*recordingSettler)(nil)
var _ billing.ClaimGate = (*retryClaimLifecycle)(nil)
var _ billing.Settler = (*retryClaimLifecycle)(nil)
