// HUAKAI · iKun
package config

import "testing"

// Owner 拍板默认开(登录即管理员);显式 false 是唯一退回纯令牌通道的路。
// 变异:envBoolDefault 的 fallback true 改 false → 「未设=开」断言 RED。
func TestAdminSessionAuthEnabledDefaultOn(t *testing.T) {
	t.Setenv("HUAKAI_ADMIN_SESSION_AUTH_ENABLED", "")
	on, err := AdminSessionAuthEnabled()
	if err != nil || !on {
		t.Fatalf("未设 env 应默认开,得 on=%v err=%v", on, err)
	}

	t.Setenv("HUAKAI_ADMIN_SESSION_AUTH_ENABLED", "false")
	on, err = AdminSessionAuthEnabled()
	if err != nil || on {
		t.Fatalf("显式 false 应关(运维退路),得 on=%v err=%v", on, err)
	}

	t.Setenv("HUAKAI_ADMIN_SESSION_AUTH_ENABLED", "not-a-bool")
	if _, err = AdminSessionAuthEnabled(); err == nil {
		t.Fatal("非法布尔值应 fail-loud,得 nil error")
	}
}
