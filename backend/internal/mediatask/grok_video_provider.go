package mediatask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/servingcapability"
	"github.com/BloomingProsperity/HUAKAI/internal/upstreamfeedback"
)

const (
	grokVideoProviderName   = "grok_video"
	maxGrokVideoBodyBytes   = 2 << 20
	selectionReleaseTimeout = 5 * time.Second
)

func isDurablyBoundVideoProvider(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case grokVideoProviderName, geminiVideoProviderName:
		return true
	default:
		return false
	}
}

type mediaDispatcher interface {
	Dispatch(context.Context, gateway.DispatchInput) (*gateway.DispatchResult, error)
}

type GrokVideoProviderDeps struct {
	Selector        pool.Selector
	AccountAdmitter AccountRequestAdmitter
	CredentialVault provider.CredentialVault
	Dispatcher      mediaDispatcher
	Feedback        *upstreamfeedback.Observer
}

type GrokVideoProvider struct {
	deps GrokVideoProviderDeps
}

func NewGrokVideoProvider(deps GrokVideoProviderDeps) *GrokVideoProvider {
	return &GrokVideoProvider{deps: deps}
}

func (p *GrokVideoProvider) SetFeedback(feedback *upstreamfeedback.Observer) {
	if p != nil {
		p.deps.Feedback = feedback
	}
}

func (*GrokVideoProvider) Submit(context.Context, SubmitReq) (string, error) {
	return "", fmt.Errorf("%w: grok video requires durable account binding", ErrInvalidInput)
}

func (*GrokVideoProvider) Poll(context.Context, string) (PollResult, error) {
	return PollResult{}, fmt.Errorf("%w: grok video requires durable account binding", ErrInvalidInput)
}

func (p *GrokVideoProvider) SubmitBound(ctx context.Context, task Task, req SubmitReq) (string, error) {
	endpoint, err := grokVideoSubmitEndpoint(task.TaskType)
	if err != nil {
		return "", err
	}
	body, err := rewriteVideoModel(req.InputParams, task.ProviderModelID)
	if err != nil {
		return "", terminalProviderError("provider_submit_request_invalid", err)
	}
	idempotencyKey := strings.TrimSpace(req.IdempotencyKey)
	if idempotencyKey == "" {
		idempotencyKey = DeriveIdempotencyKey(req.TaskID, req.RequestID)
	}
	result, account, startedAt, selection, err := p.dispatchBound(ctx, task, http.MethodPost, endpoint, body, idempotencyKey)
	if err != nil {
		return "", err
	}
	releaseSelection := true
	defer func() {
		if releaseSelection {
			releaseMediaSelection(ctx, selection.Release)
		}
	}()
	defer result.Close()
	responseBody, err := readBoundedMediaBody(result.UpstreamReader)
	if err != nil {
		return "", p.submitResponseReadError(ctx, task, account, result, startedAt, err)
	}
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		return "", p.httpProviderError(ctx, task, account, result, responseBody, startedAt, true)
	}
	p.observeSuccess(ctx, task, account, result, startedAt)
	var response struct {
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return "", terminalProviderError("provider_submit_response_invalid", err)
	}
	if strings.TrimSpace(response.RequestID) == "" {
		return "", terminalProviderError("provider_submit_response_invalid", ErrProviderUnavailable)
	}
	// 提交成功后槽位代表上游异步任务仍在运行，必须交给最终结算或失败回收释放。
	// 轮询不得重新选号，否则 claim 会绑定到已经提前释放的临时槽。
	releaseSelection = false
	return strings.TrimSpace(response.RequestID), nil
}

func (p *GrokVideoProvider) PollBound(ctx context.Context, task Task, providerTaskID string) (PollResult, error) {
	providerTaskID = strings.TrimSpace(providerTaskID)
	if providerTaskID == "" {
		return PollResult{}, terminalProviderError("provider_task_id_missing", ErrInvalidInput)
	}
	endpoint := "/v1/videos/" + url.PathEscape(providerTaskID)
	result, account, startedAt, err := p.dispatchPinned(ctx, task, http.MethodGet, endpoint, nil)
	if err != nil {
		return PollResult{}, err
	}
	defer result.Close()
	body, err := readBoundedMediaBody(result.UpstreamReader)
	if err != nil {
		return PollResult{}, retryableProviderError("provider_poll_response_invalid", err)
	}
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		return PollResult{}, p.httpProviderError(ctx, task, account, result, body, startedAt, false)
	}
	p.observeSuccess(ctx, task, account, result, startedAt)
	var response struct {
		Status   string `json:"status"`
		Progress int    `json:"progress"`
		Usage    struct {
			CostTicks int64 `json:"cost_in_usd_ticks"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return PollResult{}, retryableProviderError("provider_poll_response_invalid", err)
	}
	poll := PollResult{Progress: response.Progress, Result: json.RawMessage(body)}
	switch strings.ToLower(strings.TrimSpace(response.Status)) {
	case "pending", "queued", "in_progress", "processing":
		poll.Status = StatusInProgress
	case "done", "completed", "succeeded", "success":
		poll.Status = StatusSucceeded
		poll.ActualCents = usdTicksToCents(response.Usage.CostTicks)
		poll.RoutingReason = durableMediaRoutingReason(task)
	case "failed", "failure":
		poll.Status = StatusFailed
		poll.ErrorClass = "provider_failed"
	case "expired":
		poll.Status = StatusExpired
		poll.ErrorClass = "provider_expired"
	default:
		return PollResult{}, retryableProviderError("provider_poll_status_unknown", ErrProviderUnavailable)
	}
	return poll.Normalized(), nil
}

func (p *GrokVideoProvider) dispatchBound(ctx context.Context, task Task, method, endpoint string, body []byte, idempotencyKey string) (*gateway.DispatchResult, provider.AccountInfo, time.Time, *pool.SelectionResult, error) {
	if p == nil || p.deps.Selector == nil || p.deps.CredentialVault == nil || p.deps.Dispatcher == nil {
		return nil, provider.AccountInfo{}, time.Time{}, nil, terminalProviderError("provider_unavailable", ErrProviderUnavailable)
	}
	if !validBoundVideoTask(task) {
		return nil, provider.AccountInfo{}, time.Time{}, nil, terminalProviderError("provider_binding_incomplete", ErrInvalidInput)
	}
	claimID, _ := claimIDFromHoldRef(task.HoldRef)
	selection, err := p.deps.Selector.Select(ctx, pool.SelectionRequest{
		TenantID: task.TenantID, UserID: task.UserID, APIKeyID: task.APIKeyID,
		PoolGroupID: task.PoolGroupID, RequestedModel: task.RequestedModel,
		ProviderModelID:  task.ProviderModelID,
		ModelCooldownKey: task.ProviderModelID, ProtocolFamily: task.ProtocolFamily,
		EndpointFamily: "videos",
		RequestID:      task.RequestID, PinnedAccountID: task.ProviderAccountID,
		ClaimID: claimID, AttemptSeq: 1, Vendor: pool.VendorFromProtocolFamily(task.ProtocolFamily),
		BindingID: task.BindingID, BindingRPMLimit: task.BindingRPMLimit,
		BindingTPMLimit: task.BindingTPMLimit, MaxParallelRequests: task.BindingMaxParallelRequests,
		RateAccountingScope: pool.RateAccountingAccountOnly,
	})
	if err != nil || selection == nil || selection.AccountID != task.ProviderAccountID || selection.WaitPlan != nil {
		if selection != nil && selection.Release != nil {
			releaseMediaSelection(ctx, selection.Release)
		}
		if err == nil {
			err = pool.ErrNoSlotAvailable
		}
		return nil, provider.AccountInfo{}, time.Time{}, nil, retryableProviderError("provider_account_temporarily_unavailable", err)
	}
	credential, account, err := p.resolveBoundAccount(ctx, task)
	if err != nil {
		releaseMediaSelection(ctx, selection.Release)
		return nil, account, time.Time{}, nil, err
	}
	result, startedAt, err := p.dispatchAccount(ctx, task, method, endpoint, body, idempotencyKey, credential, account)
	if err != nil {
		releaseMediaSelection(ctx, selection.Release)
		if method == http.MethodGet {
			return nil, account, startedAt, nil, retryableProviderError("provider_poll_dispatch_error", err)
		}
		if gateway.IsDispatchOutcomeUnknown(err) {
			return nil, account, startedAt, nil, terminalProviderError("provider_submit_outcome_unknown", err)
		}
		return nil, account, startedAt, nil, retryableProviderError("provider_submit_dispatch_unavailable", err)
	}
	return result, account, startedAt, selection, nil
}

func (p *GrokVideoProvider) dispatchPinned(ctx context.Context, task Task, method, endpoint string, body []byte) (*gateway.DispatchResult, provider.AccountInfo, time.Time, error) {
	if p == nil || p.deps.CredentialVault == nil || p.deps.Dispatcher == nil {
		return nil, provider.AccountInfo{}, time.Time{}, terminalProviderError("provider_unavailable", ErrProviderUnavailable)
	}
	if !validBoundVideoTask(task) {
		return nil, provider.AccountInfo{}, time.Time{}, terminalProviderError("provider_binding_incomplete", ErrInvalidInput)
	}
	if err := p.admitPinnedAccountRequest(ctx, task); err != nil {
		return nil, provider.AccountInfo{}, time.Time{}, err
	}
	credential, account, err := p.resolveBoundAccount(ctx, task)
	if err != nil {
		return nil, account, time.Time{}, err
	}
	result, startedAt, err := p.dispatchAccount(ctx, task, method, endpoint, body, "", credential, account)
	if err != nil {
		return nil, account, startedAt, retryableProviderError("provider_poll_dispatch_error", err)
	}
	return result, account, startedAt, nil
}

func (p *GrokVideoProvider) admitPinnedAccountRequest(ctx context.Context, task Task) error {
	if p == nil || p.deps.AccountAdmitter == nil {
		return nil
	}
	if err := p.deps.AccountAdmitter.Admit(ctx, task.TenantID, task.ProviderAccountID); err != nil {
		if errors.Is(err, errAccountRequestRateLimited) {
			return retryableProviderErrorAfter("provider_account_rate_limited", time.Minute, err)
		}
		return retryableProviderError("provider_account_rate_admission_error", err)
	}
	return nil
}

func (p *GrokVideoProvider) resolveBoundAccount(ctx context.Context, task Task) (provider.Credential, provider.AccountInfo, error) {
	if task.TenantID <= 0 || task.ProviderAccountID <= 0 || strings.TrimSpace(task.ProtocolFamily) == "" {
		return provider.Credential{}, provider.AccountInfo{}, ErrInvalidInput
	}
	credential, account, err := p.deps.CredentialVault.Resolve(ctx, task.TenantID, task.ProviderAccountID)
	if err != nil {
		return provider.Credential{}, account, terminalProviderError("credential_resolve_error", err)
	}
	if account.AccountID == 0 {
		account.AccountID = task.ProviderAccountID
	}
	if err := servingcapability.ValidateRuntimeAccountCompatibility(task.ProtocolFamily, credential, account); err != nil {
		return provider.Credential{}, account, terminalProviderError("credential_incompatible", err)
	}
	return credential, account, nil
}

func validBoundVideoTask(task Task) bool {
	return task.TenantID > 0 && task.UserID > 0 && task.APIKeyID > 0 && task.ProviderAccountID > 0 &&
		task.PoolGroupID > 0 && strings.TrimSpace(task.ProtocolFamily) != "" &&
		strings.TrimSpace(task.ProviderModelID) != ""
}

func (p *GrokVideoProvider) dispatchAccount(ctx context.Context, task Task, method, endpoint string, body []byte, idempotencyKey string, credential provider.Credential, account provider.AccountInfo) (*gateway.DispatchResult, time.Time, error) {
	return p.dispatchAccountWithQuery(ctx, task, method, endpoint, "", body, idempotencyKey, credential, account)
}

func (p *GrokVideoProvider) dispatchAccountWithQuery(ctx context.Context, task Task, method, endpoint, query string, body []byte, idempotencyKey string, credential provider.Credential, account provider.AccountInfo) (*gateway.DispatchResult, time.Time, error) {
	startedAt := time.Now().UTC()
	result, err := p.deps.Dispatcher.Dispatch(ctx, gateway.DispatchInput{
		HTTPMethod: method, ProtocolFamily: task.ProtocolFamily, EndpointPath: endpoint, EndpointQuery: query,
		UpstreamModelID: task.ProviderModelID, InboundBody: body, IdempotencyKey: idempotencyKey,
		Account: account, Credential: credential, NonStreamingBuffered: true,
	})
	if err != nil && p.deps.Feedback != nil {
		p.deps.Feedback.ObserveDispatchError(ctx, providerAttempt(task, account, startedAt), err)
	}
	return result, startedAt, err
}

func durableMediaRoutingReason(task Task) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{
		"reason": "durable_media_task_binding", "selected_account_id": task.ProviderAccountID,
		"pool_group_id": task.PoolGroupID, "route_id": task.RouteID,
	})
	return raw
}

func (p *GrokVideoProvider) httpProviderError(ctx context.Context, task Task, account provider.AccountInfo, result *gateway.DispatchResult, body []byte, startedAt time.Time, submit bool) error {
	attempt := providerAttempt(task, account, startedAt)
	failure := upstreamfeedback.ClassifyHTTPError(attempt, result.StatusCode, result.Headers, body)
	if p.deps.Feedback != nil {
		failure = p.deps.Feedback.ObserveHTTPError(ctx, attempt, result.StatusCode, result.Headers, body)
	}
	class := "provider_http_error"
	if failure.Classification.Class != "" {
		class = string(failure.Classification.Class)
	}
	retryable := failure.Decision.RetryableBeforeDelivery
	if !submit && result.StatusCode == http.StatusNotFound {
		retryable = false
		class = "provider_task_not_found"
	}
	if submit && submitHTTPOutcomeUnknown(result.StatusCode) {
		return terminalProviderError("provider_submit_outcome_unknown", ErrProviderUnavailable)
	}
	if retryable {
		retryAfter := time.Duration(failure.Classification.RetryAfterMs) * time.Millisecond
		return retryableProviderErrorAfter(class, retryAfter, ErrProviderUnavailable)
	}
	return terminalProviderError(class, ErrProviderUnavailable)
}

func (p *GrokVideoProvider) submitResponseReadError(
	ctx context.Context,
	task Task,
	account provider.AccountInfo,
	result *gateway.DispatchResult,
	startedAt time.Time,
	readErr error,
) error {
	if result.StatusCode >= http.StatusOK && result.StatusCode < http.StatusMultipleChoices {
		return terminalProviderError("provider_submit_response_invalid", readErr)
	}
	// 这些状态无法证明上游没有创建异步任务。即使错误体截断或超限，也必须
	// 进入提交结果未知态，禁止把“读不到拒绝体”误当成可安全重提。
	if submitHTTPOutcomeUnknown(result.StatusCode) {
		return terminalProviderError("provider_submit_outcome_unknown", readErr)
	}
	return p.httpProviderError(ctx, task, account, result, nil, startedAt, true)
}

func submitHTTPOutcomeUnknown(status int) bool {
	return status == http.StatusRequestTimeout ||
		status == http.StatusConflict ||
		status == http.StatusTooEarly ||
		status >= http.StatusInternalServerError
}

func (p *GrokVideoProvider) observeSuccess(ctx context.Context, task Task, account provider.AccountInfo, result *gateway.DispatchResult, startedAt time.Time) {
	if p.deps.Feedback != nil {
		p.deps.Feedback.ObserveSuccess(ctx, providerAttempt(task, account, startedAt), result.StatusCode, result.Headers)
	}
}

func releaseMediaSelection(parent context.Context, release func(context.Context) error) {
	if release == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), selectionReleaseTimeout)
	defer cancel()
	_ = release(ctx)
}

func providerAttempt(task Task, account provider.AccountInfo, startedAt time.Time) upstreamfeedback.Attempt {
	return upstreamfeedback.Attempt{
		TenantID: task.TenantID, Account: account, ProtocolFamily: task.ProtocolFamily,
		ModelKey: task.ProviderModelID, RequestID: task.RequestID, StartedAt: startedAt,
	}
}

func grokVideoSubmitEndpoint(taskType string) (string, error) {
	switch strings.TrimSpace(taskType) {
	case "video_generate":
		return "/v1/videos/generations", nil
	case "video_edit":
		return "/v1/videos/edits", nil
	case "video_extend":
		return "/v1/videos/extensions", nil
	default:
		return "", fmt.Errorf("%w: unsupported grok video task type", ErrInvalidInput)
	}
}

func durableVideoSubmitEndpoint(task Task) string {
	switch strings.ToLower(strings.TrimSpace(task.Provider)) {
	case geminiVideoProviderName:
		model := strings.TrimSpace(task.ProviderModelID)
		if model == "" {
			return ""
		}
		return "/v1beta/models/" + url.PathEscape(model) + ":predictLongRunning"
	case grokVideoProviderName:
		endpoint, _ := grokVideoSubmitEndpoint(task.TaskType)
		return endpoint
	default:
		return ""
	}
}

func rewriteVideoModel(body []byte, model string) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return nil, ErrInvalidInput
	}
	payload["model"] = model
	return json.Marshal(payload)
}

func readBoundedMediaBody(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxGrokVideoBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxGrokVideoBodyBytes {
		return nil, errors.New("mediatask: upstream response too large")
	}
	return body, nil
}

func usdTicksToCents(ticks int64) int64 {
	if ticks <= 0 {
		return 0
	}
	const ticksPerCent int64 = 100_000_000
	if ticks > math.MaxInt64-(ticksPerCent/2) {
		return math.MaxInt64 / ticksPerCent
	}
	return (ticks + ticksPerCent/2) / ticksPerCent
}

type providerCallError struct {
	class      string
	retryable  bool
	retryAfter time.Duration
	err        error
}

func (e *providerCallError) Error() string { return e.class + ": " + e.err.Error() }
func (e *providerCallError) Unwrap() error { return e.err }

func retryableProviderError(class string, err error) error {
	return retryableProviderErrorAfter(class, 0, err)
}

func retryableProviderErrorAfter(class string, retryAfter time.Duration, err error) error {
	if retryAfter <= 0 {
		retryAfter = defaultProviderRetryDelay(class)
	}
	return &providerCallError{class: class, retryable: true, retryAfter: retryAfter, err: err}
}

func terminalProviderError(class string, err error) error {
	return &providerCallError{class: class, err: err}
}

func providerErrorDetails(err error) (class string, retryable, recognized bool) {
	var callErr *providerCallError
	if errors.As(err, &callErr) {
		return callErr.class, callErr.retryable, true
	}
	return "provider_runtime_error", false, false
}

func providerRetryDelay(err error) time.Duration {
	var callErr *providerCallError
	if errors.As(err, &callErr) && callErr.retryable {
		if callErr.retryAfter > 0 {
			return callErr.retryAfter
		}
		return defaultProviderRetryDelay(callErr.class)
	}
	return 5 * time.Second
}

func defaultProviderRetryDelay(class string) time.Duration {
	switch strings.TrimSpace(class) {
	case "upstream_rate_limited", "credit_exhausted":
		return time.Minute
	case "upstream_overloaded", "upstream_5xx", "upstream_timeout", "network_timeout":
		return 15 * time.Second
	default:
		return 5 * time.Second
	}
}
