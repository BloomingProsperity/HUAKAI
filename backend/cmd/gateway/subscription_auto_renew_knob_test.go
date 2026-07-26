// HUAKAI · iKun

package main

import "testing"

// TestSubscriptionAutoRenewKnobDefaultsOn 守持久化合同：用户订阅默认 auto_renew=true，
// 所以未设环境变量时 worker 必须启动，不能让界面状态与实际执行相反。
// mutation: 把 raw=="" 分支改回 false → 本测试红。
func TestSubscriptionAutoRenewKnobDefaultsOn(t *testing.T) {
	t.Setenv(subscriptionAutoRenewEnabledEnv, "")
	enabled, err := subscriptionAutoRenewEnabledFromEnv(subscriptionAutoRenewEnabledEnv)
	if err != nil {
		t.Fatalf("unset knob must not error: %v", err)
	}
	if !enabled {
		t.Fatal("自动续费开关未设时必须默认启用，否则 auto_renew=true 只是空心状态")
	}
}

// TestSubscriptionAutoRenewKnobExplicitTrue 守显式 true 仍可用。
// mutation: 把解析改成恒 false → 此用例红。
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

// TestSubscriptionAutoRenewKnobFalseStaysOff 守显式紧急停机和非法值 fail-loud。
// mutation: 忽略 false 或把非法值静默退回默认 → 本测试红。
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
