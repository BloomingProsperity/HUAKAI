package adminstepup

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/passkeyhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/twofa"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

// fakeInner 捕获透传的 proof 并回放预置错误,单测各翻译分支(不碰真 DB / 2FA)。
type fakeInner struct {
	gotTenant int64
	gotUser   int64
	gotProof  passkeyhttp.StepUpProof
	ret       error
}

func (f *fakeInner) VerifyStepUp(_ context.Context, tenantID, userID int64, proof passkeyhttp.StepUpProof) error {
	f.gotTenant = tenantID
	f.gotUser = userID
	f.gotProof = proof
	return f.ret
}

// 错误翻译矩阵:底层 verifier 各 sentinel → admin.ErrAdminStepUp*/ErrAdminBackend。
// 这是本适配器的全部价值(把 passkey/twofa/userauth 内部错误坍缩成 admin 语境可映射状态码的错误),
// 故逐分支断言。含 wrapped 变体,证明用的是 errors.Is(可穿透 %w)而非 ==。
// 变异:把某个 case 的目标 sentinel 改错、或把 errors.Is 改成 == → 对应断言 RED。
func TestVerifyStepUpErrorTranslation(t *testing.T) {
	cases := []struct {
		name string
		in   error
		want error
	}{
		{"通过", nil, nil},
		{"需证明", passkeyhttp.ErrStepUpRequired, admin.ErrAdminStepUpRequired},
		{"证明无效", passkeyhttp.ErrStepUpInvalid, admin.ErrAdminStepUpInvalid},
		{"密码错(userauth)", userauth.ErrInvalidCredentials, admin.ErrAdminStepUpInvalid},
		{"2FA 码错", twofa.ErrInvalidCode, admin.ErrAdminStepUpInvalid},
		{"2FA 码重放", twofa.ErrCodeReused, admin.ErrAdminStepUpInvalid},
		{"2FA 锁定", twofa.ErrLocked, admin.ErrAdminStepUpLocked},
		{"未配置 → 后端", passkeyhttp.ErrStepUpNotConfigured, admin.ErrAdminBackend},
		{"未知错 → 后端(不泄露)", errors.New("some db failure"), admin.ErrAdminBackend},
		// wrapped:证明 errors.Is 穿透 %w(底层若包了一层仍须正确翻译)。
		{"wrapped 锁定", fmt.Errorf("verify: %w", twofa.ErrLocked), admin.ErrAdminStepUpLocked},
		{"wrapped 需证明", fmt.Errorf("ctx: %w", passkeyhttp.ErrStepUpRequired), admin.ErrAdminStepUpRequired},
	}
	for _, c := range cases {
		v := &Verifier{inner: &fakeInner{ret: c.in}}
		got := v.VerifyStepUp(context.Background(), 1, 2, "pw", "123456")
		if c.want == nil {
			if got != nil {
				t.Fatalf("%s: 期望 nil,得 %v", c.name, got)
			}
			continue
		}
		if !errors.Is(got, c.want) {
			t.Fatalf("%s: 翻译错误,期望 %v,得 %v", c.name, c.want, got)
		}
	}
}

// proof 透传:password/code 两串须原样组装进底层 StepUpProof(顺序不能错位)。
// 变异:把 New/VerifyStepUp 里 Password/TwoFactorCode 赋值对调 → 断言 RED。
func TestVerifyStepUpForwardsProof(t *testing.T) {
	fi := &fakeInner{}
	v := &Verifier{inner: fi}
	_ = v.VerifyStepUp(context.Background(), 7, 9, "s3cret", "654321")
	if fi.gotTenant != 7 || fi.gotUser != 9 {
		t.Fatalf("tenant/user 透传错:got tenant=%d user=%d", fi.gotTenant, fi.gotUser)
	}
	if fi.gotProof.Password != "s3cret" {
		t.Fatalf("password 应透传进 proof.Password,得 %q", fi.gotProof.Password)
	}
	if fi.gotProof.TwoFactorCode != "654321" {
		t.Fatalf("code 应透传进 proof.TwoFactorCode,得 %q", fi.gotProof.TwoFactorCode)
	}
}

// nil 接收者 / nil inner → fail-closed 返 ErrAdminBackend,绝不 panic(SessionStepUp 路由未接线时的兜底)。
// 变异:去掉 VerifyStepUp 顶部的 nil 守卫 → nil inner 解引用 panic → RED。
func TestVerifyStepUpNilFailsClosed(t *testing.T) {
	var v *Verifier // nil 接收者
	if err := v.VerifyStepUp(context.Background(), 1, 2, "pw", ""); !errors.Is(err, admin.ErrAdminBackend) {
		t.Fatalf("nil 接收者应 fail-closed ErrAdminBackend,得 %v", err)
	}
	v2 := &Verifier{} // inner 为 nil
	if err := v2.VerifyStepUp(context.Background(), 1, 2, "pw", ""); !errors.Is(err, admin.ErrAdminBackend) {
		t.Fatalf("nil inner 应 fail-closed ErrAdminBackend,得 %v", err)
	}
}

// New 用真实 passkey 原语构造出可用的 inner(非 nil),接线不空转。
// 变异:若 New 返回 inner=nil 的 Verifier → 下游恒 ErrAdminBackend(空转)→ 这里 inner!=nil 断言 RED。
func TestNewWiresInner(t *testing.T) {
	v := New(nil, nil) // users/twoFactor 传 nil:仅证 inner 被构造,不触发校验
	if v == nil || v.inner == nil {
		t.Fatal("New 应构造出非 nil 的 inner(复用 passkeyhttp.LocalStepUpVerifier)")
	}
}
