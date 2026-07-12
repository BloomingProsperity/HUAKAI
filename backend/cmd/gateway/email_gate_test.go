package main

import (
	"errors"
	"testing"
)

// TestRequireEmailReleaseGate 守护 HUAKAI_REQUIRE_EMAIL_GATE 的语义:默认 false(软化:不拦启动),
// 仅显式 "true"(大小写不敏感)恢复旧严格行为。
//
// 变异检查:把默认翻成 true(例如改成 !EqualFold(...,"false")),unset/空/"false" 用例即由 false 变 true(RED)。
func TestRequireEmailReleaseGate(t *testing.T) {
	cases := []struct {
		val  string
		set  bool
		want bool
	}{
		{set: false, want: false},          // 未设 → 默认软化(不强制)
		{val: "", set: true, want: false},  // 空串 → 不强制
		{val: "false", set: true, want: false},
		{val: "0", set: true, want: false}, // 非 "true" 一律不强制(与 DEV_AUTH_RETURN_TOKEN 同款只认 true)
		{val: "true", set: true, want: true},
		{val: "TRUE", set: true, want: true},
		{val: " true ", set: true, want: true}, // 去空白后匹配
	}
	for _, c := range cases {
		c := c
		name := "unset"
		if c.set {
			name = "val=" + c.val
		}
		t.Run(name, func(t *testing.T) {
			// 未设与空串对本函数等价(TrimSpace 后非 "true" 即 false),用空串覆盖"未设"语义。
			t.Setenv("HUAKAI_REQUIRE_EMAIL_GATE", c.val)
			if got := requireEmailReleaseGate(); got != c.want {
				t.Fatalf("requireEmailReleaseGate() for %q = %v, want %v", c.val, got, c.want)
			}
		})
	}
}

// TestEmailGateStartupError 守护本切片的核心 default-flip:邮箱门未过时,默认(required=false)不拦启动,
// 仅 required=true 才返回错误拒启。
//
// 变异检查:把 emailGateStartupError 改成无条件 `return gateErr`,(someErr,false)→nil 用例即变非 nil(RED),
// 精确抓"默认软化放行"被回退成"默认拒启"。
func TestEmailGateStartupError(t *testing.T) {
	sentinel := errors.New("email gate not satisfied")

	t.Run("未过+默认软化→不拦启动", func(t *testing.T) {
		if err := emailGateStartupError(sentinel, false); err != nil {
			t.Fatalf("默认(required=false)邮箱门未过应软化放行,got %v", err)
		}
	})
	t.Run("未过+强制→拒启并透传原错误", func(t *testing.T) {
		err := emailGateStartupError(sentinel, true)
		if !errors.Is(err, sentinel) {
			t.Fatalf("required=true 邮箱门未过应返回原错误拒启,got %v", err)
		}
	})
	t.Run("门通过+默认→放行", func(t *testing.T) {
		if err := emailGateStartupError(nil, false); err != nil {
			t.Fatalf("门通过应放行,got %v", err)
		}
	})
	t.Run("门通过+强制→放行", func(t *testing.T) {
		if err := emailGateStartupError(nil, true); err != nil {
			t.Fatalf("门通过即便 required=true 也应放行,got %v", err)
		}
	})
}
