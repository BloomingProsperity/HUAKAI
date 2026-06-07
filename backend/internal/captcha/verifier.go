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
const defaultRecaptchaSiteVerifyURL = "https://www.google.com/recaptcha/api/siteverify"
const defaultHCaptchaSiteVerifyURL = "https://hcaptcha.com/siteverify"

const (
	captchaProviderTurnstile = "turnstile"
	captchaProviderRecaptcha = "recaptcha"
	captchaProviderHCaptcha  = "hcaptcha"
)

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
	return settingsProviderVerifier{
		settings:  settings,
		secret:    secret,
		client:    captchaHTTPClient(client),
		providers: defaultSiteVerifyProviders(),
	}
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
	return turnstileVerifier{
		settings: cfg.Settings,
		secret:   secret,
		client:   captchaHTTPClient(cfg.Client),
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

type siteVerifyProvider struct {
	id       string
	endpoint string
}

type settingsProviderVerifier struct {
	settings  SettingsReader
	secret    string
	client    *http.Client
	providers []siteVerifyProvider
}

func (v turnstileVerifier) Verify(
	ctx context.Context,
	token string,
	remoteIP string,
) error {
	if !v.enabled(ctx) {
		return nil
	}
	return verifySiteToken(ctx, v.client, v.endpoint, v.secret, token, remoteIP)
}

func (v settingsProviderVerifier) Verify(
	ctx context.Context,
	token string,
	remoteIP string,
) error {
	endpoint, ok := v.enabledEndpoint(ctx)
	if !ok {
		return nil
	}
	return verifySiteToken(ctx, v.client, endpoint, v.secret, token, remoteIP)
}

func verifySiteToken(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	secret string,
	token string,
	remoteIP string,
) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return ErrTokenRequired
	}
	form := url.Values{}
	form.Set("secret", secret)
	form.Set("response", token)
	if remoteIP = strings.TrimSpace(remoteIP); remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		endpoint,
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return fmt.Errorf("%w: build request", ErrVerificationFailed)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
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
	provider, ok := runtimeCaptchaProvider(ctx, v.settings)
	return ok && provider == captchaProviderTurnstile
}

func (v settingsProviderVerifier) enabledEndpoint(ctx context.Context) (string, bool) {
	provider, ok := runtimeCaptchaProvider(ctx, v.settings)
	if !ok {
		return "", false
	}
	for _, candidate := range v.providers {
		if provider == candidate.id {
			return candidate.endpoint, true
		}
	}
	return "", false
}

func runtimeCaptchaProvider(ctx context.Context, settings SettingsReader) (string, bool) {
	if settings == nil {
		return "", false
	}
	enabled, err := settings.Get(ctx, platformsettings.KeyCaptchaEnabled)
	if err != nil || strings.TrimSpace(enabled.Value) != "true" {
		return "", false
	}
	provider, err := settings.Get(ctx, platformsettings.KeyCaptchaProvider)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(provider.Value), true
}

func defaultSiteVerifyProviders() []siteVerifyProvider {
	return []siteVerifyProvider{
		{id: captchaProviderTurnstile, endpoint: defaultTurnstileSiteVerifyURL},
		{id: captchaProviderRecaptcha, endpoint: defaultRecaptchaSiteVerifyURL},
		{id: captchaProviderHCaptcha, endpoint: defaultHCaptchaSiteVerifyURL},
	}
}

func captchaHTTPClient(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return &http.Client{Timeout: 10 * time.Second}
}
