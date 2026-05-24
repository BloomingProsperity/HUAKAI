package userkey

import (
	"testing"
)

// 这里只放纯函数/结构语义的判别性 fixture (mutation 自检型);
// 真正的 DB 行为测试在 integration_pg_test.go (build tag integration_pg) 跑真 PG。

// TestIssueResult_StringRedactsPlaintext: Plaintext 不应出现在 fmt 输出。
//
// Mutation 自检:把 String() 改成直接打 r.Plaintext → 测试会发现 plaintext 串出现 → red。
func TestIssueResult_StringRedactsPlaintext(t *testing.T) {
	r := IssueResult{
		APIKeyID:  42,
		Plaintext: "hk_live_super_secret_token_value",
		KeyPrefix: "hk_live_super_se",
		Status:    "active",
	}
	s := r.String()
	if got := s; contains(got, "hk_live_super_secret_token_value") {
		t.Fatalf("Plaintext leaked into String(): %q", got)
	}
	if !contains(s, "<redacted>") {
		t.Fatalf("String() must show <redacted> for non-empty Plaintext; got %q", s)
	}
	if !contains(s, "hk_live_super_se") {
		t.Fatalf("KeyPrefix must appear (it's not secret); got %q", s)
	}
}

// TestIssueResult_StringEmptyPlaintext: 空 plaintext 走 <empty>,跟 <redacted> 不同。
//
// Mutation 自检:把 <empty> 改成 <redacted> 则两个分支无差异 → 此判别 fixture red。
func TestIssueResult_StringEmptyPlaintext(t *testing.T) {
	r := IssueResult{APIKeyID: 1, KeyPrefix: "hk_live_xxxx", Status: "active"}
	s := r.String()
	if !contains(s, "<empty>") {
		t.Fatalf("Empty Plaintext must render <empty>; got %q", s)
	}
}

// TestConstantsSane: 防止有人误把 cap 调到 0 / 负数,跑出"用户永远建不了 key"假绿。
//
// 这是结构 invariant,不是行为测试 — 直接对边界值断言。
func TestConstantsSane(t *testing.T) {
	if MaxActiveKeysPerUser <= 0 {
		t.Fatalf("MaxActiveKeysPerUser must be positive; got %d", MaxActiveKeysPerUser)
	}
	if MaxNameLen <= 0 {
		t.Fatalf("MaxNameLen must be positive; got %d", MaxNameLen)
	}
	if PageLimitDefault <= 0 || PageLimitMax < PageLimitDefault {
		t.Fatalf("page limit constants must satisfy 0 < default ≤ max; got default=%d max=%d", PageLimitDefault, PageLimitMax)
	}
}

// TestEnvAliases: 用户路径不能签 hk_admin_ 前缀 — 这是 string compare,
// 真校验在 Issue() 里(EnvAdmin 与 EnvLive/EnvTest 都不等)。这里至少
// 锁死类型别名指向 admin 包,防有人重定义。
func TestEnvAliases(t *testing.T) {
	if string(EnvLive) != "live" {
		t.Fatalf("EnvLive alias drift: got %q", EnvLive)
	}
	if string(EnvTest) != "test" {
		t.Fatalf("EnvTest alias drift: got %q", EnvTest)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
