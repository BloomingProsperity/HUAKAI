package gateway

import (
	"testing"
	"time"
)

// TOKLIFE-01:cooldown 随连续错误次数(无 provider Retry-After 时)指数级递增,
// 上限封顶 30m,这样一个不断抖动的账号会被逐步搁置更久,而不是每隔固定 60s
// 就重试一次。
func TestWithEscalatedCooldown(t *testing.T) {
	now := time.Unix(1000, 0)
	base := DefaultCooldownDuration
	cap30 := 30 * time.Minute
	cases := []struct {
		streak int
		want   time.Duration
	}{
		{0, base}, // 无连续错误 -> base
		{1, base}, // 首次错误 -> base
		{2, 2 * base},
		{3, 4 * base},
		{4, 8 * base},
		{6, cap30}, // 32*60s = 32m -> 封顶到 30m
		{50, cap30},
	}
	for _, tc := range cases {
		// 变异守卫:去掉 <<位移递增会让每个用例都塌缩为
		// base -> 2x/4x/8x/cap 这些行变红。
		c := withEscalatedCooldown(FSMClassification{}, tc.streak, now)
		if got := c.CooldownUntil.Sub(now); got != tc.want {
			t.Fatalf("streak %d: cooldown %v want %v", tc.streak, got, tc.want)
		}
	}

	// provider 的 Retry-After 覆盖优先 -> 不递增,CooldownUntil 保持不动。
	c := withEscalatedCooldown(FSMClassification{RetryAfter: 5 * time.Second}, 10, now)
	if !c.CooldownUntil.IsZero() {
		t.Fatalf("RetryAfter override must not be escalated, got %v", c.CooldownUntil)
	}
	// 显式给定的 CooldownUntil 会被保留。
	explicit := now.Add(time.Hour)
	c2 := withEscalatedCooldown(FSMClassification{CooldownUntil: explicit}, 10, now)
	if c2.CooldownUntil != explicit {
		t.Fatalf("explicit CooldownUntil must be preserved, got %v", c2.CooldownUntil)
	}
}
