package mediatask

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type httpAsyncProvider struct {
	baseURL string
	client  *http.Client
}

func NewHTTPProvider(baseURL string, client *http.Client) AsyncMediaProvider {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if client == nil {
		client = http.DefaultClient
	}
	return &httpAsyncProvider{baseURL: baseURL, client: client}
}

func (p *httpAsyncProvider) Submit(ctx context.Context, req SubmitReq) (string, error) {
	if p == nil || p.baseURL == "" {
		return "", ErrProviderUnavailable
	}
	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/tasks", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	// 携带由任务身份派生的稳定幂等键:同一任务被重复提交(例如租约过期后第二个
	// worker 再次提交)时,上游据此把重复请求去重到同一条上游任务,避免产生无人
	// 结算的孤儿上游成本。键优先取请求体里已派生好的值,缺失时就地从任务身份派生。
	idemKey := strings.TrimSpace(req.IdempotencyKey)
	if idemKey == "" {
		idemKey = DeriveIdempotencyKey(req.TaskID, req.RequestID)
	}
	if idemKey != "" {
		httpReq.Header.Set("Idempotency-Key", idemKey)
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return "", terminalProviderError("provider_submit_outcome_unknown", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		statusErr := fmt.Errorf("mediatask provider submit status %d", resp.StatusCode)
		switch {
		case submitHTTPOutcomeUnknown(resp.StatusCode):
			return "", terminalProviderError("provider_submit_outcome_unknown", statusErr)
		case resp.StatusCode == http.StatusTooManyRequests:
			return "", retryableProviderError("upstream_rate_limited", statusErr)
		default:
			return "", terminalProviderError("provider_submit_rejected", statusErr)
		}
	}
	var out struct {
		ProviderTaskID string `json:"provider_task_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", terminalProviderError("provider_submit_response_invalid", err)
	}
	if strings.TrimSpace(out.ProviderTaskID) == "" {
		return "", terminalProviderError(
			"provider_submit_response_invalid",
			fmt.Errorf("%w: empty provider task id", ErrProviderUnavailable),
		)
	}
	return strings.TrimSpace(out.ProviderTaskID), nil
}

func (p *httpAsyncProvider) Poll(ctx context.Context, providerTaskID string) (PollResult, error) {
	if p == nil || p.baseURL == "" {
		return PollResult{}, ErrProviderUnavailable
	}
	providerTaskID = strings.TrimSpace(providerTaskID)
	if providerTaskID == "" {
		return PollResult{}, fmt.Errorf("%w: provider_task_id", ErrInvalidInput)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/tasks/"+url.PathEscape(providerTaskID), nil)
	if err != nil {
		return PollResult{}, err
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return PollResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return PollResult{}, fmt.Errorf("mediatask provider poll status %d", resp.StatusCode)
	}
	var out PollResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return PollResult{}, err
	}
	return out.Normalized(), nil
}

type noopProvider struct{}

func NewNoopProvider() AsyncMediaProvider {
	return noopProvider{}
}

func (noopProvider) Submit(_ context.Context, req SubmitReq) (string, error) {
	if req.TaskID > 0 {
		return "noop-" + strconv.FormatInt(req.TaskID, 10), nil
	}
	return "noop-" + strings.TrimSpace(req.RequestID), nil
}

func (noopProvider) Poll(context.Context, string) (PollResult, error) {
	return PollResult{Status: StatusInProgress, Progress: 0}, nil
}

func (r PollResult) Normalized() PollResult {
	if r.Progress < 0 {
		r.Progress = 0
	}
	if r.Progress > 100 {
		r.Progress = 100
	}
	switch r.Status {
	case StatusQueued, StatusInProgress, StatusSucceeded, StatusFailed, StatusExpired:
	default:
		r.Status = StatusInProgress
	}
	if r.Status == StatusSucceeded {
		r.Progress = 100
	}
	return r
}

type HTTPProviderRegistry struct {
	source ConfigSource
	client *http.Client
}

type OverlayProviderRegistry struct {
	providers map[string]AsyncMediaProvider
	fallback  ProviderRegistry
}

func NewOverlayProviderRegistry(fallback ProviderRegistry, providers map[string]AsyncMediaProvider) *OverlayProviderRegistry {
	normalized := make(map[string]AsyncMediaProvider, len(providers))
	for name, mediaProvider := range providers {
		if key := strings.ToLower(strings.TrimSpace(name)); key != "" && mediaProvider != nil {
			normalized[key] = mediaProvider
		}
	}
	return &OverlayProviderRegistry{providers: normalized, fallback: fallback}
}

func (r *OverlayProviderRegistry) Provider(ctx context.Context, name string) (AsyncMediaProvider, bool, error) {
	if r == nil {
		return nil, false, nil
	}
	if mediaProvider := r.providers[strings.ToLower(strings.TrimSpace(name))]; mediaProvider != nil {
		return mediaProvider, true, nil
	}
	if r.fallback == nil {
		return nil, false, nil
	}
	return r.fallback.Provider(ctx, name)
}

func NewHTTPProviderRegistry(source ConfigSource, client *http.Client) *HTTPProviderRegistry {
	return &HTTPProviderRegistry{source: source, client: client}
}

func (r *HTTPProviderRegistry) Provider(ctx context.Context, name string) (AsyncMediaProvider, bool, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if r == nil || r.source == nil || !isHTTPMediaProviderName(name) {
		return nil, false, nil
	}
	cfg, err := r.source.Load(ctx)
	if err != nil {
		return nil, false, err
	}
	if strings.TrimSpace(cfg.ProviderBaseURL) == "" {
		return nil, false, nil
	}
	return NewHTTPProvider(cfg.ProviderBaseURL, r.client), true, nil
}

func isHTTPMediaProviderName(name string) bool {
	switch name {
	case "http", "midjourney", "suno", "video", "kling", "jimeng", "vidu", "sora", "hailuo":
		return true
	default:
		return false
	}
}
