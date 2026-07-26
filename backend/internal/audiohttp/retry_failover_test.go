package audiohttp

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
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/rate"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/upstreamfeedback"
)

func TestAudio500RetriesSecondAccountAndSettlesOnce(t *testing.T) {
	env := newAudioTestEnv(t, audioEndpointSpeech, upstreamResponse{})
	claims := &audioRetryClaimLifecycle{claimID: 8401}
	selector := &audioRetrySelector{accounts: []int64{44, 45}}
	dispatcher := &audioRetryDispatcher{steps: []audioRetryResponse{
		{status: http.StatusInternalServerError, body: `{"error":"upstream busy"}`},
		{status: http.StatusOK, body: "audio-bytes"},
	}}
	health := &audioHealthSpy{}
	env.deps.Router = audioRetryRouter{}
	env.deps.Selector = selector
	env.deps.CredentialVault = audioRetryVault(t, 44, 45)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims
	env.deps.Feedback = upstreamfeedback.NewObserver(upstreamfeedback.Dependencies{ChannelHealth: health})
	env.deps.PricingRatioResolver = audioPoolRatioResolver{}
	claims.beforeSettle = func() {
		if len(health.signals) != 2 || health.signals[1].Class != channelhealth.SignalSuccess {
			t.Fatalf("settle before success feedback: %+v", health.signals)
		}
	}

	rec := env.invokeJSON(t, `{"model":"tts-1","input":"retry safely","voice":"alloy"}`)

	if rec.Code != http.StatusOK || rec.Body.String() != "audio-bytes" {
		t.Fatalf("status/body=%d/%q want 200/audio-bytes", rec.Code, rec.Body.String())
	}
	if len(selector.requests) != 2 {
		t.Fatalf("selector calls=%d want 2", len(selector.requests))
	}
	if _, excluded := selector.requests[1].ExcludedAccounts[44]; !excluded {
		t.Fatalf("second attempt exclusions=%v want failed account 44", selector.requests[1].ExcludedAccounts)
	}
	if len(claims.reserves) != 2 || claims.reserves[0].LogicalRequestID != claims.reserves[1].LogicalRequestID {
		t.Fatalf("reserves=%+v want two attempts sharing one logical request", claims.reserves)
	}
	if claims.reserves[0].PoolingGroupID != 101 || claims.reserves[1].PoolingGroupID != 202 {
		t.Fatalf("reserve pools=%d/%d want 101/202", claims.reserves[0].PoolingGroupID, claims.reserves[1].PoolingGroupID)
	}
	if !claims.reserves[1].PredictedCost.Equal(claims.reserves[0].PredictedCost.Mul(decimal.RequireFromString("0.5"))) {
		t.Fatalf("reserve costs=%s/%s want second pool repriced at 0.5", claims.reserves[0].PredictedCost, claims.reserves[1].PredictedCost)
	}
	if len(claims.aborts) != 1 || claims.aborts[0].reason != "upstream_5xx" {
		t.Fatalf("aborts=%+v want one upstream_5xx abort", claims.aborts)
	}
	if len(claims.settles) != 1 || claims.settles[0].AccountID != 45 || claims.settles[0].AttemptSeq != 2 {
		t.Fatalf("settles=%+v want final account 45 attempt 2", claims.settles)
	}
	if len(health.signals) != 2 ||
		health.signals[0].Class != channelhealth.SignalUpstream5xx ||
		health.signals[1].Class != channelhealth.SignalSuccess {
		t.Fatalf("health signals=%+v want upstream_5xx then success", health.signals)
	}
}

func TestAudioCredentialMismatchSkipsDispatchAndUsesNextAccount(t *testing.T) {
	env := newAudioTestEnv(t, audioEndpointSpeech, upstreamResponse{})
	claims := &audioRetryClaimLifecycle{claimID: 9210}
	selector := &audioRetrySelector{accounts: []int64{44, 45}}
	dispatcher := &audioRetryDispatcher{steps: []audioRetryResponse{{status: http.StatusOK, body: "audio-bytes"}}}
	env.deps.Router = audioRetryRouter{}
	env.deps.Selector = selector
	env.deps.CredentialVault = audioCompatibilityVault(t)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims

	rec := env.invokeJSON(t, `{"model":"tts-1","input":"compatibility failover","voice":"alloy"}`)

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
	if strings.Contains(rec.Body.String(), "wrong-audio-secret") {
		t.Fatal("response leaked mismatched credential")
	}
}

func TestAudio400DoesNotRetry(t *testing.T) {
	env := newAudioTestEnv(t, audioEndpointSpeech, upstreamResponse{})
	claims := &audioRetryClaimLifecycle{claimID: 8402}
	selector := &audioRetrySelector{accounts: []int64{44, 45}}
	dispatcher := &audioRetryDispatcher{steps: []audioRetryResponse{{
		status: http.StatusBadRequest,
		body:   `{"error":{"message":"policy-match"}}`,
	}}}
	env.deps.Router = audioRetryRouter{}
	env.deps.Selector = selector
	env.deps.CredentialVault = audioRetryVault(t, 44, 45)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims
	env.deps.Feedback = audioProjectionObserver()

	rec := env.invokeJSON(t, `{"model":"tts-1","input":"terminal error","voice":"alloy"}`)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s want 422", rec.Code, rec.Body.String())
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"error":{"code":"account_busy","message":"账号暂不可用"}}` {
		t.Fatalf("投影错误响应=%s", got)
	}
	if len(selector.requests) != 1 || len(dispatcher.accounts) != 1 || len(claims.reserves) != 1 {
		t.Fatalf("selector/dispatcher/reserve=%d/%d/%d want 1/1/1",
			len(selector.requests), len(dispatcher.accounts), len(claims.reserves))
	}
	if len(claims.aborts) != 1 || claims.aborts[0].reason != "upstream_client_4xx" {
		t.Fatalf("客户端投影不得改写终态分类: %+v", claims.aborts)
	}
}

type audioErrorPolicyProvider struct{}

func (audioErrorPolicyProvider) GetAccountErrorPolicy(int64) rate.AccountErrorPolicy {
	clientStatus := http.StatusUnprocessableEntity
	affectHealth := false
	return rate.AccountErrorPolicy{Rules: []rate.TempUnschedulableRule{{
		RuleID: "busy-400", ErrorCode: http.StatusBadRequest, Keywords: []string{"policy-match"},
		DurationMinutes: 5, ClientStatus: &clientStatus, ClientCode: "account_busy",
		MessageMode: "custom", ClientMessage: "账号暂不可用", AffectHealth: &affectHealth,
	}}}
}

func audioProjectionObserver() *upstreamfeedback.Observer {
	return upstreamfeedback.NewObserver(upstreamfeedback.Dependencies{
		RateService: rate.NewUpstreamRateService(time.Now, time.Minute,
			rate.WithAccountErrorRulesProvider(audioErrorPolicyProvider{})),
	})
}

func TestAudioAbortFailureStopsBeforeRetry(t *testing.T) {
	env := newAudioTestEnv(t, audioEndpointSpeech, upstreamResponse{})
	claims := &audioRetryClaimLifecycle{claimID: 8403, abortErr: context.DeadlineExceeded}
	selector := &audioRetrySelector{accounts: []int64{44, 45}}
	dispatcher := &audioRetryDispatcher{steps: []audioRetryResponse{
		{status: http.StatusInternalServerError, body: `{"error":"upstream busy"}`},
		{status: http.StatusOK, body: "audio-bytes"},
	}}
	env.deps.Router = audioRetryRouter{}
	env.deps.Selector = selector
	env.deps.CredentialVault = audioRetryVault(t, 44, 45)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims

	rec := env.invokeJSON(t, `{"model":"tts-1","input":"no overlapping claims","voice":"alloy"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s want 502", rec.Code, rec.Body.String())
	}
	if len(selector.requests) != 1 || len(dispatcher.accounts) != 1 || len(claims.reserves) != 1 {
		t.Fatalf("selector/dispatcher/reserve=%d/%d/%d want 1/1/1",
			len(selector.requests), len(dispatcher.accounts), len(claims.reserves))
	}
	if rec.Header().Get("X-Huakai-Abort-Failed") == "" {
		t.Fatal("abort failure must remain observable")
	}
}

func TestAudio401UsesSingleAuthFailoverBeyondAttemptBudget(t *testing.T) {
	env := newAudioTestEnv(t, audioEndpointSpeech, upstreamResponse{})
	claims := &audioRetryClaimLifecycle{claimID: 8404}
	selector := &audioRetrySelector{accounts: []int64{44, 45}}
	dispatcher := &audioRetryDispatcher{steps: []audioRetryResponse{
		{status: http.StatusUnauthorized, body: `{"error":"invalid_grant"}`},
		{status: http.StatusOK, body: "audio-bytes"},
	}}
	env.deps.Router = audioSingleAttemptRouter{}
	env.deps.Selector = selector
	env.deps.CredentialVault = audioRetryVault(t, 44, 45)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims

	rec := env.invokeJSON(t, `{"model":"tts-1","input":"auth failover","voice":"alloy"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if len(selector.requests) != 2 || len(dispatcher.accounts) != 2 || len(claims.reserves) != 2 {
		t.Fatalf("selector/dispatcher/reserve=%d/%d/%d want 2/2/2",
			len(selector.requests), len(dispatcher.accounts), len(claims.reserves))
	}
}

func TestAudioTenantRetryBudgetStopsSecondAttempt(t *testing.T) {
	env := newAudioTestEnv(t, audioEndpointSpeech, upstreamResponse{})
	claims := &audioRetryClaimLifecycle{claimID: 8405}
	selector := &audioRetrySelector{accounts: []int64{44, 45}}
	dispatcher := &audioRetryDispatcher{steps: []audioRetryResponse{{
		status: http.StatusInternalServerError,
		body:   `{"error":"upstream busy"}`,
	}}}
	budget := &audioDenyRetryBudget{}
	env.deps.Router = audioRetryRouter{}
	env.deps.Selector = selector
	env.deps.CredentialVault = audioRetryVault(t, 44, 45)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims
	env.deps.RetryBudget = budget

	rec := env.invokeJSON(t, `{"model":"tts-1","input":"tenant budget","voice":"alloy"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s want 502", rec.Code, rec.Body.String())
	}
	if len(budget.tenants) != 1 || budget.tenants[0] != 7 {
		t.Fatalf("retry budget tenants=%v want [7]", budget.tenants)
	}
	if len(dispatcher.accounts) != 1 {
		t.Fatalf("dispatcher calls=%d want 1", len(dispatcher.accounts))
	}
}

func TestAudioUsesReReservedAttemptSequenceAcrossRequests(t *testing.T) {
	env := newAudioTestEnv(t, audioEndpointSpeech, upstreamResponse{})
	claims := &audioRetryClaimLifecycle{claimID: 8406, status: "aborted", attemptSeq: 4}
	selector := &audioRetrySelector{accounts: []int64{44}}
	dispatcher := &audioRetryDispatcher{steps: []audioRetryResponse{{
		status: http.StatusOK,
		body:   "audio-bytes",
	}}}
	env.deps.Selector = selector
	env.deps.CredentialVault = audioRetryVault(t, 44)
	env.deps.Dispatcher = dispatcher
	env.deps.ClaimGate = claims
	env.deps.Settler = claims

	rec := env.invokeJSON(t, `{"model":"tts-1","input":"resume claim","voice":"alloy"}`)

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

type audioRetryRouter struct{}

func (audioRetryRouter) Plan(context.Context, router.PlanInput) (router.RoutePlan, error) {
	return router.RoutePlan{
		Attempts: []router.AttemptPlan{
			{Index: 0, PoolGroupID: 101, UpstreamModelID: "tts-1", Reason: "primary"},
			{Index: 1, PoolGroupID: 202, UpstreamModelID: "tts-1", Reason: "account_failover"},
		},
		AttemptBudget: 2,
		RetryableEndClasses: []string{
			string(gateway.UpstreamError5xx),
			string(gateway.UpstreamRateLimit),
			string(gateway.FirstTokenTimeout),
			string(gateway.InterEventTimeout),
		},
		SnapshotVersion: "registry:7:1;router:audio-retry-test",
	}, nil
}

type audioSingleAttemptRouter struct{}

func (audioSingleAttemptRouter) Plan(context.Context, router.PlanInput) (router.RoutePlan, error) {
	return router.RoutePlan{
		Attempts: []router.AttemptPlan{{
			Index: 0, PoolGroupID: 101, UpstreamModelID: "tts-1", Reason: "primary",
		}},
		AttemptBudget:   1,
		SnapshotVersion: "registry:7:1;router:audio-auth-test",
	}, nil
}

type audioRetrySelector struct {
	accounts []int64
	requests []pool.SelectionRequest
}

func (s *audioRetrySelector) Select(_ context.Context, req pool.SelectionRequest) (*pool.SelectionResult, error) {
	s.requests = append(s.requests, req)
	for _, accountID := range s.accounts {
		if _, excluded := req.ExcludedAccounts[accountID]; excluded {
			continue
		}
		return &pool.SelectionResult{
			AccountID:         accountID,
			AcquisitionToken:  uuid.New(),
			RoutingReasonJSON: []byte(`{"reason":"audio-retry-test"}`),
		}, nil
	}
	return nil, pool.ErrNoEligibleAccount
}

type audioDenyRetryBudget struct {
	tenants []int64
}

func (b *audioDenyRetryBudget) Allow(tenantID int64) bool {
	b.tenants = append(b.tenants, tenantID)
	return false
}

type audioPoolRatioResolver struct{}

func (audioPoolRatioResolver) Resolve(_ context.Context, _, poolGroupID int64) (decimal.Decimal, error) {
	if poolGroupID == 202 {
		return decimal.RequireFromString("0.5"), nil
	}
	return decimal.NewFromInt(1), nil
}

func audioRetryVault(t *testing.T, accountIDs ...int64) provider.CredentialVault {
	t.Helper()
	vault := provider.NewStaticVault()
	for _, accountID := range accountIDs {
		if err := vault.Set(accountID, provider.Credential{
			Type:  provider.CredentialTypeAPIKey,
			Value: "audio-retry-secret",
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

func audioCompatibilityVault(t *testing.T) provider.CredentialVault {
	t.Helper()
	vault := provider.NewStaticVault()
	rows := []struct {
		id, credentialID int64
		platform, secret string
	}{{44, 9044, "gemini", "wrong-audio-secret"}, {45, 9045, "openai", "right-audio-secret"}}
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

type audioRetryResponse struct {
	status int
	body   string
	err    error
}

type audioRetryDispatcher struct {
	steps    []audioRetryResponse
	accounts []int64
}

func (d *audioRetryDispatcher) Dispatch(_ context.Context, in gateway.DispatchInput) (*gateway.DispatchResult, error) {
	d.accounts = append(d.accounts, in.Account.AccountID)
	step := audioRetryResponse{status: http.StatusOK, body: "audio-bytes"}
	if len(d.accounts) <= len(d.steps) {
		step = d.steps[len(d.accounts)-1]
	}
	if step.err != nil {
		return nil, step.err
	}
	return &gateway.DispatchResult{
		StatusCode:     step.status,
		Headers:        http.Header{"Content-Type": []string{"audio/mpeg"}},
		UpstreamReader: io.NopCloser(strings.NewReader(step.body)),
		Close:          func() error { return nil },
	}, nil
}

type audioHealthSpy struct {
	signals        []channelhealth.Signal
	forceCooldowns int
}

func (s *audioHealthSpy) ApplySignal(_ context.Context, signal channelhealth.Signal) (channelhealth.Record, error) {
	s.signals = append(s.signals, signal)
	return channelhealth.Record{Key: signal.Key}, nil
}

func (s *audioHealthSpy) ForceCooldown(_ context.Context, key channelhealth.ChannelKey, _ time.Time, _ string) (channelhealth.Record, error) {
	s.forceCooldowns++
	return channelhealth.Record{Key: key}, nil
}

type audioRetryClaimLifecycle struct {
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

func (s *audioRetryClaimLifecycle) Reserve(_ context.Context, req billing.ReserveRequest) (*billing.ReserveResult, error) {
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

func (s *audioRetryClaimLifecycle) Settle(_ context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
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

func (s *audioRetryClaimLifecycle) Abort(_ context.Context, tenantID, claimID int64, reason, _ string, _ int64, _ json.RawMessage) error {
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

func (s *audioRetryClaimLifecycle) CommitCacheHit(context.Context, billing.SettleRequest) error {
	s.status = "committed"
	return nil
}

func (s *audioRetryClaimLifecycle) Refund(context.Context, billing.RefundRequest) (*billing.RefundResult, error) {
	return nil, nil
}
