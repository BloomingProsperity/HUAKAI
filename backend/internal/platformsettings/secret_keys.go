package platformsettings

import "strings"

// 本文件集中声明哪些平台设置 key 承载“密钥/凭据类”敏感值,作为读路径脱敏与
// 审计日志脱敏共用的唯一判定来源,避免两处各自硬编码导致漂移(其中一处补了新
// 的密钥 key、另一处忘了补,就会重新泄露明文)。

// secretSettingKeys 列出值里包含上游凭据、不得以明文返回客户端或写入审计日志的
// key:
//   - KeyModerationExternalAPIKeys 是外部审核 provider 的 bearer 密钥数组,
//     消费侧用它拼出 "Bearer <key>" 调用上游。
//
// 注:KeyPaymentProviderConfig 刻意不在此列——它只承载支付方式开关(enabled)与
// 收银台 URL(checkout_url),schema 封闭、不含任何凭据(校验只接受 manual/taobao 的
// enabled+checkout_url)。当密钥从严脱敏会让运维读不到支付配置、伤运维;真正的支付
// 密钥应另立专用 secret key 并登记在此,而非塞进这张支付方式开关表。
var secretSettingKeys = map[SettingKey]struct{}{
	KeyModerationExternalAPIKeys: {},
}

// IsSecretKey 判定某个 key 是否属于密钥/凭据类。读路径与审计脱敏都调用它,确保
// 两处的脱敏范围始终一致。
func IsSecretKey(key SettingKey) bool {
	_, ok := secretSettingKeys[key]
	return ok
}

// HasConfiguredSecretValue 报告该密钥类 key 当前是否已配置了非空值。读路径据此
// 返回 "configured: true/false" 指示,让运维知道密钥存在与否,但不暴露明文。
// 对于密钥类 key,“已配置”意味着 trim 后非空,且不是表示空集合的 "[]"/"{}"
// 占位值(空 JSON 容器视为未配置)。
func HasConfiguredSecretValue(key SettingKey, value string) bool {
	if !IsSecretKey(key) {
		return false
	}
	trimmed := strings.TrimSpace(value)
	switch trimmed {
	case "", "[]", "{}":
		return false
	default:
		return true
	}
}
