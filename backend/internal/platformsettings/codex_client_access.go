package platformsettings

import (
	"fmt"

	"github.com/BloomingProsperity/HUAKAI/internal/codexclientaccess"
)

// codex-cli 全局加固层(片2f)的 SettingKey 常量:在片2e 每账号 codex_cli_only 开关之上的全局
// 准入策略。全部默认空/放行,运维显式配置后才收紧;匹配逻辑在后续片。
const (
	KeyCodexClientAccessBlacklist                SettingKey = "codex_client_access.blacklist"
	KeyCodexClientAccessWhitelist                SettingKey = "codex_client_access.whitelist"
	KeyCodexClientAccessMinVersion               SettingKey = "codex_client_access.min_version"
	KeyCodexClientAccessMaxVersion               SettingKey = "codex_client_access.max_version"
	KeyCodexClientAccessAllowAppServer           SettingKey = "codex_client_access.allow_app_server"
	KeyCodexClientAccessEngineFingerprintSignals SettingKey = "codex_client_access.engine_fingerprint_signals"
	KeyCodexClientAccessForceAllow               SettingKey = "codex_client_access.force_allow"
)

// codex-cli 全局加固层 settings 键的值校验。校验器解析 JSON/版本形状,并对空值归一,
// 使空配置 = 放行(默认全开)。匹配逻辑在后续片,本片只把配置形状挡在写入边界。

func normalizeCodexClientAccessEntriesValue(key SettingKey, value string, validate func([]codexclientaccess.AllowedClientEntry) error) (string, error) {
	entries, err := codexclientaccess.ParseAllowedClientEntries(value)
	if err != nil {
		return "", fmt.Errorf("%w: %s must be a JSON array: %v", ErrInvalidValue, key, err)
	}
	if err := validate(entries); err != nil {
		return "", fmt.Errorf("%w: %s %v", ErrInvalidValue, key, err)
	}
	if value == "" {
		return "[]", nil
	}
	return value, nil
}

func normalizeCodexClientAccessBlacklistValue(key SettingKey, value string) (string, error) {
	return normalizeCodexClientAccessEntriesValue(key, value, codexclientaccess.ValidateBlacklistEntries)
}

func normalizeCodexClientAccessWhitelistValue(key SettingKey, value string) (string, error) {
	return normalizeCodexClientAccessEntriesValue(key, value, codexclientaccess.ValidateWhitelistEntries)
}

func normalizeCodexClientAccessEngineFingerprintSignalsValue(key SettingKey, value string) (string, error) {
	if _, err := codexclientaccess.ParseEngineFingerprintSignals(value); err != nil {
		return "", fmt.Errorf("%w: %s must be a valid engine fingerprint signal array: %v", ErrInvalidValue, key, err)
	}
	if value == "" {
		return "[]", nil
	}
	return value, nil
}

func validateCodexClientAccessVersionValue(key SettingKey, value string) (string, error) {
	if err := codexclientaccess.ValidateVersionString(value); err != nil {
		return "", fmt.Errorf("%w: %s %v", ErrInvalidValue, key, err)
	}
	return value, nil
}
