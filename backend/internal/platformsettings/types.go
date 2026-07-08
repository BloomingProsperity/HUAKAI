package platformsettings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"
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
	// codex-cli 全局加固层的 7 个 SettingKey 常量在 codex_client_access.go 定义(§13 体量,同包)。
)

var (
	ErrUnknownKey          = errors.New("platformsettings: unknown setting key")
	ErrInvalidValue        = errors.New("platformsettings: invalid setting value")
	ErrStoreNotConfigured  = errors.New("platformsettings: store not configured")
	orderedSettingKeys     = []SettingKey{KeyRegistrationEnabled, KeyInvitationRequired, KeyPasswordRegisterEnabled, KeyPasswordLoginEnabled, KeyEmailDomainAllowlistEnabled, KeyEmailDomainAllowlist, KeyEmailAliasRestrictionEnabled, KeyReservedEmailLocalparts, KeyCaptchaEnabled, KeyTwoFactorEnabled, KeyCaptchaProvider, KeyCaptchaSiteKey, KeyCaptchaSecret, KeyOAuthProvidersEnabled, KeyOAuthProvidersConfig, KeyOAuthProvidersSecrets, KeyTelegramBotUsername, KeyTelegramBotToken, KeyPromoEnabled, KeyStreamTimeoutSeconds, KeyCooldown429Seconds, KeyCooldown529Seconds, KeyResponseHeaderDenyExtra, KeyResponseHeaderAllowOverride, KeyModelFallbackChains, KeyBudgetLimits, KeyPaymentProviderConfig, KeyCheckinEnabled, KeyCheckinMinCents, KeyCheckinMaxCents, KeyReferralRewardEnabled, KeyReferralRewardCents, KeyPasskeyEnabled, KeyPasskeyRegistrationEnabled, KeyPasskeyRPID, KeyPasskeyRPDisplayName, KeyPasskeyRPOrigins, KeyMediaTaskEnabled, KeyMediaTaskProviderBaseURL, KeyMediaTaskPollIntervalSecs, KeyMediaTaskTimeoutSecs, KeyMediaTaskDefaultEstimatedCents, KeyModerationExternalEnabled, KeyModerationExternalBaseURL, KeyModerationExternalAPIKeys, KeyModerationExternalModel, KeyModerationExternalThresholds, KeyModerationExternalTimeoutMS, KeyModerationExternalRetryCount, KeyModerationExternalImageEnabled, KeyWarmupInterceptEnabled, KeyCodexClientAccessBlacklist, KeyCodexClientAccessWhitelist, KeyCodexClientAccessMinVersion, KeyCodexClientAccessMaxVersion, KeyCodexClientAccessAllowAppServer, KeyCodexClientAccessEngineFingerprintSignals, KeyCodexClientAccessForceAllow, KeySiteName, KeySiteLogo, KeySiteFooter, KeySiteHomeContent, KeySiteSubtitle, KeySiteContactInfo, KeySiteDocURL, KeySiteAPIBaseURL, KeySiteFrontendBaseURL, KeyAdminNotificationEmail}
	defaultSettingValueMap = map[SettingKey]string{
		KeyRegistrationEnabled:            "false",
		KeyInvitationRequired:             "true",
		KeyPasswordRegisterEnabled:        "true",
		KeyPasswordLoginEnabled:           "true",
		KeyEmailDomainAllowlistEnabled:    "false",
		KeyEmailDomainAllowlist:           "",
		KeyEmailAliasRestrictionEnabled:   "false",
		KeyReservedEmailLocalparts:        "",
		KeyCaptchaEnabled:                 "false",
		KeyTwoFactorEnabled:               "true",
		KeyCaptchaProvider:                "",
		KeyCaptchaSiteKey:                 "",
		KeyCaptchaSecret:                  "",
		KeyOAuthProvidersEnabled:          "",
		KeyOAuthProvidersConfig:           "",
		KeyOAuthProvidersSecrets:          "",
		KeyTelegramBotUsername:            "",
		KeyTelegramBotToken:               "",
		KeyPromoEnabled:                   "true",
		KeyStreamTimeoutSeconds:           "120",
		KeyCooldown429Seconds:             "60",
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
		KeyMediaTaskDefaultEstimatedCents: `{"image_generation":100,"video_generation":1000}`,
		KeyModerationExternalEnabled:      "false",
		KeyModerationExternalBaseURL:      "",
		KeyModerationExternalAPIKeys:      "[]",
		KeyModerationExternalModel:        "omni-moderation-latest",
		KeyModerationExternalThresholds:   "{}",
		KeyModerationExternalTimeoutMS:    "3000",
		KeyModerationExternalRetryCount:   "2",
		KeyModerationExternalImageEnabled: "false",
		KeyWarmupInterceptEnabled:         "false",
		KeySiteName:                       "HUAKAI",
		KeySiteLogo:                       "",
		KeySiteFooter:                     "",
		KeySiteHomeContent:                "",
		KeySiteSubtitle:                   "",
		KeySiteContactInfo:                "",
		KeySiteDocURL:                     "",
		KeySiteAPIBaseURL:                 "",
		KeySiteFrontendBaseURL:            "",
		KeyAdminNotificationEmail:         "",
		// codex-cli 全局加固层默认:名单/信号空、版本无界、force 关;app-server 默认 false(放松旋钮,
		// 若默认 true 会放行任意客户端使 codex_cli_only 形同虚设;false 时空策略 = 仅官方客户端放行)。
		KeyCodexClientAccessBlacklist:                "[]",
		KeyCodexClientAccessWhitelist:                "[]",
		KeyCodexClientAccessMinVersion:               "",
		KeyCodexClientAccessMaxVersion:               "",
		KeyCodexClientAccessAllowAppServer:           "false",
		KeyCodexClientAccessEngineFingerprintSignals: "[]",
		KeyCodexClientAccessForceAllow:               "false",
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
	case KeyRegistrationEnabled, KeyInvitationRequired, KeyPasswordRegisterEnabled, KeyPasswordLoginEnabled, KeyEmailDomainAllowlistEnabled, KeyEmailAliasRestrictionEnabled, KeyCaptchaEnabled, KeyTwoFactorEnabled, KeyPromoEnabled, KeyCheckinEnabled, KeyReferralRewardEnabled, KeyPasskeyEnabled, KeyPasskeyRegistrationEnabled, KeyMediaTaskEnabled, KeyModerationExternalEnabled, KeyModerationExternalImageEnabled, KeyWarmupInterceptEnabled, KeyCodexClientAccessAllowAppServer, KeyCodexClientAccessForceAllow:
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

func validateCSVPublicTextValue(key SettingKey, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.ToLower(strings.TrimSpace(part))
		if key == KeyEmailDomainAllowlist {
			item = strings.TrimPrefix(item, "@")
		}
		if item == "" {
			return "", fmt.Errorf("%w: %s contains empty CSV value", ErrInvalidValue, key)
		}
		if _, err := validatePublicTextValue(key, item); err != nil {
			return "", err
		}
		out = append(out, item)
	}
	return strings.Join(out, ","), nil
}

func validateOptionalPublicTextValue(key SettingKey, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	return validatePublicTextValue(key, value)
}

// validateTelegramBotUsernameValue 校验公开的 Telegram bot 用户名(不是密钥,是 t.me/<name>
// 里那个公开名)。空值 = 关闭 Telegram 登录(安全默认)。为运营便利接受可选前导 "@" 并剥除。
// 非空时按 Telegram 真实命名约束硬化:仅 ASCII 字母/数字/下划线、长度 5–32、且必须以 "bot"
// 结尾(大小写不敏感,这是 Telegram bot 账户的强制规则)。这层硬化既挡配置笔误,又确保该值
// 后续被前端注入 data-telegram-login 属性时不可能携带引号/尖括号等可破坏 HTML 属性的字符
// (纵深防御 XSS)。借鉴项目 new-api 不做任何校验、原样存。
func validateTelegramBotUsernameValue(key SettingKey, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	value = strings.TrimPrefix(value, "@")
	if len(value) < 5 || len(value) > 32 {
		return "", fmt.Errorf("%w: %s 长度须为 5–32 个字符", ErrInvalidValue, key)
	}
	for _, r := range value {
		isLower := r >= 'a' && r <= 'z'
		isUpper := r >= 'A' && r <= 'Z'
		isDigit := r >= '0' && r <= '9'
		if !isLower && !isUpper && !isDigit && r != '_' {
			return "", fmt.Errorf("%w: %s 只允许字母、数字、下划线", ErrInvalidValue, key)
		}
	}
	if !strings.HasSuffix(strings.ToLower(value), "bot") {
		return "", fmt.Errorf("%w: %s 必须以 \"bot\" 结尾", ErrInvalidValue, key)
	}
	return value, nil
}

// validateOptionalEmailValue 接受空值（让每日巡检 worker 保持关闭的安全默认）
// 或单个语法上看似合理的邮箱地址。它拒绝控制字符、内部空白、多地址列表，以及
// 在恰好一个 "@" 两侧缺 local-part/host 的地址——这足以挡住格式错误的收件人进入
// SMTP header，又不必引入一个完整的 RFC 5322 解析器。
func validateOptionalEmailValue(key SettingKey, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if _, err := validatePublicTextValue(key, value); err != nil {
		return "", err
	}
	if strings.ContainsAny(value, " \t\r\n,;") {
		return "", fmt.Errorf("%w: %s must be a single address without whitespace", ErrInvalidValue, key)
	}
	at := strings.IndexByte(value, '@')
	if at <= 0 || at != strings.LastIndexByte(value, '@') || at == len(value)-1 {
		return "", fmt.Errorf("%w: %s must be a valid email address", ErrInvalidValue, key)
	}
	if !strings.Contains(value[at+1:], ".") {
		return "", fmt.Errorf("%w: %s host must contain a dot", ErrInvalidValue, key)
	}
	return value, nil
}

func validateStringArrayValue(key SettingKey, value string) (string, error) {
	if value == "" {
		return "[]", nil
	}
	var items []string
	if err := json.Unmarshal([]byte(value), &items); err != nil {
		return "", fmt.Errorf("%w: %s must be a JSON string array", ErrInvalidValue, key)
	}
	if len(items) > 20 {
		return "", fmt.Errorf("%w: %s allows at most 20 values", ErrInvalidValue, key)
	}
	for _, item := range items {
		if strings.TrimSpace(item) == "" {
			return "", fmt.Errorf("%w: %s contains empty value", ErrInvalidValue, key)
		}
		if _, err := validatePublicTextValue(key, item); err != nil {
			return "", err
		}
	}
	return value, nil
}

func validateBoolValue(key SettingKey, value string) (string, error) {
	switch value {
	case "true", "false":
		return value, nil
	default:
		return "", fmt.Errorf("%w: %s must be true or false", ErrInvalidValue, key)
	}
}

func validatePositiveIntValue(key SettingKey, value string) (string, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return "", fmt.Errorf("%w: %s must be positive integer seconds", ErrInvalidValue, key)
	}
	return strconv.Itoa(parsed), nil
}

func validateNonNegativeIntValue(key SettingKey, value string) (string, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return "", fmt.Errorf("%w: %s must be non-negative integer cents", ErrInvalidValue, key)
	}
	return strconv.Itoa(parsed), nil
}

func validateBoundedNonNegativeIntValue(key SettingKey, value string, max int, unit string) (string, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 || parsed > max {
		return "", fmt.Errorf("%w: %s must be non-negative integer %s no greater than %d", ErrInvalidValue, key, unit, max)
	}
	return strconv.Itoa(parsed), nil
}

func validateCaptchaProvider(value string) (string, error) {
	switch value {
	case "turnstile", "recaptcha", "hcaptcha":
		return value, nil
	default:
		return "", fmt.Errorf("%w: captcha_provider", ErrInvalidValue)
	}
}

func validatePublicTextValue(key SettingKey, value string) (string, error) {
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%w: %s contains control character", ErrInvalidValue, key)
		}
	}
	return value, nil
}

func validateHeaderListValue(key SettingKey, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	parts := strings.Split(value, ",")
	if len(parts) > 20 {
		return "", fmt.Errorf("%w: %s allows at most 20 header patterns", ErrInvalidValue, key)
	}
	for _, part := range parts {
		if part == "" {
			return "", fmt.Errorf("%w: %s contains empty header pattern", ErrInvalidValue, key)
		}
		if len(part) > 64 {
			return "", fmt.Errorf("%w: %s header pattern too long", ErrInvalidValue, key)
		}
		if !validHeaderPattern(part) {
			return "", fmt.Errorf("%w: %s contains invalid header pattern", ErrInvalidValue, key)
		}
	}
	return strings.Join(parts, ","), nil
}

func validHeaderPattern(value string) bool {
	for _, r := range value {
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

func validateJSONObjectValue(key SettingKey, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	var probe map[string]any
	if err := json.Unmarshal([]byte(value), &probe); err != nil {
		return "", fmt.Errorf("%w: %s must be a JSON object", ErrInvalidValue, key)
	}
	if probe == nil {
		return "", fmt.Errorf("%w: %s must be a JSON object", ErrInvalidValue, key)
	}
	return value, nil
}

func validateModerationExternalThresholdsValue(key SettingKey, value string) (string, error) {
	if value == "" {
		return "{}", nil
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(value)))
	dec.UseNumber()
	var doc map[string]json.Number
	if err := dec.Decode(&doc); err != nil || doc == nil {
		return "", fmt.Errorf("%w: %s must be a JSON object of category thresholds", ErrInvalidValue, key)
	}
	normalized := make(map[string]float64, len(doc))
	for category, raw := range doc {
		category = strings.TrimSpace(category)
		if category == "" {
			return "", fmt.Errorf("%w: %s contains empty category", ErrInvalidValue, key)
		}
		threshold, err := strconv.ParseFloat(raw.String(), 64)
		if err != nil || threshold < 0 || threshold > 1 {
			return "", fmt.Errorf("%w: %s contains threshold outside [0,1]", ErrInvalidValue, key)
		}
		normalized[category] = threshold
	}
	out, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func validatePaymentProviderConfigValue(key SettingKey, value string) (string, error) {
	type providerConfig struct {
		Enabled     *bool  `json:"enabled"`
		CheckoutURL string `json:"checkout_url"`
	}
	var doc struct {
		Manual *providerConfig `json:"manual"`
		Taobao *providerConfig `json:"taobao"`
	}
	if err := json.Unmarshal([]byte(value), &doc); err != nil {
		return "", fmt.Errorf("%w: %s must be a JSON object", ErrInvalidValue, key)
	}
	if doc.Manual == nil || doc.Taobao == nil || doc.Manual.Enabled == nil || doc.Taobao.Enabled == nil {
		return "", fmt.Errorf("%w: %s requires manual and taobao enabled flags", ErrInvalidValue, key)
	}
	if strings.TrimSpace(doc.Manual.CheckoutURL) != "" {
		return "", fmt.Errorf("%w: manual checkout_url must be empty", ErrInvalidValue)
	}
	if *doc.Taobao.Enabled && strings.TrimSpace(doc.Taobao.CheckoutURL) == "" {
		return "", fmt.Errorf("%w: enabled taobao requires checkout_url", ErrInvalidValue)
	}
	return value, nil
}

func validateOptionalHTTPURLValue(key SettingKey, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if _, err := validatePublicTextValue(key, value); err != nil {
		return "", err
	}
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("%w: %s must be http(s) URL", ErrInvalidValue, key)
	}
	return value, nil
}

func validateMediaTaskDefaultEstimatedCentsValue(key SettingKey, value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("%w: %s must be a JSON object", ErrInvalidValue, key)
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(value)))
	dec.UseNumber()
	var doc map[string]json.Number
	if err := dec.Decode(&doc); err != nil || doc == nil {
		return "", fmt.Errorf("%w: %s must be a JSON object of non-negative integer cents", ErrInvalidValue, key)
	}
	if len(doc) == 0 {
		return "", fmt.Errorf("%w: %s must not be empty", ErrInvalidValue, key)
	}
	normalized := make(map[string]int64, len(doc))
	for taskType, raw := range doc {
		taskType = strings.TrimSpace(taskType)
		if taskType == "" {
			return "", fmt.Errorf("%w: %s contains empty task type", ErrInvalidValue, key)
		}
		cents, err := strconv.ParseInt(raw.String(), 10, 64)
		if err != nil || cents < 0 {
			return "", fmt.Errorf("%w: %s contains invalid cents", ErrInvalidValue, key)
		}
		normalized[taskType] = cents
	}
	out, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
