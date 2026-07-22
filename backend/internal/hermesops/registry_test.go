package hermesops

import (
	"context"
	"errors"
	"testing"
)

func okSpec(name, role string) ToolSpec {
	return ToolSpec{
		Name: name, Category: CategoryDiagnostic, Description: "测试只读工具",
		ReadOnly: true, RequiredRole: role, InputSchema: ObjectSchema(nil),
		Run: func(_ context.Context, _ ToolRequest) (ToolResult, error) {
			return ToolResult{Summary: map[string]any{"ran": name}}, nil
		},
	}
}

func TestRoleAllowedFloor(t *testing.T) {
	// 回归(变异:让 RoleAllowed 总是返回 true):角色下限必须拒绝一个 rank 低于工具要求角色的 actor,
	// 且未知/空角色必须永远无法满足任何工具。
	cases := []struct {
		actor, required string
		want            bool
	}{
		{RolePlatformAdmin, RoleTenantOperator, true},  // 更高者可通过更低者
		{RolePlatformAdmin, RolePlatformAdmin, true},   // 相等可通过
		{RoleTenantOperator, RoleTenantOperator, true}, // 相等可通过
		{RoleTenantOperator, RolePlatformAdmin, false}, // 更低者无法通过更高者
		{"", RoleTenantOperator, false},                // 未知角色被拒
		{"superuser", RoleTenantOperator, false},       // 伪造角色被拒
	}
	for _, c := range cases {
		if got := RoleAllowed(c.actor, c.required); got != c.want {
			t.Fatalf("RoleAllowed(%q,%q)=%v want %v", c.actor, c.required, got, c.want)
		}
	}
}

func TestRegistryAuthorizeUnknownTool(t *testing.T) {
	// 回归:一个未注册的工具名必须呈现 ErrToolUnknown(由 handler 映射到 404),而不是静默地
	// dispatch 一个 nil 的 Run。
	reg := NewRegistry()
	reg.Register(okSpec("a", RoleTenantOperator))
	if _, err := reg.Authorize("does_not_exist", RolePlatformAdmin); !errors.Is(err, ErrToolUnknown) {
		t.Fatalf("Authorize unknown err=%v want ErrToolUnknown", err)
	}
}

func TestRegistryAuthorizeRoleFloor(t *testing.T) {
	// 回归(变异:在 Authorize 中跳过 RoleAllowed 检查):一个 tenant_operator 对仅 platform_admin
	// 可用的工具必须被以 ErrToolForbidden 拒绝,而 platform_admin 可通过。这是 tool-execute 拒绝
	// 路径所依赖的 RBAC 权威。
	reg := NewRegistry()
	reg.Register(okSpec("admin_only", RolePlatformAdmin))

	if _, err := reg.Authorize("admin_only", RoleTenantOperator); !errors.Is(err, ErrToolForbidden) {
		t.Fatalf("operator on admin-only tool err=%v want ErrToolForbidden", err)
	}
	if _, err := reg.Authorize("admin_only", RolePlatformAdmin); err != nil {
		t.Fatalf("platform_admin on admin-only tool err=%v want nil", err)
	}
}

func TestRegistryRunDeniesBeforeDispatch(t *testing.T) {
	// 回归:Run 必须在调用 spec.Run BEFORE(之前)授权——一个被禁止的调用方必须永远到不了工具体
	// (它可能触及某个 store)。我们让工具体翻转一个哨兵,以此证明它没有运行。
	ran := false
	reg := NewRegistry()
	reg.Register(ToolSpec{
		Name: "admin_only", Description: "仅平台管理员可用的测试工具",
		RequiredRole: RolePlatformAdmin, ReadOnly: true, InputSchema: ObjectSchema(nil),
		Run: func(_ context.Context, _ ToolRequest) (ToolResult, error) {
			ran = true
			return ToolResult{}, nil
		},
	})
	_, err := reg.Run(context.Background(), "admin_only", ToolRequest{Role: RoleTenantOperator})
	if !errors.Is(err, ErrToolForbidden) {
		t.Fatalf("Run err=%v want ErrToolForbidden", err)
	}
	if ran {
		t.Fatalf("tool body ran despite RBAC denial (authorize-after-dispatch bug)")
	}
}

func TestRegistryListSorted(t *testing.T) {
	// 回归:List 必须返回一个稳定的、按 name 排序的集合,这样 GET /tools 输出才是确定的。
	reg := NewRegistry()
	reg.Register(okSpec("zeta", RoleTenantOperator))
	reg.Register(okSpec("alpha", RoleTenantOperator))
	got := reg.List()
	if len(got) != 2 || got[0].Name != "alpha" || got[1].Name != "zeta" {
		t.Fatalf("List=%v want [alpha zeta] sorted", names(got))
	}
}

func names(specs []ToolSpec) []string {
	out := make([]string, len(specs))
	for i, s := range specs {
		out[i] = s.Name
	}
	return out
}
