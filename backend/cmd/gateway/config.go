package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	runtimeconfig "github.com/BloomingProsperity/HUAKAI/internal/config"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	mailinfra "github.com/BloomingProsperity/HUAKAI/internal/email"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp"
	obsoutbox "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

type Config = runtimeconfig.Config

func loadGatewayConfig(logger *zap.Logger) (*Config, error) {
	cfg, err := runtimeconfig.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	logger.Info("config loaded",
		zap.String("listen", cfg.Listen),
		zap.String("auth_mode", "api_keys_table"),
	)
	return cfg, nil
}

func buildAuthEmailSender(_ *Config, store mailinfra.SettingsStore, keys credentialstore.KeyProvider, logger *zap.Logger, outbox obsoutbox.Outbox, frontendBaseURL func(context.Context) string) (gatewayhttp.AuthEmailSender, error) {
	opts := []mailinfra.AuthSenderOption{mailinfra.WithOutbox(outbox)}
	if frontendBaseURL != nil {
		// 前端 base URL 已配置时,鉴权邮件投递完整可点链接(用户直接点),否则回退裸 token + 前端粘贴框。
		opts = append(opts, mailinfra.WithFrontendBaseURL(frontendBaseURL))
	}
	sender, err := mailinfra.BuildEmailSender(context.Background(), store, keys, opts...)
	if err != nil {
		return nil, err
	}
	if logger != nil && strings.EqualFold(strings.TrimSpace(os.Getenv("HUAKAI_DEV_AUTH_RETURN_TOKEN")), "true") {
		logger.Warn("dev mode, do not enable in production", zap.String("env", "HUAKAI_DEV_AUTH_RETURN_TOKEN"))
	}
	return sender, nil
}

func releaseModeProduction() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("HUAKAI_RELEASE_MODE")), "production")
}

// validateReleaseMode 在启动时校验 HUAKAI_RELEASE_MODE。
// 空/未设或拼错的值都会让 releaseModeProduction() 判 false,
// 静默降级为 dev —— 绕过 postgres ledger / 持久私钥 / email / channelhealth signer 等全部
// production fail-closed 门控。启动必须显式声明 production 或一个非生产模式,
// 杜绝"部署遗漏 env 就跑在 dev"和"想上生产却因打错字静默跑在 dev"。
func validateReleaseMode() error {
	raw := strings.TrimSpace(os.Getenv("HUAKAI_RELEASE_MODE"))
	switch strings.ToLower(raw) {
	case "dev", "development", "test", "production":
		return nil
	case "":
		return fmt.Errorf("HUAKAI_RELEASE_MODE 未设置:必须显式声明运行模式,拒绝静默降级。生产部署设 HUAKAI_RELEASE_MODE=production;本机/内网自测设 dev(亦可 development / test)")
	default:
		return fmt.Errorf("HUAKAI_RELEASE_MODE=%q 不是已知取值(production / development / dev / test);拒绝静默降级为 dev。生产设 production,自测设 dev", raw)
	}
}

func releaseMode() eventbus.ReleaseMode {
	if releaseModeProduction() {
		return eventbus.ReleaseModeProduction
	}
	return eventbus.ReleaseModeDev
}

func captchaTurnstileSecret() string {
	return strings.TrimSpace(os.Getenv("HUAKAI_CAPTCHA_TURNSTILE_SECRET"))
}

type gatewayPlatformSettings interface {
	Get(context.Context, platformsettings.SettingKey) (platformsettings.StoredSetting, error)
}

type captchaConfigStatus struct {
	Enabled          bool
	EnabledSource    string
	SecretConfigured bool
	ConfigurationOK  bool
	Issue            string
}

func validateProductionCaptchaConfig(ctx context.Context, settings gatewayPlatformSettings, secret string) error {
	if !releaseModeProduction() {
		return nil
	}
	if _, err := captchaConfigurationStatus(ctx, settings, secret); err != nil {
		return fmt.Errorf("read captcha_enabled for production CAPTCHA startup gate: %w", err)
	}
	return nil
}

func captchaConfigurationStatus(ctx context.Context, settings gatewayPlatformSettings, secret string) (captchaConfigStatus, error) {
	status := captchaConfigStatus{
		SecretConfigured: strings.TrimSpace(secret) != "",
		ConfigurationOK:  true,
	}
	enabled, source, err := captchaEnabledSetting(ctx, settings)
	if err != nil {
		return captchaConfigStatus{}, err
	}
	status.Enabled = enabled
	status.EnabledSource = source
	if status.Enabled && !status.SecretConfigured {
		status.ConfigurationOK = false
		status.Issue = "turnstile_secret_missing"
	}
	return status, nil
}

func logCaptchaConfig(ctx context.Context, logger *zap.Logger, settings gatewayPlatformSettings, secret string) {
	if logger == nil {
		return
	}
	status, err := captchaConfigurationStatus(ctx, settings, secret)
	fields := []zap.Field{}
	if err != nil {
		fields = append(fields, zap.Error(err))
	} else {
		fields = append(fields,
			zap.Bool("turnstile_secret_configured", status.SecretConfigured),
			zap.Bool("captcha_enabled", status.Enabled),
			zap.String("captcha_enabled_source", status.EnabledSource),
			zap.Bool("captcha_configuration_ok", status.ConfigurationOK),
			zap.String("captcha_configuration_issue", status.Issue),
		)
	}
	if err == nil && !status.ConfigurationOK {
		logger.Warn("captcha configuration requires operator action", fields...)
		return
	}
	logger.Info("captcha configuration loaded", fields...)
}

func captchaEnabledSetting(ctx context.Context, settings gatewayPlatformSettings) (bool, string, error) {
	if settings == nil {
		return false, "unconfigured", nil
	}
	setting, err := settings.Get(ctx, platformsettings.KeyCaptchaEnabled)
	if err != nil {
		return false, "", err
	}
	return strings.TrimSpace(setting.Value) == "true", strings.TrimSpace(setting.Source), nil
}

func loadUserRegistrationModeFromEnv() (userauth.RegistrationMode, error) {
	raw := strings.TrimSpace(os.Getenv("HUAKAI_USER_REGISTRATION_MODE"))
	if raw == "" {
		if releaseModeProduction() {
			return userauth.RegistrationModeDisabled, nil
		}
		return userauth.RegistrationModeOpen, nil
	}
	mode, err := userauth.ParseRegistrationMode(raw)
	if err != nil {
		return "", fmt.Errorf("HUAKAI_USER_REGISTRATION_MODE=%q 不是已知取值（open / invite_required / disabled / admin_only）；%w", raw, err)
	}
	return mode, nil
}

// loadSessionDevicePolicyFromEnv 读新设备策略两旋钮, 注入 usersession.Service:
//   - HUAKAI_SESSION_MAX_ACTIVE_DEVICES: 每用户活跃登录设备 (session family) 上限; 默认 0 = 策略休眠。
//   - HUAKAI_SESSION_DEVICE_POLICY: 达上限时的处置 ("" / revoke_oldest / confirm); 默认 "" = 拒绝超限设备。
//
// fail-loud: 非法的数字 / 负数 / 未知 policy 取值一律返回 error 拒启, 不静默回落到休眠 (否则运维以为
// 已开策略实则没开)。默认 (两个 env 都不设) 保持休眠 (max=0), 零生产行为变更。
func loadSessionDevicePolicyFromEnv() (maxActiveDevices int, devicePolicy string, err error) {
	rawMax := strings.TrimSpace(os.Getenv("HUAKAI_SESSION_MAX_ACTIVE_DEVICES"))
	if rawMax != "" {
		n, parseErr := strconv.Atoi(rawMax)
		if parseErr != nil || n < 0 {
			return 0, "", fmt.Errorf("HUAKAI_SESSION_MAX_ACTIVE_DEVICES=%q 必须是 >=0 的整数（0=设备策略休眠）", rawMax)
		}
		maxActiveDevices = n
	}
	devicePolicy = strings.TrimSpace(os.Getenv("HUAKAI_SESSION_DEVICE_POLICY"))
	switch devicePolicy {
	case "", "revoke_oldest", "confirm":
		// 合法取值: "" = 达上限直接拒 (ErrDeviceLimitExceeded); revoke_oldest = 撤最老腾位; confirm = 走确认流。
	default:
		return 0, "", fmt.Errorf("HUAKAI_SESSION_DEVICE_POLICY=%q 不是已知取值（空 / revoke_oldest / confirm）", devicePolicy)
	}
	return maxActiveDevices, devicePolicy, nil
}

// validateDevAuthTokenFlag 在启动时 fail-closed:HUAKAI_DEV_AUTH_RETURN_TOKEN=true 会让公开的
// 注册/密码重置接口把一次性明文 secret 直接回写进 JSON 响应体(addDevAuthToken),仅供本地/CI 调试。
// 生产环境(HUAKAI_RELEASE_MODE=production)绝不能开启,否则每次注册/重置都会泄露令牌。
// 该 dev 开关纳入与 audit/email/channelhealth 一致的启动门控,带病配置直接拒启,
// 而非此前仅打一条 warning 仍照常 boot 并泄露。
func validateDevAuthTokenFlag() error {
	if releaseModeProduction() && strings.EqualFold(strings.TrimSpace(os.Getenv("HUAKAI_DEV_AUTH_RETURN_TOKEN")), "true") {
		return fmt.Errorf("HUAKAI_DEV_AUTH_RETURN_TOKEN 不得在 production 下为 true（会在注册/重置响应中泄露明文一次性令牌）")
	}
	return nil
}

// auditKeySetupHint 在 production 审计私钥相关启动门拒启时附带打印,直接给出生成与设置命令,
// 避免运维卡在"知道缺私钥却不知怎么补"。这是降摩擦文案,不改任何启动门判定。
const auditKeySetupHint = "生成 ed25519 私钥:openssl genpkey -algorithm ed25519 -out secrets/audit_key.pem " +
	"(或跑 backend/scripts/gen-audit-key.sh 一键生成),再设 HUAKAI_AUDIT_PRIVATE_KEY_PATH 指向它;" +
	"容器部署经 volumes 把宿主私钥挂到该路径,详见 docs/deploy/production-bootstrap.md"

func requireProductionChannelHealthSigner(signer *sign.Signer) error {
	if !releaseModeProduction() {
		return nil
	}
	if signer == nil {
		return fmt.Errorf("production 模式要求 channelhealth audit signer:请设置 HUAKAI_AUDIT_PRIVATE_KEY_PATH。%s", auditKeySetupHint)
	}
	return nil
}

func loadCredentialKeyProvider() (credentialstore.KeyProvider, error) {
	keyID := strings.TrimSpace(os.Getenv("HUAKAI_CREDENTIAL_KEY_ID"))
	if keyID == "" {
		keyID = "local-v1"
	}
	raw := strings.TrimSpace(os.Getenv("HUAKAI_CREDENTIAL_KEY_B64"))
	if raw == "" {
		return nil, fmt.Errorf("%w: HUAKAI_CREDENTIAL_KEY_B64", credentialstore.ErrKeyUnavailable)
	}
	material, err := credentialstore.DecodeKeyMaterial(raw)
	if err != nil {
		return nil, err
	}
	return credentialstore.NewStaticKeyProvider(keyID, material)
}

func loadSessionSigningKey(logger *zap.Logger) ([]byte, error) {
	b64Names := []string{"HUAKAI_SESSION_SIGNING_KEY_B64", "HUAKAI_SESSION_HMAC_KEY_B64"}
	for _, name := range b64Names {
		raw := strings.TrimSpace(os.Getenv(name))
		if raw == "" {
			continue
		}
		for _, decode := range []func(string) ([]byte, error){
			base64.StdEncoding.DecodeString,
			base64.RawStdEncoding.DecodeString,
			base64.RawURLEncoding.DecodeString,
		} {
			key, err := decode(raw)
			if err == nil && len(key) >= 32 {
				return key, nil
			}
		}
		return nil, fmt.Errorf("%s must decode to at least 32 bytes", name)
	}
	if raw := strings.TrimSpace(os.Getenv("HUAKAI_SESSION_SIGNING_KEY_HEX")); raw != "" {
		key, err := hex.DecodeString(raw)
		if err != nil {
			return nil, fmt.Errorf("decode HUAKAI_SESSION_SIGNING_KEY_HEX: %w", err)
		}
		if len(key) < 32 {
			return nil, fmt.Errorf("HUAKAI_SESSION_SIGNING_KEY_HEX must decode to at least 32 bytes")
		}
		return key, nil
	}
	// 无任何显式配置(上面 B64/HEX 都未设)。production 仍强制 fail-loud——跨重启稳定的会话签名
	// 必须由运维显式提供持久 key。非生产模式(dev/development/test)则自动生成一把临时随机 key:
	// 仅省本地开发"没设 key 就起不来"的摩擦,不持久化(重启即换、旧会话失效,本地无所谓)。
	// 安全姿态:production 行为完全不变;此分支绝不在 production 触发。
	if releaseModeProduction() {
		return nil, fmt.Errorf("HUAKAI_SESSION_SIGNING_KEY_B64 or HUAKAI_SESSION_SIGNING_KEY_HEX is required")
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("生成临时 session 签名 key 失败: %w", err)
	}
	if logger != nil {
		logger.Warn("非生产模式未设 HUAKAI_SESSION_SIGNING_KEY:已自动生成临时 key(重启即换、会话失效);" +
			"如需跨重启稳定,显式设 HUAKAI_SESSION_SIGNING_KEY_B64")
	}
	return key, nil
}

func buildUserOAuthService(logger *zap.Logger) *userauth.OAuthService {
	configs := envOAuthConfigMap(logger)
	providers := make([]userauth.OAuthProvider, 0, len(configs))
	for _, cfg := range configs {
		if p := buildOAuthProvider(logger, cfg); p != nil {
			providers = append(providers, p)
		}
	}
	return userauth.NewOAuthService(providers...)
}

// envOAuthConfigMap 从环境变量构建各 OAuth provider 的配置模板(provider 名 → OAuthConfig,含默认 URL)。
// boot 期静态构建与请求期 settings-first 解析共用同一份 env 基线,避免默认 URL / 字段两处漂移。
// linuxdo 仅当 min-trust-level env 合法时纳入(与旧行为一致)。
func envOAuthConfigMap(logger *zap.Logger) map[string]userauth.OAuthConfig {
	configs := map[string]userauth.OAuthConfig{
		userauth.SocialProviderGoogle: {
			Provider:     userauth.SocialProviderGoogle,
			ClientID:     os.Getenv("HUAKAI_GOOGLE_OAUTH_CLIENT_ID"),
			ClientSecret: os.Getenv("HUAKAI_GOOGLE_OAUTH_CLIENT_SECRET"),
			RedirectURI:  os.Getenv("HUAKAI_GOOGLE_OAUTH_REDIRECT_URI"),
			AuthURL:      os.Getenv("HUAKAI_GOOGLE_OAUTH_AUTH_URL"),
			TokenURL:     envDefault("HUAKAI_GOOGLE_OAUTH_TOKEN_URL", "https://oauth2.googleapis.com/token"),
			JWKSURL:      envDefault("HUAKAI_GOOGLE_OAUTH_JWKS_URL", "https://www.googleapis.com/oauth2/v3/certs"),
			Issuer:       envDefault("HUAKAI_GOOGLE_OAUTH_ISSUER", "https://accounts.google.com"),
		},
		userauth.SocialProviderGitHub: {
			Provider:     userauth.SocialProviderGitHub,
			ClientID:     os.Getenv("HUAKAI_GITHUB_OAUTH_CLIENT_ID"),
			ClientSecret: os.Getenv("HUAKAI_GITHUB_OAUTH_CLIENT_SECRET"),
			RedirectURI:  os.Getenv("HUAKAI_GITHUB_OAUTH_REDIRECT_URI"),
			AuthURL:      os.Getenv("HUAKAI_GITHUB_OAUTH_AUTH_URL"),
			TokenURL:     envDefault("HUAKAI_GITHUB_OAUTH_TOKEN_URL", "https://github.com/login/oauth/access_token"),
			UserURL:      envDefault("HUAKAI_GITHUB_OAUTH_USER_URL", "https://api.github.com/user"),
			EmailsURL:    envDefault("HUAKAI_GITHUB_OAUTH_EMAILS_URL", "https://api.github.com/user/emails"),
		},
		userauth.SocialProviderQQ: {
			Provider:     userauth.SocialProviderQQ,
			ClientID:     os.Getenv("HUAKAI_QQ_OAUTH_CLIENT_ID"),
			ClientSecret: os.Getenv("HUAKAI_QQ_OAUTH_CLIENT_SECRET"),
			RedirectURI:  os.Getenv("HUAKAI_QQ_OAUTH_REDIRECT_URI"),
			AuthURL:      os.Getenv("HUAKAI_QQ_OAUTH_AUTH_URL"),
			TokenURL:     os.Getenv("HUAKAI_QQ_OAUTH_TOKEN_URL"),
			OpenIDURL:    os.Getenv("HUAKAI_QQ_OAUTH_OPENID_URL"),
			UserURL:      os.Getenv("HUAKAI_QQ_OAUTH_USER_URL"),
		},
		userauth.SocialProviderDingTalk: {
			Provider:     userauth.SocialProviderDingTalk,
			ClientID:     os.Getenv("HUAKAI_DINGTALK_OAUTH_CLIENT_ID"),
			ClientSecret: os.Getenv("HUAKAI_DINGTALK_OAUTH_CLIENT_SECRET"),
			RedirectURI:  os.Getenv("HUAKAI_DINGTALK_OAUTH_REDIRECT_URI"),
			AuthURL:      os.Getenv("HUAKAI_DINGTALK_OAUTH_AUTH_URL"),
			TokenURL:     os.Getenv("HUAKAI_DINGTALK_OAUTH_TOKEN_URL"),
			UserURL:      os.Getenv("HUAKAI_DINGTALK_OAUTH_USER_URL"),
		},
		userauth.SocialProviderNodeSeek: {
			Provider:           userauth.SocialProviderNodeSeek,
			ClientID:           os.Getenv("HUAKAI_NODESEEK_OAUTH_CLIENT_ID"),
			ClientSecret:       os.Getenv("HUAKAI_NODESEEK_OAUTH_CLIENT_SECRET"),
			RedirectURI:        os.Getenv("HUAKAI_NODESEEK_OAUTH_REDIRECT_URI"),
			AuthURL:            os.Getenv("HUAKAI_NODESEEK_OAUTH_AUTH_URL"),
			TokenURL:           os.Getenv("HUAKAI_NODESEEK_OAUTH_TOKEN_URL"),
			UserURL:            os.Getenv("HUAKAI_NODESEEK_OAUTH_USERINFO_URL"),
			SubjectField:       os.Getenv("HUAKAI_NODESEEK_OAUTH_SUBJECT_FIELD"),
			EmailField:         os.Getenv("HUAKAI_NODESEEK_OAUTH_EMAIL_FIELD"),
			EmailVerifiedField: os.Getenv("HUAKAI_NODESEEK_OAUTH_EMAIL_VERIFIED_FIELD"),
			DisplayNameField:   os.Getenv("HUAKAI_NODESEEK_OAUTH_DISPLAY_NAME_FIELD"),
			Scopes:             parseCSVAllowlistEnv("HUAKAI_NODESEEK_OAUTH_SCOPES"),
		},
		userauth.SocialProviderDiscord: {
			Provider:     userauth.SocialProviderDiscord,
			ClientID:     os.Getenv("HUAKAI_DISCORD_OAUTH_CLIENT_ID"),
			ClientSecret: os.Getenv("HUAKAI_DISCORD_OAUTH_CLIENT_SECRET"),
			RedirectURI:  os.Getenv("HUAKAI_DISCORD_OAUTH_REDIRECT_URI"),
			AuthURL:      os.Getenv("HUAKAI_DISCORD_OAUTH_AUTH_URL"),
			TokenURL:     os.Getenv("HUAKAI_DISCORD_OAUTH_TOKEN_URL"),
			UserURL:      os.Getenv("HUAKAI_DISCORD_OAUTH_USERINFO_URL"),
			Scopes:       parseCSVAllowlistEnv("HUAKAI_DISCORD_OAUTH_SCOPES"),
		},
	}
	if minTrustLevel, ok := linuxDoOAuthMinTrustLevel(logger); ok {
		configs[userauth.SocialProviderLinuxDo] = userauth.OAuthConfig{
			Provider:                 userauth.SocialProviderLinuxDo,
			ClientID:                 os.Getenv("HUAKAI_LINUXDO_OAUTH_CLIENT_ID"),
			ClientSecret:             os.Getenv("HUAKAI_LINUXDO_OAUTH_CLIENT_SECRET"),
			RedirectURI:              os.Getenv("HUAKAI_LINUXDO_OAUTH_REDIRECT_URI"),
			AuthURL:                  os.Getenv("HUAKAI_LINUXDO_OAUTH_AUTH_URL"),
			TokenURL:                 os.Getenv("HUAKAI_LINUXDO_OAUTH_TOKEN_URL"),
			UserURL:                  os.Getenv("HUAKAI_LINUXDO_OAUTH_USERINFO_URL"),
			SubjectField:             os.Getenv("HUAKAI_LINUXDO_OAUTH_SUBJECT_FIELD"),
			EmailField:               os.Getenv("HUAKAI_LINUXDO_OAUTH_EMAIL_FIELD"),
			EmailVerifiedField:       os.Getenv("HUAKAI_LINUXDO_OAUTH_EMAIL_VERIFIED_FIELD"),
			DisplayNameField:         os.Getenv("HUAKAI_LINUXDO_OAUTH_DISPLAY_NAME_FIELD"),
			MinimumNumericClaimField: envDefault("HUAKAI_LINUXDO_OAUTH_TRUST_LEVEL_FIELD", "trust_level"),
			MinimumNumericClaimValue: minTrustLevel,
			Scopes:                   parseCSVAllowlistEnv("HUAKAI_LINUXDO_OAUTH_SCOPES"),
		}
	}
	return configs
}

// envOAuthConfigFor 返回单个 provider 的 env 配置模板(供请求期 resolver 取基线)。
func envOAuthConfigFor(logger *zap.Logger, provider string) (userauth.OAuthConfig, bool) {
	cfg, ok := envOAuthConfigMap(logger)[strings.ToLower(strings.TrimSpace(provider))]
	return cfg, ok
}

func linuxDoOAuthMinTrustLevel(logger *zap.Logger) (int64, bool) {
	raw := strings.TrimSpace(os.Getenv("HUAKAI_LINUXDO_OAUTH_MIN_TRUST_LEVEL"))
	if raw == "" {
		return 1, true
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		if logger != nil {
			logger.Warn("user oauth provider disabled",
				zap.String("provider", userauth.SocialProviderLinuxDo),
				zap.String("reason", "min_trust_level_invalid"),
			)
		}
		return 0, false
	}
	return value, true
}

func buildOAuthProvider(logger *zap.Logger, cfg userauth.OAuthConfig) userauth.OAuthProvider {
	if strings.TrimSpace(cfg.ClientID) == "" {
		if logger != nil {
			logger.Info("user oauth provider disabled", zap.String("provider", cfg.Provider), zap.String("reason", "client_id_missing"))
		}
		return nil
	}
	if oauthProviderRequiresClientSecret(cfg.Provider) && strings.TrimSpace(cfg.ClientSecret) == "" {
		if logger != nil {
			logger.Info("user oauth provider disabled", zap.String("provider", cfg.Provider), zap.String("reason", "client_secret_missing"))
		}
		return nil
	}
	// 给 social OAuth 出站调用(token 兑换 / JWKS / GitHub user&emails)注入拨号期 SSRF 防护
	// 客户端,带私有/环回/元数据 IP 拦截 + Proxy=nil + 抑制 3xx,堵住凭据被发往内网/metadata/被 DNS-rebind。
	p, err := userauth.NewOAuthHTTPProvider(cfg, auth.NewSSRFProtectedOAuthClient(http.DefaultClient))
	if err != nil {
		if logger != nil {
			logger.Warn("user oauth provider disabled", zap.String("provider", cfg.Provider), zap.Error(err))
		}
		return nil
	}
	return p
}

func oauthProviderRequiresClientSecret(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case userauth.SocialProviderQQ, userauth.SocialProviderDingTalk, userauth.SocialProviderNodeSeek, userauth.SocialProviderLinuxDo, userauth.SocialProviderDiscord:
		return true
	default:
		return false
	}
}

func envDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

// buildAuditLedger 根据 HUAKAI_AUDIT_LEDGER_BACKEND 构建 audit ledger。
//   - memory (默认 / dev): 内存版，重启丢失，打 warn 日志
//   - postgres: PostgresLedger，持久化；production 模式强制使用此后端
func buildAuditLedger(_ context.Context, pgPool *pgxpool.Pool, signer *sign.Signer, logger *zap.Logger) (auditledger.Ledger, error) {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("HUAKAI_AUDIT_LEDGER_BACKEND")))
	isProd := releaseModeProduction()
	if isProd && backend != "postgres" {
		return nil, fmt.Errorf("production 模式要求 HUAKAI_AUDIT_LEDGER_BACKEND=postgres，当前值: %q", backend)
	}
	if backend == "postgres" {
		l, err := auditledger.NewPostgresLedger(pgPool, signer)
		if err != nil {
			return nil, fmt.Errorf("NewPostgresLedger: %w", err)
		}
		logger.Info("audit ledger backend: postgres（持久化）")
		return l, nil
	}
	// 默认 memory（dev 模式）
	logger.Warn("audit ledger backend: memory — 重启后 audit 链丢失，仅适用于开发环境")
	return auditledger.NewMemoryLedger(signer)
}

func loadAuditSigner(logger *zap.Logger) (*sign.Signer, error) {
	path := strings.TrimSpace(os.Getenv("HUAKAI_AUDIT_PRIVATE_KEY_PATH"))
	isProd := releaseModeProduction()
	if path == "" {
		if isProd {
			return nil, fmt.Errorf("production 模式要求持久审计私钥:请设置 HUAKAI_AUDIT_PRIVATE_KEY_PATH。%s", auditKeySetupHint)
		}
		logger.Warn("using ephemeral key — restart loses chain")
		return sign.GenerateKey()
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read HUAKAI_AUDIT_PRIVATE_KEY_PATH: %w", err)
	}
	signer, err := parseAuditPrivateKey(raw)
	if err != nil {
		return nil, err
	}
	logger.Info("audit private key loaded", zap.String("path", path), zap.String("fingerprint", signer.Fingerprint()))
	return signer, nil
}

func parseAuditPrivateKey(raw []byte) (*sign.Signer, error) {
	if len(raw) == ed25519.PrivateKeySize {
		return sign.NewSignerFromKey(ed25519.PrivateKey(raw))
	}
	trimmed := strings.TrimSpace(string(raw))
	if block, _ := pem.Decode([]byte(trimmed)); block != nil {
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse audit PEM private key: %w", err)
		}
		priv, ok := key.(ed25519.PrivateKey)
		if !ok {
			return nil, sign.ErrInvalidPrivateKey
		}
		return sign.NewSignerFromKey(priv)
	}
	for _, decode := range []func(string) ([]byte, error){
		base64.StdEncoding.DecodeString,
		base64.RawStdEncoding.DecodeString,
		hex.DecodeString,
	} {
		decoded, err := decode(trimmed)
		if err == nil && len(decoded) == ed25519.PrivateKeySize {
			return sign.NewSignerFromKey(ed25519.PrivateKey(decoded))
		}
	}
	return nil, sign.ErrInvalidPrivateKey
}
