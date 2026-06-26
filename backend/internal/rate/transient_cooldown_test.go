package rate

import (
	"net/http"
	"testing"
	"time"
)

func TestTransientCooldown(t *testing.T) {
	// 变异:对 502 返回 ok=false 必须让此断言转红。
	d, reason, ok := TransientCooldown(http.StatusBadGateway, TransientCooldownConfig{Duration: 30 * time.Second})
	if !ok {
		t.Fatal("502 transient cooldown ok=false, want true")
	}
	if d != 30*time.Second {
		t.Fatalf("duration=%s want 30s", d)
	}
	if reason != ReasonUpstreamTransient {
		t.Fatalf("reason=%s want %s", reason, ReasonUpstreamTransient)
	}

	d, reason, ok = TransientCooldown(http.StatusOK, TransientCooldownConfig{Duration: 30 * time.Second})
	if ok || d != 0 || reason != "" {
		t.Fatalf("200 transient cooldown=(%s,%s,%v), want zero/false", d, reason, ok)
	}

	// 守卫:零 duration 会禁用附加的瞬态冷却路径。
	d, reason, ok = TransientCooldown(http.StatusBadGateway, TransientCooldownConfig{})
	if ok || d != 0 || reason != "" {
		t.Fatalf("disabled cfg transient cooldown=(%s,%s,%v), want zero/false", d, reason, ok)
	}
}
