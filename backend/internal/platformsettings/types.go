package platformsettings

import (
	"errors"
	"fmt"
	"strings"
)

const (
	GlobalScope   = "global"
	SourceDefault = "default"
	SourceDB      = "db"
)

type SettingKey string

const (
	KeyRegistrationEnabled          SettingKey = "registration_enabled"
	KeyInvitationRequired           SettingKey = "invitation_required"
	KeyPasswordRegisterEnabled      SettingKey = "password_register_enabled"
	KeyPasswordLoginEnabled         SettingKey = "password_login_enabled"
	KeyEmailDomainAllowlistEnabled  SettingKey = "email_domain_allowlist_enabled"
	KeyEmailDomainAllowlist         SettingKey = "email_domain_allowlist"
	KeyEmailAliasRestrictionEnabled SettingKey = "email_alias_restriction_enabled"
	KeyReservedEmailLocalparts      SettingKey = "reserved_email_localparts"
	KeyCaptchaEnabled               SettingKey = "captcha_enabled"
	KeyTwoFactorEnabled             SettingKey = "two_factor_enabled"
	KeyCaptchaProvider              SettingKey = "captcha_provider"
	KeyCaptchaSiteKey               SettingKey = "captcha_site_key"
	// KeyCaptchaSecret 是人机验证提供方(Turnstile/reCAPTCHA/hCaptcha)的**服务端** secret,
	// 校验 token 时用。secret key,at-rest 加密、写后不回显;网关登录/注册端点请求期读它做
	// 校验,空则回退 env HUAKAI_CAPTCHA_TURNSTILE_SECRET(back-compat)。与 captcha_site_key
	//(公开、前端渲染用)相对——site key 公开、secret 保密。
	KeyCaptchaSecret         SettingKey = "captcha_secret"
	KeyOAuthProvidersEnabled SettingKey = "oauth_providers_enabled"
	// KeyOAuthProvidersConfig 是第三方登录(OAuth)各 provider 的**非密钥**配置(公开可读):
	// JSON 对象 {"github":{"client_id":"...","redirect_uri":"...","auth_url":"...",...},"google":{...}}。
	// 网关登录/回调端点请求期读它,按 provider 逐字段覆盖 env 基线(settings-first);未配置的
	// provider 回退 boot 期 env 静态 provider。client_secret 不在此(见下面的 secrets 密钥)。
	KeyOAuthProvidersConfig SettingKey = "oauth_providers_config"
	// KeyOAuthProvidersSecrets 是第三方登录各 provider 的 **client_secret** 集合(密钥):
	// JSON 对象 {"github":"secret1","google":"secret2"}。secret key,at-rest 加密、写后不回显。
	// 与上面公开 config 分离,使 config 可读、secret 保密。
	KeyOAuthProvidersSecrets SettingKey = "oauth_providers_secrets"
	// KeyTelegramBotUsername 是 Telegram Login Widget 渲染所需的**公开** bot 用户名
	//(即 t.me/<name> 里那个名,绝非密钥;密钥是下面的 KeyTelegramBotToken)。
	// 空值 = 关闭 Telegram 登录入口(配合 oauth_providers_enabled 含 telegram 才渲染按钮)。
	KeyTelegramBotUsername SettingKey = "telegram_bot_username"
	// KeyTelegramBotToken 是 Telegram Login Widget HMAC 校验用的 bot token(密钥)。secret key,
	// at-rest 加密、写后不回显。配置后 telegram 登录/绑定端点读它做校验;空则回退 env
	// HUAKAI_TELEGRAM_LOGIN_BOT_TOKEN(back-compat)。
	KeyTelegramBotToken               SettingKey = "telegram_bot_token"
	KeyPromoEnabled                   SettingKey = "promo_enabled"
	KeyStreamTimeoutSeconds           SettingKey = "stream_timeout_seconds"
	KeyCooldown429Seconds             SettingKey = "cooldown_429_seconds"
	KeyCooldown529Seconds             SettingKey = "cooldown_529_seconds"
	KeyResponseHeaderDenyExtra        SettingKey = "response_header_deny_extra"
	KeyResponseHeaderAllowOverride    SettingKey = "response_header_allow_override"
	KeyModelFallbackChains            SettingKey = "model_fallback_chains"
	KeyBudgetLimits                   SettingKey = "budget_limits"
	KeyPaymentProviderConfig          SettingKey = "payment_provider_config"
	KeyCheckinEnabled                 SettingKey = "checkin_enabled"
	KeyCheckinMinCents                SettingKey = "checkin_min_cents"
	KeyCheckinMaxCents                SettingKey = "checkin_max_cents"
	KeyReferralRewardEnabled          SettingKey = "referral_reward_enabled"
	KeyReferralRewardCents            SettingKey = "referral_reward_cents"
	KeyPasskeyEnabled                 SettingKey = "passkey_enabled"
	KeyPasskeyRegistrationEnabled     SettingKey = "passkey_registration_enabled"
	KeyPasskeyRPID                    SettingKey = "passkey_rp_id"
	KeyPasskeyRPDisplayName           SettingKey = "passkey_rp_display_name"
	KeyPasskeyRPOrigins               SettingKey = "passkey_rp_origins"
	KeyMediaTaskEnabled               SettingKey = "mediatask_enabled"
	KeyMediaTaskProviderBaseURL       SettingKey = "mediatask_provider_base_url"
	KeyMediaTaskPollIntervalSecs      SettingKey = "mediatask_poll_interval_seconds"
	KeyMediaTaskTimeoutSecs           SettingKey = "mediatask_task_timeout_seconds"
	KeyMediaTaskDefaultEstimatedCents SettingKey = "mediatask_default_estimated_cents"
	KeyModerationExternalEnabled      SettingKey = "moderation_external_enabled"
	KeyModerationExternalBaseURL      SettingKey = "moderation_external_base_url"
	KeyModerationExternalAPIKeys      SettingKey = "moderation_external_api_keys"
	KeyModerationExternalModel        SettingKey = "moderation_external_model"
	KeyModerationExternalThresholds   SettingKey = "moderation_external_thresholds"
	KeyModerationExternalTimeoutMS    SettingKey = "moderation_external_timeout_ms"
	KeyModerationExternalRetryCount   SettingKey = "moderation_external_retry_count"
	KeyModerationExternalImageEnabled SettingKey = "moderation_external_image_enabled"
	KeyWarmupInterceptEnabled         SettingKey = "warmup_intercept_enabled"
	KeySiteName                       SettingKey = "site_name"
	KeySiteLogo                       SettingKey = "site_logo"
	KeySiteFooter                     SettingKey = "site_footer"
	KeySiteHomeContent                SettingKey = "site_home_content"
	// KeySiteSubtitle 是显示在站点名下方的一句公开短标语。
	KeySiteSubtitle SettingKey = "site_subtitle"
	// KeySiteContactInfo 是展示给用户的公开运营者联系方式字符串（邮箱、IM 账号
	// 或自由文本）；非机密、纯展示文本。
	KeySiteContactInfo SettingKey = "site_contact_info"
	// KeySiteDocURL 是 UI 中露出的公开文档链接。
	KeySiteDocURL SettingKey = "site_doc_url"
	// KeySiteAPIBaseURL 是展示给用户用于客户端导入/配置的公开网关 base URL
	// （例如客户端要指向的 OpenAI 兼容 base）。它是一个公开 endpoint 地址，
	// 绝非机密。
	KeySiteAPIBaseURL SettingKey = "site_api_base_url"
	// KeySiteFrontendBaseURL 是本站前端的公开 base URL(如 https://huakai.example),供后端拼接
	// 邮件里的完整可点链接(验证/重置/新设备确认)。空 = 不拼链接,邮件回退到投递裸 token
	//(前端落地页的手动粘贴框兜底)。非机密,是站点自己的地址。
	KeySiteFrontendBaseURL SettingKey = "site_frontend_base_url"
	// KeyAdminNotificationEmail 是每日运维巡检报告寄送到的运营者地址。默认为空：
	// 未配置的部署解析不出收件人，巡检 worker 保持关闭（fail-safe）。worker 层
	// 还另有一处 env 回退。
	KeyAdminNotificationEmail SettingKey = "admin_notification_email"
	// 自动上架管道(part 2)。AutoListingEnabled 是自动挡总闸(Owner 2026-07-23 拍板默认开);
	// AutoListingAutoVendors 是"默认走自动挡"的 vendor 白名单(JSON string 数组),不在名单内的
	// vendor 走人工审批挡。仅总闸开启时生效。
	KeyAutoListingEnabled     SettingKey = "auto_listing_enabled"
	KeyAutoListingAutoVendors SettingKey = "auto_listing_auto_vendors"
	// codex-cli 全局加固层的 7 个 SettingKey 常量在 codex_client_access.go 定义(§13 体量,同包)。
)

var (
	ErrUnknownKey          = errors.New("platformsettings: unknown setting key")
	ErrInvalidValue        = errors.New("platformsettings: invalid setting value")
	ErrStoreNotConfigured  = errors.New("platformsettings: store not configured")
	orderedSettingKeys     = []SettingKey{KeyRegistrationEnabled, KeyInvitationRequired, KeyPasswordRegisterEnabled, KeyPasswordLoginEnabled, KeyEmailDomainAllowlistEnabled, KeyEmailDomainAllowlist, KeyEmailAliasRestrictionEnabled, KeyReservedEmailLocalparts, KeyCaptchaEnabled, KeyTwoFactorEnabled, KeyCaptchaProvider, KeyCaptchaSiteKey, KeyCaptchaSecret, KeyOAuthProvidersEnabled, KeyOAuthProvidersConfig, KeyOAuthProvidersSecrets, KeyTelegramBotUsername, KeyTelegramBotToken, KeyPromoEnabled, KeyStreamTimeoutSeconds, KeyCooldown429Seconds, KeyCooldown529Seconds, KeyResponseHeaderDenyExtra, KeyResponseHeaderAllowOverride, KeyModelFallbackChains, KeyBudgetLimits, KeyPaymentProviderConfig, KeyCheckinEnabled, KeyCheckinMinCents, KeyCheckinMaxCents, KeyReferralRewardEnabled, KeyReferralRewardCents, KeyPasskeyEnabled, KeyPasskeyRegistrationEnabled, KeyPasskeyRPID, KeyPasskeyRPDisplayName, KeyPasskeyRPOrigins, KeyMediaTaskEnabled, KeyMediaTaskProviderBaseURL, KeyMediaTaskPollIntervalSecs, KeyMediaTaskTimeoutSecs, KeyMediaTaskDefaultEstimatedCents, KeyModerationExternalEnabled, KeyModerationExternalBaseURL, KeyModerationExternalAPIKeys, KeyModerationExternalModel, KeyModerationExternalThresholds, KeyModerationExternalTimeoutMS, KeyModerationExternalRetryCount, KeyModerationExternalImageEnabled, KeyWarmupInterceptEnabled, KeyQuotaProbeEnabled, KeyQuotaProbeIntervalMinutes, KeyCacheAnthropicTTL1hRewrite, KeyCodexClientAccessBlacklist, KeyCodexClientAccessWhitelist, KeyCodexClientAccessMinVersion, KeyCodexClientAccessMaxVersion, KeyCodexClientAccessAllowAppServer, KeyCodexClientAccessEngineFingerprintSignals, KeyCodexClientAccessForceAllow, KeySiteName, KeySiteLogo, KeySiteFooter, KeySiteHomeContent, KeySiteSubtitle, KeySiteContactInfo, KeySiteDocURL, KeySiteAPIBaseURL, KeySiteFrontendBaseURL, KeyAdminNotificationEmail, KeyAutoListingEnabled, KeyAutoListingAutoVendors}
	defaultSettingValueMap = map[SettingKey]string{
		KeyRegistrationEnabled:          "false",
		KeyInvitationRequired:           "true",
		KeyPasswordRegisterEnabled:      "true",
		KeyPasswordLoginEnabled:         "true",
		KeyEmailDomainAllowlistEnabled:  "false",
		KeyEmailDomainAllowlist:         "",
		KeyEmailAliasRestrictionEnabled: "false",
		KeyReservedEmailLocalparts:      "",
		KeyCaptchaEnabled:               "false",
		KeyTwoFactorEnabled:             "true",
		KeyCaptchaProvider:              "",
		KeyCaptchaSiteKey:               "",
		KeyCaptchaSecret:                "",
		KeyOAuthProvidersEnabled:        "",
		KeyOAuthProvidersConfig:         "",
		KeyOAuthProvidersSecrets:        "",
		KeyTelegramBotUsername:          "",
		KeyTelegramBotToken:             "",
		KeyPromoEnabled:                 "true",
		// 现实默认来自 cmd/gateway.defaultGatewayTotalStreamTimeout（600 秒）。
		KeyStreamTimeoutSeconds: "600",
		// 现实默认来自 channelhealth.DefaultPolicy().DefaultRateLimitCooldown（5 分钟）。
		KeyCooldown429Seconds: "300",
		// 现实默认来自 channelhealth.DefaultPolicy().Upstream5xxCooldown 与 rate.defaultUpstreamCooldown（均为 5 分钟）。
		KeyCooldown529Seconds:             "300",
		KeyResponseHeaderDenyExtra:        "",
		KeyResponseHeaderAllowOverride:    "",
		KeyModelFallbackChains:            "",
		KeyBudgetLimits:                   "",
		KeyPaymentProviderConfig:          `{"manual":{"enabled":true,"checkout_url":""},"taobao":{"enabled":false,"checkout_url":""}}`,
		KeyCheckinEnabled:                 "false",
		KeyCheckinMinCents:                "1",
		KeyCheckinMaxCents:                "20",
		KeyReferralRewardEnabled:          "false",
		KeyReferralRewardCents:            "50",
		KeyPasskeyEnabled:                 "false",
		KeyPasskeyRegistrationEnabled:     "false",
		KeyPasskeyRPID:                    "",
		KeyPasskeyRPDisplayName:           "HUAKAI",
		KeyPasskeyRPOrigins:               "[]",
		KeyMediaTaskEnabled:               "false",
		KeyMediaTaskProviderBaseURL:       "",
		KeyMediaTaskPollIntervalSecs:      "5",
		KeyMediaTaskTimeoutSecs:           "900",
		KeyMediaTaskDefaultEstimatedCents: `{"image_generation":100,"music_generation":300,"video_generation":1000}`,
		KeyModerationExternalEnabled:      "false",
		KeyModerationExternalBaseURL:      "",
		KeyModerationExternalAPIKeys:      "[]",
		KeyModerationExternalModel:        "omni-moderation-latest",
		KeyModerationExternalThresholds:   "{}",
		KeyModerationExternalTimeoutMS:    "3000",
		KeyModerationExternalRetryCount:   "2",
		KeyModerationExternalImageEnabled: "false",
		KeyWarmupInterceptEnabled:         "false",
		KeyQuotaProbeEnabled:              "true",
		KeyQuotaProbeIntervalMinutes:      "30",
		KeyCacheAnthropicTTL1hRewrite:     "false",
		KeySiteName:                       "HUAKAI",
		KeySiteLogo:                       "",
		KeySiteFooter:                     "",
		KeySiteHomeContent:                "",
		KeySiteSubtitle:                   "",
		KeySiteContactInfo:                "",
		KeySiteDocURL:                     "",
		KeySiteAPIBaseURL:                 "",
		KeySiteFrontendBaseURL:            "",
		// 收件人现实默认为空；AlertRule.NotifyEmail 的 Go 布尔零值为 false，未配置时不产生告警邮件。
		KeyAdminNotificationEmail: "",
		// codex-cli 全局加固层默认：名单与信号为空、版本无界，force 和 app-server 均关闭。
		KeyCodexClientAccessBlacklist:                "[]",
		KeyCodexClientAccessWhitelist:                "[]",
		KeyCodexClientAccessMinVersion:               "",
		KeyCodexClientAccessMaxVersion:               "",
		KeyCodexClientAccessAllowAppServer:           "false",
		KeyCodexClientAccessEngineFingerprintSignals: "[]",
		KeyCodexClientAccessForceAllow:               "false",
		// 自动上架总闸默认开(Owner 2026-07-23 拍板):官方号(定价可信)自动上架 + 保鲜,
		// 反转号(grok/kimi/antigravity)默认走人工审批挡。auto-vendor 白名单即"走自动挡"的
		// vendor,仅总闸开启时生效;运营可随时改这两个键调挡。
		KeyAutoListingEnabled:     "true",
		KeyAutoListingAutoVendors: `["openai","anthropic","gemini"]`,
	}
)

func AllKeys() []SettingKey {
	return append([]SettingKey(nil), orderedSettingKeys...)
}

func DefaultValue(key SettingKey) (string, bool) {
	value, ok := defaultSettingValueMap[key]
	return value, ok
}

func IsAllowedKey(key SettingKey) bool {
	_, ok := defaultSettingValueMap[key]
	return ok
}

func ParseKey(raw string) (SettingKey, error) {
	key := SettingKey(strings.TrimSpace(raw))
	if !IsAllowedKey(key) {
		return "", fmt.Errorf("%w: %s", ErrUnknownKey, raw)
	}
	return key, nil
}

func ValidateValue(key SettingKey, raw string) (string, error) {
	if !IsAllowedKey(key) {
		return "", fmt.Errorf("%w: %s", ErrUnknownKey, key)
	}
	value := strings.TrimSpace(raw)
	// codex-cli 全局加固层键在 value=="" 守卫之前处理:空 JSON 名单/信号归一为 "[]",空版本合法。
	if key == KeyCodexClientAccessBlacklist {
		return normalizeCodexClientAccessBlacklistValue(key, value)
	}
	if key == KeyCodexClientAccessWhitelist {
		return normalizeCodexClientAccessWhitelistValue(key, value)
	}
	if key == KeyCodexClientAccessEngineFingerprintSignals {
		return normalizeCodexClientAccessEngineFingerprintSignalsValue(key, value)
	}
	if key == KeyCodexClientAccessMinVersion || key == KeyCodexClientAccessMaxVersion {
		return validateCodexClientAccessVersionValue(key, value)
	}
	if key == KeyResponseHeaderDenyExtra || key == KeyResponseHeaderAllowOverride {
		return validateHeaderListValue(key, value)
	}
	if key == KeyPaymentProviderConfig {
		return validatePaymentProviderConfigValue(key, value)
	}
	if key == KeyPasskeyRPOrigins || key == KeyModerationExternalAPIKeys {
		return validateStringArrayValue(key, value)
	}
	if key == KeyPasskeyRPID || key == KeySiteSubtitle || key == KeySiteContactInfo || key == KeyTelegramBotToken || key == KeyCaptchaSecret {
		return validateOptionalPublicTextValue(key, value)
	}
	if key == KeyTelegramBotUsername {
		return validateTelegramBotUsernameValue(key, value)
	}
	if key == KeyMediaTaskProviderBaseURL || key == KeyModerationExternalBaseURL ||
		key == KeySiteDocURL || key == KeySiteAPIBaseURL || key == KeySiteFrontendBaseURL {
		return validateOptionalHTTPURLValue(key, value)
	}
	if key == KeyModerationExternalThresholds {
		return validateModerationExternalThresholdsValue(key, value)
	}
	if key == KeyModerationExternalTimeoutMS {
		return validateBoundedNonNegativeIntValue(key, value, 30000, "milliseconds")
	}
	if key == KeyModerationExternalRetryCount {
		return validateBoundedNonNegativeIntValue(key, value, 5, "retries")
	}
	if key == KeyMediaTaskDefaultEstimatedCents {
		return validateMediaTaskDefaultEstimatedCentsValue(key, value)
	}
	if key == KeyQuotaProbeIntervalMinutes {
		return validateQuotaProbeIntervalMinutes(key, value)
	}
	if key == KeyModelFallbackChains {
		return validateModelFallbackChainsValue(key, value)
	}
	if key == KeyBudgetLimits {
		return validateJSONObjectValue(key, value)
	}
	if key == KeyEmailDomainAllowlist || key == KeyReservedEmailLocalparts {
		return validateCSVPublicTextValue(key, value)
	}
	if key == KeyAdminNotificationEmail {
		return validateOptionalEmailValue(key, value)
	}
	if key == KeyAutoListingAutoVendors {
		return validateAutoListingVendorsValue(key, value)
	}
	if key == KeyOAuthProvidersConfig {
		return validateOAuthProvidersConfigValue(key, value)
	}
	if key == KeyOAuthProvidersSecrets {
		return validateOAuthProvidersSecretsValue(key, value)
	}
	if value == "" {
		return "", fmt.Errorf("%w: %s", ErrInvalidValue, key)
	}
	switch key {
	case KeyRegistrationEnabled, KeyInvitationRequired, KeyPasswordRegisterEnabled, KeyPasswordLoginEnabled, KeyEmailDomainAllowlistEnabled, KeyEmailAliasRestrictionEnabled, KeyCaptchaEnabled, KeyTwoFactorEnabled, KeyPromoEnabled, KeyCheckinEnabled, KeyReferralRewardEnabled, KeyPasskeyEnabled, KeyPasskeyRegistrationEnabled, KeyMediaTaskEnabled, KeyModerationExternalEnabled, KeyModerationExternalImageEnabled, KeyWarmupInterceptEnabled, KeyQuotaProbeEnabled, KeyCacheAnthropicTTL1hRewrite, KeyCodexClientAccessAllowAppServer, KeyCodexClientAccessForceAllow, KeyAutoListingEnabled:
		return validateBoolValue(key, value)
	case KeyStreamTimeoutSeconds, KeyCooldown429Seconds, KeyCooldown529Seconds, KeyCheckinMinCents, KeyCheckinMaxCents, KeyMediaTaskPollIntervalSecs, KeyMediaTaskTimeoutSecs:
		return validatePositiveIntValue(key, value)
	case KeyReferralRewardCents:
		return validateNonNegativeIntValue(key, value)
	case KeyCaptchaProvider:
		return validateCaptchaProvider(value)
	default:
		return validatePublicTextValue(key, value)
	}
}
