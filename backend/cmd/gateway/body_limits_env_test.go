package main

import (
	"net/http/httptest"
	"testing"
)

// TestBodyLimitBytesFromEnv 守护 MB 单位容量 env 的解析:合法正整数 MB → 字节(<<20),
// 空/非法/非正一律回退默认。
//
// 变异检查:① 把 <<20 去掉(直接返回 MB 数)→ "32"→32 而非 32MiB,用例 RED;
// ② 忽略 env 永远返回默认 → "32" 用例 RED。
func TestBodyLimitBytesFromEnv(t *testing.T) {
	const def int64 = 32 << 20
	cases := []struct {
		val  string
		want int64
	}{
		{"", def},      // 未设 → 默认
		{"abc", def},   // 非法 → 默认
		{"0", def},     // 非正 → 默认
		{"-5", def},    // 负 → 默认
		{"32", 32 << 20},
		{"128", 128 << 20},
	}
	for _, c := range cases {
		c := c
		t.Run("val="+c.val, func(t *testing.T) {
			t.Setenv("HUAKAI_TEST_BODY_MB", c.val)
			if got := bodyLimitBytesFromEnv("HUAKAI_TEST_BODY_MB", def); got != c.want {
				t.Fatalf("bodyLimitBytesFromEnv(%q) = %d, want %d", c.val, got, c.want)
			}
		})
	}
}

// TestPrivacyBodyLimitForRequest 守护 privacy 缓冲上限按路径解耦:relay 数据面用大上限,
// 其余(尤其未认证 login/register)用小上限——这正是修复点:抬高 relay 上限不应把 pre-auth
// 内存放大面波及未认证控制面端点。
//
// 自证式:同函数对 relay 与非 relay 路径给不同结果。变异检查:若去掉 isAIRelayPath 分支恒返回
// relayMax,则 login/register 等非 relay 用例(want=nonRelay)由 RED 暴露。
func TestPrivacyBodyLimitForRequest(t *testing.T) {
	const relayMax = 32 << 20
	const nonRelayMax = 8 << 20
	cases := []struct {
		path string
		want int
	}{
		// relay 数据面 → 大上限
		{"/v1/chat/completions", relayMax},
		{"/v1/responses", relayMax},
		{"/v1/messages", relayMax},
		{"/v1/images/generations", relayMax},
		// 未认证控制面 / 非 relay → 小上限(关键:不被 relay 放大波及)
		{"/v1/auth/login", nonRelayMax},
		{"/v1/auth/register", nonRelayMax},
		{"/healthz", nonRelayMax},
	}
	for _, c := range cases {
		c := c
		t.Run(c.path, func(t *testing.T) {
			req := httptest.NewRequest("POST", c.path, nil)
			if got := privacyBodyLimitForRequest(req, relayMax, nonRelayMax); got != c.want {
				t.Fatalf("privacyBodyLimitForRequest(%q) = %d, want %d", c.path, got, c.want)
			}
		})
	}
}
