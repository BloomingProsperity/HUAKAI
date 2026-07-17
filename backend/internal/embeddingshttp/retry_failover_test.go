package embeddingshttp

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

func TestEmbeddings500RetriesSecondAccountAndSettlesOnce(t *testing.T) {
	env := newEmbeddingsTestEnv(t, upstreamResponse{})
	claims := &embeddingRetryClaimLifecycle{claimID: 8101}
	selector := &embeddingRetrySelector{accounts: []int64{44, 45}}
	dispatcher := &embeddingRetryDispatcher{steps: []upstreamResponse{
		{status: http.StatusInternalServerError, body: `{"error":"upstream busy"}`},
		{status: http.StatusOK, body: successfulEmbeddingBody()},
	}}
	health := &embeddingHealthSpy{}
	env.deps.Router = embeddingRetryRouter{}
	env.deps.Selector = selector
	env.deps.CredentialVault = embeddingRetryVault(t, 44, 45)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims
	env.deps.Feedback = upstreamfeedback.NewObserver(upstreamfeedback.Dependencies{ChannelHealth: health})

	rec := env.invoke(t, `{"model":"embed-public","input":"fail over safely"}`)

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
	if claims.settles[0].AccountID != 45 || claims.settles[0].AttemptSeq != 2 {
		t.Fatalf("settle account/attempt=%d/%d want 45/2", claims.settles[0].AccountID, claims.settles[0].AttemptSeq)
	}
	if len(health.signals) != 2 || health.signals[0].Class != channelhealth.SignalUpstream5xx || health.signals[1].Class != channelhealth.SignalSuccess {
		t.Fatalf("health signals=%+v want upstream_5xx then success", health.signals)
	}
}

func TestEmbeddingsCredentialMismatchSkipsDispatchAndUsesNextAccount(t *testing.T) {
	env := newEmbeddingsTestEnv(t, upstreamResponse{})
	claims := &embeddingRetryClaimLifecycle{claimID: 8110}
	selector := &embeddingRetrySelector{accounts: []int64{44, 45}}
	dispatcher := &embeddingRetryDispatcher{steps: []upstreamResponse{{status: http.StatusOK, body: successfulEmbeddingBody()}}}
	env.deps.Router = embeddingRetryRouter{}
	env.deps.Selector = selector
	env.deps.CredentialVault = embeddingCompatibilityVault(t)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims

	rec := env.invoke(t, `{"model":"embed-public","input":"compatibility failover"}`)

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
	if strings.Contains(rec.Body.String(), "wrong-embeddings-secret") {
		t.Fatal("response leaked mismatched credential")
	}
}

func TestEmbeddings400DoesNotRetry(t *testing.T) {
	env := newEmbeddingsTestEnv(t, upstreamResponse{})
	claims := &embeddingRetryClaimLifecycle{claimID: 8102}
	selector := &embeddingRetrySelector{accounts: []int64{44, 45}}
	dispatcher := &embeddingRetryDispatcher{steps: []upstreamResponse{{
		status: http.StatusBadRequest,
		body:   `{"error":"invalid input"}`,
	}}}
	env.deps.Router = embeddingRetryRouter{}
	env.deps.Selector = selector
	env.deps.CredentialVault = embeddingRetryVault(t, 44, 45)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims

	rec := env.invoke(t, `{"model":"embed-public","input":"terminal client error"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s want 400", rec.Code, rec.Body.String())
	}
	if len(selector.requests) != 1 || len(dispatcher.accounts) != 1 || len(claims.reserves) != 1 {
		t.Fatalf("selector/dispatcher/reserve calls=%d/%d/%d want 1/1/1", len(selector.requests), len(dispatcher.accounts), len(claims.reserves))
	}
}

func TestEmbeddingsAbortFailureStopsBeforeRetry(t *testing.T) {
	env := newEmbeddingsTestEnv(t, upstreamResponse{})
	claims := &embeddingRetryClaimLifecycle{claimID: 8103, abortErr: context.DeadlineExceeded}
	selector := &embeddingRetrySelector{accounts: []int64{44, 45}}
	dispatcher := &embeddingRetryDispatcher{steps: []upstreamResponse{
		{status: http.StatusInternalServerError, body: `{"error":"upstream busy"}`},
		{status: http.StatusOK, body: successfulEmbeddingBody()},
	}}
	env.deps.Router = embeddingRetryRouter{}
	env.deps.Selector = selector
	env.deps.CredentialVault = embeddingRetryVault(t, 44, 45)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims

	rec := env.invoke(t, `{"model":"embed-public","input":"do not overlap claims"}`)

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

func TestEmbeddings401UsesSingleAuthFailoverBeyondAttemptBudget(t *testing.T) {
	env := newEmbeddingsTestEnv(t, upstreamResponse{})
	claims := &embeddingRetryClaimLifecycle{claimID: 8105}
	selector := &embeddingRetrySelector{accounts: []int64{44, 45}}
	dispatcher := &embeddingRetryDispatcher{steps: []upstreamResponse{
		{status: http.StatusUnauthorized, body: `{"error":"invalid_grant"}`},
		{status: http.StatusOK, body: successfulEmbeddingBody()},
	}}
	env.deps.Router = embeddingSingleAttemptRouter{}
	env.deps.Selector = selector
	env.deps.CredentialVault = embeddingRetryVault(t, 44, 45)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims

	rec := env.invoke(t, `{"model":"embed-public","input":"refresh and fail over once"}`)

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

func TestEmbeddingsTenantRetryBudgetStopsSecondAttempt(t *testing.T) {
	env := newEmbeddingsTestEnv(t, upstreamResponse{})
	claims := &embeddingRetryClaimLifecycle{claimID: 8106}
	selector := &embeddingRetrySelector{accounts: []int64{44, 45}}
	dispatcher := &embeddingRetryDispatcher{steps: []upstreamResponse{
		{status: http.StatusInternalServerError, body: `{"error":"upstream busy"}`},
		{status: http.StatusOK, body: successfulEmbeddingBody()},
	}}
	budget := &embeddingDenyRetryBudget{}
	env.deps.Router = embeddingRetryRouter{}
	env.deps.Selector = selector
	env.deps.CredentialVault = embeddingRetryVault(t, 44, 45)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims
	env.deps.RetryBudget = budget

	rec := env.invoke(t, `{"model":"embed-public","input":"tenant budget exhausted"}`)

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

func TestEmbeddingsUsesReReservedAttemptSequenceAcrossRequests(t *testing.T) {
	env := newEmbeddingsTestEnv(t, upstreamResponse{})
	claims := &embeddingRetryClaimLifecycle{claimID: 8104, status: "aborted", attemptSeq: 4}
	selector := &embeddingRetrySelector{accounts: []int64{44}}
	dispatcher := &embeddingRetryDispatcher{steps: []upstreamResponse{{
		status: http.StatusOK,
		body:   successfulEmbeddingBody(),
	}}}
	env.deps.Selector = selector
	env.deps.CredentialVault = embeddingRetryVault(t, 44)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims

	rec := env.invoke(t, `{"model":"embed-public","input":"resume aborted claim"}`)

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

func TestEmbeddingsUpstreamSuccessRecordedBeforeUsageParsingFailure(t *testing.T) {
	env := newEmbeddingsTestEnv(t, upstreamResponse{
		status: http.StatusOK,
		body:   `{"object":"list","data":[]}`,
	})
	health := &embeddingHealthSpy{}
	env.deps.Feedback = upstreamfeedback.NewObserver(upstreamfeedback.Dependencies{ChannelHealth: health})
	env.deps.CredentialVault = embeddingRetryVault(t, 44)

	rec := env.invoke(t, `{"model":"embed-public","input":"upstream succeeded"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s want 502", rec.Code, rec.Body.String())
	}
	if len(health.signals) != 1 || health.signals[0].Class != channelhealth.SignalSuccess {
		t.Fatalf("health signals=%+v want one upstream success", health.signals)
	}
	if len(env.settler.aborts) != 1 || env.settler.aborts[0].reason != "usage_missing" {
		t.Fatalf("aborts=%+v want usage_missing", env.settler.aborts)
	}
}

func successfulEmbeddingBody() string {
	return `{"object":"list","data":[{"object":"embedding","embedding":[0.1],"index":0}],"model":"text-embedding-3-small","usage":{"prompt_tokens":3,"total_tokens":3}}`
}

type embeddingRetryRouter struct{}

func (embeddingRetryRouter) Plan(context.Context, router.PlanInput) (router.RoutePlan, error) {
	return router.RoutePlan{
		Attempts: []router.AttemptPlan{
			{Index: 0, PoolGroupID: 101, UpstreamModelID: "text-embedding-3-small", Reason: "primary"},
			{Index: 1, PoolGroupID: 101, UpstreamModelID: "text-embedding-3-small", Reason: "account_failover"},
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

type embeddingSingleAttemptRouter struct{}

func (embeddingSingleAttemptRouter) Plan(context.Context, router.PlanInput) (router.RoutePlan, error) {
	return router.RoutePlan{
		Attempts: []router.AttemptPlan{{
			Index: 0, PoolGroupID: 101, UpstreamModelID: "text-embedding-3-small", Reason: "primary",
		}},
		AttemptBudget:   1,
		SnapshotVersion: "registry:7:1;router:auth-retry-test",
	}, nil
}

type embeddingDenyRetryBudget struct {
	tenants []int64
}

func (b *embeddingDenyRetryBudget) Allow(tenantID int64) bool {
	b.tenants = append(b.tenants, tenantID)
	return false
}

type embeddingRetrySelector struct {
	accounts []int64
	requests []pool.SelectionRequest
}

func (s *embeddingRetrySelector) Select(_ context.Context, req pool.SelectionRequest) (*pool.SelectionResult, error) {
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

func embeddingRetryVault(t *testing.T, accountIDs ...int64) provider.CredentialVault {
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

func embeddingCompatibilityVault(t *testing.T) provider.CredentialVault {
	t.Helper()
	vault := provider.NewStaticVault()
	rows := []struct {
		id, credentialID int64
		platform, secret string
	}{{44, 9044, "gemini", "wrong-embeddings-secret"}, {45, 9045, "openai", "right-embeddings-secret"}}
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

type embeddingRetryDispatcher struct {
	steps    []upstreamResponse
	accounts []int64
}

func (d *embeddingRetryDispatcher) Dispatch(_ context.Context, in gateway.DispatchInput) (*gateway.DispatchResult, error) {
	d.accounts = append(d.accounts, in.Account.AccountID)
	step := upstreamResponse{status: http.StatusOK, body: successfulEmbeddingBody()}
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

type embeddingHealthSpy struct {
	signals []channelhealth.Signal
}

func (s *embeddingHealthSpy) ApplySignal(_ context.Context, signal channelhealth.Signal) (channelhealth.Record, error) {
	s.signals = append(s.signals, signal)
	return channelhealth.Record{Key: signal.Key}, nil
}

func (s *embeddingHealthSpy) ForceCooldown(_ context.Context, key channelhealth.ChannelKey, _ time.Time, _ string) (channelhealth.Record, error) {
	return channelhealth.Record{Key: key}, nil
}

type embeddingRetryClaimLifecycle struct {
	claimID     int64
	status      string
	attemptSeq  int32
	fingerprint string
	abortErr    error
	reserves    []billing.ReserveRequest
	aborts      []abortCall
	settles     []billing.SettleRequest
}

func (s *embeddingRetryClaimLifecycle) Reserve(_ context.Context, req billing.ReserveRequest) (*billing.ReserveResult, error) {
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

func (s *embeddingRetryClaimLifecycle) Settle(_ context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
	if s.status != "reserving" || req.ClaimID != s.claimID || req.AttemptSeq != s.attemptSeq {
		return nil, errors.New("test claim lifecycle: settle identity mismatch")
	}
	s.settles = append(s.settles, req)
	s.status = "committed"
	return &billing.SettleResult{}, nil
}

func (s *embeddingRetryClaimLifecycle) Abort(_ context.Context, tenantID, claimID int64, reason, _ string, _ int64, _ json.RawMessage) error {
	if s.abortErr != nil {
		return s.abortErr
	}
	if s.status != "reserving" || claimID != s.claimID {
		return errors.New("test claim lifecycle: abort identity mismatch")
	}
	s.aborts = append(s.aborts, abortCall{tenantID: tenantID, claimID: claimID, reason: reason})
	s.status = "aborted"
	return nil
}

func (s *embeddingRetryClaimLifecycle) CommitCacheHit(context.Context, billing.SettleRequest) error {
	s.status = "committed"
	return nil
}

func (s *embeddingRetryClaimLifecycle) Refund(context.Context, billing.RefundRequest) (*billing.RefundResult, error) {
	return nil, nil
}
