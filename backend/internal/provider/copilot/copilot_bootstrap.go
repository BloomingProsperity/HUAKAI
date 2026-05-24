package copilot

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
)

const (
	defaultGitHubCopilotClientID       = "Iv1.b507a08c87ecfe98"
	defaultGitHubDeviceCodeURL         = "https://github.com/login/device/code"
	defaultGitHubDeviceAccessTokenURL  = "https://github.com/login/oauth/access_token"
	defaultGitHubDeviceScope           = "read:user"
	defaultGitHubDevicePollIntervalSec = 5
	defaultGitHubDeviceMaxPolls        = 120

	deviceCodeGrantType = "urn:ietf:params:oauth:grant-type:device_code"
)

type OAuthBootstrap struct {
	ClientID       string
	Scope          string
	DeviceCodeURL  string
	AccessTokenURL string
	HTTPClient     *http.Client
	MaxPolls       int
	Sleep          func(context.Context, time.Duration) error
}

type OAuthBootstrapResult struct {
	DeviceCode      string
	UserCode        string
	VerificationURL string
	Interval        time.Duration
	ExpiresIn       time.Duration
	AccessToken     string
	TokenType       string
}

type deviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	VerificationURL string `json:"verification_url"`
	Interval        int64  `json:"interval"`
	ExpiresIn       int64  `json:"expires_in"`
}

type accessTokenPollResponse struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int64  `json:"expires_in"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func (b OAuthBootstrap) Authorize(ctx context.Context) (OAuthBootstrapResult, error) {
	device, err := b.requestDeviceCode(ctx)
	if err != nil {
		return OAuthBootstrapResult{}, err
	}
	token, err := b.pollAccessToken(ctx, device)
	if err != nil {
		return OAuthBootstrapResult{}, err
	}
	return OAuthBootstrapResult{
		DeviceCode:      device.DeviceCode,
		UserCode:        device.UserCode,
		VerificationURL: firstNonEmptyString(device.VerificationURL, device.VerificationURI),
		Interval:        secondsDuration(device.Interval),
		ExpiresIn:       secondsDuration(device.ExpiresIn),
		AccessToken:     strings.TrimSpace(token.AccessToken),
		TokenType:       strings.TrimSpace(token.TokenType),
	}, nil
}

func (b OAuthBootstrap) requestDeviceCode(ctx context.Context) (deviceCodeResponse, error) {
	payload, err := json.Marshal(map[string]string{
		"client_id": b.clientID(),
		"scope":     b.scope(),
	})
	if err != nil {
		return deviceCodeResponse{}, fmt.Errorf("copilot oauth bootstrap: marshal device request: %w", err)
	}
	var out deviceCodeResponse
	if err := b.doJSON(ctx, http.MethodPost, b.deviceCodeURL(), payload, &out); err != nil {
		return deviceCodeResponse{}, fmt.Errorf("copilot oauth bootstrap: request device code: %w", err)
	}
	if strings.TrimSpace(out.DeviceCode) == "" {
		return deviceCodeResponse{}, errors.New("copilot oauth bootstrap: device_code missing")
	}
	if strings.TrimSpace(out.UserCode) == "" {
		return deviceCodeResponse{}, errors.New("copilot oauth bootstrap: user_code missing")
	}
	if strings.TrimSpace(firstNonEmptyString(out.VerificationURL, out.VerificationURI)) == "" {
		return deviceCodeResponse{}, errors.New("copilot oauth bootstrap: verification URL missing")
	}
	if out.Interval <= 0 {
		out.Interval = defaultGitHubDevicePollIntervalSec
	}
	return out, nil
}

func (b OAuthBootstrap) pollAccessToken(ctx context.Context, device deviceCodeResponse) (accessTokenPollResponse, error) {
	interval := secondsDuration(device.Interval)
	maxPolls := b.MaxPolls
	if maxPolls <= 0 {
		maxPolls = defaultGitHubDeviceMaxPolls
	}
	for attempt := 0; attempt < maxPolls; attempt++ {
		payload, err := json.Marshal(map[string]string{
			"client_id":   b.clientID(),
			"device_code": device.DeviceCode,
			"grant_type":  deviceCodeGrantType,
		})
		if err != nil {
			return accessTokenPollResponse{}, fmt.Errorf("copilot oauth bootstrap: marshal poll request: %w", err)
		}
		var out accessTokenPollResponse
		if err := b.doJSON(ctx, http.MethodPost, b.accessTokenURL(), payload, &out); err != nil {
			return accessTokenPollResponse{}, fmt.Errorf("copilot oauth bootstrap: poll access token: %w", err)
		}
		if strings.TrimSpace(out.AccessToken) != "" {
			return out, nil
		}
		switch strings.TrimSpace(out.Error) {
		case "authorization_pending":
		case "slow_down":
			interval += 5 * time.Second
		case "expired_token":
			return accessTokenPollResponse{}, errors.New("copilot oauth bootstrap: device code expired")
		case "access_denied":
			return accessTokenPollResponse{}, errors.New("copilot oauth bootstrap: user denied device authorization")
		case "":
			return accessTokenPollResponse{}, errors.New("copilot oauth bootstrap: access_token missing")
		default:
			if out.ErrorDescription != "" {
				return accessTokenPollResponse{}, fmt.Errorf("copilot oauth bootstrap: %s: %s", out.Error, out.ErrorDescription)
			}
			return accessTokenPollResponse{}, fmt.Errorf("copilot oauth bootstrap: %s", out.Error)
		}
		if err := b.sleep(ctx, interval); err != nil {
			return accessTokenPollResponse{}, err
		}
	}
	return accessTokenPollResponse{}, errors.New("copilot oauth bootstrap: timed out waiting for device authorization")
}

func (b OAuthBootstrap) doJSON(ctx context.Context, method, endpoint string, payload []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", defaultCopilotUserAgent)
	resp, err := b.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return fmt.Errorf("decode json: %w", err)
	}
	return nil
}

func (b OAuthBootstrap) clientID() string {
	return firstNonEmptyString(b.ClientID, defaultGitHubCopilotClientID)
}

func (b OAuthBootstrap) scope() string {
	return firstNonEmptyString(b.Scope, defaultGitHubDeviceScope)
}

func (b OAuthBootstrap) deviceCodeURL() string {
	return firstNonEmptyString(b.DeviceCodeURL, defaultGitHubDeviceCodeURL)
}

func (b OAuthBootstrap) accessTokenURL() string {
	return firstNonEmptyString(b.AccessTokenURL, defaultGitHubDeviceAccessTokenURL)
}

func (b OAuthBootstrap) httpClient() *http.Client {
	if b.HTTPClient != nil {
		return b.HTTPClient
	}
	return http.DefaultClient
}

func (b OAuthBootstrap) sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	if b.Sleep != nil {
		return b.Sleep(ctx, d)
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

func secondsDuration(seconds int64) time.Duration {
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}
