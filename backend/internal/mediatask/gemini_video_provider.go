package mediatask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/upstreamfeedback"
)

const geminiVideoProviderName = "gemini_video"

// GeminiVideoProvider 通过持久账号绑定执行 Gemini API 的 Veo 长任务。
// 传输、账号兼容校验和健康回流复用统一视频出站内核。
type GeminiVideoProvider struct {
	bound *GrokVideoProvider
}

func NewGeminiVideoProvider(deps GrokVideoProviderDeps) *GeminiVideoProvider {
	return &GeminiVideoProvider{bound: NewGrokVideoProvider(deps)}
}

func (p *GeminiVideoProvider) SetFeedback(feedback *upstreamfeedback.Observer) {
	if p != nil && p.bound != nil {
		p.bound.SetFeedback(feedback)
	}
}

func (*GeminiVideoProvider) Submit(context.Context, SubmitReq) (string, error) {
	return "", fmt.Errorf("%w: gemini video requires durable account binding", ErrInvalidInput)
}

func (*GeminiVideoProvider) Poll(context.Context, string) (PollResult, error) {
	return PollResult{}, fmt.Errorf("%w: gemini video requires durable account binding", ErrInvalidInput)
}

func (p *GeminiVideoProvider) SubmitBound(ctx context.Context, task Task, req SubmitReq) (string, error) {
	if p == nil || p.bound == nil {
		return "", terminalProviderError("provider_unavailable", ErrProviderUnavailable)
	}
	if strings.TrimSpace(task.TaskType) != "video_generate" {
		return "", terminalProviderError("provider_operation_not_supported", ErrInvalidInput)
	}
	body, err := geminiVideoSubmitBody(req.InputParams)
	if err != nil {
		return "", terminalProviderError("provider_submit_request_invalid", err)
	}
	endpoint := "/v1beta/models/" + url.PathEscape(strings.TrimSpace(task.ProviderModelID)) + ":predictLongRunning"
	idempotencyKey := strings.TrimSpace(req.IdempotencyKey)
	if idempotencyKey == "" {
		idempotencyKey = DeriveIdempotencyKey(req.TaskID, req.RequestID)
	}
	result, account, startedAt, selection, err := p.bound.dispatchBound(ctx, task, http.MethodPost, endpoint, body, idempotencyKey)
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
		return "", terminalProviderError("provider_submit_response_invalid", err)
	}
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		return "", p.bound.httpProviderError(ctx, task, account, result, responseBody, startedAt, true)
	}
	p.bound.observeSuccess(ctx, task, account, result, startedAt)
	var response struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(responseBody, &response) != nil || strings.TrimSpace(response.Name) == "" {
		return "", terminalProviderError("provider_submit_response_invalid", ErrProviderUnavailable)
	}
	releaseSelection = false
	return strings.TrimSpace(response.Name), nil
}

func (p *GeminiVideoProvider) PollBound(ctx context.Context, task Task, providerTaskID string) (PollResult, error) {
	if p == nil || p.bound == nil {
		return PollResult{}, terminalProviderError("provider_unavailable", ErrProviderUnavailable)
	}
	operation := strings.Trim(strings.TrimSpace(providerTaskID), "/")
	if operation == "" || strings.Contains(operation, "..") {
		return PollResult{}, terminalProviderError("provider_task_id_invalid", ErrInvalidInput)
	}
	result, account, startedAt, err := p.bound.dispatchPinned(ctx, task, http.MethodGet, "/v1beta/"+operation, nil)
	if err != nil {
		return PollResult{}, err
	}
	defer result.Close()
	body, err := readBoundedMediaBody(result.UpstreamReader)
	if err != nil {
		return PollResult{}, retryableProviderError("provider_poll_response_invalid", err)
	}
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		return PollResult{}, p.bound.httpProviderError(ctx, task, account, result, body, startedAt, false)
	}
	p.bound.observeSuccess(ctx, task, account, result, startedAt)

	var operationResult struct {
		Done  bool `json:"done"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
		Response struct {
			GenerateVideoResponse struct {
				GeneratedSamples []struct {
					Video struct {
						URI string `json:"uri"`
					} `json:"video"`
				} `json:"generatedSamples"`
			} `json:"generateVideoResponse"`
		} `json:"response"`
	}
	if err := json.Unmarshal(body, &operationResult); err != nil {
		return PollResult{}, retryableProviderError("provider_poll_response_invalid", err)
	}
	if !operationResult.Done {
		return PollResult{Status: StatusInProgress, Progress: 0}, nil
	}
	if operationResult.Error != nil {
		class := strings.ToLower(strings.TrimSpace(operationResult.Error.Status))
		if class == "" {
			class = "provider_failed"
		}
		return PollResult{Status: StatusFailed, Progress: 100, ErrorClass: class}, nil
	}
	if len(operationResult.Response.GenerateVideoResponse.GeneratedSamples) == 0 {
		return PollResult{}, retryableProviderError("provider_poll_result_missing", ErrProviderUnavailable)
	}
	uri := strings.TrimSpace(operationResult.Response.GenerateVideoResponse.GeneratedSamples[0].Video.URI)
	if uri == "" {
		return PollResult{}, retryableProviderError("provider_poll_result_missing", ErrProviderUnavailable)
	}
	storedResult, err := json.Marshal(map[string]any{
		"status": "completed", "progress": 100, "model": task.RequestedModel,
		"upstream_content": map[string]string{"uri": uri},
	})
	if err != nil {
		return PollResult{}, errors.Join(err, ErrProviderUnavailable)
	}
	return PollResult{
		Status: StatusSucceeded, Progress: 100, Result: storedResult,
		ActualCents: task.EstimatedCents, RoutingReason: durableMediaRoutingReason(task),
	}, nil
}

func (p *GeminiVideoProvider) DownloadBound(ctx context.Context, task Task) (ContentResult, error) {
	if p == nil || p.bound == nil {
		return ContentResult{}, ErrProviderUnavailable
	}
	source, err := geminiVideoSource(task.Result)
	if err != nil {
		return ContentResult{}, err
	}
	parsed, err := url.Parse(source)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() != "generativelanguage.googleapis.com" || parsed.User != nil || parsed.Fragment != "" || !strings.HasPrefix(parsed.Path, "/v1beta/") {
		return ContentResult{}, terminalProviderError("provider_content_uri_invalid", ErrContentUnavailable)
	}
	if err := p.bound.admitPinnedAccountRequest(ctx, task); err != nil {
		return ContentResult{}, err
	}
	credential, account, err := p.bound.resolveBoundAccount(ctx, task)
	if err != nil {
		return ContentResult{}, err
	}
	result, startedAt, err := p.bound.dispatchAccountWithQuery(ctx, task, http.MethodGet, parsed.EscapedPath(), parsed.RawQuery, nil, "", credential, account)
	if err != nil {
		return ContentResult{}, retryableProviderError("provider_content_dispatch_error", err)
	}
	if result == nil {
		return ContentResult{}, retryableProviderError("provider_content_response_invalid", ErrProviderUnavailable)
	}
	if result.StatusCode < http.StatusOK || result.StatusCode >= http.StatusMultipleChoices {
		defer result.Close()
		body, readErr := readBoundedMediaBody(result.UpstreamReader)
		if readErr != nil {
			return ContentResult{}, retryableProviderError("provider_content_response_invalid", readErr)
		}
		return ContentResult{}, p.bound.httpProviderError(ctx, task, account, result, body, startedAt, false)
	}
	p.bound.observeSuccess(ctx, task, account, result, startedAt)
	return ContentResult{Body: result.UpstreamReader, Headers: result.Headers.Clone(), StatusCode: result.StatusCode, Close: result.Close}, nil
}

func geminiVideoSource(result json.RawMessage) (string, error) {
	var stored struct {
		UpstreamContent struct {
			URI string `json:"uri"`
		} `json:"upstream_content"`
	}
	if json.Unmarshal(result, &stored) != nil || strings.TrimSpace(stored.UpstreamContent.URI) == "" {
		return "", ErrContentUnavailable
	}
	return strings.TrimSpace(stored.UpstreamContent.URI), nil
}

func RequiresContentProxy(task Task) bool {
	return task.Provider == geminiVideoProviderName && task.Status == StatusSucceeded && func() bool {
		_, err := geminiVideoSource(task.Result)
		return err == nil
	}()
}

// ContentProxyPath 返回该任务产物在网关上的代理下载路径。
func ContentProxyPath(task Task) string {
	return "/v1/videos/" + url.PathEscape(task.RequestID) + "/content"
}

// ContentProxyResult 返回受凭据保护产物任务的对外结果:上游地址必须用生成时绑定
// 账号的凭据才能取,直接交给用户会 401/403;故对外只暴露网关代理下载地址。
func ContentProxyResult(task Task) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{
		"status": "completed", "progress": 100, "model": task.RequestedModel,
		"data": []map[string]string{{"url": ContentProxyPath(task)}},
	})
	return raw
}

func geminiVideoSubmitBody(raw []byte) ([]byte, error) {
	var request map[string]any
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	prompt, _ := request["prompt"].(string)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, ErrInvalidInput
	}
	parameters := make(map[string]any)
	for _, mapping := range []struct {
		in  string
		out string
	}{
		{in: "aspect_ratio", out: "aspectRatio"},
		{in: "duration", out: "durationSeconds"},
		{in: "resolution", out: "resolution"},
		{in: "negative_prompt", out: "negativePrompt"},
		{in: "person_generation", out: "personGeneration"},
	} {
		if value, ok := request[mapping.in]; ok {
			parameters[mapping.out] = value
		}
	}
	payload := map[string]any{"instances": []map[string]string{{"prompt": prompt}}}
	if len(parameters) > 0 {
		payload["parameters"] = parameters
	}
	return json.Marshal(payload)
}
