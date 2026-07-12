// HUAKAI · iKun

// UserGate 会话使用期资格复核测试: 封禁/删除的主体在 Validate 与 Refresh 两路都必须
// 被拒且撤整个家族(bearer 短效 + refresh 30 天长链, 不复核则封禁后既有会话活到自然过期)。

package usersession

import (
	"context"
	"errors"
	"testing"
	"time"
)

// userGateStub 可切换资格的桩; calls 记录复核确实发生。
type userGateStub struct {
	err   error
	calls int
}

func (g *userGateStub) CheckSessionUser(context.Context, int64, int64) error {
	g.calls++
	return g.err
}

// TestValidateRejectsIneligibleUserAndRevokesFamily 守 Validate 惰性复核:
// gate 报 ineligible → 拒 + 撤家族(后续请求快速失败在 family revoked, 不再走复核)。
// mutation: Validate 里去掉 UserGate 调用 → 封禁主体 bearer 继续可用 → 第一断言红。
func TestValidateRejectsIneligibleUserAndRevokesFamily(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 5, 11, 0, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore())
	svc.Now = func() time.Time { return now }
	svc.SigningKey = testSigningKey()
	gate := &userGateStub{}
	svc.UserGate = gate

	issued, err := svc.Create(ctx, CreateInput{TenantID: 1, UserID: 42, IP: "10.1.2.3", UserAgent: "Chrome/1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// 资格正常: 放行且复核确实执行。
	if _, err := svc.Validate(ctx, issued.SessionToken, "10.1.2.3", "Chrome/1"); err != nil {
		t.Fatalf("eligible validate: %v", err)
	}
	if gate.calls == 0 {
		t.Fatal("UserGate 未被调用 —— 惰性复核是死接线")
	}

	// 账号被封: 下一次 Validate 即拒。
	gate.err = ErrUserIneligible
	if _, err := svc.Validate(ctx, issued.SessionToken, "10.1.2.3", "Chrome/1"); !errors.Is(err, ErrUserIneligible) {
		t.Fatalf("banned validate err=%v, want ErrUserIneligible (封禁主体 bearer 仍可用)", err)
	}
	// 家族已撤: 即使资格恢复, 旧会话不复活。
	gate.err = nil
	if _, err := svc.Validate(ctx, issued.SessionToken, "10.1.2.3", "Chrome/1"); !errors.Is(err, ErrFamilyRevoked) {
		t.Fatalf("after revoke err=%v, want ErrFamilyRevoked (ineligible 未撤家族)", err)
	}
	families, _ := svc.List(ctx, 1, 42)
	if len(families) != 1 || families[0].Status != FamilyStatusRevoked || families[0].RevokedReason != "user_ineligible" {
		t.Fatalf("family = %+v, want revoked/user_ineligible", families)
	}
}

// TestRefreshRejectsIneligibleUserAndRevokesFamily 守 Refresh 惰性复核(30 天链的关键闸):
// gate 报 ineligible → 拒续期 + 撤家族, refresh 链就地掐断。
// mutation: Refresh 里去掉 UserGate 调用 → 封禁主体无限续期 → 第一断言红。
func TestRefreshRejectsIneligibleUserAndRevokesFamily(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 5, 11, 30, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore())
	svc.Now = func() time.Time { return now }
	svc.SigningKey = testSigningKey()
	gate := &userGateStub{err: ErrUserIneligible}
	svc.UserGate = gate

	issued, err := svc.Create(ctx, CreateInput{TenantID: 1, UserID: 43, IP: "10.1.2.3", UserAgent: "Chrome/1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	now = now.Add(time.Minute)
	if _, err := svc.Refresh(ctx, RefreshInput{TenantID: 1, UserID: 43, RefreshToken: issued.RefreshToken, IP: "10.1.2.3", UserAgent: "Chrome/1"}); !errors.Is(err, ErrUserIneligible) {
		t.Fatalf("banned refresh err=%v, want ErrUserIneligible (封禁主体仍能续期)", err)
	}
	families, _ := svc.List(ctx, 1, 43)
	if len(families) != 1 || families[0].Status != FamilyStatusRevoked {
		t.Fatalf("family = %+v, want revoked (ineligible 未掐断 refresh 链)", families)
	}
}

// TestUserGateBackendErrorFailsClosed 守 fail-closed: gate 后端瞬时故障 → 原样上抛拒绝,
// 但不撤家族(故障非资格判定, 恢复后会话应继续可用)。
// mutation: 把 gate 错误吞掉放行 → 第一断言红。
func TestUserGateBackendErrorFailsClosed(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore())
	svc.Now = func() time.Time { return now }
	svc.SigningKey = testSigningKey()
	transient := errors.New("users backend down")
	gate := &userGateStub{err: transient}
	svc.UserGate = gate

	issued, err := svc.Create(ctx, CreateInput{TenantID: 1, UserID: 44, IP: "10.1.2.3", UserAgent: "Chrome/1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.Validate(ctx, issued.SessionToken, "10.1.2.3", "Chrome/1"); !errors.Is(err, transient) {
		t.Fatalf("backend error err=%v, want 原样上抛 (吞错=资格门失效)", err)
	}
	// 故障不撤家族: 恢复后同一会话继续可用。
	gate.err = nil
	if _, err := svc.Validate(ctx, issued.SessionToken, "10.1.2.3", "Chrome/1"); err != nil {
		t.Fatalf("recovered validate: %v (瞬时故障不该永久杀会话)", err)
	}
}
