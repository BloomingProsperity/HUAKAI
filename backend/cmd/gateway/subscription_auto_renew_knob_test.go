// HUAKAI · iKun

package main

import "testing"

// TestSubscriptionAutoRenewKnobDefaultsOff 守"money 安全默认关":未设/空环境变量必须解析为
// false (worker 不启动 = 自动扣费续期不激活)。
// mutation: 把 subscriptionAutoRenewEnabledFromEnv 的 raw=="" 分支改成 return true → 本测试红。
func TestSubscriptionAutoRenewKnobDefaultsOff(t *testing.T) {
	t.Setenv(subscriptionAutoRenewEnabledEnv, "")
	enabled, err := subscriptionAutoRenewEnabledFromEnv(subscriptionAutoRenewEnabledEnv)
	if err != nil {
		t.Fatalf("unset knob must not error: %v", err)
	}
	if enabled {
		t.Fatal("自动续费 KNOB 未设时必须默认关 (false), 实际为 true —— 会在未授权下自动扣用户余额")
	}
}

// TestSubscriptionAutoRenewKnobExplicitTrue 守"显式 opt-in 才开":只有 =true 才返回 true。
// mutation: 把解析改成恒 false → 此用例红 (Owner 翻 true 也开不起来)。
func TestSubscriptionAutoRenewKnobExplicitTrue(t *testing.T) {
	t.Setenv(subscriptionAutoRenewEnabledEnv, "true")
	enabled, err := subscriptionAutoRenewEnabledFromEnv(subscriptionAutoRenewEnabledEnv)
	if err != nil {
		t.Fatalf("true knob must not error: %v", err)
	}
	if !enabled {
		t.Fatal("自动续费 KNOB =true 时必须启用, 实际未启用")
	}
}

// TestSubscriptionAutoRenewKnobFalseStaysOff 守"显式 false 关闭"且"非法值 fail-loud"。
// 非法值绝不能静默退回某个默认 (静默 true 会偷偷开自动扣费; 静默 false 也掩盖配置错误)。
// mutation: 非法分支改成 return false,nil (静默) → 本测试的 err==nil 断言红。
func TestSubscriptionAutoRenewKnobFalseStaysOff(t *testing.T) {
	t.Setenv(subscriptionAutoRenewEnabledEnv, "false")
	if enabled, err := subscriptionAutoRenewEnabledFromEnv(subscriptionAutoRenewEnabledEnv); err != nil || enabled {
		t.Fatalf("false 必须关闭且不报错: enabled=%v err=%v", enabled, err)
	}
	t.Setenv(subscriptionAutoRenewEnabledEnv, "not-a-bool")
	if _, err := subscriptionAutoRenewEnabledFromEnv(subscriptionAutoRenewEnabledEnv); err == nil {
		t.Fatal("非法布尔值必须 fail-loud 报错, 不能静默退回默认")
	}
}
