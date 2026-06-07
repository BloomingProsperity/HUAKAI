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
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("mediatask provider submit status %d", resp.StatusCode)
	}
	var out struct {
		ProviderTaskID string `json:"provider_task_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if strings.TrimSpace(out.ProviderTaskID) == "" {
		return "", fmt.Errorf("%w: empty provider task id", ErrProviderUnavailable)
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

func NewHTTPProviderRegistry(source ConfigSource, client *http.Client) *HTTPProviderRegistry {
	return &HTTPProviderRegistry{source: source, client: client}
}

func (r *HTTPProviderRegistry) Provider(ctx context.Context, name string) (AsyncMediaProvider, bool, error) {
	name = strings.TrimSpace(name)
	if r == nil || r.source == nil || !isHTTPProviderAlias(name) {
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

func isHTTPProviderAlias(name string) bool {
	switch name {
	case "http", "midjourney", "suno":
		return true
	default:
		return false
	}
}
