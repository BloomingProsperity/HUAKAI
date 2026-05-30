// HUAKAI · iKun

package panelauth

import (
	"context"
	"errors"
	"testing"
)

// 守二分映射: 精确 'admin' → 管理面板; 'user' → 用户面板。
// mutation: PanelForRole 把 admin 误映成 user(或反)→ 红。
func TestPanelForRole_Binary(t *testing.T) {
	if got := PanelForRole(RoleAdmin); got != PanelAdmin {
		t.Fatalf("PanelForRole('admin') = %q, want %q", got, PanelAdmin)
	}
	if got := PanelForRole(RoleUser); got != PanelUser {
		t.Fatalf("PanelForRole('user') = %q, want %q", got, PanelUser)
	}
}

// 守越权安全默认(头号风险): 任何非精确 'admin' 的 role —— 空串/大小写不符/未知/未来新增值 ——
// 都必须落到用户面板, 绝不误授管理面板。
// mutation: PanelForRole 改成「role != 'user' → admin」或 default 分支返回 PanelAdmin → 这些 case 全红。
func TestPanelForRole_NonAdminNeverGetsAdminPanel(t *testing.T) {
	for _, role := range []string{"", "user", "ADMIN", "Admin", " admin", "admin ", "staff", "operator", "platform_admin", "tenant_operator", "root", "superuser", "0", "true"} {
		if got := PanelForRole(role); got == PanelAdmin {
			t.Fatalf("PanelForRole(%q) = PanelAdmin — privilege escalation! only exact 'admin' may reach the admin panel", role)
		}
		if got := PanelForRole(role); got != PanelUser {
			t.Fatalf("PanelForRole(%q) = %q, want PanelUser (deny-by-default)", role, got)
		}
	}
}

// 守 hk_admin token 主体 → 管理面板。
func TestPanelForAdminToken(t *testing.T) {
	if got := PanelForAdminToken(); got != PanelAdmin {
		t.Fatalf("PanelForAdminToken() = %q, want %q", got, PanelAdmin)
	}
}

// 守 Resolver: 查 role → 映射面板; admin 用户→admin 面板, user 用户→user 面板。
// mutation: PanelForUser 不查 store 直接返 PanelUser → admin 用户 case 红。
func TestResolver_PanelForUser(t *testing.T) {
	store := NewMemoryRoleStore().WithUser(5, 100, RoleAdmin).WithUser(5, 200, RoleUser)
	r := NewResolver(store)

	if p, err := r.PanelForUser(context.Background(), 5, 100); err != nil || p != PanelAdmin {
		t.Fatalf("admin user: panel=%q err=%v, want PanelAdmin/nil", p, err)
	}
	if p, err := r.PanelForUser(context.Background(), 5, 200); err != nil || p != PanelUser {
		t.Fatalf("normal user: panel=%q err=%v, want PanelUser/nil", p, err)
	}
}

// 守用户不存在 → ErrUserNotFound(不 fallback 成某面板)。
func TestResolver_UnknownUser(t *testing.T) {
	r := NewResolver(NewMemoryRoleStore())
	if p, err := r.PanelForUser(context.Background(), 5, 999); !errors.Is(err, ErrUserNotFound) || p != PanelNone {
		t.Fatalf("unknown user: panel=%q err=%v, want PanelNone/ErrUserNotFound", p, err)
	}
}

// 守 nil store 防御。
func TestResolver_NilStore(t *testing.T) {
	r := NewResolver(nil)
	if p, err := r.PanelForUser(context.Background(), 5, 100); !errors.Is(err, ErrStoreNotConfigured) || p != PanelNone {
		t.Fatalf("nil store: panel=%q err=%v, want PanelNone/ErrStoreNotConfigured", p, err)
	}
}
