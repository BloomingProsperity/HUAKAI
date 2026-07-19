package quotaprobe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/accountquota"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

const grokBillingBase = "https://cli-chat-proxy.grok.com/v1"

type GrokBillingAdapter struct {
	http adapterHTTP
	base string
}

func NewGrokBillingAdapter(client *http.Client, proxyResolver provider.ProxyResolver) *GrokBillingAdapter {
	return &GrokBillingAdapter{http: newAdapterHTTP(client, proxyResolver), base: grokBillingBase}
}

func (a *GrokBillingAdapter) Supports(credential provider.Credential, info provider.AccountInfo) bool {
	return a != nil && strings.EqualFold(strings.TrimSpace(info.Platform), "grok") && strings.EqualFold(strings.TrimSpace(info.AccountType), "xai_oauth") && accessToken(credential) != ""
}

func (a *GrokBillingAdapter) Source() accountquota.Source { return accountquota.SourceUpstreamBilling }

func (a *GrokBillingAdapter) Fetch(ctx context.Context, accountID int64, credential provider.Credential, _ provider.AccountInfo, observedAt time.Time) (VendorResult, error) {
	client, err := a.http.clientForAccount(ctx, accountID)
	if err != nil {
		return VendorResult{}, err
	}
	var weekly, monthly []accountquota.Fact
	var weeklyErr, monthlyErr error
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		weekly, weeklyErr = a.fetch(ctx, client, credential, true, observedAt)
	}()
	go func() {
		defer group.Done()
		monthly, monthlyErr = a.fetch(ctx, client, credential, false, observedAt)
	}()
	group.Wait()
	facts := append(weekly, monthly...)
	if len(facts) == 0 {
		if weeklyErr != nil {
			return VendorResult{}, weeklyErr
		}
		return VendorResult{}, monthlyErr
	}
	result := VendorResult{Source: accountquota.SourceUpstreamBilling, Facts: facts, Complete: weeklyErr == nil && monthlyErr == nil}
	if !result.Complete {
		result.ErrorClass = ErrorClassUpstreamPartialResponse
	}
	return result, nil
}

type grokBillingPayload struct {
	Config *struct {
		CurrentPeriod *struct {
			End string `json:"end"`
		} `json:"currentPeriod"`
		CreditUsagePercent *float64 `json:"creditUsagePercent"`
		ProductUsage       []struct {
			Product      string   `json:"product"`
			UsagePercent *float64 `json:"usagePercent"`
		} `json:"productUsage"`
		MonthlyLimit     json.RawMessage `json:"monthlyLimit"`
		Used             json.RawMessage `json:"used"`
		BillingPeriodEnd string          `json:"billingPeriodEnd"`
	} `json:"config"`
}

func (a *GrokBillingAdapter) fetch(ctx context.Context, client *http.Client, credential provider.Credential, weekly bool, observedAt time.Time) ([]accountquota.Fact, error) {
	path := "/billing"
	if weekly {
		path += "?format=credits"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(a.base, "/")+path, nil)
	if err != nil {
		return nil, withErrorClass(ErrorClassConfigurationInvalid, err)
	}
	version := strings.TrimSpace(credential.Extra["client_version"])
	if version == "" {
		version = "0.2.93"
	}
	req.Header.Set("Authorization", "Bearer "+accessToken(credential))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-xai-token-auth", "xai-grok-cli")
	req.Header.Set("x-grok-client-version", version)
	req.Header.Set("User-Agent", "HUAKAI-GrokCLI/"+version)
	resp, err := client.Do(req)
	if err != nil {
		return nil, withErrorClass(ErrorClassUpstreamUnreachable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, upstreamStatusError(resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxUsageResponseSize+1))
	if err != nil {
		return nil, withErrorClass(ErrorClassUpstreamResponseInvalid, err)
	}
	if len(raw) > maxUsageResponseSize {
		return nil, withErrorClass(ErrorClassUpstreamResponseInvalid, errors.New("grok billing 响应过大"))
	}
	var payload grokBillingPayload
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Config == nil {
		return nil, withErrorClass(ErrorClassUpstreamResponseInvalid, errors.New("grok billing 响应格式无效"))
	}
	if weekly {
		return weeklyGrokFacts(payload, observedAt)
	}
	return monthlyGrokFacts(payload, observedAt)
}

func weeklyGrokFacts(payload grokBillingPayload, observedAt time.Time) ([]accountquota.Fact, error) {
	config := payload.Config
	facts := make([]accountquota.Fact, 0, 1+len(config.ProductUsage))
	resetAt, err := optionalTime(config.CurrentPeriod)
	if err != nil {
		return nil, withErrorClass(ErrorClassUpstreamResponseInvalid, err)
	}
	appendPercentFact := func(metric, model string, used *float64) error {
		if used == nil {
			return nil
		}
		if *used < 0 || *used > 100 || math.IsNaN(*used) || math.IsInf(*used, 0) {
			return fmt.Errorf("grok weekly 使用率越界")
		}
		remaining := 100 - *used
		state := accountquota.StateAvailable
		if remaining <= 0 {
			state = accountquota.StateExhausted
		}
		facts = append(facts, accountquota.Fact{MetricKey: metric, ModelKey: model, State: state, UtilizationPercent: used, RemainingPercent: &remaining, ResetsAt: resetAt, ValidUntil: validUntil(observedAt)})
		return nil
	}
	if err := appendPercentFact("weekly_credits", "", config.CreditUsagePercent); err != nil {
		return nil, withErrorClass(ErrorClassUpstreamResponseInvalid, err)
	}
	for _, product := range config.ProductUsage {
		if err := appendPercentFact("product_credits", strings.TrimSpace(product.Product), product.UsagePercent); err != nil {
			return nil, withErrorClass(ErrorClassUpstreamResponseInvalid, err)
		}
	}
	if len(facts) == 0 {
		facts = append(facts, accountquota.Fact{MetricKey: "weekly_credits", State: accountquota.StateUnknown, ValidUntil: validUntil(observedAt)})
	}
	return facts, nil
}

func monthlyGrokFacts(payload grokBillingPayload, observedAt time.Time) ([]accountquota.Fact, error) {
	limit, limitOK := jsonNumber(payload.Config.MonthlyLimit)
	used, usedOK := jsonNumber(payload.Config.Used)
	if !limitOK || !usedOK || limit <= 0 {
		return []accountquota.Fact{{MetricKey: "monthly_spend", State: accountquota.StateUnknown, ValidUntil: validUntil(observedAt)}}, nil
	}
	utilization := math.Min(100, used/limit*100)
	remainingPercent := math.Max(0, 100-utilization)
	remainingValue := math.Max(0, limit-used)
	state := accountquota.StateAvailable
	if remainingValue <= 0 {
		state = accountquota.StateExhausted
	}
	fact := accountquota.Fact{MetricKey: "monthly_spend", State: state, UsedValue: &used, LimitValue: &limit, RemainingValue: &remainingValue, Unit: "cent", UtilizationPercent: &utilization, RemainingPercent: &remainingPercent, ValidUntil: validUntil(observedAt)}
	if raw := strings.TrimSpace(payload.Config.BillingPeriodEnd); raw != "" {
		resetAt, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, withErrorClass(ErrorClassUpstreamResponseInvalid, err)
		}
		resetAt = resetAt.UTC()
		fact.ResetsAt = &resetAt
	}
	return []accountquota.Fact{fact}, nil
}

func optionalTime(period *struct {
	End string `json:"end"`
}) (*time.Time, error) {
	if period == nil || strings.TrimSpace(period.End) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(period.End))
	if err != nil {
		return nil, err
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func jsonNumber(raw json.RawMessage) (float64, bool) {
	text := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if text == "" || text == "null" {
		return 0, false
	}
	value, err := strconv.ParseFloat(text, 64)
	return value, err == nil && !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

var _ VendorAdapter = (*GrokBillingAdapter)(nil)
