package gatewayhttp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/authcooldown"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	l2cache "github.com/BloomingProsperity/HUAKAI/internal/cache"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	provideranthropic "github.com/BloomingProsperity/HUAKAI/internal/provider/anthropic"
	provideropenai "github.com/BloomingProsperity/HUAKAI/internal/provider/openai"
	"github.com/BloomingProsperity/HUAKAI/internal/quota"
	"github.com/BloomingProsperity/HUAKAI/internal/quotaenforce"
	"github.com/BloomingProsperity/HUAKAI/internal/rate"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/retrybudget"
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

func TestPR5NonStream429RecordsModelCooldownAndRetriesNextAccount(t *testing.T) {
	enableHCSFDispatchForTest(t)
	now := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	selector := newPR5Selector(t, 201, 202)
	health := &recordingChannelHealth{}
	modelCooldowns := &recordingModelRateLimiter{}
	dispatcher := &pr5CanonicalSequenceDispatcher{
		steps: []pr5CanonicalStep{
			{status: http.StatusTooManyRequests, body: `{"error":"rate limited"}`, headers: http.Header{"Retry-After": []string{"3600"}}},
			{successText: "success after rate limit"},
		},
	}
	deps := pr5NonStreamDeps(t, selector, &pr5ClaimGate{claimID: 88002}, &recordingSettler{}, dispatcher)
	deps.ChannelHealth = health
	deps.ModelCooldowns = modelCooldowns
	deps.RateService = rate.NewUpstreamRateService(func() time.Time { return now }, time.Minute)
	deps.Router = stubRouter{plan: pr5RoutePlan(
		router.AttemptPlan{Index: 0, PoolGroupID: 42, UpstreamModelID: "provider-gpt-4o", Reason: "primary"},
		router.AttemptPlan{Index: 1, PoolGroupID: 42, UpstreamModelID: "provider-gpt-4o", Reason: "same_pool_account_failover"},
	)}

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s; want 200 after 429 failover", rec.Code, rec.Body.String())
	}
	if selector.calls != 2 {
		t.Fatalf("selector calls=%d want 2", selector.calls)
	}
	if _, excluded := selector.requests[1].ExcludedAccounts[201]; !excluded {
		t.Fatalf("second attempt exclusions=%v want failed account 201 excluded for this request", selector.requests[1].ExcludedAccounts)
	}
	if got := countHealthSignals(health, channelhealth.SignalRateLimit); got != 0 {
		t.Fatalf("SignalRateLimit count=%d want 0;纯 429 不得污染账号健康", got)
	}
	if len(health.forceCooldowns) != 0 {
		t.Fatalf("ForceCooldown calls=%d want 0; calls=%+v", len(health.forceCooldowns), health.forceCooldowns)
	}
	if modelCooldowns.calls != 1 {
		t.Fatalf("model cooldown calls=%d want 1", modelCooldowns.calls)
	}
	if in := modelCooldowns.input; in.TenantID != validIdentity().TenantID || in.ProviderAccountID != 201 || in.ModelKey != "provider-gpt-4o" {
		t.Fatalf("model cooldown scope=%+v want tenant/current account/upstream model", in)
	}
	if modelCooldowns.input.Reason != rate.ReasonRateLimitRPM || modelCooldowns.input.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("model cooldown reason/status=%s/%d want rpm/429", modelCooldowns.input.Reason, modelCooldowns.input.StatusCode)
	}
	if !modelCooldowns.input.ResetAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("model cooldown reset=%s want %s", modelCooldowns.input.ResetAt, now.Add(time.Hour))
	}
}

func TestPR5NonStreamQuota429RecordsAccountSuspendedNotModelCooldown(t *testing.T) {
	enableHCSFDispatchForTest(t)
	now := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	selector := newPR5Selector(t, 231, 232)
	health := &recordingChannelHealth{}
	modelCooldowns := &recordingModelRateLimiter{}
	dispatcher := &pr5CanonicalSequenceDispatcher{
		steps: []pr5CanonicalStep{
			{status: http.StatusTooManyRequests, body: `{"error":{"type":"insufficient_quota","message":"billing hard limit reached"}}`},
			{successText: "must not retry quota exhausted account"},
		},
	}
	deps := pr5NonStreamDeps(t, selector, &pr5ClaimGate{claimID: 88012}, &recordingSettler{}, dispatcher)
	deps.ChannelHealth = health
	deps.ModelCooldowns = modelCooldowns
	deps.RateService = rate.NewUpstreamRateService(func() time.Time { return now }, time.Minute)

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d body=%s; want upstream quota 429 returned without rate failover", rec.Code, rec.Body.String())
	}
	if selector.calls != 1 || dispatcher.calls != 1 {
		t.Fatalf("selector/dispatcher calls=%d/%d want 1/1;配额耗尽不应按纯限速换号", selector.calls, dispatcher.calls)
	}
	if modelCooldowns.calls != 0 {
		t.Fatalf("model cooldown calls=%d want 0 for quota exhausted 429", modelCooldowns.calls)
	}
	if len(health.forceCooldowns) != 0 {
		t.Fatalf("ForceCooldown calls=%d want 0;配额耗尽由 SignalAccountSuspended 处理", len(health.forceCooldowns))
	}
	if got := countHealthSignals(health, channelhealth.SignalAccountSuspended); got != 1 {
		t.Fatalf("SignalAccountSuspended count=%d want 1;signals=%+v", got, health.signals)
	}
	if got := countHealthSignals(health, channelhealth.SignalRateLimit); got != 0 {
		t.Fatalf("SignalRateLimit count=%d want 0 for quota exhausted 429", got)
	}
}

func TestPR5RawNonStreamHeaderTimeoutRetriesSecondAccount(t *testing.T) {
	t.Setenv("HUAKAI_DISPATCH_HCSF", "0")
	responseBody := `{"id":"chatcmpl-pr5","object":"chat.completion","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"second account raw success"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`
	rt := pr5SequencedHeaderTransport([]time.Duration{200 * time.Millisecond, 0}, []string{responseBody, responseBody})
	t.Cleanup(rt.CloseIdleConnections)
	tf := transport.NewFactory()
	tf.SetStandard(rt)
	adapters := provider.NewStaticRegistry()
	adapters.MustRegister("openai_chat", &provideropenai.PassthroughAdapter{Endpoint: "http://upstream.test/v1/chat/completions"})
	selector := newPR5Selector(t, 331, 332)
	settler := &recordingSettler{}
	deps := pr5NonStreamDeps(t, selector, &pr5ClaimGate{claimID: 88331}, settler, nil)
	deps.Dispatcher = &gateway.UpstreamDispatcher{
		Adapters:         adapters,
		TransportFactory: tf,
		Timeouts: gateway.TimeoutConfig{
			HeaderToFirstByte:   25 * time.Millisecond,
			RequestTotalTimeout: time.Second,
		},
	}

	started := time.Now()
	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	elapsed := time.Since(started)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s; want second account success after header timeout failover", rec.Code, rec.Body.String())
	}
	if elapsed > 150*time.Millisecond {
		t.Fatalf("non-stream failover elapsed=%s; removing header timeout waits for slow first account", elapsed)
	}
	if selector.calls != 2 {
		t.Fatalf("selector calls=%d want 2", selector.calls)
	}
	if _, excluded := selector.requests[1].ExcludedAccounts[331]; !excluded {
		t.Fatalf("second attempt exclusions=%v want timed-out first account 331 excluded", selector.requests[1].ExcludedAccounts)
	}
	if len(settler.aborts) != 1 || settler.aborts[0].reason != "transport_upstream_header_timeout" {
		t.Fatalf("aborts=%+v want one transport_upstream_header_timeout abort", settler.aborts)
	}
	if len(settler.calls) != 1 || settler.calls[0].AccountID != 332 {
		t.Fatalf("settle calls=%+v want success on account 332", settler.calls)
	}
}

func TestPR5RawNonStreamCustomErrorPolicyForcesAccountCooldown(t *testing.T) {
	t.Setenv("HUAKAI_DISPATCH_HCSF", "0")
	tf := transport.NewFactory()
	tf.SetStandard(pr5RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"policy-match"}}`)),
			Request:    req,
		}, nil
	}))
	adapters := provider.NewStaticRegistry()
	adapters.MustRegister("openai_chat", &provideropenai.PassthroughAdapter{Endpoint: "http://upstream.test/v1/chat/completions"})
	selector := newPR5Selector(t, 333)
	settler := &recordingSettler{}
	health := &recordingChannelHealth{}
	deps := pr5NonStreamDeps(t, selector, &pr5ClaimGate{claimID: 88333}, settler, nil)
	deps.Dispatcher = &gateway.UpstreamDispatcher{Adapters: adapters, TransportFactory: tf}
	deps.ChannelHealth = health
	deps.RateService = rate.NewUpstreamRateService(time.Now, time.Minute,
		rate.WithAccountErrorRulesProvider(chatErrorPolicyProvider{}))

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s，期望自定义投影 422", rec.Code, rec.Body.String())
	}
	if selector.calls != 1 || len(settler.calls) != 0 || len(settler.aborts) != 1 {
		t.Fatalf("selector/settle/abort=%d/%d/%d，期望单次失败且不结算", selector.calls, len(settler.calls), len(settler.aborts))
	}
	if len(health.forceCooldowns) != 1 || health.forceCooldowns[0].key.ProviderAccountID != 333 {
		t.Fatalf("raw-buffered 路径未执行账号显式冷却: %+v", health.forceCooldowns)
	}
	if len(health.signals) != 0 {
		t.Fatalf("affect_health=false 只能抑制普通健康信号，得到 %+v", health.signals)
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
	// 缺口① 修复后:401 auth 会记 SignalAuthChallenge(走 auth 降级车道临时排除坏号),但绝不进
	// 健康 FSM——守护每条信号都是 auth_challenge,不是 rate_limit/5xx/error 等健康降级类,
	// 即 401 仍不污染健康分/error-rate(与旧「完全不记信号」等价的健康不变量,机制升级为独立车道)。
	if len(health.signals) == 0 {
		t.Fatal("canonical 401 应至少记一条 auth_challenge 信号")
	}
	for _, sig := range health.signals {
		if sig.Class != channelhealth.SignalAuthChallenge {
			t.Fatalf("401 应只记 auth_challenge(不污染健康),实得 %+v", health.signals)
		}
		// 审查 S2:canonical 缓冲(默认非流式主路径)必须带真实 iron-clad 分级——body 是
		// invalid_grant(R-001)→ iron-clad;若该路径 authClass 写死 0(ambiguous),strike 硬禁
		// 在主路径永不可达。判别:call-site 传回字面量 0 → AuthFailureClass=ambiguous → 红。
		if sig.AuthFailureClass != authcooldown.ClassIronClad {
			t.Fatalf("canonical 401 invalid_grant 应带 iron-clad 分级,实得 %v(主路径 authClass 丢失)", sig.AuthFailureClass)
		}
	}
}

func TestPR5NonStream401TriggersNonBlockingHotRefreshAndStillReturnsSecondAccount(t *testing.T) {
	enableHCSFDispatchForTest(t)
	selector := newPR5Selector(t, 311, 312)
	settler := &recordingSettler{}
	dispatcher := &pr5CanonicalSequenceDispatcher{
		steps: []pr5CanonicalStep{
			{status: http.StatusUnauthorized, body: `{"error":"invalid_grant"}`},
			{successText: "second account wins while refresh keeps running"},
		},
	}
	refresher := newBlockingHotRefreshSpy()
	deps := pr5NonStreamDeps(t, selector, &pr5ClaimGate{claimID: 88311}, settler, dispatcher)
	deps.CredentialHotRefresher = refresher

	done := make(chan *httptestResponse, 1)
	go func() {
		rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
		done <- &httptestResponse{code: rec.Code, body: rec.Body.String()}
	}()

	select {
	case call := <-refresher.called:
		if call.accountID != 311 || call.tenantID != 7 || call.vendor != "openai" {
			t.Fatalf("hot refresh call=%+v, want tenant 7 account 311 vendor openai", call)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("hot refresh was not triggered for retryable OAuth auth failure")
	}

	select {
	case rec := <-done:
		if rec.code != http.StatusOK {
			t.Fatalf("status=%d body=%s; want second account success while refresh is blocked", rec.code, rec.body)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("handler waited for hot refresh; refresh must be best-effort background work")
	}
	close(refresher.release)
	if selector.calls != 2 || dispatcher.calls != 2 {
		t.Fatalf("selector/dispatcher calls=%d/%d want 2/2", selector.calls, dispatcher.calls)
	}
	if len(settler.calls) != 1 || settler.calls[0].AccountID != 312 {
		t.Fatalf("settle calls=%+v want success settled on second account 312", settler.calls)
	}
}

func TestPR5NonStream401HotRefreshDedupesSameAccountWithinWindow(t *testing.T) {
	enableHCSFDispatchForTest(t)
	selector := newPR5Selector(t, 321, 322)
	dispatcher := &pr5CanonicalSequenceDispatcher{
		steps: []pr5CanonicalStep{
			{status: http.StatusUnauthorized, body: `{"error":"invalid_grant"}`},
			{successText: "first request success"},
			{status: http.StatusUnauthorized, body: `{"error":"invalid_grant"}`},
			{successText: "second request success"},
		},
	}
	refresher := newRecordingHotRefreshSpy()
	deps := pr5NonStreamDeps(t, selector, &pr5ClaimGate{claimID: 88321}, &recordingSettler{}, dispatcher)
	deps.CredentialHotRefresher = refresher
	handler := NewChatCompletionsHandler(deps)

	first := invokeExistingHandlerPath(handler, "/v1/chat/completions", pr5NonStreamBody())
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s; want success after auth failover", first.Code, first.Body.String())
	}
	if got := refresher.waitForCall(t); got.accountID != 321 {
		t.Fatalf("first hot refresh account=%d want 321", got.accountID)
	}
	second := invokeExistingHandlerPath(handler, "/v1/chat/completions", pr5NonStreamBody())
	if second.Code != http.StatusOK {
		t.Fatalf("second status=%d body=%s; want success after auth failover", second.Code, second.Body.String())
	}
	select {
	case call := <-refresher.called:
		t.Fatalf("duplicate hot refresh call within dedupe window: %+v", call)
	case <-time.After(150 * time.Millisecond):
	}
	if calls := refresher.snapshot(); len(calls) != 1 || calls[0].accountID != 321 {
		t.Fatalf("hot refresh calls=%+v want exactly one call for account 321", calls)
	}
}

func TestCodexAgentInvalidTaskRecoversSameRequestOnceWithoutHealthPenalty(t *testing.T) {
	enableHCSFDispatchForTest(t)
	selector := newPR5Selector(t, 341)
	settler := &recordingSettler{}
	dispatcher := &pr5CanonicalSequenceDispatcher{steps: []pr5CanonicalStep{
		{status: http.StatusUnauthorized, body: `{"error":{"code":"invalid_task_id"}}`},
		{successText: "recovered agent task"},
	}}
	recoverer := &recordingAgentTaskRecoverer{}
	hotRefresher := newRecordingHotRefreshSpy()
	health := &recordingChannelHealth{}
	deps := pr5NonStreamDeps(t, selector, &pr5ClaimGate{claimID: 88341}, settler, dispatcher)
	deps.Registry = stubRegistry{resolved: registry.Resolved{
		PublicAlias: "gpt-4o", CanonicalModelID: "openai/gpt-4o", ProviderModelID: "gpt-4o",
		ProtocolFamily: "openai_codex", PoolCandidates: []int64{42},
	}}
	deps.CredentialVault = codexAgentTestVault(t, 341)
	deps.AgentTaskRecoverer = recoverer
	deps.CredentialHotRefresher = hotRefresher
	deps.ChannelHealth = health

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if dispatcher.calls != 2 || selector.calls != 2 || len(dispatcher.accounts) != 2 ||
		dispatcher.accounts[0] != 341 || dispatcher.accounts[1] != 341 {
		t.Fatalf("dispatcher=%d selector=%d accounts=%v，期望同账号请求内重试一次", dispatcher.calls, selector.calls, dispatcher.accounts)
	}
	if recoverer.calls != 1 || recoverer.accountID != 341 || recoverer.tenantID != 7 || recoverer.version != 1 {
		t.Fatalf("recoverer=%+v", recoverer)
	}
	if len(health.forceCooldowns) != 0 {
		t.Fatalf("可恢复任务失效不应写账号冷却：%v", health.forceCooldowns)
	}
	for _, signal := range health.signals {
		if signal.Class != channelhealth.SignalSuccess {
			t.Fatalf("任务失效本身不应写健康惩罚，恢复后只允许成功信号：%v", health.signals)
		}
	}
	select {
	case call := <-hotRefresher.called:
		t.Fatalf("任务恢复不应触发普通 OAuth 后台刷新：%+v", call)
	case <-time.After(50 * time.Millisecond):
	}
	if len(settler.aborts) != 1 || len(settler.calls) != 1 {
		t.Fatalf("aborts=%v settles=%v，期望首尝试释放、恢复尝试结算", settler.aborts, settler.calls)
	}
}

func TestAntigravityProjectPermissionRecoversSameRequestOnceWithoutPermanentDisable(t *testing.T) {
	enableHCSFDispatchForTest(t)
	selector := newPR5Selector(t, 345)
	settler := &recordingSettler{}
	dispatcher := &pr5CanonicalSequenceDispatcher{steps: []pr5CanonicalStep{
		{status: http.StatusForbidden, body: `{"error":{"message":"Permission denied on resource project","status":"PERMISSION_DENIED"}}`},
		{successText: "recovered antigravity project"},
	}}
	refresher := newRecordingHotRefreshSpy()
	health := &recordingChannelHealth{}
	deps := pr5NonStreamDeps(t, selector, &pr5ClaimGate{claimID: 88345}, settler, dispatcher)
	deps.Registry = stubRegistry{resolved: registry.Resolved{
		PublicAlias: "gemini-pro", CanonicalModelID: "gemini/gemini-pro", ProviderModelID: "gemini-pro",
		ProtocolFamily: "antigravity_session", PoolCandidates: []int64{42},
	}}
	deps.CredentialVault = antigravityProjectTestVault(t, 345)
	deps.CredentialHotRefresher = refresher
	deps.ChannelHealth = health

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s selector=%d dispatcher=%d refresh=%+v aborts=%+v", rec.Code, rec.Body.String(), selector.calls, dispatcher.calls, refresher.snapshot(), settler.aborts)
	}
	if dispatcher.calls != 2 || selector.calls != 2 || len(dispatcher.accounts) != 2 ||
		dispatcher.accounts[0] != 345 || dispatcher.accounts[1] != 345 {
		t.Fatalf("dispatcher=%d selector=%d accounts=%v，期望同账号请求内只恢复一次", dispatcher.calls, selector.calls, dispatcher.accounts)
	}
	if calls := refresher.snapshot(); len(calls) != 1 || calls[0].tenantID != 7 || calls[0].accountID != 345 || calls[0].vendor != credentialstore.VendorAntigravity {
		t.Fatalf("project refresh calls=%+v", calls)
	}
	for _, signal := range health.signals {
		if signal.Class != channelhealth.SignalSuccess {
			t.Fatalf("项目恢复成功前不得误记永久或降级健康信号：%v", health.signals)
		}
	}
	if len(settler.aborts) != 1 || len(settler.calls) != 1 {
		t.Fatalf("aborts=%v settles=%v，期望首尝试释放、恢复尝试结算", settler.aborts, settler.calls)
	}
}

func TestCodexAgentInvalidTaskDoesNotRecoverMoreThanOncePerRequest(t *testing.T) {
	enableHCSFDispatchForTest(t)
	selector := newPR5Selector(t, 351)
	dispatcher := &pr5CanonicalSequenceDispatcher{steps: []pr5CanonicalStep{
		{status: http.StatusUnauthorized, body: `{"error":{"code":"task_expired"}}`},
		{status: http.StatusUnauthorized, body: `{"error":{"code":"task_expired"}}`},
	}}
	recoverer := &recordingAgentTaskRecoverer{}
	deps := pr5NonStreamDeps(t, selector, &pr5ClaimGate{claimID: 88351}, &recordingSettler{}, dispatcher)
	deps.Registry = stubRegistry{resolved: registry.Resolved{
		PublicAlias: "gpt-4o", CanonicalModelID: "openai/gpt-4o", ProviderModelID: "gpt-4o",
		ProtocolFamily: "openai_codex", PoolCandidates: []int64{42},
	}}
	deps.CredentialVault = codexAgentTestVault(t, 351)
	deps.AgentTaskRecoverer = recoverer

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s，二次失效应终止", rec.Code, rec.Body.String())
	}
	if recoverer.calls != 1 || dispatcher.calls != 2 {
		t.Fatalf("recovery=%d dispatch=%d，必须严格一次恢复", recoverer.calls, dispatcher.calls)
	}
}

type claudeSessionSuccessDoer struct {
	calls         int
	urls          []string
	authorization []string
	bodies        [][]byte
}

type claudeSessionStreamingDoer struct {
	calls int
	url   string
	auth  string
	body  []byte
}

func (d *claudeSessionStreamingDoer) Do(req *http.Request) (*http.Response, error) {
	d.body, _ = io.ReadAll(req.Body)
	d.calls++
	d.url = req.URL.String()
	d.auth = req.Header.Get("Authorization")
	wire := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg-r1a-stream","model":"claude-sonnet","usage":{"input_tokens":2}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"pong"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(wire)),
	}, nil
}

func (d *claudeSessionSuccessDoer) Do(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	d.calls++
	d.urls = append(d.urls, req.URL.String())
	d.authorization = append(d.authorization, req.Header.Get("Authorization"))
	d.bodies = append(d.bodies, body)
	response := `{"id":"msg-r1a","type":"message","role":"assistant","model":"claude-sonnet","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":3}}`
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(response)),
	}, nil
}

type claudeSessionQuotaFinalizer struct {
	settles  []quota.SettleRequest
	releases []quota.ReleaseRequest
}

func (f *claudeSessionQuotaFinalizer) Settle(_ context.Context, req quota.SettleRequest) (quota.SettleResult, error) {
	f.settles = append(f.settles, req)
	return quota.SettleResult{}, nil
}

func (f *claudeSessionQuotaFinalizer) Release(_ context.Context, req quota.ReleaseRequest) (quota.ReleaseResult, error) {
	f.releases = append(f.releases, req)
	return quota.ReleaseResult{}, nil
}

func (f *claudeSessionQuotaFinalizer) CommitCacheHit(_ context.Context, req quota.CacheHitRequest) (quota.CacheHitResult, error) {
	return quota.CacheHitResult{}, nil
}

func claudeSessionOfficialBody() string {
	return `{
  "model":"claude-sonnet",
  "max_tokens":8,
  "stream":false,
  "system":[{"type":"text","text":"supported CLI"}],
  "metadata":{"user_id":"user_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa_account_00000000-1111-2222-3333-444444444444_session_11111111-2222-3333-4444-555555555555"},
  "messages":[{"role":"user","content":"hi"}]
}`
}

func claudeSessionOfficialHeaders() map[string]string {
	return map[string]string{
		"User-Agent":                  "claude-cli/2.1.78 (external, cli)",
		"X-App":                       "cli",
		"X-Stainless-Lang":            "js",
		"X-Stainless-Runtime":         "node",
		"X-Stainless-Package-Version": "0.74.0",
		"X-Stainless-Retry-Count":     "0",
		"Anthropic-Version":           "2023-06-01",
		"Anthropic-Beta":              "claude-code-20250219",
	}
}

// TestClaudeSessionLocalExpiryReleasesARefreshesAndSettlesBOnce 是 R1A 的资源
// 生命周期判别用例：A 在 adapter 本地发现过期后不得发 HTTP，必须 abort/quota
// release、触发一次刷新并加入排除；B 成功且 billing/quota 各只 settle 一次。
func TestClaudeSessionLocalExpiryReleasesARefreshesAndSettlesBOnce(t *testing.T) {
	t.Setenv("HUAKAI_DISPATCH_HCSF", "0")
	selector := newPR5Selector(t, 7101, 7102)
	claimGate := &pr5ClaimGate{claimID: 88710}
	innerSettler := &recordingSettler{}
	quotaFinalizer := &claudeSessionQuotaFinalizer{}
	refresher := newRecordingHotRefreshSpy()
	health := &recordingChannelHealth{}
	doer := &claudeSessionSuccessDoer{}

	adapters := provider.NewStaticRegistry()
	adapters.MustRegister("anthropic_claude_session", &provideranthropic.OAuthSessionAdapter{
		Now: func() time.Time { return time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC) },
	})
	tf := transport.NewFactory()
	tf.SetSidecarForTesting(http.DefaultTransport)

	vault := provider.NewStaticVault()
	for _, account := range []struct {
		id      int64
		token   string
		expires string
	}{
		{7101, "expired-A", "2026-07-10T11:59:00Z"},
		{7102, "fresh-B", "2026-07-10T13:00:00Z"},
	} {
		if err := vault.Set(account.id, provider.Credential{
			Type: provider.CredentialTypeOAuthAccessToken, Value: account.token,
			Extra: map[string]string{"expires_at": account.expires},
		}, provider.AccountInfo{
			AccountID: account.id, Platform: credentialstore.VendorAnthropic,
			AccountType:         credentialstore.AuthModeClaudeAIOAuth,
			AccountCredentialID: 9000 + account.id, CredentialVersion: 1,
		}); err != nil {
			t.Fatalf("vault.Set(%d): %v", account.id, err)
		}
	}

	deps := pr5NonStreamDeps(t, selector, claimGate, quotaenforce.NewSettler(innerSettler, quotaFinalizer), nil)
	deps.Registry = stubRegistry{resolved: registry.Resolved{
		PublicAlias: "claude-sonnet", CanonicalModelID: "anthropic/claude-sonnet",
		ProviderModelID: "claude-sonnet", ProtocolFamily: "anthropic_claude_session",
		PoolCandidates: []int64{42},
	}}
	deps.Router = stubRouter{plan: pr5RoutePlan(router.AttemptPlan{
		Index: 0, PoolGroupID: 42, UpstreamModelID: "claude-sonnet", Reason: "primary",
	})}
	deps.CredentialVault = vault
	deps.CredentialHotRefresher = refresher
	deps.ChannelHealth = health
	deps.Dispatcher = &gateway.UpstreamDispatcher{
		Adapters: adapters, TransportFactory: tf, HTTPClient: doer,
	}

	rec := invokeHandlerPathWithHeaders(t, deps, "/v1/messages", claudeSessionOfficialBody(), claudeSessionOfficialHeaders())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s; want B success", rec.Code, rec.Body.String())
	}
	if selector.calls != 2 || len(selector.requests) != 2 {
		t.Fatalf("selector calls=%d requests=%d want 2", selector.calls, len(selector.requests))
	}
	if _, excluded := selector.requests[1].ExcludedAccounts[7101]; !excluded {
		t.Fatalf("second exclusions=%v want expired A", selector.requests[1].ExcludedAccounts)
	}
	if doer.calls != 1 || len(doer.authorization) != 1 || doer.authorization[0] != "Bearer fresh-B" {
		t.Fatalf("HTTP calls=%d auth=%v want only fresh B", doer.calls, doer.authorization)
	}
	if len(doer.urls) != 1 || doer.urls[0] != "https://api.anthropic.com/v1/messages?beta=true" {
		t.Fatalf("upstream URLs=%v want official beta=true", doer.urls)
	}
	if len(doer.bodies) != 1 || !bytes.Equal(doer.bodies[0], []byte(claudeSessionOfficialBody())) {
		t.Fatalf("raw buffered 官方直发 body 漂移\ngot:  %s\nwant: %s", doer.bodies[0], claudeSessionOfficialBody())
	}
	if len(innerSettler.aborts) != 1 || innerSettler.aborts[0].reason != "local_credential_expired" {
		t.Fatalf("billing aborts=%+v want one local expiry", innerSettler.aborts)
	}
	if len(innerSettler.calls) != 1 || innerSettler.calls[0].AccountID != 7102 || innerSettler.calls[0].AttemptSeq != 2 {
		t.Fatalf("billing settles=%+v want B once at attempt 2", innerSettler.calls)
	}
	if len(quotaFinalizer.releases) != 1 || len(quotaFinalizer.settles) != 1 {
		t.Fatalf("quota release/settle=%d/%d want 1/1", len(quotaFinalizer.releases), len(quotaFinalizer.settles))
	}
	refresh := refresher.waitForCall(t)
	if refresh.accountID != 7101 || refresh.vendor != credentialstore.VendorAnthropic {
		t.Fatalf("hot refresh=%+v want expired A/anthropic", refresh)
	}
	for _, signal := range health.signals {
		if signal.Key.ProviderAccountID == 7101 && signal.Class != "" {
			t.Fatalf("本地过期 A 不得写普通 channel health 信号: %+v", health.signals)
		}
	}
}

// TestClaudeSessionLocalExpiryHotRefreshConcurrentDedupe 模拟同一过期账号被并发
// 请求同时击中；分类路径都会触发热刷新，但去重层必须只把一次调用交给刷新器。
// 变异：移除 admit 的互斥/窗口判断会使 calls>1。
func TestClaudeSessionLocalExpiryHotRefreshConcurrentDedupe(t *testing.T) {
	inner := newRecordingHotRefreshSpy()
	refresher := newDedupingCredentialHotRefresher(inner, time.Minute)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_ = refresher.RefreshHotPath(context.Background(), 7, 7101, credentialstore.VendorAnthropic)
		}()
	}
	close(start)
	wg.Wait()
	if calls := inner.snapshot(); len(calls) != 1 || calls[0].accountID != 7101 {
		t.Fatalf("concurrent hot refresh calls=%+v want exactly one for A", calls)
	}
}

// TestClaudeSessionMissingAdapterReleasesWithoutHTTP 证明即便启动闭合闸被绕过，
// 热路径缺 adapter 仍会在发网前终结 billing/quota，不留下 hold 或并发槽。
func TestClaudeSessionMissingAdapterReleasesWithoutHTTP(t *testing.T) {
	t.Setenv("HUAKAI_DISPATCH_HCSF", "0")
	selector := newPR5Selector(t, 7201)
	innerSettler := &recordingSettler{}
	quotaFinalizer := &claudeSessionQuotaFinalizer{}
	doer := &claudeSessionSuccessDoer{}
	tf := transport.NewFactory()
	tf.SetSidecarForTesting(http.DefaultTransport)
	vault := provider.NewStaticVault()
	if err := vault.Set(7201, provider.Credential{
		Type: provider.CredentialTypeOAuthAccessToken, Value: "fresh",
	}, provider.AccountInfo{
		AccountID: 7201, Platform: credentialstore.VendorAnthropic,
		AccountType:         credentialstore.AuthModeClaudeAIOAuth,
		AccountCredentialID: 16201, CredentialVersion: 1,
	}); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	deps := pr5NonStreamDeps(t, selector, &pr5ClaimGate{claimID: 88720}, quotaenforce.NewSettler(innerSettler, quotaFinalizer), nil)
	deps.Registry = stubRegistry{resolved: registry.Resolved{
		PublicAlias: "claude-sonnet", CanonicalModelID: "anthropic/claude-sonnet",
		ProviderModelID: "claude-sonnet", ProtocolFamily: "anthropic_claude_session", PoolCandidates: []int64{42},
	}}
	deps.Router = stubRouter{plan: pr5RoutePlan(router.AttemptPlan{Index: 0, PoolGroupID: 42, UpstreamModelID: "claude-sonnet"})}
	deps.CredentialVault = vault
	deps.Dispatcher = &gateway.UpstreamDispatcher{
		Adapters: provider.NewStaticRegistry(), TransportFactory: tf, HTTPClient: doer,
	}
	rec := invokeHandlerPathWithHeaders(t, deps, "/v1/messages", claudeSessionOfficialBody(), claudeSessionOfficialHeaders())
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s want 502", rec.Code, rec.Body.String())
	}
	if doer.calls != 0 {
		t.Fatalf("missing adapter HTTP calls=%d want 0", doer.calls)
	}
	if len(innerSettler.aborts) != 1 || len(innerSettler.calls) != 0 || len(quotaFinalizer.releases) != 1 || len(quotaFinalizer.settles) != 0 {
		t.Fatalf("resource finalization abort/settle/quota-release/quota-settle=%d/%d/%d/%d want 1/0/1/0",
			len(innerSettler.aborts), len(innerSettler.calls), len(quotaFinalizer.releases), len(quotaFinalizer.settles))
	}
}

// TestClaudeSessionRuntimeCompatibilityRejectsWrongAAndUsesB 证明配置面被绕过时，
// 发网前二次校验仍会拒绝 API-key A、释放其资源并只让兼容的 OAuth B 发 HTTP。
func TestClaudeSessionRuntimeCompatibilityRejectsWrongAAndUsesB(t *testing.T) {
	t.Setenv("HUAKAI_DISPATCH_HCSF", "0")
	selector := newPR5Selector(t, 7301, 7302)
	innerSettler := &recordingSettler{}
	quotaFinalizer := &claudeSessionQuotaFinalizer{}
	doer := &claudeSessionSuccessDoer{}
	tf := transport.NewFactory()
	tf.SetSidecarForTesting(http.DefaultTransport)
	adapters := provider.NewStaticRegistry()
	adapters.MustRegister("anthropic_claude_session", &provideranthropic.OAuthSessionAdapter{})
	vault := provider.NewStaticVault()
	if err := vault.Set(7301, provider.Credential{
		Type: provider.CredentialTypeAPIKey, Value: "must-not-leave-process",
	}, provider.AccountInfo{
		AccountID: 7301, Platform: credentialstore.VendorAnthropic,
		AccountType: credentialstore.AuthModeAPIKey, AccountCredentialID: 16301, CredentialVersion: 1,
	}); err != nil {
		t.Fatalf("vault.Set A: %v", err)
	}
	if err := vault.Set(7302, provider.Credential{
		Type: provider.CredentialTypeOAuthAccessToken, Value: "fresh-B",
	}, provider.AccountInfo{
		AccountID: 7302, Platform: credentialstore.VendorAnthropic,
		AccountType: credentialstore.AuthModeClaudeAIOAuth, AccountCredentialID: 16302, CredentialVersion: 1,
	}); err != nil {
		t.Fatalf("vault.Set B: %v", err)
	}
	deps := pr5NonStreamDeps(t, selector, &pr5ClaimGate{claimID: 88730}, quotaenforce.NewSettler(innerSettler, quotaFinalizer), nil)
	deps.Registry = stubRegistry{resolved: registry.Resolved{
		PublicAlias: "claude-sonnet", CanonicalModelID: "anthropic/claude-sonnet",
		ProviderModelID: "claude-sonnet", ProtocolFamily: "anthropic_claude_session", PoolCandidates: []int64{42},
	}}
	deps.Router = stubRouter{plan: pr5RoutePlan(router.AttemptPlan{Index: 0, PoolGroupID: 42, UpstreamModelID: "claude-sonnet"})}
	deps.CredentialVault = vault
	deps.Dispatcher = &gateway.UpstreamDispatcher{Adapters: adapters, TransportFactory: tf, HTTPClient: doer}
	rec := invokeHandlerPathWithHeaders(t, deps, "/v1/messages", claudeSessionOfficialBody(), claudeSessionOfficialHeaders())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want B success", rec.Code, rec.Body.String())
	}
	if doer.calls != 1 || doer.authorization[0] != "Bearer fresh-B" {
		t.Fatalf("HTTP calls/auth=%d/%v want only OAuth B", doer.calls, doer.authorization)
	}
	if len(innerSettler.aborts) != 1 || innerSettler.aborts[0].reason != "credential_protocol_incompatible" {
		t.Fatalf("aborts=%+v want incompatible A once", innerSettler.aborts)
	}
	if len(innerSettler.calls) != 1 || innerSettler.calls[0].AccountID != 7302 || len(quotaFinalizer.releases) != 1 || len(quotaFinalizer.settles) != 1 {
		t.Fatalf("settlement mismatch billing=%+v quota release/settle=%d/%d", innerSettler.calls, len(quotaFinalizer.releases), len(quotaFinalizer.settles))
	}
}

// TestClaudeSessionStreamingUsesAnthropicScannerAndSettlesOnce 走真实 session
// adapter + SSE scanner + Anthropic event adapter，证明流式路不是仅靠注册表假绿。
func TestClaudeSessionStreamingUsesAnthropicScannerAndSettlesOnce(t *testing.T) {
	selector := newPR5Selector(t, 7401)
	settler := &recordingSettler{}
	doer := &claudeSessionStreamingDoer{}
	tf := transport.NewFactory()
	tf.SetSidecarForTesting(http.DefaultTransport)
	adapters := provider.NewStaticRegistry()
	adapters.MustRegister("anthropic_claude_session", &provideranthropic.OAuthSessionAdapter{})
	vault := provider.NewStaticVault()
	if err := vault.Set(7401, provider.Credential{
		Type: provider.CredentialTypeOAuthAccessToken, Value: "stream-token",
	}, provider.AccountInfo{
		AccountID: 7401, Platform: credentialstore.VendorAnthropic,
		AccountType: credentialstore.AuthModeClaudeAIOAuth, AccountCredentialID: 16401, CredentialVersion: 1,
	}); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	deps := pr5NonStreamDeps(t, selector, &pr5ClaimGate{claimID: 88740}, settler, nil)
	deps.Registry = stubRegistry{resolved: registry.Resolved{
		PublicAlias: "claude-sonnet", CanonicalModelID: "anthropic/claude-sonnet",
		ProviderModelID: "claude-sonnet", ProtocolFamily: "anthropic_claude_session", PoolCandidates: []int64{42},
	}}
	deps.Router = stubRouter{plan: pr5RoutePlan(router.AttemptPlan{Index: 0, PoolGroupID: 42, UpstreamModelID: "claude-sonnet"})}
	deps.CredentialVault = vault
	deps.Dispatcher = &gateway.UpstreamDispatcher{Adapters: adapters, TransportFactory: tf, HTTPClient: doer}
	deps.Forwarder = &gateway.StreamForwarder{
		ProtocolAdapters: gateway.BuildDefaultProtocolAdapterRegistry(),
		Scanners:         gateway.BuildDefaultStreamScannerRegistry(),
	}
	body := strings.Replace(claudeSessionOfficialBody(), `"stream":false`, `"stream":true`, 1)
	rec := invokeHandlerPathWithHeaders(t, deps, "/v1/messages", body, claudeSessionOfficialHeaders())
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "pong") || !strings.Contains(rec.Body.String(), "message_stop") {
		t.Fatalf("status=%d body=%s want Anthropic SSE pong+stop", rec.Code, rec.Body.String())
	}
	if doer.calls != 1 || doer.url != "https://api.anthropic.com/v1/messages?beta=true" || doer.auth != "Bearer stream-token" {
		t.Fatalf("stream doer calls/url/auth=%d/%s/%s", doer.calls, doer.url, doer.auth)
	}
	if !bytes.Equal(doer.body, []byte(body)) {
		t.Fatalf("raw stream 官方直发 body 漂移\ngot:  %s\nwant: %s", doer.body, body)
	}
	if len(settler.calls) != 1 || len(settler.aborts) != 0 || settler.calls[0].AccountID != 7401 {
		t.Fatalf("stream settle/abort=%+v/%+v want one settle", settler.calls, settler.aborts)
	}
}

// TestClaudeSessionHCSFMarshalAndResponseSettleOnce 走默认 HCSF 非流路径；严格官方
// 请求体跳过 canonical 重排，而响应仍经 Anthropic adapter 回到 HCSF 并只结算一次。
func TestClaudeSessionHCSFMarshalAndResponseSettleOnce(t *testing.T) {
	enableHCSFDispatchForTest(t)
	selector := newPR5Selector(t, 7501)
	settler := &recordingSettler{}
	doer := &claudeSessionSuccessDoer{}
	tf := transport.NewFactory()
	tf.SetSidecarForTesting(http.DefaultTransport)
	adapters := provider.NewStaticRegistry()
	adapters.MustRegister("anthropic_claude_session", &provideranthropic.OAuthSessionAdapter{})
	vault := provider.NewStaticVault()
	if err := vault.Set(7501, provider.Credential{
		Type: provider.CredentialTypeOAuthAccessToken, Value: "hcsf-token",
	}, provider.AccountInfo{
		AccountID: 7501, Platform: credentialstore.VendorAnthropic,
		AccountType: credentialstore.AuthModeClaudeAIOAuth, AccountCredentialID: 16501, CredentialVersion: 1,
	}); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	dispatcher := &gateway.UpstreamDispatcher{
		Adapters: provider.NewStaticRegistry(), TransportFactory: tf, HTTPClient: doer,
		ProtocolAdapters: gateway.BuildDefaultProtocolAdapterRegistry(),
	}
	dispatcher.Adapters = adapters
	deps := pr5NonStreamDeps(t, selector, &pr5ClaimGate{claimID: 88750}, settler, dispatcher)
	deps.Registry = stubRegistry{resolved: registry.Resolved{
		PublicAlias: "claude-sonnet", CanonicalModelID: "anthropic/claude-sonnet",
		ProviderModelID: "claude-sonnet", ProtocolFamily: "anthropic_claude_session", PoolCandidates: []int64{42},
	}}
	deps.Router = stubRouter{plan: pr5RoutePlan(router.AttemptPlan{Index: 0, PoolGroupID: 42, UpstreamModelID: "claude-sonnet"})}
	deps.CredentialVault = vault
	deps.Dispatcher = dispatcher
	deps.CanonicalDispatcher = dispatcher
	rec := invokeHandlerPathWithHeaders(t, deps, "/v1/messages", claudeSessionOfficialBody(), claudeSessionOfficialHeaders())
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ok") {
		t.Fatalf("status=%d body=%s want HCSF success", rec.Code, rec.Body.String())
	}
	if doer.calls != 1 || doer.authorization[0] != "Bearer hcsf-token" {
		t.Fatalf("HCSF HTTP calls/auth=%d/%v", doer.calls, doer.authorization)
	}
	if len(doer.bodies) != 1 || !bytes.Equal(doer.bodies[0], []byte(claudeSessionOfficialBody())) {
		t.Fatalf("HCSF 官方直发 body 漂移\ngot:  %s\nwant: %s", doer.bodies[0], claudeSessionOfficialBody())
	}
	if len(settler.calls) != 1 || len(settler.aborts) != 0 || settler.calls[0].AccountID != 7501 {
		t.Fatalf("HCSF settle/abort=%+v/%+v want one settle", settler.calls, settler.aborts)
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

func TestTenantRetryBudgetStopsBeforeThirdRetryAndKeepsTenantIsolation(t *testing.T) {
	enableHCSFDispatchForTest(t)
	budget := retrybudget.New(2, time.Minute)
	route := pr5RoutePlan(
		router.AttemptPlan{Index: 0, PoolGroupID: 42, UpstreamModelID: "gpt-4o", Reason: "primary"},
		router.AttemptPlan{Index: 1, PoolGroupID: 42, UpstreamModelID: "gpt-4o", Reason: "retry_one"},
		router.AttemptPlan{Index: 2, PoolGroupID: 42, UpstreamModelID: "gpt-4o", Reason: "retry_two"},
		router.AttemptPlan{Index: 3, PoolGroupID: 42, UpstreamModelID: "gpt-4o", Reason: "must_not_reach"},
	)

	tenantASelector := newPR5Selector(t, 9011, 9012, 9013, 9014)
	tenantADispatcher := &pr5CanonicalSequenceDispatcher{steps: []pr5CanonicalStep{
		{status: http.StatusInternalServerError, body: `{"error":"a1"}`},
		{status: http.StatusInternalServerError, body: `{"error":"a2"}`},
		{status: http.StatusInternalServerError, body: `{"error":"a3"}`},
		{successText: "mutation would reach forbidden third retry"},
	}}
	tenantADeps := pr5NonStreamDeps(t, tenantASelector, &pr5ClaimGate{claimID: 88901}, &recordingSettler{}, tenantADispatcher)
	tenantADeps.Router = stubRouter{plan: route}
	tenantADeps.RetryBudget = budget

	tenantA := invokeHandlerPath(t, tenantADeps, "/v1/chat/completions", pr5NonStreamBody())
	if tenantA.Code != http.StatusBadGateway {
		t.Fatalf("tenant A status=%d body=%s; want last 5xx failure after only two retries", tenantA.Code, tenantA.Body.String())
	}
	if tenantADispatcher.calls != 3 || tenantASelector.calls != 3 {
		t.Fatalf("tenant A dispatch/select calls=%d/%d want 3/3; removing Allow would reach the 4th success", tenantADispatcher.calls, tenantASelector.calls)
	}

	tenantBSelector := newPR5Selector(t, 9021, 9022, 9023)
	tenantBDispatcher := &pr5CanonicalSequenceDispatcher{steps: []pr5CanonicalStep{
		{status: http.StatusInternalServerError, body: `{"error":"b1"}`},
		{status: http.StatusInternalServerError, body: `{"error":"b2"}`},
		{successText: "tenant b still has retry budget"},
	}}
	tenantBDeps := pr5NonStreamDeps(t, tenantBSelector, &pr5ClaimGate{claimID: 88902}, &recordingSettler{}, tenantBDispatcher)
	tenantBDeps.Router = stubRouter{plan: pr5RoutePlan(route.Attempts[:3]...)}
	tenantBDeps.RetryBudget = budget
	tenantBIdent := validIdentity()
	tenantBIdent.TenantID = 8
	tenantBDeps.Auth = stubAuth{identity: tenantBIdent}

	tenantB := invokeHandlerPath(t, tenantBDeps, "/v1/chat/completions", pr5NonStreamBody())
	if tenantB.Code != http.StatusOK {
		t.Fatalf("tenant B status=%d body=%s; want success after two retries despite tenant A exhaustion", tenantB.Code, tenantB.Body.String())
	}
	if tenantBDispatcher.calls != 3 {
		t.Fatalf("tenant B dispatch calls=%d want 3; tenant A budget must not bleed across tenants", tenantBDispatcher.calls)
	}
}

func TestTenantRetryBudgetZeroPreservesLegacyUnlimitedRetryBehavior(t *testing.T) {
	enableHCSFDispatchForTest(t)
	selector := newPR5Selector(t, 9031, 9032, 9033, 9034)
	dispatcher := &pr5CanonicalSequenceDispatcher{steps: []pr5CanonicalStep{
		{status: http.StatusInternalServerError, body: `{"error":"first"}`},
		{status: http.StatusInternalServerError, body: `{"error":"second"}`},
		{status: http.StatusInternalServerError, body: `{"error":"third"}`},
		{successText: "legacy retry budget zero reaches configured plan budget"},
	}}
	deps := pr5NonStreamDeps(t, selector, &pr5ClaimGate{claimID: 88903}, &recordingSettler{}, dispatcher)
	deps.Router = stubRouter{plan: pr5RoutePlan(
		router.AttemptPlan{Index: 0, PoolGroupID: 42, UpstreamModelID: "gpt-4o", Reason: "primary"},
		router.AttemptPlan{Index: 1, PoolGroupID: 42, UpstreamModelID: "gpt-4o", Reason: "retry_one"},
		router.AttemptPlan{Index: 2, PoolGroupID: 42, UpstreamModelID: "gpt-4o", Reason: "retry_two"},
		router.AttemptPlan{Index: 3, PoolGroupID: 42, UpstreamModelID: "gpt-4o", Reason: "retry_three"},
	)}
	deps.RetryBudget = retrybudget.New(0, time.Minute)

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s; want 200 because budget=0 must not cap retries", rec.Code, rec.Body.String())
	}
	if dispatcher.calls != 4 {
		t.Fatalf("dispatch calls=%d want 4; budget=0 must preserve existing attempt budget behavior", dispatcher.calls)
	}
}

func TestTenantRetryBudgetFirstAttemptDoesNotConsumeBudget(t *testing.T) {
	enableHCSFDispatchForTest(t)
	budget := retrybudget.New(1, time.Minute)
	deps := pr5NonStreamDeps(t, newPR5Selector(t, 9041), &pr5ClaimGate{claimID: 88904}, &recordingSettler{}, &pr5CanonicalSequenceDispatcher{
		steps: []pr5CanonicalStep{{successText: "first attempt success"}},
	})
	deps.RetryBudget = budget

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s; want first attempt success", rec.Code, rec.Body.String())
	}
	if !budget.Allow(validIdentity().TenantID) {
		t.Fatal("first upstream attempt consumed retry budget; only retry attempts may be counted")
	}
}

func TestAT_GW_002_04_PreStreamFinalErrorSanitizesUpstreamBody(t *testing.T) {
	// 消除的风险:当每一次交付前的尝试都失败时,客户端收到的是
	// 一个带类型的、已脱敏的错误,而原始 upstream body 只用于内部
	// 分类。变异自检:把 upstreamErr.Body 写给客户端会
	// 泄露 SENSITIVE_UPSTREAM_MARKER,使本测试变红。
	enableHCSFDispatchForTest(t)
	const marker = "SENSITIVE_UPSTREAM_MARKER"
	selector := newPR5Selector(t, 451)
	dispatcher := &pr5CanonicalSequenceDispatcher{
		steps: []pr5CanonicalStep{{
			status: http.StatusInternalServerError,
			body:   `{"error":"` + marker + `"}`,
		}},
	}
	deps := pr5NonStreamDeps(t, selector, &pr5ClaimGate{claimID: 88451}, &recordingSettler{}, dispatcher)
	deps.Router = stubRouter{plan: pr5RoutePlan(router.AttemptPlan{Index: 0, PoolGroupID: 42, UpstreamModelID: "gpt-4o", Reason: "only"})}

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s; want sanitized 502", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), marker) {
		t.Fatalf("response leaked upstream marker: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "upstream_upstream_5xx") {
		t.Fatalf("body=%s want typed upstream_5xx code", rec.Body.String())
	}
	if selector.calls != 1 || dispatcher.calls != 1 {
		t.Fatalf("selector/dispatcher calls=%d/%d want single final attempt", selector.calls, dispatcher.calls)
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
	// 消除的风险:某次交付前失败尝试产生的 Deferred ledger 结果,
	// 绝不能把 X-HUAKAI-Ledger-DLQ-Ref 泄露进后续成功的流式
	// 响应。变异自检:从重试清理中移除 DLQ trailer,会让
	// audit_ledger_dlq:729 残留在最终响应 trailer 里。
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

func TestAT_GW_002_03_PreStreamRetryAnd13MidStreamRetryBlocked(t *testing.T) {
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

func TestModelFallback_PrimaryNoCapacityFallsBackAndSettlesOnlyFallbackModel(t *testing.T) {
	enableHCSFDispatchForTest(t)
	selector := &modelFallbackSelector{
		noCapacity: map[string]bool{"gpt-4o": true},
		accounts:   map[string]int64{"gpt-4o-mini": 2202},
	}
	claimGate := &modelFallbackClaimGate{nextClaimID: 91001}
	settler := &recordingSettler{}
	dispatcher := &pr5CanonicalSequenceDispatcher{steps: []pr5CanonicalStep{{successText: "fallback model success"}}}
	deps := modelFallbackDeps(t, selector, claimGate, settler, dispatcher, `{
		"enabled": true,
		"max_depth": 2,
		"general": {"gpt-4o":["gpt-4o-mini"]}
	}`)

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s; want fallback success", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Huakai-Model-Fallback"); got != "gpt-4o-mini" {
		t.Fatalf("fallback header=%q want gpt-4o-mini", got)
	}
	if got := rec.Header().Get("X-Huakai-Fallback-Attempts"); got != "1" {
		t.Fatalf("fallback attempts header=%q want 1", got)
	}
	if got := reserveModels(claimGate.requests); strings.Join(got, ",") != "gpt-4o,gpt-4o-mini" {
		t.Fatalf("reserve models=%v want primary then fallback", got)
	}
	if claimGate.requests[0].LogicalRequestID == claimGate.requests[1].LogicalRequestID {
		t.Fatalf("fallback reserve reused primary logical_request_id=%q; models need separate idempotency scopes", claimGate.requests[0].LogicalRequestID)
	}
	if len(settler.aborts) != 1 || settler.aborts[0].claimID != 91001 || settler.aborts[0].reason != "pool_no_capacity" {
		t.Fatalf("aborts=%+v want primary claim aborted once for no capacity", settler.aborts)
	}
	if len(settler.calls) != 1 || settler.calls[0].ClaimID != 91002 || settler.calls[0].RequestedModel != "gpt-4o-mini" {
		t.Fatalf("settles=%+v want exactly fallback claim/model settled", settler.calls)
	}
	assertNoHangingModelFallbackClaims(t, claimGate, settler)
}

func TestModelFallback_AllModelsFailAbortsEveryReservedClaimAndReturnsLastError(t *testing.T) {
	enableHCSFDispatchForTest(t)
	selector := &modelFallbackSelector{
		noCapacity: map[string]bool{"gpt-4o": true, "gpt-4o-mini": true},
	}
	claimGate := &modelFallbackClaimGate{nextClaimID: 92001}
	settler := &recordingSettler{}
	deps := modelFallbackDeps(t, selector, claimGate, settler, &pr5CanonicalSequenceDispatcher{}, `{
		"enabled": true,
		"max_depth": 2,
		"general": {"gpt-4o":["gpt-4o-mini"]}
	}`)

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s; want last no-capacity error", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Huakai-Model-Fallback") != "" || rec.Header().Get("X-Huakai-Fallback-Attempts") != "" {
		t.Fatalf("final error leaked fallback success headers: %v", rec.Header())
	}
	if len(settler.calls) != 0 {
		t.Fatalf("settles=%+v want none when chain fails", settler.calls)
	}
	if len(settler.aborts) != 2 {
		t.Fatalf("aborts=%+v want both model claims aborted", settler.aborts)
	}
	assertNoHangingModelFallbackClaims(t, claimGate, settler)
}

func TestModelFallback_FallbackRouteFailureDoesNotLeakSuccessHeadersOrReserveUnknown(t *testing.T) {
	enableHCSFDispatchForTest(t)
	selector := &modelFallbackSelector{
		noCapacity: map[string]bool{"gpt-4o": true},
	}
	claimGate := &modelFallbackClaimGate{nextClaimID: 92501}
	settler := &recordingSettler{}
	deps := modelFallbackDeps(t, selector, claimGate, settler, &pr5CanonicalSequenceDispatcher{}, `{
		"enabled": true,
		"max_depth": 2,
		"general": {"gpt-4o":["missing-fallback-model"]}
	}`)

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s; want fallback route failure", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Huakai-Model-Fallback") != "" || rec.Header().Get("X-Huakai-Fallback-Attempts") != "" {
		t.Fatalf("route failure leaked fallback success headers: %v", rec.Header())
	}
	if got := reserveModels(claimGate.requests); strings.Join(got, ",") != "gpt-4o" {
		t.Fatalf("reserve models=%v want only primary; unknown fallback must fail before reserve", got)
	}
	if len(settler.aborts) != 1 || len(settler.calls) != 0 {
		t.Fatalf("aborts/settles=%+v/%+v want primary abort only", settler.aborts, settler.calls)
	}
	assertNoHangingModelFallbackClaims(t, claimGate, settler)
}

func TestModelFallback_NonRetryableClientErrorDoesNotFallback(t *testing.T) {
	enableHCSFDispatchForTest(t)
	selector := &modelFallbackSelector{accounts: map[string]int64{"gpt-4o": 2301, "gpt-4o-mini": 2302}}
	claimGate := &modelFallbackClaimGate{nextClaimID: 93001}
	settler := &recordingSettler{}
	dispatcher := &pr5CanonicalSequenceDispatcher{
		steps: []pr5CanonicalStep{{status: http.StatusBadRequest, body: `{"error":{"message":"policy-match"}}`}, {successText: "must not fallback"}},
	}
	deps := modelFallbackDeps(t, selector, claimGate, settler, dispatcher, `{
		"enabled": true,
		"general": {"gpt-4o":["gpt-4o-mini"]}
	}`)
	deps.RateService = rate.NewUpstreamRateService(time.Now, time.Minute,
		rate.WithAccountErrorRulesProvider(chatErrorPolicyProvider{}))

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s; want projected 422 without fallback", rec.Code, rec.Body.String())
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"error":{"code":"account_busy","message":"账号暂不可用"}}` {
		t.Fatalf("投影错误响应=%s", got)
	}
	if len(claimGate.requests) != 1 || dispatcher.calls != 1 {
		t.Fatalf("reserves/dispatches=%d/%d want one primary attempt only", len(claimGate.requests), dispatcher.calls)
	}
	if len(settler.calls) != 0 || len(settler.aborts) != 1 {
		t.Fatalf("settles/aborts=%+v/%+v want one abort and no settle", settler.calls, settler.aborts)
	}
	if settler.aborts[0].reason != "upstream_client_4xx" {
		t.Fatalf("客户端投影不得改写终态分类: %+v", settler.aborts)
	}
}

type chatErrorPolicyProvider struct{}

func (chatErrorPolicyProvider) GetAccountErrorPolicy(int64) rate.AccountErrorPolicy {
	clientStatus := http.StatusUnprocessableEntity
	affectHealth := false
	return rate.AccountErrorPolicy{Rules: []rate.TempUnschedulableRule{{
		RuleID: "busy-400", ErrorCode: http.StatusBadRequest, Keywords: []string{"policy-match"},
		DurationMinutes: 5, ClientStatus: &clientStatus, ClientCode: "account_busy",
		MessageMode: "custom", ClientMessage: "账号暂不可用", AffectHealth: &affectHealth,
	}}}
}

func TestModelFallback_PostDeliveryStreamErrorDoesNotFallback(t *testing.T) {
	replayStore := billing.NewMemoryReplayStore()
	settler := &recordingSettler{}
	doer := &pr5SequentialStreamingDoer{
		steps: []pr5StreamStep{
			{body: io.NopCloser(strings.NewReader(openAIStreamingFixture()))},
			{body: io.NopCloser(strings.NewReader(openAIStreamingFixture()))},
		},
	}
	deps := streamingReplayDeps(t, 94001, false, "", replayStore)
	deps.Dispatcher.HTTPClient = doer
	plan := pr5RoutePlan(router.AttemptPlan{Index: 0, PoolGroupID: 42, UpstreamModelID: "gpt-4o", Reason: "primary"})
	plan.FallbackPhases = []router.FallbackPhasePlan{bfcPhase("manual", 55, 550, 1)}
	deps.Router = stubRouter{plan: plan}
	selector := newPR5Selector(t, 2401, 2402)
	deps.Selector = selector
	deps.CredentialVault = pr5CredentialVault(t, 2401, 2402)
	deps.Settler = settler
	deps.ModelFallbackSettings = modelFallbackSettings(t, `{
		"enabled": true,
		"general": {"gpt-4o":["gpt-4o-mini"]}
	}`)
	scanners := gateway.NewStaticStreamScannerRegistry()
	scanners.MustRegister("openai_chat", scannerThenError{
		event: partialOpenAIStreamingEventBeforeReadError(),
		err:   errors.New("body idle timeout after first byte"),
	})
	deps.Forwarder.Scanners = scanners

	rec := invokeWithIdempotencyKey(t, deps, openAIStreamingRequestBody(), "model-fallback-post-byte")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s; want delivered partial stream, not fallback JSON", rec.Code, rec.Body.String())
	}
	if doer.calls != 1 || selector.calls != 1 {
		t.Fatalf("stream dispatch/selector calls=%d/%d want 1/1; delivery-started failure must not fallback", doer.calls, selector.calls)
	}
	if got := rec.Header().Get("X-Huakai-Model-Fallback"); got != "" {
		t.Fatalf("fallback header=%q want empty after post-delivery failure", got)
	}
}

func TestModelFallback_DefaultClosedDoesNotFallbackOrExtraReserve(t *testing.T) {
	enableHCSFDispatchForTest(t)
	selector := &modelFallbackSelector{
		noCapacity: map[string]bool{"gpt-4o": true},
		accounts:   map[string]int64{"gpt-4o-mini": 2502},
	}
	claimGate := &modelFallbackClaimGate{nextClaimID: 95001}
	settler := &recordingSettler{}
	deps := modelFallbackDeps(t, selector, claimGate, settler, &pr5CanonicalSequenceDispatcher{}, "")
	deps.ModelFallbackSettings = nil

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s; want primary no-capacity error", rec.Code, rec.Body.String())
	}
	if len(claimGate.requests) != 1 {
		t.Fatalf("reserve calls=%d want 1 when fallback disabled", len(claimGate.requests))
	}
	if len(settler.aborts) != 1 || len(settler.calls) != 0 {
		t.Fatalf("aborts/settles=%+v/%+v want one abort and no settle", settler.aborts, settler.calls)
	}
}

func TestModelFallback_MaxDepthStopsBeforeLongerChain(t *testing.T) {
	enableHCSFDispatchForTest(t)
	selector := &modelFallbackSelector{
		noCapacity: map[string]bool{"gpt-4o": true, "gpt-4o-mini": true},
		accounts:   map[string]int64{"gpt-4o-backup": 2603},
	}
	claimGate := &modelFallbackClaimGate{nextClaimID: 96001}
	settler := &recordingSettler{}
	dispatcher := &pr5CanonicalSequenceDispatcher{steps: []pr5CanonicalStep{{successText: "must not reach C"}}}
	deps := modelFallbackDeps(t, selector, claimGate, settler, dispatcher, `{
		"enabled": true,
		"max_depth": 1,
		"general": {"gpt-4o":["gpt-4o-mini","gpt-4o-backup"]}
	}`)

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s; want B failure after max_depth=1", rec.Code, rec.Body.String())
	}
	if got := reserveModels(claimGate.requests); strings.Join(got, ",") != "gpt-4o,gpt-4o-mini" {
		t.Fatalf("reserve models=%v want only A then B; C must be blocked by max_depth", got)
	}
	if dispatcher.calls != 0 {
		t.Fatalf("dispatcher calls=%d want 0 because C success must not be attempted", dispatcher.calls)
	}
	assertNoHangingModelFallbackClaims(t, claimGate, settler)
}

func pr5NonStreamBody() string {
	return `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`
}

func countHealthSignals(health *recordingChannelHealth, class channelhealth.SignalClass) int {
	if health == nil {
		return 0
	}
	count := 0
	for _, sig := range health.signals {
		if sig.Class == class {
			count++
		}
	}
	return count
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
			AccountType:         credentialstore.AuthModeAPIKey,
			AccountCredentialID: 9000 + accountID,
			CredentialVersion:   1,
		}); err != nil {
			t.Fatalf("vault.Set(%d): %v", accountID, err)
		}
	}
	return vault
}

func modelFallbackDeps(t *testing.T, selector *modelFallbackSelector, claimGate *modelFallbackClaimGate, settler *recordingSettler, dispatcher HCSFDispatcher, settingsJSON string) ChatHandlerDeps {
	t.Helper()
	deps := clientAdapterDeps(t)
	deps.Registry = &modelFallbackRegistry{requests: make([]string, 0), models: map[string]registry.Resolved{
		"gpt-4o":        modelFallbackResolved("gpt-4o", 42),
		"gpt-4o-mini":   modelFallbackResolved("gpt-4o-mini", 43),
		"gpt-4o-backup": modelFallbackResolved("gpt-4o-backup", 44),
	}}
	deps.Router = &modelFallbackRouter{plans: map[string]router.RoutePlan{
		"gpt-4o":        pr5RoutePlan(router.AttemptPlan{Index: 0, PoolGroupID: 42, UpstreamModelID: "gpt-4o", Reason: "primary"}),
		"gpt-4o-mini":   pr5RoutePlan(router.AttemptPlan{Index: 0, PoolGroupID: 43, UpstreamModelID: "gpt-4o-mini", Reason: "model_fallback"}),
		"gpt-4o-backup": pr5RoutePlan(router.AttemptPlan{Index: 0, PoolGroupID: 44, UpstreamModelID: "gpt-4o-backup", Reason: "model_fallback"}),
	}}
	deps.Selector = selector
	deps.ClaimGate = claimGate
	deps.Settler = settler
	deps.CanonicalDispatcher = dispatcher
	deps.CredentialVault = pr5CredentialVault(t, 2202, 2301, 2302, 2502, 2603)
	if settingsJSON != "" {
		deps.ModelFallbackSettings = modelFallbackSettings(t, settingsJSON)
	}
	return deps
}

func modelFallbackResolved(model string, poolGroupID int64) registry.Resolved {
	return registry.Resolved{
		PublicAlias:      model,
		CanonicalModelID: "openai/" + model,
		ProviderModelID:  model,
		ProtocolFamily:   "openai_chat",
		PoolCandidates:   []int64{poolGroupID},
		SnapshotVersion:  "registry:7:model-fallback-test",
	}
}

func modelFallbackSettings(t *testing.T, raw string) *platformsettings.Service {
	t.Helper()
	settings := platformsettings.NewService(platformsettings.NewMemoryStore(), nil)
	if _, err := settings.Upsert(context.Background(), platformsettings.UpsertInput{
		Key:       platformsettings.KeyModelFallbackChains,
		Value:     raw,
		UpdatedBy: "test",
	}); err != nil {
		t.Fatalf("model fallback settings upsert: %v", err)
	}
	return settings
}

type modelFallbackRegistry struct {
	models   map[string]registry.Resolved
	requests []string
}

func (r *modelFallbackRegistry) ResolveModel(_ context.Context, publicAlias string, _ int64) (registry.Resolved, error) {
	r.requests = append(r.requests, publicAlias)
	if resolved, ok := r.models[publicAlias]; ok {
		return resolved, nil
	}
	return registry.Resolved{}, registry.ErrUnknownModel
}

type modelFallbackRouter struct {
	plans map[string]router.RoutePlan
}

func (r *modelFallbackRouter) Plan(_ context.Context, in router.PlanInput) (router.RoutePlan, error) {
	if plan, ok := r.plans[in.Model.PublicAlias]; ok {
		return plan, nil
	}
	return router.RoutePlan{}, &router.PlanError{Code: "missing_test_plan", Message: in.Model.PublicAlias}
}

type modelFallbackSelector struct {
	noCapacity map[string]bool
	accounts   map[string]int64
	requests   []pool.SelectionRequest
}

func (s *modelFallbackSelector) Select(_ context.Context, req pool.SelectionRequest) (*pool.SelectionResult, error) {
	s.requests = append(s.requests, req)
	if s.noCapacity[req.RequestedModel] {
		return nil, pool.ErrNoEligibleAccount
	}
	accountID := s.accounts[req.RequestedModel]
	if accountID == 0 {
		return nil, pool.ErrNoEligibleAccount
	}
	return &pool.SelectionResult{
		AccountID:         accountID,
		AcquisitionToken:  uuid.New(),
		RoutingReasonJSON: []byte(`{"test":"model_fallback"}`),
	}, nil
}

type modelFallbackClaimGate struct {
	nextClaimID int64
	requests    []billing.ReserveRequest
}

func (g *modelFallbackClaimGate) Reserve(_ context.Context, req billing.ReserveRequest) (*billing.ReserveResult, error) {
	g.requests = append(g.requests, req)
	claimID := g.nextClaimID + int64(len(g.requests)) - 1
	return &billing.ReserveResult{ClaimID: claimID}, nil
}

func reserveModels(reqs []billing.ReserveRequest) []string {
	out := make([]string, 0, len(reqs))
	for _, req := range reqs {
		out = append(out, req.RequestedModel)
	}
	return out
}

func assertNoHangingModelFallbackClaims(t *testing.T, gate *modelFallbackClaimGate, settler *recordingSettler) {
	t.Helper()
	closed := make(map[int64]string, len(settler.calls)+len(settler.aborts))
	for _, req := range settler.calls {
		closed[req.ClaimID] = "settled"
	}
	for _, abort := range settler.aborts {
		if existing := closed[abort.claimID]; existing != "" {
			t.Fatalf("claim %d closed twice: first=%s second=aborted", abort.claimID, existing)
		}
		closed[abort.claimID] = "aborted"
	}
	for i := range gate.requests {
		claimID := gate.nextClaimID + int64(i)
		if closed[claimID] == "" {
			t.Fatalf("claim %d for model %s is hanging; closed=%v", claimID, gate.requests[i].RequestedModel, closed)
		}
	}
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

func (s *failingAbortSettler) Abort(ctx context.Context, tenantID, claimID int64, reason, auditRequestID string, observedInputTokens int64, protocolLoss json.RawMessage) error {
	_ = s.recordingSettler.Abort(ctx, tenantID, claimID, reason, auditRequestID, observedInputTokens, protocolLoss)
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
	status  int
	headers http.Header
	body    io.ReadCloser
	err     error
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
	headers := step.headers
	if headers == nil {
		headers = make(http.Header)
	}
	return &http.Response{
		StatusCode: step.status,
		Header:     headers,
		Body:       step.body,
	}, nil
}

type httptestResponse struct {
	code int
	body string
}

func invokeExistingHandlerPath(h http.HandlerFunc, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

type hotRefreshCall struct {
	tenantID  int64
	accountID int64
	vendor    string
}

type recordingAgentTaskRecoverer struct {
	calls     int
	tenantID  int64
	accountID int64
	version   int
	err       error
}

func (r *recordingAgentTaskRecoverer) RecoverAgentTask(_ context.Context, tenantID, accountID int64, version int) error {
	r.calls++
	r.tenantID = tenantID
	r.accountID = accountID
	r.version = version
	return r.err
}

func codexAgentTestVault(t *testing.T, accountID int64) provider.CredentialVault {
	t.Helper()
	vault := provider.NewStaticVault()
	if err := vault.Set(accountID, provider.Credential{
		Type: provider.CredentialTypeUpstreamPassthrough, Value: "AgentAssertion test",
	}, provider.AccountInfo{
		AccountID: accountID, Platform: "openai", AccountType: credentialstore.AuthModeCodexAgent,
		AccountCredentialID: 9000 + accountID, CredentialVersion: 1,
	}); err != nil {
		t.Fatalf("vault.Set(%d): %v", accountID, err)
	}
	return vault
}

func antigravityProjectTestVault(t *testing.T, accountID int64) provider.CredentialVault {
	t.Helper()
	vault := provider.NewStaticVault()
	if err := vault.Set(accountID, provider.Credential{
		Type: provider.CredentialTypeSessionToken, Value: "antigravity-access",
		Extra: map[string]string{"project_id": "stale-project"},
	}, provider.AccountInfo{
		AccountID: accountID, Platform: credentialstore.VendorAntigravity, AccountType: credentialstore.AuthModeOAuth,
		AccountCredentialID: 9000 + accountID, CredentialVersion: 1,
	}); err != nil {
		t.Fatalf("vault.Set(%d): %v", accountID, err)
	}
	return vault
}

type blockingHotRefreshSpy struct {
	called  chan hotRefreshCall
	release chan struct{}
}

func newBlockingHotRefreshSpy() *blockingHotRefreshSpy {
	return &blockingHotRefreshSpy{
		called:  make(chan hotRefreshCall, 1),
		release: make(chan struct{}),
	}
}

func (s *blockingHotRefreshSpy) RefreshHotPath(ctx context.Context, tenantID, accountID int64, vendor string) error {
	s.called <- hotRefreshCall{tenantID: tenantID, accountID: accountID, vendor: vendor}
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type recordingHotRefreshSpy struct {
	mu     sync.Mutex
	calls  []hotRefreshCall
	called chan hotRefreshCall
}

func newRecordingHotRefreshSpy() *recordingHotRefreshSpy {
	return &recordingHotRefreshSpy{called: make(chan hotRefreshCall, 4)}
}

func (s *recordingHotRefreshSpy) RefreshHotPath(_ context.Context, tenantID, accountID int64, vendor string) error {
	call := hotRefreshCall{tenantID: tenantID, accountID: accountID, vendor: vendor}
	s.mu.Lock()
	s.calls = append(s.calls, call)
	s.mu.Unlock()
	s.called <- call
	return nil
}

func (s *recordingHotRefreshSpy) waitForCall(t *testing.T) hotRefreshCall {
	t.Helper()
	select {
	case call := <-s.called:
		return call
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for hot refresh call")
	}
	return hotRefreshCall{}
}

func (s *recordingHotRefreshSpy) snapshot() []hotRefreshCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]hotRefreshCall(nil), s.calls...)
}

func pr5SequencedHeaderTransport(delays []time.Duration, bodies []string) *http.Transport {
	var mu sync.Mutex
	calls := 0
	return &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		client, server := net.Pipe()
		mu.Lock()
		idx := calls
		calls++
		mu.Unlock()
		delay := indexedDelay(delays, idx)
		body := indexedBody(bodies, idx)
		go pr5WritePipeHTTPResponse(server, delay, body)
		return client, nil
	}}
}

type pr5RoundTripFunc func(*http.Request) (*http.Response, error)

func (f pr5RoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func indexedDelay(delays []time.Duration, idx int) time.Duration {
	if idx >= 0 && idx < len(delays) {
		return delays[idx]
	}
	return 0
}

func indexedBody(bodies []string, idx int) string {
	if idx >= 0 && idx < len(bodies) {
		return bodies[idx]
	}
	return bodies[len(bodies)-1]
}

func pr5WritePipeHTTPResponse(conn net.Conn, delay time.Duration, body string) {
	defer conn.Close()
	if !pr5DrainPipeHTTPRequest(conn) {
		return
	}
	time.Sleep(delay)
	_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: "+
		strconv.Itoa(len(body))+"\r\n\r\n"+body)
}

func pr5DrainPipeHTTPRequest(conn net.Conn) bool {
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return false
		}
		if line == "\r\n" {
			go func() { _, _ = io.Copy(io.Discard, reader) }()
			return true
		}
	}
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

func TestPR5Stream429RetryAfterRecordsModelCooldownBeforeRetry(t *testing.T) {
	now := time.Date(2026, 7, 5, 9, 0, 0, 0, time.UTC)
	streamDoer := &pr5SequentialStreamingDoer{
		steps: []pr5StreamStep{
			{
				status:  http.StatusTooManyRequests,
				headers: http.Header{"Retry-After": []string{"120"}},
				body:    io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
			},
			{body: io.NopCloser(strings.NewReader(openAIStreamingFixture()))},
		},
	}
	health := &recordingChannelHealth{}
	modelCooldowns := &recordingModelRateLimiter{}
	deps := streamingReplayDeps(t, 88011, false, "", nil)
	deps.Dispatcher.HTTPClient = streamDoer
	deps.ChannelHealth = health
	deps.ModelCooldowns = modelCooldowns
	deps.RateService = rate.NewUpstreamRateService(func() time.Time { return now }, time.Minute)
	deps.Router = stubRouter{plan: pr5RoutePlan(
		router.AttemptPlan{Index: 0, PoolGroupID: 42, UpstreamModelID: "gpt-4o", Reason: "primary"},
		router.AttemptPlan{Index: 1, PoolGroupID: 42, UpstreamModelID: "gpt-4o", Reason: "same_pool_account_failover"},
	)}
	deps.Selector = newPR5Selector(t, 1201, 1202)
	deps.CredentialVault = pr5CredentialVault(t, 1201, 1202)

	rec := invokeHandler(t, deps, openAIStreamingRequestBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s; want 200 after streaming 429 failover", rec.Code, rec.Body.String())
	}
	if streamDoer.calls != 2 {
		t.Fatalf("stream dispatch calls=%d want 2", streamDoer.calls)
	}
	if got := countHealthSignals(health, channelhealth.SignalRateLimit); got != 0 {
		t.Fatalf("SignalRateLimit count=%d want 0 for streaming pure 429", got)
	}
	if len(health.forceCooldowns) != 0 {
		t.Fatalf("ForceCooldown calls=%d want 0 for streaming pure 429", len(health.forceCooldowns))
	}
	if modelCooldowns.calls != 1 {
		t.Fatalf("model cooldown calls=%d want 1", modelCooldowns.calls)
	}
	if in := modelCooldowns.input; in.ProviderAccountID != 1201 || in.ModelKey != "gpt-4o" || in.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("model cooldown input=%+v want first stream account/gpt-4o/429", in)
	}
	if modelCooldowns.input.Reason != rate.ReasonRateLimitRPM || !modelCooldowns.input.ResetAt.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("model cooldown reason/reset=%s/%s want rpm/%s", modelCooldowns.input.Reason, modelCooldowns.input.ResetAt, now.Add(2*time.Minute))
	}
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

// 守 429 无 Retry-After 的默认模型冷却:上游 429 不带 Retry-After 头时,仍必须按
// RateService/ModelCooldownService 默认时长写入当前账号×当前上游模型,但不得整号冷却。
func TestPR5NonStream429NoRetryAfterRecordsDefaultModelCooldown(t *testing.T) {
	enableHCSFDispatchForTest(t)
	now := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	selector := newPR5Selector(t, 201, 202)
	health := &recordingChannelHealth{}
	modelCooldowns := &recordingModelRateLimiter{}
	dispatcher := &pr5CanonicalSequenceDispatcher{
		steps: []pr5CanonicalStep{
			{status: http.StatusTooManyRequests, body: `{"error":"rate limited"}`}, // 故意无 Retry-After 头
			{successText: "success after rate limit"},
		},
	}
	deps := pr5NonStreamDeps(t, selector, &pr5ClaimGate{claimID: 88003}, &recordingSettler{}, dispatcher)
	deps.ChannelHealth = health
	deps.ModelCooldowns = modelCooldowns
	deps.RateService = rate.NewUpstreamRateService(func() time.Time { return now }, time.Minute)

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s; want 200 after 429 failover", rec.Code, rec.Body.String())
	}
	if selector.calls != 2 {
		t.Fatalf("selector calls=%d want 2 (failover to 2nd account)", selector.calls)
	}
	if got := countHealthSignals(health, channelhealth.SignalRateLimit); got != 0 {
		t.Fatalf("SignalRateLimit count=%d want 0 for 429 model cooldown", got)
	}
	if len(health.forceCooldowns) != 0 {
		t.Fatalf("ForceCooldown calls=%d want 0 for 429 model cooldown", len(health.forceCooldowns))
	}
	if modelCooldowns.calls != 1 {
		t.Fatalf("model cooldown calls=%d want 1", modelCooldowns.calls)
	}
	if !modelCooldowns.input.ResetAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("model cooldown reset=%s want %s (default cooldown)", modelCooldowns.input.ResetAt, now.Add(time.Minute))
	}
}

func TestPR5NonStreamTransient5xxForceCooldownWhenEnabled(t *testing.T) {
	enableHCSFDispatchForTest(t)
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	selector := newPR5Selector(t, 211, 212)
	health := &recordingChannelHealth{}
	dispatcher := &pr5CanonicalSequenceDispatcher{
		steps: []pr5CanonicalStep{
			{status: http.StatusBadGateway, body: `{"error":"upstream transient"}`},
			{successText: "success after transient failover"},
		},
	}
	deps := pr5NonStreamDeps(t, selector, &pr5ClaimGate{claimID: 88005}, &recordingSettler{}, dispatcher)
	deps.ChannelHealth = health
	deps.RateService = rate.NewUpstreamRateService(
		func() time.Time { return now },
		time.Minute,
		rate.WithTransientCooldown(30*time.Second),
	)

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s; want 200 after transient failover", rec.Code, rec.Body.String())
	}
	if selector.calls != 2 {
		t.Fatalf("selector calls=%d want 2 (failover to 2nd account)", selector.calls)
	}
	// 变异:把调用方预过滤器仍限定为只匹配 429/529,会让这里停在零。
	if len(health.forceCooldowns) != 1 {
		t.Fatalf("ForceCooldown calls=%d want 1 for enabled 502 transient cooldown", len(health.forceCooldowns))
	}
	forced := health.forceCooldowns[0]
	if forced.reason != string(rate.ReasonUpstreamTransient) {
		t.Fatalf("ForceCooldown reason=%q want %q", forced.reason, rate.ReasonUpstreamTransient)
	}
	if !forced.until.Equal(now.Add(30 * time.Second)) {
		t.Fatalf("ForceCooldown until=%s want %s", forced.until, now.Add(30*time.Second))
	}
}

// 守 wave-2 P2(401 最终 attempt 仍获 auth-failover): budget=1 的计划里, 唯一(也是最后)的普通
// attempt 收到 401 时, 仍必须获得一次换号 auth-failover 重试(设计: gateway/attempt_error.go
// "401 可交付前换一次号")。原 bug: shouldRetryAttemptFailure 的 finalAttempt 门在最前, 把最终
// attempt 的 auth-failover 也短路了。
// 变异: 还原 finalAttempt 前置门 / 移除 loop 的 attemptCap+1 -> 仅 1 次 attempt -> rec.Code==401,
// selector.calls==1 -> 红。
func TestPR5NonStream401FinalAttemptStillGetsAuthFailover(t *testing.T) {
	enableHCSFDispatchForTest(t)
	selector := newPR5Selector(t, 501, 502)
	settler := &recordingSettler{}
	health := &recordingChannelHealth{}
	dispatcher := &pr5CanonicalSequenceDispatcher{
		steps: []pr5CanonicalStep{
			{status: http.StatusUnauthorized, body: `{"error":"invalid_grant"}`}, // attempt0(唯一/最终普通): 401
			{successText: "auth failover success on second account"},             // attempt1: auth-failover 额外 slot
		},
	}
	deps := pr5NonStreamDeps(t, selector, &pr5ClaimGate{claimID: 88004}, settler, dispatcher)
	deps.ChannelHealth = health
	// budget=1: 单 attempt plan -> 401 落在唯一(最终)普通 attempt 上。
	deps.Router = stubRouter{plan: pr5RoutePlan(
		router.AttemptPlan{Index: 0, PoolGroupID: 42, UpstreamModelID: "gpt-4o", Reason: "primary"},
	)}

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", pr5NonStreamBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s; want 200 (auth-failover retry on final-attempt 401)", rec.Code, rec.Body.String())
	}
	if selector.calls != 2 || dispatcher.calls != 2 {
		t.Fatalf("selector/dispatcher calls=%d/%d want 2/2 (one auth failover beyond budget=1)", selector.calls, dispatcher.calls)
	}
}
