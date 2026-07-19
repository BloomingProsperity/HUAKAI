package quotaprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/accountquota"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	providerantigravity "github.com/BloomingProsperity/HUAKAI/internal/provider/antigravity"
)

const (
	antigravityModelsEndpoint         = "https://cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels"
	antigravityModelsFallbackEndpoint = "https://daily-cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels"
)

type AntigravityAdapter struct {
	http             adapterHTTP
	endpoint         string
	fallbackEndpoint string
}

func NewAntigravityAdapter(client *http.Client, proxyResolver provider.ProxyResolver) *AntigravityAdapter {
	return &AntigravityAdapter{
		http: newAdapterHTTP(client, proxyResolver), endpoint: antigravityModelsEndpoint,
		fallbackEndpoint: antigravityModelsFallbackEndpoint,
	}
}

func (a *AntigravityAdapter) Supports(credential provider.Credential, info provider.AccountInfo) bool {
	return a != nil && strings.EqualFold(strings.TrimSpace(info.Platform), "antigravity") && accessToken(credential) != ""
}

func (a *AntigravityAdapter) Source() accountquota.Source {
	return accountquota.SourceUpstreamModelCatalog
}

func (a *AntigravityAdapter) Fetch(ctx context.Context, accountID int64, credential provider.Credential, _ provider.AccountInfo, observedAt time.Time) (VendorResult, error) {
	projectID := strings.TrimSpace(credential.Extra["project_id"])
	if projectID == "" {
		return VendorResult{}, withErrorClass(ErrorClassCredentialUnavailable, errors.New("antigravity 额度采集缺少 project_id"))
	}
	body, err := json.Marshal(map[string]string{"project": projectID})
	if err != nil {
		return VendorResult{}, withErrorClass(ErrorClassConfigurationInvalid, err)
	}
	endpoint := strings.TrimSpace(a.endpoint)
	if endpoint == "" {
		return VendorResult{}, withErrorClass(ErrorClassConfigurationInvalid, ErrNotConfigured)
	}
	client, err := a.http.clientForAccount(ctx, accountID)
	if err != nil {
		return VendorResult{}, err
	}
	endpoints := []string{endpoint}
	if fallback := strings.TrimSpace(a.fallbackEndpoint); fallback != "" && fallback != endpoint {
		endpoints = append(endpoints, fallback)
	}
	resp, err := fetchAntigravityModels(ctx, client, endpoints, body, accessToken(credential))
	if err != nil {
		return VendorResult{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxUsageResponseSize+1))
	if err != nil {
		return VendorResult{}, withErrorClass(ErrorClassUpstreamResponseInvalid, err)
	}
	if len(raw) > maxUsageResponseSize {
		return VendorResult{}, withErrorClass(ErrorClassUpstreamResponseInvalid, errors.New("antigravity 额度响应过大"))
	}
	var decoded struct {
		Models map[string]struct {
			QuotaInfo *struct {
				RemainingFraction *float64 `json:"remainingFraction"`
				ResetTime         string   `json:"resetTime"`
			} `json:"quotaInfo"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return VendorResult{}, withErrorClass(ErrorClassUpstreamResponseInvalid, err)
	}
	facts := make([]accountquota.Fact, 0, len(decoded.Models))
	for modelKey, model := range decoded.Models {
		if strings.TrimSpace(modelKey) == "" || model.QuotaInfo == nil || model.QuotaInfo.RemainingFraction == nil {
			continue
		}
		fraction := *model.QuotaInfo.RemainingFraction
		if fraction < 0 || fraction > 1 {
			return VendorResult{}, withErrorClass(ErrorClassUpstreamResponseInvalid, fmt.Errorf("antigravity 模型 %q 剩余比例越界", modelKey))
		}
		remaining := fraction * 100
		used := 100 - remaining
		state := accountquota.StateAvailable
		if remaining <= 0 {
			state = accountquota.StateExhausted
		}
		fact := accountquota.Fact{
			MetricKey: "model_quota", ModelKey: strings.TrimSpace(modelKey), State: state,
			UtilizationPercent: &used, RemainingPercent: &remaining, ValidUntil: validUntil(observedAt),
		}
		if rawReset := strings.TrimSpace(model.QuotaInfo.ResetTime); rawReset != "" {
			resetAt, parseErr := time.Parse(time.RFC3339, rawReset)
			if parseErr != nil {
				return VendorResult{}, withErrorClass(ErrorClassUpstreamResponseInvalid, fmt.Errorf("antigravity 模型 %q 重置时间无效", modelKey))
			}
			resetAt = resetAt.UTC()
			fact.ResetsAt = &resetAt
		}
		facts = append(facts, fact)
	}
	if len(facts) == 0 {
		facts = append(facts, accountquota.Fact{MetricKey: "model_quota", State: accountquota.StateUnknown, ValidUntil: validUntil(observedAt)})
	}
	return VendorResult{Source: accountquota.SourceUpstreamModelCatalog, Facts: facts, Complete: true}, nil
}

func fetchAntigravityModels(ctx context.Context, client *http.Client, endpoints []string, body []byte, token string) (*http.Response, error) {
	for index, endpoint := range endpoints {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, withErrorClass(ErrorClassConfigurationInvalid, err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")
		providerantigravity.ApplyCloudCodeHeaders(req.Header)
		resp, err := client.Do(req)
		if err != nil {
			if index+1 < len(endpoints) {
				continue
			}
			return nil, withErrorClass(ErrorClassUpstreamUnreachable, err)
		}
		if resp.StatusCode == http.StatusOK {
			return resp, nil
		}
		statusCode := resp.StatusCode
		_ = resp.Body.Close()
		if index+1 < len(endpoints) && antigravityFallbackStatus(statusCode) {
			continue
		}
		return nil, upstreamStatusError(statusCode)
	}
	return nil, withErrorClass(ErrorClassConfigurationInvalid, ErrNotConfigured)
}

func antigravityFallbackStatus(statusCode int) bool {
	return statusCode == http.StatusRequestTimeout || statusCode == http.StatusNotFound ||
		statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError
}

var _ VendorAdapter = (*AntigravityAdapter)(nil)
