package platformsettings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// 各设置键的值校验器。键注册与分发(ValidateValue)在 types.go,具体校验规则集中在本文件。

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
// (纵深防御 XSS)。
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
