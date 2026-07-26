package mediatask

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func TestGrokVideoProviderPinsAccountAcrossSubmitAndPoll(t *testing.T) {
	selector := &capturingVideoSelector{accountID: 41}
	admitter := &recordingAccountAdmitter{}
	vault := provider.NewStaticVault()
	if err := vault.Set(41,
		provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "secret"},
		provider.AccountInfo{AccountID: 41, TenantID: 7, Platform: "grok", AccountType: credentialstore.AuthModeAPIKey},
	); err != nil {
		t.Fatal(err)
	}
	dispatcher := &videoDispatcherStub{responses: []*gateway.DispatchResult{
		dispatchResult(http.StatusOK, `{"request_id":"upstream-video-1"}`),
		dispatchResult(http.StatusOK, `{"status":"done","progress":100,"model":"grok-imagine-video","video":{"url":"https://vidgen.x.ai/result.mp4"},"usage":{"cost_in_usd_ticks":500000000}}`),
	}}
	mediaProvider := NewGrokVideoProvider(GrokVideoProviderDeps{
		Selector: selector, AccountAdmitter: admitter, CredentialVault: vault, Dispatcher: dispatcher,
	})
	task := boundVideoTask()

	providerTaskID, err := mediaProvider.SubmitBound(context.Background(), task, SubmitReq{
		TaskID: task.ID, RequestID: task.RequestID, TaskType: task.TaskType,
		InputParams: jsonBody(`{"model":"public-video","prompt":"hello"}`),
	})
	if err != nil {
		t.Fatalf("SubmitBound: %v", err)
	}
	if providerTaskID != "upstream-video-1" {
		t.Fatalf("provider task id=%q", providerTaskID)
	}
	poll, err := mediaProvider.PollBound(context.Background(), task, providerTaskID)
	if err != nil {
		t.Fatalf("PollBound: %v", err)
	}
	if poll.Status != StatusSucceeded || poll.Progress != 100 || poll.ActualCents != 5 {
		t.Fatalf("poll=%+v", poll)
	}
	if len(selector.requests) != 1 {
		t.Fatalf("selector calls=%d want 1；轮询必须复用持久账号绑定", len(selector.requests))
	}
	for _, request := range selector.requests {
		if request.PinnedAccountID != 41 || request.APIKeyID != 13 || request.PoolGroupID != 29 || request.EndpointFamily != "videos" {
			t.Fatalf("selection request lost durable binding: %+v", request)
		}
		if request.BindingID != 19 || request.BindingRPMLimit != 7 || request.BindingTPMLimit != 700 ||
			request.MaxParallelRequests != 3 || request.RateAccountingScope != pool.RateAccountingAccountOnly {
			t.Fatalf("后台提交没有恢复原绑定合同: %+v", request)
		}
		if len(request.CapabilityFlags) != 0 {
			t.Fatalf("选号能力=%v,pin 重选不得携带账号级媒体能力门(资格由提交时清单门保证)", request.CapabilityFlags)
		}
	}
	if len(dispatcher.inputs) != 2 {
		t.Fatalf("dispatch calls=%d want 2", len(dispatcher.inputs))
	}
	if dispatcher.inputs[0].HTTPMethod != http.MethodPost || dispatcher.inputs[0].EndpointPath != "/v1/videos/generations" {
		t.Fatalf("submit dispatch=%+v", dispatcher.inputs[0])
	}
	if dispatcher.inputs[0].IdempotencyKey == "" {
		t.Fatal("提交出站缺少稳定幂等键")
	}
	if !strings.Contains(string(dispatcher.inputs[0].InboundBody), `"model":"grok-imagine-video"`) {
		t.Fatalf("upstream model was not rewritten: %s", dispatcher.inputs[0].InboundBody)
	}
	if dispatcher.inputs[1].HTTPMethod != http.MethodGet || dispatcher.inputs[1].EndpointPath != "/v1/videos/upstream-video-1" {
		t.Fatalf("poll dispatch=%+v", dispatcher.inputs[1])
	}
	if selector.releaseCount != 0 {
		t.Fatalf("released slots=%d want 0；提交成功后的任务槽必须留给统一结算器释放", selector.releaseCount)
	}
	if admitter.calls != 1 || admitter.tenantID != 7 || admitter.accountID != 41 {
		t.Fatalf("轮询没有执行固定账号 RPM 准入: %+v", admitter)
	}
}

func TestGrokVideoProviderDefersPollWhenPinnedAccountRateLimited(t *testing.T) {
	mediaProvider := NewGrokVideoProvider(GrokVideoProviderDeps{
		Selector: &capturingVideoSelector{accountID: 41}, AccountAdmitter: &recordingAccountAdmitter{err: errAccountRequestRateLimited},
		CredentialVault: provider.NewStaticVault(), Dispatcher: &videoDispatcherStub{},
	})
	_, err := mediaProvider.PollBound(context.Background(), boundVideoTask(), "upstream-video-1")
	class, retryable, recognized := providerErrorDetails(err)
	if !recognized || !retryable || class != "provider_account_rate_limited" || providerRetryDelay(err) != time.Minute {
		t.Fatalf("err=%v class=%q retryable=%v delay=%s", err, class, retryable, providerRetryDelay(err))
	}
}

func TestGrokVideoProviderRejectsSelectorAccountDrift(t *testing.T) {
	selector := &capturingVideoSelector{accountID: 99}
	mediaProvider := NewGrokVideoProvider(GrokVideoProviderDeps{
		Selector: selector, CredentialVault: provider.NewStaticVault(), Dispatcher: &videoDispatcherStub{},
	})
	_, err := mediaProvider.SubmitBound(context.Background(), boundVideoTask(), SubmitReq{InputParams: jsonBody(`{"model":"m"}`)})
	class, retryable, recognized := providerErrorDetails(err)
	if !recognized || !retryable || class != "provider_account_temporarily_unavailable" {
		t.Fatalf("err=%v class=%q retryable=%v recognized=%v", err, class, retryable, recognized)
	}
	if selector.releaseCount != 1 {
		t.Fatalf("drifted selection slot not released: %d", selector.releaseCount)
	}
}

func TestGrokVideoProviderUnknownPollStatusIsRetryable(t *testing.T) {
	selector := &capturingVideoSelector{accountID: 41}
	vault := provider.NewStaticVault()
	_ = vault.Set(41, provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "secret"},
		provider.AccountInfo{AccountID: 41, TenantID: 7, Platform: "grok", AccountType: credentialstore.AuthModeAPIKey})
	mediaProvider := NewGrokVideoProvider(GrokVideoProviderDeps{
		Selector: selector, CredentialVault: vault,
		Dispatcher: &videoDispatcherStub{responses: []*gateway.DispatchResult{dispatchResult(http.StatusOK, `{"status":"new_state"}`)}},
	})
	_, err := mediaProvider.PollBound(context.Background(), boundVideoTask(), "upstream-video-1")
	class, retryable, recognized := providerErrorDetails(err)
	if !recognized || !retryable || class != "provider_poll_status_unknown" {
		t.Fatalf("err=%v class=%q retryable=%v recognized=%v", err, class, retryable, recognized)
	}
}

func TestGrokVideoProviderTruncatedAmbiguousSubmitResponseNeverRetries(t *testing.T) {
	for _, status := range []int{
		http.StatusRequestTimeout,
		http.StatusConflict,
		http.StatusTooEarly,
		http.StatusInternalServerError,
		http.StatusServiceUnavailable,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			selector := &capturingVideoSelector{accountID: 41}
			vault := provider.NewStaticVault()
			_ = vault.Set(41,
				provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "secret"},
				provider.AccountInfo{
					AccountID: 41, TenantID: 7, Platform: "grok",
					AccountType: credentialstore.AuthModeAPIKey,
				},
			)
			mediaProvider := NewGrokVideoProvider(GrokVideoProviderDeps{
				Selector: selector, CredentialVault: vault,
				Dispatcher: &videoDispatcherStub{responses: []*gateway.DispatchResult{
					dispatchReadErrorResult(status),
				}},
			})
			task := boundVideoTask()
			_, err := mediaProvider.SubmitBound(context.Background(), task, SubmitReq{
				TaskID: task.ID, RequestID: task.RequestID, TaskType: task.TaskType,
				InputParams: jsonBody(`{"model":"m","prompt":"x"}`),
			})
			class, retryable, recognized := providerErrorDetails(err)
			if !recognized || retryable || class != "provider_submit_outcome_unknown" {
				t.Fatalf("status=%d class=%q retryable=%v recognized=%v err=%v",
					status, class, retryable, recognized, err)
			}
			if selector.releaseCount != 1 {
				t.Fatalf("提交结果未知时本轮临时选择句柄释放次数=%d want 1", selector.releaseCount)
			}
		})
	}
}

func TestVideoProviderRateLimitPreservesClassAndBackoff(t *testing.T) {
	selector := &capturingVideoSelector{accountID: 41}
	vault := provider.NewStaticVault()
	_ = vault.Set(41, provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "secret"},
		provider.AccountInfo{AccountID: 41, TenantID: 7, Platform: "gemini", AccountType: credentialstore.AuthModeAIStudioAPIKey})
	mediaProvider := NewGeminiVideoProvider(GrokVideoProviderDeps{
		Selector: selector, CredentialVault: vault,
		Dispatcher: &videoDispatcherStub{responses: []*gateway.DispatchResult{
			dispatchResult(http.StatusTooManyRequests, `{"error":{"status":"RESOURCE_EXHAUSTED","message":"quota exceeded"}}`),
		}},
	})
	task := geminiBoundVideoTask()
	_, err := mediaProvider.SubmitBound(context.Background(), task, SubmitReq{
		TaskID: task.ID, RequestID: task.RequestID, TaskType: task.TaskType,
		InputParams: jsonBody(`{"model":"veo","prompt":"x","aspect_ratio":"16:9"}`),
	})
	class, retryable, recognized := providerErrorDetails(err)
	if !recognized || !retryable || class != "upstream_rate_limited" {
		t.Fatalf("err=%v class=%q retryable=%v recognized=%v", err, class, retryable, recognized)
	}
	if delay := providerRetryDelay(err); delay != time.Minute {
		t.Fatalf("retry delay=%s want 1m", delay)
	}
	if selector.releaseCount != 1 {
		t.Fatalf("限流提交必须释放本轮临时选择句柄: %d", selector.releaseCount)
	}
}

func TestUSDTicksToCents(t *testing.T) {
	tests := []struct {
		ticks int64
		want  int64
	}{
		{ticks: 0, want: 0},
		{ticks: 49_999_999, want: 0},
		{ticks: 50_000_000, want: 1},
		{ticks: 500_000_000, want: 5},
	}
	for _, test := range tests {
		if got := usdTicksToCents(test.ticks); got != test.want {
			t.Fatalf("usdTicksToCents(%d)=%d want %d", test.ticks, got, test.want)
		}
	}
}

type capturingVideoSelector struct {
	accountID    int64
	requests     []pool.SelectionRequest
	releaseCount int
}

type recordingAccountAdmitter struct {
	calls     int
	tenantID  int64
	accountID int64
	err       error
}

func (a *recordingAccountAdmitter) Admit(_ context.Context, tenantID, accountID int64) error {
	a.calls++
	a.tenantID = tenantID
	a.accountID = accountID
	return a.err
}

func (s *capturingVideoSelector) Select(_ context.Context, request pool.SelectionRequest) (*pool.SelectionResult, error) {
	s.requests = append(s.requests, request)
	return &pool.SelectionResult{
		AccountID: s.accountID, AcquisitionToken: uuid.New(),
		Release: func(context.Context) error {
			s.releaseCount++
			return nil
		},
	}, nil
}

type videoDispatcherStub struct {
	inputs    []gateway.DispatchInput
	responses []*gateway.DispatchResult
	err       error
}

func (d *videoDispatcherStub) Dispatch(_ context.Context, input gateway.DispatchInput) (*gateway.DispatchResult, error) {
	d.inputs = append(d.inputs, input)
	if d.err != nil {
		return nil, d.err
	}
	if len(d.responses) == 0 {
		return nil, errorsForTest("missing response")
	}
	response := d.responses[0]
	d.responses = d.responses[1:]
	return response, nil
}

func dispatchResult(status int, body string) *gateway.DispatchResult {
	return &gateway.DispatchResult{
		StatusCode: status, Headers: make(http.Header), UpstreamReader: strings.NewReader(body),
		Close: func() error { return nil },
	}
}

func dispatchReadErrorResult(status int) *gateway.DispatchResult {
	return &gateway.DispatchResult{
		StatusCode: status, Headers: make(http.Header), UpstreamReader: mediaReadError{},
		Close: func() error { return nil },
	}
}

type mediaReadError struct{}

func (mediaReadError) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func boundVideoTask() Task {
	return Task{
		ID: 17, TenantID: 7, UserID: 11, APIKeyID: 13, TaskType: "video_generate",
		Provider: grokVideoProviderName, ProviderAccountID: 41, PoolGroupID: 29,
		ProtocolFamily: "grok_chat", RequestedModel: "public-video",
		ProviderModelID: "grok-imagine-video", RequestID: "video_public_1", HoldRef: "claim:31",
		BindingID: 19, BindingRPMLimit: 7, BindingTPMLimit: 700, BindingMaxParallelRequests: 3,
	}
}

func jsonBody(value string) []byte { return []byte(value) }

type testError string

func (e testError) Error() string      { return string(e) }
func errorsForTest(value string) error { return testError(value) }
