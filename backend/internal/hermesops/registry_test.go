package hermesops

import (
	"context"
	"errors"
	"testing"
)

func okSpec(name, role string) ToolSpec {
	return ToolSpec{
		Name: name, Category: CategoryDiagnostic, ReadOnly: true, RequiredRole: role,
		Run: func(_ context.Context, _ ToolRequest) (ToolResult, error) {
			return ToolResult{Summary: map[string]any{"ran": name}}, nil
		},
	}
}

func TestRoleAllowedFloor(t *testing.T) {
	// Regression (mutation: make RoleAllowed always return true): the role floor
	// must reject an actor whose rank is below the tool's required role, and an
	// unknown/empty role must never satisfy any tool.
	cases := []struct {
		actor, required string
		want            bool
	}{
		{RolePlatformAdmin, RoleTenantOperator, true},  // higher clears lower
		{RolePlatformAdmin, RolePlatformAdmin, true},   // equal clears
		{RoleTenantOperator, RoleTenantOperator, true}, // equal clears
		{RoleTenantOperator, RolePlatformAdmin, false}, // lower cannot clear higher
		{"", RoleTenantOperator, false},                // unknown role denied
		{"superuser", RoleTenantOperator, false},       // bogus role denied
	}
	for _, c := range cases {
		if got := RoleAllowed(c.actor, c.required); got != c.want {
			t.Fatalf("RoleAllowed(%q,%q)=%v want %v", c.actor, c.required, got, c.want)
		}
	}
}

func TestRegistryAuthorizeUnknownTool(t *testing.T) {
	// Regression: an unregistered tool name must surface ErrToolUnknown (mapped to
	// 404 by the handler), not silently dispatch a nil Run.
	reg := NewRegistry()
	reg.Register(okSpec("a", RoleTenantOperator))
	if _, err := reg.Authorize("does_not_exist", RolePlatformAdmin); !errors.Is(err, ErrToolUnknown) {
		t.Fatalf("Authorize unknown err=%v want ErrToolUnknown", err)
	}
}

func TestRegistryAuthorizeRoleFloor(t *testing.T) {
	// Regression (mutation: skip the RoleAllowed check in Authorize): a
	// tenant_operator must be denied a platform_admin-only tool with
	// ErrToolForbidden, while a platform_admin clears it. This is the RBAC
	// authority the tool-execute denial path depends on.
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
	// Regression: Run must authorize BEFORE calling spec.Run — a forbidden caller
	// must never reach the tool body (which could touch a store). We prove the
	// body did not run by having it flip a sentinel.
	ran := false
	reg := NewRegistry()
	reg.Register(ToolSpec{
		Name: "admin_only", RequiredRole: RolePlatformAdmin, ReadOnly: true,
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
	// Regression: List must return a stable, name-sorted set so GET /tools output
	// is deterministic.
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
