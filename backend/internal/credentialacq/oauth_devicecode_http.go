package credentialacq

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
)

func postJSON(ctx context.Context, client *http.Client, url string, body map[string]any, out any) error {
	status, err := postJSONStatus(ctx, client, url, body, out)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("credentialacq: endpoint returned status %d", status)
	}
	return nil
}

func postJSONStatus(ctx context.Context, client *http.Client, url string, body map[string]any, out any) (int, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, oauthFormResponseMaxBytes+1))
	if err != nil {
		return resp.StatusCode, err
	}
	if len(respBody) > oauthFormResponseMaxBytes {
		return resp.StatusCode, ErrResponseTooLarge
	}
	if len(respBody) > 0 && out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

func postFormJSON(ctx context.Context, client *http.Client, rawURL string, form url.Values, out any) (int, error) {
	if client == nil {
		client = defaultDeviceCodeHTTPClient()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, oauthFormResponseMaxBytes+1))
	if err != nil {
		return resp.StatusCode, err
	}
	if len(respBody) > oauthFormResponseMaxBytes {
		return resp.StatusCode, ErrResponseTooLarge
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return resp.StatusCode, fmt.Errorf("credentialacq: endpoint returned status %d", resp.StatusCode)
	}
	if len(respBody) > 0 && out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

func normalizedTokenPayload(response map[string]any, accessToken string) ([]byte, error) {
	payload := map[string]any{"access_token": strings.TrimSpace(accessToken)}
	copyFirstStringField(payload, response, "refresh_token", "refresh_token", "refreshToken")
	copyFirstStringField(payload, response, "id_token", "id_token", "idToken")
	copyFirstStringField(payload, response, "token_type", "token_type", "tokenType")
	copyFirstStringField(payload, response, "scope", "scope")
	copyFirstStringField(payload, response, "chatgpt_plan_type", "chatgpt_plan_type", "plan_type", "subscription_plan")
	copyFirstStringField(payload, response, "chatgpt_account_id", "chatgpt_account_id", "account_id", "accountId")
	copyFirstStringField(payload, response, "chatgpt_user_id", "chatgpt_user_id", "user_id", "userId")
	copyFirstStringField(payload, response, "email", "email")
	for _, key := range []string{"expires_in", "expiresIn"} {
		if value, ok := response[key]; ok && value != nil {
			payload["expires_in"] = value
			break
		}
	}
	return json.Marshal(payload)
}

func copyFirstStringField(out, in map[string]any, target string, aliases ...string) {
	for _, alias := range aliases {
		if value := stringField(in, alias); value != "" {
			out[target] = value
			return
		}
	}
}

func mergeRedactedContext(base map[string]any, extra map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		if strings.TrimSpace(k) != "" && v != nil {
			out[k] = v
		}
	}
	return out
}

func issuedAtFromPayload(payload map[string]any, fallback time.Time) time.Time {
	if raw := stringFromPayload(payload, "issued_at"); raw != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			return parsed
		}
	}
	return fallback
}

func stringFromPayload(payload map[string]any, key string) string {
	return stringField(payload, key)
}

func intFromPayload(payload map[string]any, key string) int {
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
		n, _ := value.Int64()
		return int(n)
	case string:
		var n int
		_, _ = fmt.Sscanf(strings.TrimSpace(value), "%d", &n)
		return n
	default:
		return 0
	}
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func sleepDeviceContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func resolveOpenAICodexOAuthTokenURL(deviceTokenURL string) string {
	u, err := url.Parse(strings.TrimSpace(deviceTokenURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return openAICodexOAuthTokenURL
	}
	u.Path = "/oauth/token"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// defaultDeviceCodeHTTPClient 返回带 SSRF 防护的设备授权 HTTP 客户端。
func defaultDeviceCodeHTTPClient() *http.Client {
	return auth.NewSSRFProtectedOAuthClient(&http.Client{Timeout: 30 * time.Second})
}
