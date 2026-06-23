package main

import (
	"os"
	"strings"
)

// requireEmailReleaseGate 读 HUAKAI_REQUIRE_EMAIL_GATE,决定 production 邮箱门是否仍 fail-loud 拒启。
//
// 默认 false(对齐成熟中转站的"请求时惰性"做法):production 下即使有 active 租户未配齐 SMTP 或未开
// 邮箱验证,也不再拦死启动——这些租户的验证邮件相关功能改为请求时惰性返回错误(注册按"验证关闭"放行,
// 用户直接 active;显式触发验证邮件/密码重置时返回 503 EMAIL_BACKEND_UNCONFIGURED)。
//
// 设为 true 恢复旧严格行为:每个 active 租户必须配齐 SMTP 且开启邮箱验证才放行启动(想强制全员邮箱
// 验证的运维可显式开回)。仅 "true"(大小写不敏感)开启,与 HUAKAI_DEV_AUTH_RETURN_TOKEN 同款读法。
func requireEmailReleaseGate() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("HUAKAI_REQUIRE_EMAIL_GATE")), "true")
}

// emailGateStartupError 把"邮箱门校验结果 + 是否强制要求"折叠为启动决策:
//   - 门未过(gateErr != nil)且 required=true → 返回该错误(fail-loud 拒启,旧严格行为);
//   - 门未过且 required=false(默认)→ 返回 nil(软化:不拦启动,由调用方 warn 提示,功能请求时惰性失败);
//   - 门通过(gateErr == nil)→ 返回 nil。
//
// 抽成纯函数便于判别式单测:把 `&& required` 改成无条件返回 gateErr,(someErr, false) 用例即由 nil 变非 nil(RED)。
func emailGateStartupError(gateErr error, required bool) error {
	if gateErr != nil && required {
		return gateErr
	}
	return nil
}
