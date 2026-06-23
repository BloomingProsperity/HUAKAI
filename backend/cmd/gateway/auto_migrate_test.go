package main

import "testing"

// TestAutoMigrateEnabled 守护 HUAKAI_AUTO_MIGRATE 语义:默认 false(迁移保持外置),
// 仅显式 "true"(大小写不敏感)开启进程内自迁移。
//
// 变异检查:把默认翻成 true(如改成 !EqualFold(...,"false")),unset/空/"false" 用例由 false 变 true(RED)。
func TestAutoMigrateEnabled(t *testing.T) {
	cases := []struct {
		val  string
		want bool
	}{
		{val: "", want: false},   // 未设/空 → 默认外置(不自迁移)
		{val: "false", want: false},
		{val: "0", want: false},  // 非 "true" 一律不开启
		{val: "true", want: true},
		{val: "TRUE", want: true},
		{val: " true ", want: true},
	}
	for _, c := range cases {
		c := c
		t.Run("val="+c.val, func(t *testing.T) {
			t.Setenv("HUAKAI_AUTO_MIGRATE", c.val)
			if got := autoMigrateEnabled(); got != c.want {
				t.Fatalf("autoMigrateEnabled() for %q = %v, want %v", c.val, got, c.want)
			}
		})
	}
}
