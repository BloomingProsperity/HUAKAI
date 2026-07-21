package quotaprobe

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/accountquota"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionprofile"
)

const codexUsageEndpoint = "https://chatgpt.com/backend-api/wham/usage"

type CodexUsageAdapter struct {
	http     adapterHTTP
	endpoint string
}

func NewCodexUsageAdapter(client *http.Client, proxyResolver provider.ProxyResolver) *CodexUsageAdapter {
	return &CodexUsageAdapter{http: newAdapterHTTP(client, proxyResolver), endpoint: codexUsageEndpoint}
}

func (a *CodexUsageAdapter) Supports(credential provider.Credential, info provider.AccountInfo) bool {
	if a == nil || !strings.EqualFold(strings.TrimSpace(info.Platform), credentialstore.VendorOpenAI) || strings.TrimSpace(credential.Value) == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(info.AccountType)) {
	case credentialstore.AuthModeChatGPTOAuth, credentialstore.AuthModeCodexCLIOAuth, credentialstore.AuthModeCodexWebOAuth:
		return credential.Type == provider.CredentialTypeSessionToken || credential.Type == provider.CredentialTypeOAuthAccessToken
	default:
		return false
	}
}

func (a *CodexUsageAdapter) Source() accountquota.Source { return accountquota.SourceUpstreamUsage }

func (a *CodexUsageAdapter) Fetch(ctx context.Context, accountID int64, credential provider.Credential, _ provider.AccountInfo, observedAt time.Time) (VendorResult, error) {
	client, err := a.http.clientForAccount(ctx, accountID)
	if err != nil {
		return VendorResult{}, err
	}
	workspaceID := firstNonEmptyValue(credential.Extra, "chatgpt_account_id", "account_id", "organization_id")
	if workspaceID == "" {
		return VendorResult{}, withErrorClass(ErrorClassCredentialUnavailable, errors.New("codex 额度采集缺少上游账号标识"))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.endpoint, nil)
	if err != nil {
		return VendorResult{}, withErrorClass(ErrorClassConfigurationInvalid, err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken(credential))
	req.Header.Set("ChatGPT-Account-Id", workspaceID)
	req.Header.Set("OpenAI-Beta", "codex-1")
	req.Header.Set("OAI-Language", "zh-CN")
	req.Header.Set("Originator", codexQuotaOriginator(credential.Extra))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-Mode", "no-cors")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Priority", "u=4, i")
	req.Header.Set("User-Agent", codexQuotaUserAgent(credential.Extra))

	resp, err := client.Do(req)
	if err != nil {
		return VendorResult{}, withErrorClass(ErrorClassUpstreamUnreachable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return VendorResult{}, upstreamStatusError(resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxUsageResponseSize+1))
	if err != nil {
		return VendorResult{}, withErrorClass(ErrorClassUpstreamResponseInvalid, err)
	}
	if len(body) > maxUsageResponseSize {
		return VendorResult{}, withErrorClass(ErrorClassUpstreamResponseInvalid, errors.New("codex 额度响应过大"))
	}
	var payload codexUsagePayload
	if err := json.Unmarshal(body, &payload); err != nil || payload.RateLimit == nil {
		return VendorResult{}, withErrorClass(ErrorClassUpstreamResponseInvalid, errors.New("codex 额度响应格式无效"))
	}
	snapshot, facts, complete := projectCodexUsage(payload.RateLimit, observedAt)
	if len(facts) == 0 {
		return VendorResult{}, withErrorClass(ErrorClassUpstreamResponseIncomplete, errors.New("codex 额度响应没有有效窗口"))
	}
	result := VendorResult{Source: accountquota.SourceUpstreamUsage, Facts: facts, Complete: complete, Session: &snapshot}
	if !complete {
		result.ErrorClass = ErrorClassUpstreamPartialResponse
	}
	if rawPlan := strings.TrimSpace(payload.PlanType); rawPlan != "" {
		result.Subscription = subscriptionprofile.FromRaw(
			subscriptionprofile.VendorOpenAI, rawPlan,
			subscriptionprofile.SourceProviderAPI,
			subscriptionprofile.TrustVerifiedAPI,
			subscriptionprofile.VerificationVerified,
			strings.TrimSpace(payload.UserID), strings.TrimSpace(payload.AccountID),
		)
	}
	return result, nil
}

type codexUsagePayload struct {
	UserID    string           `json:"user_id"`
	AccountID string           `json:"account_id"`
	PlanType  string           `json:"plan_type"`
	RateLimit *codexRateLimits `json:"rate_limit"`
}

type codexRateLimits struct {
	Primary   *codexRateWindow `json:"primary_window"`
	Secondary *codexRateWindow `json:"secondary_window"`
}

type codexRateWindow struct {
	UsedPercent       float64 `json:"used_percent"`
	WindowSeconds     int64   `json:"limit_window_seconds"`
	ResetAfterSeconds int64   `json:"reset_after_seconds"`
	ResetAtUnix       int64   `json:"reset_at"`
}

func projectCodexUsage(limits *codexRateLimits, observedAt time.Time) (UsageSnapshot, []accountquota.Fact, bool) {
	if limits == nil {
		return UsageSnapshot{}, nil, false
	}
	short, long := classifyCodexBodyWindows(limits.Primary, limits.Secondary)
	fiveHour, fiveFact, fiveOK := projectCodexWindow("five_hour", short, observedAt)
	sevenDay, sevenFact, sevenOK := projectCodexWindow("seven_day", long, observedAt)
	facts := make([]accountquota.Fact, 0, 2)
	if fiveFact != nil {
		facts = append(facts, *fiveFact)
	}
	if sevenFact != nil {
		facts = append(facts, *sevenFact)
	}
	return UsageSnapshot{FiveHour: fiveHour, SevenDay: sevenDay}, facts, fiveOK && sevenOK
}

func classifyCodexBodyWindows(primary, secondary *codexRateWindow) (*codexRateWindow, *codexRateWindow) {
	switch {
	case primary != nil && secondary != nil && primary.WindowSeconds > 0 && secondary.WindowSeconds > 0:
		if primary.WindowSeconds < secondary.WindowSeconds {
			return primary, secondary
		}
		return secondary, primary
	case primary != nil && primary.WindowSeconds > 0:
		if primary.WindowSeconds <= int64(6*time.Hour/time.Second) {
			return primary, secondary
		}
		return secondary, primary
	case secondary != nil && secondary.WindowSeconds > 0:
		if secondary.WindowSeconds <= int64(6*time.Hour/time.Second) {
			return secondary, primary
		}
		return primary, secondary
	default:
		// 上游省略窗口时长时按协议角色兜底：主窗口是短周期，次窗口是长周期。
		return primary, secondary
	}
}

func projectCodexWindow(metric string, window *codexRateWindow, observedAt time.Time) (UsageWindow, *accountquota.Fact, bool) {
	if window == nil || math.IsNaN(window.UsedPercent) || math.IsInf(window.UsedPercent, 0) || window.UsedPercent < 0 || window.UsedPercent > 100 {
		return UsageWindow{}, nil, false
	}
	resetAt, ok := codexWindowResetAt(window, observedAt)
	if !ok {
		return UsageWindow{}, nil, false
	}
	used := window.UsedPercent
	remaining := 100 - used
	state := accountquota.StateAvailable
	if remaining <= 0 {
		state = accountquota.StateExhausted
	}
	fact := accountquota.Fact{
		MetricKey: metric, State: state,
		UtilizationPercent: &used, RemainingPercent: &remaining,
		ResetsAt: &resetAt, ValidUntil: validUntil(observedAt),
	}
	return UsageWindow{Utilization: &used, ResetsAt: &resetAt}, &fact, true
}

func codexWindowResetAt(window *codexRateWindow, observedAt time.Time) (time.Time, bool) {
	if window.ResetAtUnix > 0 {
		resetAt := time.Unix(window.ResetAtUnix, 0).UTC()
		if !resetAt.Before(observedAt.Add(-time.Minute)) && !resetAt.After(observedAt.Add(8*24*time.Hour)) {
			return resetAt, true
		}
	}
	if window.ResetAfterSeconds < 0 || window.ResetAfterSeconds > int64(8*24*time.Hour/time.Second) {
		return time.Time{}, false
	}
	return observedAt.UTC().Add(time.Duration(window.ResetAfterSeconds) * time.Second), true
}

func firstNonEmptyValue(values map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(values[key]); value != "" {
			return value
		}
	}
	return ""
}

func codexQuotaOriginator(extra map[string]string) string {
	if value := strings.TrimSpace(extra["originator"]); value != "" {
		return value
	}
	return "Codex Desktop"
}

func codexQuotaUserAgent(extra map[string]string) string {
	if value := strings.TrimSpace(extra["user_agent"]); value != "" && !strings.HasPrefix(strings.ToLower(value), "mozilla/") {
		return value
	}
	if version := strings.TrimSpace(extra["codex_version"]); version != "" {
		return "codex_cli_rs/" + version
	}
	return "codex/1.0.0 (linux; go)"
}

var _ VendorAdapter = (*CodexUsageAdapter)(nil)
