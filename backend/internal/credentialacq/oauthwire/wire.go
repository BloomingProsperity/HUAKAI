// Package oauthwire 提供设备授权流程共用的 HTTP 与响应归一化能力。
package oauthwire

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
)

const maxResponseBytes = 1 << 20

var ErrResponseTooLarge = errors.New("credentialacq: response too large")

func PostJSON(ctx context.Context, client *http.Client, rawURL string, body map[string]any, out any) error {
	status, err := PostJSONStatus(ctx, client, rawURL, body, out)
	if err != nil {
		return err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return fmt.Errorf("credentialacq: endpoint returned status %d", status)
	}
	return nil
}

func PostJSONStatus(ctx context.Context, client *http.Client, rawURL string, body map[string]any, out any) (int, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	return executeJSONRequest(client, req, out, false)
}

func PostFormJSON(ctx context.Context, client *http.Client, rawURL string, form url.Values, out any) (int, error) {
	if client == nil {
		client = DefaultHTTPClient()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return executeJSONRequest(client, req, out, true)
}

func executeJSONRequest(client *http.Client, req *http.Request, out any, requireSuccess bool) (int, error) {
	res, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes+1))
	if err != nil {
		return res.StatusCode, err
	}
	if len(body) > maxResponseBytes {
		return res.StatusCode, ErrResponseTooLarge
	}
	if requireSuccess && (res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices) {
		return res.StatusCode, fmt.Errorf("credentialacq: endpoint returned status %d", res.StatusCode)
	}
	if len(body) > 0 && out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return res.StatusCode, err
		}
	}
	return res.StatusCode, nil
}

func NormalizeTokenPayload(response map[string]any, accessToken string) ([]byte, error) {
	payload := map[string]any{"access_token": strings.TrimSpace(accessToken)}
	copyFirstString(payload, response, "refresh_token", "refresh_token", "refreshToken")
	copyFirstString(payload, response, "id_token", "id_token", "idToken")
	copyFirstString(payload, response, "token_type", "token_type", "tokenType")
	copyFirstString(payload, response, "scope", "scope")
	copyFirstString(payload, response, "chatgpt_plan_type", "chatgpt_plan_type", "plan_type", "subscription_plan")
	copyFirstString(payload, response, "subscription_tier", "subscription_tier", "subscriptionTier", "tier_id")
	copyFirstString(payload, response, "chatgpt_account_id", "chatgpt_account_id", "account_id", "accountId")
	copyFirstString(payload, response, "chatgpt_user_id", "chatgpt_user_id", "user_id", "userId")
	copyFirstString(payload, response, "email", "email")
	for _, key := range []string{"expires_in", "expiresIn"} {
		if value, ok := response[key]; ok && value != nil {
			payload["expires_in"] = value
			break
		}
	}
	return json.Marshal(payload)
}

func copyFirstString(out, in map[string]any, target string, aliases ...string) {
	for _, alias := range aliases {
		if value := String(in, alias); value != "" {
			out[target] = value
			return
		}
	}
}

func MergeRedactedContext(base, extra map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range base {
		out[key] = value
	}
	for key, value := range extra {
		if strings.TrimSpace(key) != "" && value != nil {
			out[key] = value
		}
	}
	return out
}

func IssuedAt(payload map[string]any, fallback time.Time) time.Time {
	if raw := String(payload, "issued_at"); raw != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			return parsed
		}
	}
	return fallback
}

func String(payload map[string]any, key string) string {
	if value, ok := payload[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func Int(payload map[string]any, key string) int {
	if payload == nil {
		return 0
	}
	switch value := payload[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		number, _ := value.Int64()
		return int(number)
	case string:
		var number int
		_, _ = fmt.Sscanf(strings.TrimSpace(value), "%d", &number)
		return number
	default:
		return 0
	}
}

func FirstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func Sleep(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func ResolveTokenURL(deviceTokenURL, fallback string) string {
	u, err := url.Parse(strings.TrimSpace(deviceTokenURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fallback
	}
	u.Path = "/oauth/token"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func DefaultHTTPClient() *http.Client {
	return auth.NewSSRFProtectedOAuthClient(&http.Client{Timeout: 30 * time.Second})
}
