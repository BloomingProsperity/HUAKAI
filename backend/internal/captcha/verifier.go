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

// NewVerifier 构造按运行时 captcha_provider 路由校验端点的 verifier。secret 不再在 boot 期
// 固化,而是通过 secretResolver 在**请求期**解析(后台设置 captcha_secret 优先、空回退 env),
// 使运营在管理台配/换 secret 即生效、不重部署;secretResolver 为 nil 时退化为 noop(fail-open)。
// 注:与旧版「boot 期 secret 空即 noop」不同——现在 secret 可来自后台设置,故延迟到请求期判定;
// 若请求期解析仍为空(设置与 env 都没配),Verify 仍按 fail-open noop 处理,语义与旧版一致。
func NewVerifier(
	settings SettingsReader,
	secretResolver func(context.Context) string,
	client *http.Client,
) CaptchaVerifier {
	if secretResolver == nil {
		return noopVerifier{}
	}
	return settingsProviderVerifier{
		settings:       settings,
		secretResolver: secretResolver,
		client:         captchaHTTPClient(client),
		providers:      defaultSiteVerifyProviders(),
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
	settings       SettingsReader
	secretResolver func(context.Context) string
	client         *http.Client
	providers      []siteVerifyProvider
}

// resolveSecret 请求期解析 captcha secret(settings-first 回退 env 已封装在注入的 resolver 里)。
func (v settingsProviderVerifier) resolveSecret(ctx context.Context) string {
	if v.secretResolver == nil {
		return ""
	}
	return strings.TrimSpace(v.secretResolver(ctx))
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
	// secret 先行:请求期解析仍为空 = 未配置 → fail-open noop(且不读运行时设置),
	// 与旧版「secret 缺失即 noop」语义一致;有 secret 才按运行时 provider 路由校验。
	secret := v.resolveSecret(ctx)
	if secret == "" {
		return nil
	}
	endpoint, ok := v.enabledEndpoint(ctx)
	if !ok {
		return nil
	}
	return verifySiteToken(ctx, v.client, endpoint, secret, token, remoteIP)
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
