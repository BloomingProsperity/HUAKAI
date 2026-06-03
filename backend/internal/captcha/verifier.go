package captcha

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

const defaultTurnstileSiteVerifyURL = "https://challenges.cloudflare.com" +
	"/turnstile/v0/siteverify"

var (
	ErrTokenRequired      = errors.New("captcha: token required")
	ErrVerificationFailed = errors.New("captcha: verification failed")
)

type CaptchaVerifier interface {
	Verify(ctx context.Context, token, remoteIP string) error
}

type Verifier = CaptchaVerifier

type SettingsReader interface {
	Get(
		ctx context.Context,
		key platformsettings.SettingKey,
	) (platformsettings.StoredSetting, error)
}

type TurnstileConfig struct {
	Settings      SettingsReader
	Secret        string
	Client        *http.Client
	SiteVerifyURL string
}

func NewVerifier(
	settings SettingsReader,
	secret string,
	client *http.Client,
) CaptchaVerifier {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return noopVerifier{}
	}
	return NewTurnstileVerifier(TurnstileConfig{
		Settings: settings,
		Secret:   secret,
		Client:   client,
	})
}

func NewTurnstileVerifier(cfg TurnstileConfig) CaptchaVerifier {
	secret := strings.TrimSpace(cfg.Secret)
	if secret == "" {
		return noopVerifier{}
	}
	endpoint := strings.TrimSpace(cfg.SiteVerifyURL)
	if endpoint == "" {
		endpoint = defaultTurnstileSiteVerifyURL
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return turnstileVerifier{
		settings: cfg.Settings,
		secret:   secret,
		client:   client,
		endpoint: endpoint,
	}
}

type noopVerifier struct{}

func (noopVerifier) Verify(context.Context, string, string) error {
	return nil
}

type turnstileVerifier struct {
	settings SettingsReader
	secret   string
	client   *http.Client
	endpoint string
}

func (v turnstileVerifier) Verify(
	ctx context.Context,
	token string,
	remoteIP string,
) error {
	if !v.enabled(ctx) {
		return nil
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return ErrTokenRequired
	}
	form := url.Values{}
	form.Set("secret", v.secret)
	form.Set("response", token)
	if remoteIP = strings.TrimSpace(remoteIP); remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		v.endpoint,
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return fmt.Errorf("%w: build request", ErrVerificationFailed)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: request failed", ErrVerificationFailed)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%w: read response", ErrVerificationFailed)
	}
	if resp.StatusCode < http.StatusOK ||
		resp.StatusCode >= http.StatusMultipleChoices {
		return ErrVerificationFailed
	}
	var out struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return fmt.Errorf("%w: parse response", ErrVerificationFailed)
	}
	if !out.Success {
		return ErrVerificationFailed
	}
	return nil
}

func (v turnstileVerifier) enabled(ctx context.Context) bool {
	if v.settings == nil {
		return false
	}
	enabled, err := v.settings.Get(ctx, platformsettings.KeyCaptchaEnabled)
	if err != nil || strings.TrimSpace(enabled.Value) != "true" {
		return false
	}
	provider, err := v.settings.Get(ctx, platformsettings.KeyCaptchaProvider)
	if err != nil {
		return false
	}
	return strings.TrimSpace(provider.Value) == "turnstile"
}
