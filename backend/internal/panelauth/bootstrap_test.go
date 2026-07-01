package panelauth

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// env 未设 → no-op:在触碰 pool 之前就返回 nil(故此处传 nil pool 不应 panic 也不报错)。
// 变异:若去掉 email=="" 的短路,会走到 pool.BeginTx(nil) 分支 → 返 ErrBootstrapBackend → RED。
func TestBootstrapAdminUserNoopWhenEnvUnset(t *testing.T) {
	t.Setenv(AdminBootstrapEmailEnv, "")
	if err := MaybeBootstrapAdminUser(context.Background(), nil, 1, nil); err != nil {
		t.Fatalf("env 未设应 no-op 返 nil(不碰 pool),得 %v", err)
	}
}

// 仅空白的 env 值等同未设(TrimSpace)→ no-op。
// 变异:若不 TrimSpace,"  " 会被当有效邮箱走到 nil pool → 报错 → RED。
func TestBootstrapAdminUserBlankEnvIsNoop(t *testing.T) {
	t.Setenv(AdminBootstrapEmailEnv, "   ")
	if err := MaybeBootstrapAdminUser(context.Background(), nil, 1, nil); err != nil {
		t.Fatalf("纯空白 env 应 no-op,得 %v", err)
	}
}

// env 已设但 pool 为 nil → fail-loud(基础设施错),绝不静默跳过。
// 变异:若把 nil pool 守卫删掉 → pool.BeginTx 在 nil 上 panic → 本测试崩(RED)。
func TestBootstrapAdminUserFailsLoudOnNilPool(t *testing.T) {
	t.Setenv(AdminBootstrapEmailEnv, "ops@example.test")
	if err := MaybeBootstrapAdminUser(context.Background(), nil, 1, nil); !errors.Is(err, ErrBootstrapBackend) {
		t.Fatalf("env 已设 + nil pool 应 fail-loud 返 ErrBootstrapBackend,得 %v", err)
	}
}

// 守值校验顺序:tenantID<=0 的守卫必须排在 nil-pool 守卫之前。env 已设 + tenantID 非法 + pool 也为 nil
// 时,应报 tenant 错(而非 nil pool 错)——顺序反了会先撞 nil-pool。
// 变异:把 tenantID 守卫挪到 nil-pool 守卫之后 → 消息变 "nil pool" 不含 "tenantID" → RED。
func TestBootstrapAdminUserTenantCheckBeforePoolCheck(t *testing.T) {
	t.Setenv(AdminBootstrapEmailEnv, "ops@example.test")
	err := MaybeBootstrapAdminUser(context.Background(), nil, 0, nil)
	if !errors.Is(err, ErrBootstrapBackend) || !strings.Contains(err.Error(), "tenantID") {
		t.Fatalf("tenantID 守卫应先于 nil-pool 守卫触发(错误含 tenantID),得 %v", err)
	}
}

// env 已设但 tenantID 非法(<=0)→ fail-loud,不越权全表提升。
// 断言错误消息含 "tenantID"(而非只判 ErrBootstrapBackend):tenant 守卫排在 nil-pool 守卫之前,
// 二者都返 ErrBootstrapBackend——只判错误类型无法区分是哪道守卫拦的。
// 变异:若删掉 tenantID<=0 守卫,会落到 nil-pool 守卫,消息变 "nil pool" 不含 "tenantID" → RED。
func TestBootstrapAdminUserRejectsNonPositiveTenant(t *testing.T) {
	t.Setenv(AdminBootstrapEmailEnv, "ops@example.test")
	for _, tid := range []int64{0, -1} {
		err := MaybeBootstrapAdminUser(context.Background(), nil, tid, nil)
		if !errors.Is(err, ErrBootstrapBackend) || !strings.Contains(err.Error(), "tenantID") {
			t.Fatalf("tenantID=%d 应被 tenant 守卫拦(错误含 tenantID),得 %v", tid, err)
		}
	}
}
