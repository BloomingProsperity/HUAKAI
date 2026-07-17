package upstreamfeedback

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/authcooldown"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/rate"
)

func TestObserveHTTPError429WritesModelCooldownWithoutAccountSignal(t *testing.T) {
	now := time.Date(2026, 7, 16, 16, 0, 0, 0, time.UTC)
	health := &channelHealthSpy{}
	models := &modelCooldownSpy{}
	observer := NewObserver(Dependencies{
		ChannelHealth:  health,
		ModelCooldowns: models,
		Now:            func() time.Time { return now },
	})

	got := observer.ObserveHTTPError(
		context.Background(),
		validAttempt("openai"),
		http.StatusTooManyRequests,
		http.Header{"Retry-After": []string{"60"}},
		[]byte(`{"error":"rate limited"}`),
	)

	if !got.Decision.RetryableBeforeDelivery || !got.Decision.SwitchAccount {
		t.Fatalf("429 decision=%+v want pre-delivery account failover", got.Decision)
	}
	if got.Classification.Class != gateway.ErrorClassRateLimited {
		t.Fatalf("classification=%s want %s", got.Classification.Class, gateway.ErrorClassRateLimited)
	}
	if len(models.inputs) != 1 {
		t.Fatalf("model cooldown calls=%d want 1", len(models.inputs))
	}
	in := models.inputs[0]
	if in.TenantID != 7 || in.ProviderAccountID != 44 || in.ModelKey != "provider-model" {
		t.Fatalf("model cooldown scope=%+v want tenant/account/model 7/44/provider-model", in)
	}
	if in.StatusCode != http.StatusTooManyRequests || in.Reason != rate.ReasonRateLimitRPM {
		t.Fatalf("model cooldown status/reason=%d/%s want 429/%s", in.StatusCode, in.Reason, rate.ReasonRateLimitRPM)
	}
	if !in.ResetAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("model cooldown reset=%s want %s", in.ResetAt, now.Add(time.Minute))
	}
	if len(health.signals) != 0 || len(health.forceCooldowns) != 0 {
		t.Fatalf("纯模型 429 不得污染账号健康: signals=%+v force=%+v", health.signals, health.forceCooldowns)
	}
}

func TestObserveHTTPErrorUsesProtocolProviderForBedrockRules(t *testing.T) {
	observer := NewObserver(Dependencies{})
	attempt := validAttempt("anthropic")
	attempt.ProtocolFamily = "bedrock_invoke"

	got := observer.ObserveHTTPError(
		context.Background(),
		attempt,
		http.StatusTooManyRequests,
		nil,
		[]byte(`{"type":"ThrottlingException"}`),
	)

	if got.Classification.RuleID != "R-018" {
		t.Fatalf("classification rule=%s want R-018 for bedrock throttling", got.Classification.RuleID)
	}
	if got.Classification.Class != gateway.ErrorClassRateLimited {
		t.Fatalf("classification=%s want %s", got.Classification.Class, gateway.ErrorClassRateLimited)
	}
}

func TestObserveHTTPError401RecordsAuthChallengeAndDeduplicatesRefresh(t *testing.T) {
	health := &channelHealthSpy{}
	refreshCalls := make(chan refreshCall, 2)
	refreshResults := make(chan authRefreshResult, 2)
	observer := NewObserver(Dependencies{
		ChannelHealth:          health,
		CredentialHotRefresher: refreshSpy{calls: refreshCalls},
		AuthCooldown:           authCooldownSpy{results: refreshResults},
		RefreshDedupeWindow:    time.Minute,
	})
	attempt := validAttempt("openai")

	first := observer.ObserveHTTPError(
		context.Background(),
		attempt,
		http.StatusUnauthorized,
		nil,
		[]byte(`{"error":"invalid_grant"}`),
	)
	second := observer.ObserveHTTPError(
		context.Background(),
		attempt,
		http.StatusUnauthorized,
		nil,
		[]byte(`{"error":"invalid_grant"}`),
	)

	if !first.Decision.CountsAgainstAuthFailoverBudget || !second.Decision.CountsAgainstAuthFailoverBudget {
		t.Fatalf("401 decisions=%+v/%+v want auth failover sub-budget", first.Decision, second.Decision)
	}
	if len(health.signals) != 2 {
		t.Fatalf("auth health signals=%d want 2 request observations", len(health.signals))
	}
	for _, signal := range health.signals {
		if signal.Class != channelhealth.SignalAuthChallenge || signal.AuthFailureClass != authcooldown.ClassIronClad {
			t.Fatalf("auth signal=%+v want iron-clad auth challenge", signal)
		}
	}
	select {
	case call := <-refreshCalls:
		if call.tenantID != 7 || call.accountID != 44 || call.vendor != "openai" {
			t.Fatalf("refresh call=%+v want tenant/account/vendor 7/44/openai", call)
		}
	case <-time.After(time.Second):
		t.Fatal("401 did not trigger credential hot refresh")
	}
	select {
	case duplicate := <-refreshCalls:
		t.Fatalf("dedupe window allowed duplicate refresh: %+v", duplicate)
	case <-time.After(30 * time.Millisecond):
	}
	select {
	case result := <-refreshResults:
		if result.accountID != 44 || !result.succeeded || result.permanentFailure {
			t.Fatalf("refresh result=%+v want successful non-permanent result", result)
		}
	case <-time.After(time.Second):
		t.Fatal("refresh result did not reach auth cooldown lane")
	}
}

func TestObserveDispatchConnectionRefusedRecordsChannelError(t *testing.T) {
	health := &channelHealthSpy{}
	observer := NewObserver(Dependencies{ChannelHealth: health})

	decision := observer.ObserveDispatchError(
		context.Background(),
		validAttempt("openai"),
		errors.New("dial tcp 203.0.113.10:443: connect: connection refused"),
	)

	if !decision.RetryableBeforeDelivery || !decision.SwitchAccount {
		t.Fatalf("decision=%+v want retryable account failover", decision)
	}
	if decision.TransportClass != gateway.TransportErrorConnectionRefused {
		t.Fatalf("transport class=%s want %s", decision.TransportClass, gateway.TransportErrorConnectionRefused)
	}
	if len(health.signals) != 1 || health.signals[0].Class != channelhealth.SignalChannelError {
		t.Fatalf("health signals=%+v want one channel_error", health.signals)
	}
}

func TestObserveSuccessUpdatesAnthropicSessionAndSelfHeals(t *testing.T) {
	health := &channelHealthSpy{}
	rates := &rateServiceSpy{}
	recent := &recentRequestsSpy{}
	observer := NewObserver(Dependencies{
		ChannelHealth:  health,
		RateService:    rates,
		RecentRequests: recent,
	})
	headers := http.Header{"Anthropic-Ratelimit-Unified-Reset": []string{"2026-07-16T17:00:00Z"}}

	observer.ObserveSuccess(context.Background(), validAttempt("anthropic"), http.StatusOK, headers)

	if rates.updateCalls != 1 || rates.lastAccountID != 44 {
		t.Fatalf("session update calls/account=%d/%d want 1/44", rates.updateCalls, rates.lastAccountID)
	}
	if len(health.signals) != 1 || health.signals[0].Class != channelhealth.SignalSuccess {
		t.Fatalf("health signals=%+v want one success", health.signals)
	}
	if len(recent.calls) != 1 || recent.calls[0].accountID != 44 || !recent.calls[0].success {
		t.Fatalf("recent request calls=%+v want account 44 success", recent.calls)
	}
}

func validAttempt(platform string) Attempt {
	return Attempt{
		TenantID: 7,
		Account: provider.AccountInfo{
			AccountID:           44,
			TenantID:            7,
			Platform:            platform,
			AccountCredentialID: 4044,
			CredentialVersion:   3,
		},
		ProtocolFamily: "openai_chat",
		ModelKey:       "provider-model",
		RequestID:      "req-feedback",
		StartedAt:      time.Now().Add(-10 * time.Millisecond),
	}
}

type channelHealthSpy struct {
	signals        []channelhealth.Signal
	forceCooldowns []forceCooldownCall
}

type forceCooldownCall struct {
	key    channelhealth.ChannelKey
	until  time.Time
	reason string
}

func (s *channelHealthSpy) ApplySignal(_ context.Context, signal channelhealth.Signal) (channelhealth.Record, error) {
	s.signals = append(s.signals, signal)
	return channelhealth.Record{Key: signal.Key}, nil
}

func (s *channelHealthSpy) ForceCooldown(_ context.Context, key channelhealth.ChannelKey, until time.Time, reason string) (channelhealth.Record, error) {
	s.forceCooldowns = append(s.forceCooldowns, forceCooldownCall{key: key, until: until, reason: reason})
	return channelhealth.Record{Key: key}, nil
}

type modelCooldownSpy struct {
	inputs []rate.ModelCooldownInput
}

func (s *modelCooldownSpy) RecordModelRateLimit(_ context.Context, in rate.ModelCooldownInput) error {
	s.inputs = append(s.inputs, in)
	return nil
}

type refreshCall struct {
	tenantID  int64
	accountID int64
	vendor    string
}

type refreshSpy struct {
	calls chan<- refreshCall
}

func (s refreshSpy) RefreshHotPath(_ context.Context, tenantID, accountID int64, vendor string) error {
	s.calls <- refreshCall{tenantID: tenantID, accountID: accountID, vendor: vendor}
	return nil
}

type authRefreshResult struct {
	accountID        int64
	succeeded        bool
	permanentFailure bool
}

type authCooldownSpy struct {
	results chan<- authRefreshResult
}

func (s authCooldownSpy) OnRefreshResult(_ context.Context, accountID int64, succeeded, permanentFailure bool) {
	s.results <- authRefreshResult{
		accountID:        accountID,
		succeeded:        succeeded,
		permanentFailure: permanentFailure,
	}
}

type rateServiceSpy struct {
	updateCalls   int
	lastAccountID int64
}

func (s *rateServiceSpy) HandleUpstreamError(context.Context, int64, int, http.Header, []byte) (rate.Decision, error) {
	return rate.Decision{}, nil
}

func (s *rateServiceSpy) ClearCascade(context.Context, int64, string) error {
	return nil
}

func (s *rateServiceSpy) UpdateSessionWindow(_ context.Context, accountID int64, _ http.Header) error {
	s.updateCalls++
	s.lastAccountID = accountID
	return nil
}

type recentRequestCall struct {
	accountID int64
	success   bool
}

type recentRequestsSpy struct {
	calls []recentRequestCall
}

func (s *recentRequestsSpy) Record(accountID int64, success bool) {
	s.calls = append(s.calls, recentRequestCall{accountID: accountID, success: success})
}
